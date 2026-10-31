package service

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Alertas operacionais de agente externo, lidos da telemetria gravada (ADR-0083 itens 1, 3 e 5; `alerts.py@845764f`): fila,
// destino recusando, spool, cota, rodada parada ou antiga, proxy do LLM, captura do ai-memory e hook sem confiança. Cada
// empresa usa os próprios limites (`company_alert_settings`) e a própria janela de leitura. Host parado (sem registro há mais
// do limite de `host_no_data`) não liga nenhum deles: o último valor dele é velho. A linha fecha sozinha quando o dado seguinte
// não mostra mais o problema (EvaluateAlerts).

const (
	minNS = int64(time.Minute)

	metricQueueSize     = "otelcol_exporter_queue_size"
	metricQueueCapacity = "otelcol_exporter_queue_capacity"
	attrSpoolBytes      = "oute.emit.spool.bytes"
	attrSpoolDropped    = "oute.emit.spool.dropped"
	metricQuotaUsed     = "oute.quota.used_pct"
	metricQuotaReset    = "oute.quota.reset_in_seconds"
	quotaWindowAttr     = "oute.quota.window"
	metricLLMProxy      = "oute.llm_proxy.up"
	metricCapturePct    = "oute.memory.capture.missing_pct"
	metricCaptureLocal  = "oute.memory.capture.local"
	metricCaptureDone   = "oute.memory.capture.captured"
	metricCaptureHooks  = "oute.memory.capture.hooks_untrusted"
	deltaTemporality    = 1
)

var sendFailedMetrics = []string{"otelcol_exporter_send_failed_spans", "otelcol_exporter_send_failed_metric_points", "otelcol_exporter_send_failed_log_records"}

// telLimits são os limites em vigor de uma empresa.
type telLimits struct {
	noData, queueRatio, refusedWindow, spoolMax, spoolWindow, lookbackH, llmStale float64
	captureMissing, captureDays, quotaMax, quotaGrace, roundStalled               float64
}

type limitFns map[string]func(domain.ID) float64

func (t *txc) loadTelLimits() (func(domain.ID) telLimits, error) {
	fns := limitFns{}
	for _, k := range []string{"host_no_data_minutes", "queue_max_ratio", "refused_window_minutes", "spool_max_bytes", "spool_dropped_window_minutes",
		"lookback_hours", "llm_proxy_stale_minutes", "memory_capture_max_missing_pct", "memory_capture_days", "quota_max_pct",
		"quota_reset_grace_minutes", "round_stalled_minutes"} {
		f, err := t.companyLimit("limit." + k)
		if err != nil {
			return nil, err
		}
		fns[k] = f
	}
	return func(c domain.ID) telLimits {
		g := func(k string) float64 { return fns[k](c) }
		return telLimits{noData: g("host_no_data_minutes"), queueRatio: g("queue_max_ratio"), refusedWindow: g("refused_window_minutes"),
			spoolMax: g("spool_max_bytes"), spoolWindow: g("spool_dropped_window_minutes"), lookbackH: g("lookback_hours"),
			llmStale: g("llm_proxy_stale_minutes"), captureMissing: g("memory_capture_max_missing_pct"), captureDays: g("memory_capture_days"),
			quotaMax: g("quota_max_pct"), quotaGrace: g("quota_reset_grace_minutes"), roundStalled: g("round_stalled_minutes")}
	}, nil
}

// telCtx reúne o que cada avaliador precisa: a empresa, a janela lida e os limites.
type telCtx struct {
	t       *txc
	company domain.ID
	lo, at  int64
	lim     telLimits
}

func (c *telCtx) cond(kind domain.AlertKind, subjectType, host, instance string, dims []string, msg string, data map[string]any) alertCond {
	if data == nil {
		data = map[string]any{}
	}
	data["host"], data["instance"] = host, instance
	return alertCond{companyID: c.company, kind: kind, subjectType: subjectType,
		subjectID: domain.AlertSubjectID(c.company, kind, append([]string{host, instance}, dims...)...),
		severity:  domain.ExternalAlertSeverity(kind), message: msg, data: data}
}

// telemetryConditions avalia, empresa por empresa, os alertas operacionais lidos da telemetria.
func (t *txc) telemetryConditions() ([]alertCond, error) {
	limits, err := t.loadTelLimits()
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(t.ctx, `SELECT DISTINCT company_id FROM external_host`)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: empresas com host: %w", err)
	}
	var companies []domain.ID
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("alert conditions: empresas com host: %w", err)
		}
		companies = append(companies, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []alertCond
	for _, co := range companies {
		lim := limits(co)
		at := t.now.UnixNano()
		c := &telCtx{t: t, company: co, at: at, lo: max(0, at-int64(lim.lookbackH*60)*minNS), lim: lim}
		conds, err := c.evaluate()
		if err != nil {
			return nil, err
		}
		out = append(out, conds...)
	}
	return out, nil
}

func (c *telCtx) evaluate() ([]alertCond, error) {
	last, err := c.lastData()
	if err != nil {
		return nil, err
	}
	var out []alertCond
	for _, f := range []func() ([]alertCond, error){c.queue, c.refusing, c.spool, c.quota, c.llmProxy, c.memoryCapture, c.memoryHooks, c.rounds} {
		conds, err := f()
		if err != nil {
			return nil, err
		}
		out = append(out, conds...)
	}
	kept := out[:0]
	for _, k := range out { // host parado não liga nada além de host_no_data
		if h, _ := k.data["host"].(string); h != "" && c.stopped(last[h]) {
			continue
		}
		kept = append(kept, k)
	}
	return kept, nil
}

// stopped: sem registro há mais de `host_no_data_minutes` (t = 0: nenhum na janela).
func (c *telCtx) stopped(t int64) bool { return t == 0 || c.at-t > int64(c.lim.noData*float64(minNS)) }

// lastData devolve, por host, a hora do último registro (log, span ou métrica) na janela lida.
func (c *telCtx) lastData() (map[string]int64, error) {
	rows, err := c.t.tx.Query(c.t.ctx, `SELECT host, max(t) FROM (
		SELECT host_name AS host, max(time_unix_nano) AS t FROM telemetry_logs WHERE company_id = $1 AND time_unix_nano BETWEEN $2 AND $3 GROUP BY 1
		UNION ALL SELECT host_name, max(time_unix_nano) FROM telemetry_spans WHERE company_id = $1 AND time_unix_nano BETWEEN $2 AND $3 GROUP BY 1
		UNION ALL SELECT host_name, max(time_unix_nano) FROM telemetry_metrics WHERE company_id = $1 AND time_unix_nano BETWEEN $2 AND $3 GROUP BY 1
	) x WHERE host IS NOT NULL GROUP BY host`, c.company, c.lo, c.at)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: último dado: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var h string
		var t int64
		if err := rows.Scan(&h, &t); err != nil {
			return nil, fmt.Errorf("alert conditions: último dado: %w", err)
		}
		out[h] = t
	}
	return out, rows.Err()
}

// ---------- leitura de métricas ----------

type mpoint struct {
	host, instance, agent, exporter, attrs, metric string
	t                                              int64
	start                                          *int64
	value                                          *float64
	temporality                                    *int32
}

func (p mpoint) v() float64 {
	if p.value == nil {
		return 0
	}
	return *p.value
}

func (c *telCtx) metricPoints(lo int64, names ...string) ([]mpoint, error) {
	rows, err := c.t.tx.Query(c.t.ctx, `SELECT COALESCE(host_name, ''), COALESCE(oute_instance, ''), COALESCE(oute_agent, ''),
		COALESCE(attributes->>'exporter', ''), COALESCE(attributes::text, ''), metric_name, time_unix_nano, start_unix_nano, value, aggregation_temporality
		FROM telemetry_metrics WHERE company_id = $1 AND metric_name = ANY($2) AND time_unix_nano BETWEEN $3 AND $4
		ORDER BY host_name, oute_instance, oute_agent, attributes::text, metric_name, time_unix_nano`, c.company, names, lo, c.at)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: métricas: %w", err)
	}
	defer rows.Close()
	var out []mpoint
	for rows.Next() {
		var p mpoint
		if err := rows.Scan(&p.host, &p.instance, &p.agent, &p.exporter, &p.attrs, &p.metric, &p.t, &p.start, &p.value, &p.temporality); err != nil {
			return nil, fmt.Errorf("alert conditions: métricas: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// seriesOf agrupa pontos (já em ordem de hora) pela chave.
func seriesOf(pts []mpoint, key func(mpoint) string) (map[string][]mpoint, []string) {
	m := map[string][]mpoint{}
	var order []string
	for _, p := range pts {
		k := key(p)
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = append(m[k], p)
	}
	return m, order
}

func iso(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339) }

// runStart é o início da sequência final de pontos em que bad vale.
func runStart(ts []int64, bad func(int) bool) int64 {
	since := int64(0)
	for i := len(ts) - 1; i >= 0; i-- {
		if !bad(i) {
			break
		}
		since = ts[i]
	}
	return since
}

// chainStart é a hora da primeira subida da sequência final, com intervalos de até window entre subidas.
func chainStart(rises []int64, window int64) int64 {
	since := rises[len(rises)-1]
	for i := len(rises) - 2; i >= 0; i-- {
		if since-rises[i] > window {
			break
		}
		since = rises[i]
	}
	return since
}

// ---------- fila e destino recusando ----------

func (c *telCtx) queue() ([]alertCond, error) {
	pts, err := c.metricPoints(c.lo, metricQueueSize, metricQueueCapacity)
	if err != nil {
		return nil, err
	}
	series, order := seriesOf(pts, func(p mpoint) string { return p.host + "\x00" + p.instance + "\x00" + p.exporter })
	var out []alertCond
	for _, k := range order {
		var ratios []float64
		var times, sizes []int64
		var caps []int64
		capacity := 0.0
		byMetric, _ := seriesOf(series[k], func(p mpoint) string { return p.metric })
		cp := byMetric[metricQueueCapacity]
		ci := 0
		for _, s := range byMetric[metricQueueSize] {
			for ci < len(cp) && cp[ci].t <= s.t {
				capacity = cp[ci].v()
				ci++
			}
			if capacity > 0 && s.value != nil {
				ratios = append(ratios, s.v()/capacity)
				times = append(times, s.t)
				sizes = append(sizes, int64(s.v()))
				caps = append(caps, int64(capacity))
			}
		}
		n := len(ratios)
		if n == 0 || ratios[n-1] <= c.lim.queueRatio {
			continue
		}
		first := series[k][0]
		since := runStart(times, func(i int) bool { return ratios[i] > c.lim.queueRatio })
		out = append(out, c.cond(domain.AlertQueue, domain.AlertSubjectExternalHost, first.host, first.instance, []string{first.exporter},
			fmt.Sprintf("Fila do exporter %s em %.0f%% (limite %.0f%%) no host %s", first.exporter, ratios[n-1]*100, c.lim.queueRatio*100, first.host),
			map[string]any{"exporter": first.exporter, "ratio": math.Round(ratios[n-1]*10000) / 10000, "limit": c.lim.queueRatio, "size": sizes[n-1],
				"capacity": caps[n-1], "since": iso(since), "at": iso(times[n-1])}))
	}
	return out, nil
}

type rise struct {
	t int64
	d float64
}

// risesOf são as subidas de um contador em ordem de hora. Cumulativo: diferença entre pontos seguidos; valor menor ou `start`
// diferente é reinício e sobe o valor inteiro. O primeiro ponto só conta se o contador começou dentro da janela lida.
func risesOf(pts []mpoint, lo int64) []rise {
	var out []rise
	var prev *mpoint
	for i := range pts {
		p := pts[i]
		var d float64
		switch {
		case p.temporality != nil && int(*p.temporality) == deltaTemporality:
			d = p.v()
		case prev == nil:
			if p.start != nil && *p.start >= lo {
				d = p.v()
			}
		case p.v() < prev.v() || (p.start != nil && prev.start != nil && *p.start != *prev.start):
			d = p.v()
		default:
			d = p.v() - prev.v()
		}
		if d > 0 {
			out = append(out, rise{p.t, d})
		}
		prev = &pts[i]
	}
	return out
}

func (c *telCtx) refusing() ([]alertCond, error) {
	pts, err := c.metricPoints(c.lo, sendFailedMetrics...)
	if err != nil {
		return nil, err
	}
	series, order := seriesOf(pts, func(p mpoint) string { return p.host + "\x00" + p.instance + "\x00" + p.exporter + "\x00" + p.metric })
	type agg struct {
		host, instance, exporter string
		rises                    []rise
		metrics                  map[string]bool
	}
	groups := map[string]*agg{}
	var gorder []string
	for _, k := range order {
		rs := risesOf(series[k], c.lo)
		if len(rs) == 0 {
			continue
		}
		p := series[k][0]
		gk := p.host + "\x00" + p.instance + "\x00" + p.exporter
		g := groups[gk]
		if g == nil {
			g = &agg{host: p.host, instance: p.instance, exporter: p.exporter, metrics: map[string]bool{}}
			groups[gk] = g
			gorder = append(gorder, gk)
		}
		g.rises = append(g.rises, rs...)
		g.metrics[p.metric] = true
	}
	window := int64(c.lim.refusedWindow * float64(minNS))
	var out []alertCond
	for _, gk := range gorder {
		g := groups[gk]
		sort.Slice(g.rises, func(i, j int) bool { return g.rises[i].t < g.rises[j].t })
		var total float64
		var lastRise int64
		var times []int64
		for _, r := range g.rises {
			times = append(times, r.t)
			if r.t > c.at-window {
				total += r.d
				lastRise = r.t
			}
		}
		if total == 0 {
			continue
		}
		names := make([]string, 0, len(g.metrics))
		for m := range g.metrics {
			names = append(names, m)
		}
		sort.Strings(names)
		out = append(out, c.cond(domain.AlertDestinationRefusing, domain.AlertSubjectExternalHost, g.host, g.instance, []string{g.exporter},
			fmt.Sprintf("O destino %s recusou %.0f itens nos últimos %.0f min (host %s)", g.exporter, total, c.lim.refusedWindow, g.host),
			map[string]any{"exporter": g.exporter, "failed_items": total, "metrics": names, "window_minutes": c.lim.refusedWindow,
				"since": iso(chainStart(times, window)), "last_rise": iso(lastRise)}))
	}
	return out, nil
}

// ---------- spool ----------

type spoolEvent struct {
	t       int64
	bytes   *float64
	dropped *float64
	name    string
}

func (c *telCtx) spool() ([]alertCond, error) {
	rows, err := c.t.tx.Query(c.t.ctx, `SELECT COALESCE(host_name, ''), COALESCE(oute_instance, ''), time_unix_nano, COALESCE(event_name, ''),
		NULLIF(attributes->>$3, ''), NULLIF(attributes->>$4, '') FROM telemetry_logs
		WHERE company_id = $1 AND time_unix_nano BETWEEN $2 AND $5 AND (attributes ? $3 OR attributes ? $4)
		ORDER BY host_name, oute_instance, time_unix_nano, oute_event_id`, c.company, c.lo, attrSpoolBytes, attrSpoolDropped, c.at)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: spool: %w", err)
	}
	defer rows.Close()
	type key struct{ host, instance string }
	evs := map[key][]spoolEvent{}
	var order []key
	for rows.Next() {
		var k key
		var e spoolEvent
		var b, d *string
		if err := rows.Scan(&k.host, &k.instance, &e.t, &e.name, &b, &d); err != nil {
			return nil, fmt.Errorf("alert conditions: spool: %w", err)
		}
		e.bytes, e.dropped = parseNum(b), parseNum(d)
		if _, ok := evs[k]; !ok {
			order = append(order, k)
		}
		evs[k] = append(evs[k], e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []alertCond
	for _, k := range order {
		out = append(out, c.spoolBytes(k.host, k.instance, evs[k])...)
		out = append(out, c.spoolDropped(k.host, k.instance, evs[k])...)
	}
	return out, nil
}

func parseNum(s *string) *float64 {
	if s == nil {
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	if err != nil {
		return nil
	}
	return &v
}

func (c *telCtx) spoolBytes(host, instance string, evs []spoolEvent) []alertCond {
	var sized []spoolEvent
	for _, e := range evs {
		if e.bytes != nil {
			sized = append(sized, e)
		}
	}
	if len(sized) == 0 || *sized[len(sized)-1].bytes <= c.lim.spoolMax {
		return nil
	}
	ts := make([]int64, len(sized))
	for i, e := range sized {
		ts[i] = e.t
	}
	last := sized[len(sized)-1]
	since := runStart(ts, func(i int) bool { return *sized[i].bytes > c.lim.spoolMax })
	return []alertCond{c.cond(domain.AlertSpool, domain.AlertSubjectExternalHost, host, instance, []string{"bytes"},
		fmt.Sprintf("Spool do oute-emit com %.0f bytes (limite %.0f) no host %s", *last.bytes, c.lim.spoolMax, host),
		map[string]any{"attribute": attrSpoolBytes, "bytes": *last.bytes, "limit": c.lim.spoolMax, "since": iso(since), "event_name": last.name, "at": iso(last.t)})}
}

// spoolDropped: o contador do spool nunca zera; sem evento anterior na janela lida não há base e não liga.
func (c *telCtx) spoolDropped(host, instance string, evs []spoolEvent) []alertCond {
	var counted []spoolEvent
	for _, e := range evs {
		if e.dropped != nil {
			counted = append(counted, e)
		}
	}
	var rises []rise
	for i := 1; i < len(counted); i++ {
		d := *counted[i].dropped
		if d >= *counted[i-1].dropped {
			d -= *counted[i-1].dropped
		}
		if d > 0 {
			rises = append(rises, rise{counted[i].t, d})
		}
	}
	window := int64(c.lim.spoolWindow * float64(minNS))
	var total float64
	var times []int64
	for _, r := range rises {
		times = append(times, r.t)
		if r.t > c.at-window {
			total += r.d
		}
	}
	if total == 0 {
		return nil
	}
	return []alertCond{c.cond(domain.AlertSpool, domain.AlertSubjectExternalHost, host, instance, []string{"dropped"},
		fmt.Sprintf("O spool do oute-emit descartou %.0f eventos na última hora (host %s)", total, host),
		map[string]any{"attribute": attrSpoolDropped, "dropped_events": total, "window_minutes": c.lim.spoolWindow, "since": iso(chainStart(times, window))})}
}

// ---------- cota ----------

func quotaWindowOf(attrs string) string {
	i := strings.Index(attrs, `"`+quotaWindowAttr+`"`)
	if i < 0 {
		return ""
	}
	rest := attrs[i+len(quotaWindowAttr)+2:]
	if j := strings.Index(rest, `"`); j >= 0 {
		if k := strings.Index(rest[j+1:], `"`); k >= 0 {
			return rest[j+1 : j+1+k]
		}
	}
	return ""
}

type quotaLive struct {
	pts     []mpoint
	resetAt int64
}

func (q quotaLive) last() mpoint { return q.pts[len(q.pts)-1] }

// liveQuota reúne as séries de cota cujo último ponto tem o reset ainda no futuro: ponto velho e ponto sem par de reset ficam de fora.
func (c *telCtx) liveQuota(series map[string][]mpoint, order []string) []quotaLive {
	var out []quotaLive
	for _, k := range order {
		used := series[k]
		p0 := used[0]
		if p0.metric != metricQuotaUsed {
			continue
		}
		last := used[len(used)-1]
		var resetIn *float64
		for _, r := range series[p0.host+"\x00"+p0.instance+"\x00"+p0.agent+"\x00"+p0.attrs+"\x00"+metricQuotaReset] {
			if r.t == last.t {
				resetIn = r.value
			}
		}
		if resetIn == nil {
			continue
		}
		if at := last.t + int64(*resetIn*1e9); at > c.at {
			out = append(out, quotaLive{used, at})
		}
	}
	return out
}

func (c *telCtx) quotaOver(p mpoint) bool { return p.value != nil && *p.value >= c.lim.quotaMax }

// quotaGraceSkips: a janela de 5h a menos de `grace` do reset, abaixo de 100%, só alerta se a janela longa do mesmo agente também passou do corte.
func (c *telCtx) quotaGraceSkips(q quotaLive, all []quotaLive) bool {
	last := q.last()
	if quotaWindowOf(last.attrs) != "5h" || last.v() >= 100 || q.resetAt-c.at >= int64(c.lim.quotaGrace*float64(minNS)) {
		return false
	}
	for _, o := range all {
		ol := o.last()
		if ol.host == last.host && ol.instance == last.instance && ol.agent == last.agent && quotaWindowOf(ol.attrs) != "5h" && c.quotaOver(ol) {
			return false
		}
	}
	return true
}

func (c *telCtx) quota() ([]alertCond, error) {
	pts, err := c.metricPoints(c.lo, metricQuotaUsed, metricQuotaReset)
	if err != nil {
		return nil, err
	}
	series, order := seriesOf(pts, func(p mpoint) string {
		return p.host + "\x00" + p.instance + "\x00" + p.agent + "\x00" + p.attrs + "\x00" + p.metric
	})
	lives := c.liveQuota(series, order)
	var out []alertCond
	for _, q := range lives {
		last := q.last()
		if !c.quotaOver(last) || c.quotaGraceSkips(q, lives) {
			continue
		}
		ts := make([]int64, len(q.pts))
		for i, p := range q.pts {
			ts[i] = p.t
		}
		since := runStart(ts, func(i int) bool { return c.quotaOver(q.pts[i]) })
		out = append(out, c.cond(domain.AlertQuota, domain.AlertSubjectExternalHost, last.host, last.instance, []string{last.agent, last.attrs},
			fmt.Sprintf("Cota do %s em %.0f%% (limite %.0f%%, host %s)", last.agent, last.v(), c.lim.quotaMax, last.host),
			map[string]any{"agent": last.agent, "pct": last.v(), "limit": c.lim.quotaMax, "window": quotaWindowOf(last.attrs), "since": iso(since),
				"resets_at": iso(q.resetAt), "at": iso(last.t)}))
	}
	return out, nil
}

// ---------- proxy do LLM e captura do ai-memory ----------

func (c *telCtx) llmProxy() ([]alertCond, error) {
	rows, err := c.t.tx.Query(c.t.ctx, `SELECT COALESCE(host_name, ''), COALESCE(oute_instance, ''), max(time_unix_nano) FROM telemetry_metrics
		WHERE company_id = $1 AND metric_name = $2 AND time_unix_nano BETWEEN $3 AND $4 GROUP BY 1, 2 ORDER BY 1, 2`, c.company, metricLLMProxy, c.lo, c.at)
	if err != nil {
		return nil, fmt.Errorf("alert conditions: proxy do LLM: %w", err)
	}
	defer rows.Close()
	limit := int64(c.lim.llmStale * float64(minNS))
	var out []alertCond
	for rows.Next() {
		var host, instance string
		var last int64
		if err := rows.Scan(&host, &instance, &last); err != nil {
			return nil, fmt.Errorf("alert conditions: proxy do LLM: %w", err)
		}
		if c.at-last <= limit {
			continue
		}
		out = append(out, c.cond(domain.AlertLLMProxyDown, domain.AlertSubjectExternalHost, host, instance, nil,
			fmt.Sprintf("O proxy do LLM do ai-memory está sem batimento há %s (limite %.0f min, host %s)", humanDuration(time.Duration(c.at-last)), c.lim.llmStale, host),
			map[string]any{"attribute": metricLLMProxy, "last_heartbeat": iso(last), "limit_minutes": c.lim.llmStale, "since": iso(last)}))
	}
	return out, rows.Err()
}

func (c *telCtx) capturePoints(names ...string) (map[string][]mpoint, []string, error) {
	lo := max(0, c.at-int64(c.lim.captureDays*24*60)*minNS)
	pts, err := c.metricPoints(lo, names...)
	if err != nil {
		return nil, nil, err
	}
	m, order := seriesOf(pts, func(p mpoint) string { return p.metric + "\x00" + p.host + "\x00" + p.instance + "\x00" + p.agent })
	return m, order, nil
}

func (c *telCtx) memoryCapture() ([]alertCond, error) {
	m, order, err := c.capturePoints(metricCapturePct, metricCaptureLocal, metricCaptureDone)
	if err != nil {
		return nil, err
	}
	var out []alertCond
	for _, k := range order {
		pts := m[k]
		last := pts[len(pts)-1]
		if last.metric != metricCapturePct || last.value == nil || *last.value <= c.lim.captureMissing {
			continue
		}
		ts := make([]int64, len(pts))
		for i, p := range pts {
			ts[i] = p.t
		}
		since := runStart(ts, func(i int) bool { return pts[i].value != nil && *pts[i].value > c.lim.captureMissing })
		data := map[string]any{"attribute": metricCapturePct, "agent": last.agent, "missing_pct": last.v(), "limit": c.lim.captureMissing, "since": iso(since), "at": iso(last.t)}
		for name, metric := range map[string]string{"local": metricCaptureLocal, "captured": metricCaptureDone} {
			for _, p := range m[metric+"\x00"+last.host+"\x00"+last.instance+"\x00"+last.agent] {
				if p.t == last.t && p.value != nil {
					data[name] = int64(*p.value)
				}
			}
		}
		out = append(out, c.cond(domain.AlertMemoryCapture, domain.AlertSubjectExternalAgent, last.host, last.instance, []string{last.agent},
			fmt.Sprintf("O ai-memory deixou de capturar %.0f%% das sessões do %s (limite %.0f%%, host %s)", last.v(), last.agent, c.lim.captureMissing, last.host), data))
	}
	return out, nil
}

func (c *telCtx) memoryHooks() ([]alertCond, error) {
	m, order, err := c.capturePoints(metricCaptureHooks)
	if err != nil {
		return nil, err
	}
	var out []alertCond
	for _, k := range order {
		pts := m[k]
		last := pts[len(pts)-1]
		if last.value == nil || *last.value <= 0 {
			continue
		}
		ts := make([]int64, len(pts))
		for i, p := range pts {
			ts[i] = p.t
		}
		since := runStart(ts, func(i int) bool { return pts[i].value != nil && *pts[i].value > 0 })
		out = append(out, c.cond(domain.AlertMemoryHooksUntrusted, domain.AlertSubjectExternalAgent, last.host, last.instance, []string{last.agent},
			fmt.Sprintf("%d hooks do ai-memory sem trusted_hash no %s (host %s): o agente não roda o hook", int64(last.v()), last.agent, last.host),
			map[string]any{"attribute": metricCaptureHooks, "agent": last.agent, "hooks": int64(last.v()), "since": iso(since), "at": iso(last.t)}))
	}
	return out, nil
}

// ---------- rodada parada ou antiga ----------

func (c *telCtx) rounds() ([]alertCond, error) {
	day := int64(c.lim.lookbackH * 60 * float64(minNS))
	stalled := int64(c.lim.roundStalled * float64(minNS))
	rows, err := c.t.tx.Query(c.t.ctx, `SELECT COALESCE(host_name, ''), COALESCE(oute_instance, ''), oute_swarm_round, max(time_unix_nano),
		max(time_unix_nano) FILTER (WHERE event_name = 'oute.swarm.round.opened'), max(time_unix_nano) FILTER (WHERE event_name = 'oute.swarm.round.closed')
		FROM telemetry_logs WHERE company_id = $1 AND starts_with(event_name, 'oute.swarm.') AND oute_swarm_round IS NOT NULL AND time_unix_nano <= $2
		GROUP BY 1, 2, 3 HAVING max(time_unix_nano) >= $3 AND max(time_unix_nano) FILTER (WHERE event_name = 'oute.swarm.round.opened') IS NOT NULL
		  AND max(time_unix_nano) FILTER (WHERE event_name = 'oute.swarm.round.closed') IS NULL`, c.company, c.at, max(0, c.at-2*day))
	if err != nil {
		return nil, fmt.Errorf("alert conditions: rodadas: %w", err)
	}
	type rnd struct {
		host, instance, round string
		last, opened          int64
	}
	var rs []rnd
	for rows.Next() {
		var r rnd
		var closed *int64
		if err := rows.Scan(&r.host, &r.instance, &r.round, &r.last, &r.opened, &closed); err != nil {
			rows.Close()
			return nil, fmt.Errorf("alert conditions: rodadas: %w", err)
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []alertCond
	for _, r := range rs {
		idle := c.at - r.last
		if idle <= stalled {
			continue
		}
		slugs, spawned, err := c.openSessions(r.host, r.round)
		if err != nil {
			return nil, err
		}
		kind, why := domain.AlertRoundStalled, "triage"
		switch {
		case idle > day:
			kind, why = domain.AlertRoundOld, "old"
		case len(slugs) > 0:
			why = "sessions"
		case spawned > 0:
			why = "unclosed"
		}
		msg := fmt.Sprintf("Rodada %s parada há %s (host %s)", r.round, humanDuration(time.Duration(idle)), r.host)
		if kind == domain.AlertRoundOld {
			msg = fmt.Sprintf("Rodada antiga sem fechamento: %s, sem evento há %s (host %s)", r.round, humanDuration(time.Duration(idle)), r.host)
		}
		out = append(out, c.cond(kind, domain.AlertSubjectExternalRound, r.host, r.instance, []string{r.round}, msg,
			map[string]any{"round": r.round, "sessions": slugs, "spawned": spawned, "kind": why, "idle_seconds": idle / int64(time.Second),
				"limit_minutes": c.lim.roundStalled, "since": iso(r.last), "last_event": iso(r.last), "opened_at": iso(r.opened)}))
	}
	return out, nil
}

// openSessions devolve as sessões abertas da rodada (aberta e ainda não fechada) e quantas ela abriu.
func (c *telCtx) openSessions(host, round string) ([]string, int, error) {
	rows, err := c.t.tx.Query(c.t.ctx, `SELECT attributes->>'oute.swarm.session', event_name FROM telemetry_logs
		WHERE company_id = $1 AND event_name IN ('oute.swarm.session.spawned', 'oute.swarm.session.closed') AND time_unix_nano <= $2
		  AND oute_swarm_round = $3 AND COALESCE(host_name, '') = $4 ORDER BY time_unix_nano`, c.company, c.at, round, host)
	if err != nil {
		return nil, 0, fmt.Errorf("alert conditions: sessões da rodada: %w", err)
	}
	defer rows.Close()
	state := map[string]bool{}
	for rows.Next() {
		var slug *string
		var ev string
		if err := rows.Scan(&slug, &ev); err != nil {
			return nil, 0, fmt.Errorf("alert conditions: sessões da rodada: %w", err)
		}
		if slug != nil && *slug != "" {
			state[*slug] = strings.HasSuffix(ev, ".spawned")
		}
	}
	var open []string
	for s, v := range state {
		if v {
			open = append(open, s)
		}
	}
	sort.Strings(open)
	return open, len(state), rows.Err()
}
