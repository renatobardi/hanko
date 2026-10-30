package ingest

import (
	"math/big"
	"strings"
)

// Fixed são os atributos que viram coluna fixa: procurados primeiro no registro, depois no resource (`FIXED` de
// otlp.py). O repositório fica como chegou: o acerto do histórico (#617, #803) é atribuição inferida, da migração
// (ADR-0075 item 7).
type Fixed struct {
	HostName         *string `json:"host_name"`
	OuteInstance     *string `json:"oute_instance"`
	OuteAgent        *string `json:"oute_agent"`
	ServiceName      *string `json:"service_name"`
	SessionID        *string `json:"session_id"`
	OuteTaskID       *string `json:"oute_task_id"`
	OuteSwarmRound   *string `json:"oute_swarm_round"`
	OuteRepo         *string `json:"oute_repo"`
	OuteSubscription *string `json:"oute_subscription"`
}

// noNUL: a coluna de texto do Postgres não aceita o byte NUL. Valor com NUL vira ausente (a coluna fica NULL); o fato
// segue no bruto, em `attributes`, e a derivação não usa o texto.
func noNUL(s *string) *string {
	if s != nil && strings.ContainsRune(*s, 0) {
		return nil
	}
	return s
}

func fixedOf(rec, res map[string]any) Fixed {
	pick := func(key string) *string {
		v := rec[key]
		if v == nil {
			v = res[key]
		}
		return noNUL(pyText(v))
	}
	f := Fixed{
		HostName: pick("host.name"), OuteInstance: pick("oute.instance"), OuteAgent: pick("oute.agent"),
		ServiceName: pick("service.name"), SessionID: pick("session.id"), OuteTaskID: pick("oute.task.id"),
		OuteSwarmRound: pick("oute.swarm.round"), OuteRepo: pick("oute.task.repo"), OuteSubscription: pick("oute.subscription"),
	}
	// vazio = sem repositório (#528) e sem assinatura marcada
	if f.OuteRepo != nil && *f.OuteRepo == "" {
		f.OuteRepo = nil
	}
	if f.OuteSubscription != nil && *f.OuteSubscription == "" {
		f.OuteSubscription = nil
	}
	return f
}

func listOf(obj any, key string) ([]any, error) {
	m, ok := asMap(obj)
	if !ok {
		return nil, bad("%s não está num objeto", key)
	}
	v := m[key]
	if v == nil {
		return nil, nil
	}
	l, ok := v.([]any)
	if !ok {
		return nil, bad("%s não é lista", key)
	}
	return l, nil
}

func payloadList(payload any, key string) ([]any, error) {
	if _, ok := asMap(payload); !ok {
		return nil, bad("corpo não é um objeto JSON")
	}
	return listOf(payload, key)
}

func objects(l []any, what string) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(l))
	for _, e := range l {
		m, ok := asMap(e)
		if !ok {
			return nil, bad("%s não é objeto", what)
		}
		out = append(out, m)
	}
	return out, nil
}

// resourceAndScope lê os atributos do resource e o escopo de um item de nível superior.
func resourceAttrs(item map[string]any) (map[string]any, error) {
	r, _ := asMap(item["resource"])
	return attrs(getKey(r, "attributes"))
}

// factTime é a hora do fato: timeUnixNano; sem ela, a hora em que a fonte observou; só em último caso, a chegada.
func factTime(t, observed *big.Int, received int64) (int64, error) {
	switch {
	case t.Sign() != 0:
		return i64(t, "hora do fato")
	case observed.Sign() != 0:
		return i64(observed, "hora observada")
	}
	return received, nil
}

func bigOrNil(n *big.Int) any {
	return n
}

// ---------- logs ----------

// LogRow é uma linha de `telemetry_logs` (sem a empresa e a chave que entregou, que o gravador acrescenta).
type LogRow struct {
	DedupeKey        string `json:"dedupe_key"`
	TimeUnixNano     int64  `json:"time_unix_nano"`
	ObservedUnixNano *int64 `json:"observed_unix_nano"`
	Fixed
	EventName          *string `json:"event_name"`
	OuteEventID        *string `json:"oute_event_id"`
	SeverityNumber     *int32  `json:"severity_number"`
	SeverityText       *string `json:"severity_text"`
	Body               *string `json:"body"`
	TraceID            *string `json:"trace_id"`
	SpanID             *string `json:"span_id"`
	ScopeName          *string `json:"scope_name"`
	ResourceAttributes string  `json:"resource_attributes"`
	Attributes         string  `json:"attributes"`
	ReceivedUnixNano   int64   `json:"received_unix_nano"`
}

// LogRows lê um ExportLogsServiceRequest em JSON. Dedupe: `oute.event.id` quando existe (o mesmo ao vivo, no spool e no
// backfill); senão, o hash do conteúdo (hora em ns, origem e registro inteiro).
func LogRows(payload any, received int64) ([]LogRow, error) {
	rls, err := payloadList(payload, "resourceLogs")
	if err != nil {
		return nil, err
	}
	var rows []LogRow
	for _, rl := range rls {
		m, ok := asMap(rl)
		if !ok {
			return nil, bad("resourceLogs não é objeto")
		}
		res, err := resourceAttrs(m)
		if err != nil {
			return nil, err
		}
		sls, err := listOf(m, "scopeLogs")
		if err != nil {
			return nil, err
		}
		for _, sl := range sls {
			part, err := logScope(sl, res, received)
			if err != nil {
				return nil, err
			}
			rows = append(rows, part...)
		}
	}
	return rows, nil
}

func logScope(sl any, res map[string]any, received int64) ([]LogRow, error) {
	sm, ok := asMap(sl)
	if !ok {
		return nil, bad("scopeLogs não é objeto")
	}
	scope, _ := asMap(sm["scope"])
	lrs, err := listOf(sm, "logRecords")
	if err != nil {
		return nil, err
	}
	recs, err := objects(lrs, "logRecord")
	if err != nil {
		return nil, err
	}
	rows := make([]LogRow, 0, len(recs))
	for _, lr := range recs {
		row, err := logRow(lr, scope, res, received)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func logRow(lr, scope, res map[string]any, received int64) (LogRow, error) {
	rec, err := attrs(lr["attributes"])
	if err != nil {
		return LogRow{}, err
	}
	t, err := toIntOrZero(lr["timeUnixNano"])
	if err != nil {
		return LogRow{}, err
	}
	observed, err := toIntOrZero(lr["observedTimeUnixNano"])
	if err != nil {
		return LogRow{}, err
	}
	body, err := anyValue(lr["body"])
	if err != nil {
		return LogRow{}, err
	}
	eventID := rec["oute.event.id"]
	var key string
	if truthy(eventID) {
		key = "ev:" + pyStr(eventID)
	} else {
		key = "h:" + contentHash(map[string]any{
			"time": bigOrNil(t), "observed": bigOrNil(observed), "resource": res,
			"scope":          map[string]any{"name": getKey(scope, "name"), "version": getKey(scope, "version")},
			"severityNumber": lr["severityNumber"], "severityText": lr["severityText"], "body": body, "attributes": rec,
			"eventName": lr["eventName"], "traceId": lr["traceId"], "spanId": lr["spanId"], "flags": lr["flags"],
		})
	}
	at, err := factTime(t, observed, received)
	if err != nil {
		return LogRow{}, err
	}
	obs, err := ptrInt64OrNil(observed, "hora observada")
	if err != nil {
		return LogRow{}, err
	}
	sev, err := toIntOrZero(lr["severityNumber"])
	if err != nil {
		return LogRow{}, err
	}
	sevNum, err := ptrInt32OrNil(sev, "severityNumber")
	if err != nil {
		return LogRow{}, err
	}
	return LogRow{
		DedupeKey: key, TimeUnixNano: at, ObservedUnixNano: obs, Fixed: fixedOf(rec, res),
		EventName: pyText(or(lr["eventName"], rec["event.name"])), OuteEventID: pyText(eventID),
		SeverityNumber: sevNum, SeverityText: textOrNil(lr["severityText"]), Body: pyText(body),
		TraceID: textOrNil(lr["traceId"]), SpanID: textOrNil(lr["spanId"]), ScopeName: textOrNil(getKey(scope, "name")),
		ResourceAttributes: Canon(res), Attributes: Canon(rec), ReceivedUnixNano: received,
	}, nil
}

// ---------- spans ----------

// Colunas fixas de modelo, tokens e custo: o primeiro atributo presente, na ordem (Claude Code: claude_code.llm_request;
// Codex: session_task.turn; o histórico até 2026-09-30 traz também as chaves do jev.decision e do LiteLLM).
var (
	spanModelKeys         = []string{"gen_ai.response.model", "oute.served_model", "model", "gen_ai.request.model", "llm.model_name"}
	spanInputKeys         = []string{"gen_ai.usage.input_tokens", "input_tokens", "codex.turn.token_usage.non_cached_input_tokens"}
	spanOutputKeys        = []string{"gen_ai.usage.output_tokens", "output_tokens", "codex.turn.token_usage.output_tokens"}
	spanCacheReadKeys     = []string{"gen_ai.usage.cache_read_input_tokens", "cache_read_tokens", "codex.turn.token_usage.cached_input_tokens"}
	spanCacheCreationKeys = []string{"gen_ai.usage.cache_creation_input_tokens", "cache_creation_tokens"}
	spanCostKeys          = []string{"oute.cost_usd", "cost_usd", "gen_ai.usage.cost"}
)

// SpanRow é uma linha de `telemetry_spans`.
type SpanRow struct {
	DedupeKey    string `json:"dedupe_key"`
	TimeUnixNano int64  `json:"time_unix_nano"`
	EndUnixNano  *int64 `json:"end_unix_nano"`
	DurationNs   *int64 `json:"duration_ns"`
	Fixed
	TraceID             string   `json:"trace_id"`
	SpanID              string   `json:"span_id"`
	ParentSpanID        *string  `json:"parent_span_id"`
	Name                *string  `json:"name"`
	Kind                *int32   `json:"kind"`
	StatusCode          *int32   `json:"status_code"`
	StatusMessage       *string  `json:"status_message"`
	Model               *string  `json:"model"`
	InputTokens         *int64   `json:"input_tokens"`
	OutputTokens        *int64   `json:"output_tokens"`
	CacheReadTokens     *int64   `json:"cache_read_tokens"`
	CacheCreationTokens *int64   `json:"cache_creation_tokens"`
	CostUSD             *float64 `json:"cost_usd"`
	ScopeName           *string  `json:"scope_name"`
	ResourceAttributes  string   `json:"resource_attributes"`
	Attributes          string   `json:"attributes"`
	Events              string   `json:"events"`
	Links               string   `json:"links"`
	ReceivedUnixNano    int64    `json:"received_unix_nano"`
}

// SpanRows lê um ExportTraceServiceRequest em JSON. Dedupe por trace_id + span_id.
func SpanRows(payload any, received int64) ([]SpanRow, error) {
	rss, err := payloadList(payload, "resourceSpans")
	if err != nil {
		return nil, err
	}
	var rows []SpanRow
	for _, rs := range rss {
		m, ok := asMap(rs)
		if !ok {
			return nil, bad("resourceSpans não é objeto")
		}
		res, err := resourceAttrs(m)
		if err != nil {
			return nil, err
		}
		sss, err := listOf(m, "scopeSpans")
		if err != nil {
			return nil, err
		}
		for _, ss := range sss {
			part, err := spanScope(ss, res, received)
			if err != nil {
				return nil, err
			}
			rows = append(rows, part...)
		}
	}
	return rows, nil
}

func spanScope(ss any, res map[string]any, received int64) ([]SpanRow, error) {
	sm, ok := asMap(ss)
	if !ok {
		return nil, bad("scopeSpans não é objeto")
	}
	scope, _ := asMap(sm["scope"])
	sl, err := listOf(sm, "spans")
	if err != nil {
		return nil, err
	}
	sps, err := objects(sl, "span")
	if err != nil {
		return nil, err
	}
	rows := make([]SpanRow, 0, len(sps))
	for _, sp := range sps {
		row, err := spanRow(sp, scope, res, received)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func spanRow(sp, scope, res map[string]any, received int64) (SpanRow, error) {
	traceID, ok1 := sp["traceId"].(string)
	spanID, ok2 := sp["spanId"].(string)
	if !truthy(sp["traceId"]) || !truthy(sp["spanId"]) {
		return SpanRow{}, bad("span sem traceId/spanId")
	}
	if !ok1 || !ok2 {
		return SpanRow{}, bad("traceId/spanId não são texto")
	}
	rec, err := attrs(sp["attributes"])
	if err != nil {
		return SpanRow{}, err
	}
	t, err := timesOf(sp, received)
	if err != nil {
		return SpanRow{}, err
	}
	row := SpanRow{
		DedupeKey: "s:" + strings.ToLower(traceID) + ":" + strings.ToLower(spanID), TimeUnixNano: t.at,
		EndUnixNano: t.end, DurationNs: t.duration, Fixed: fixedOf(rec, res),
		TraceID: strings.ToLower(traceID), SpanID: strings.ToLower(spanID),
		Name: pyText(sp["name"]), Model: pyText(first(rec, spanModelKeys)),
		InputTokens: numberInt(first(rec, spanInputKeys)), OutputTokens: numberInt(first(rec, spanOutputKeys)),
		CacheReadTokens: numberInt(first(rec, spanCacheReadKeys)), CacheCreationTokens: numberInt(first(rec, spanCacheCreationKeys)),
		CostUSD: numberFloat(first(rec, spanCostKeys)), ScopeName: textOrNil(getKey(scope, "name")),
		ResourceAttributes: Canon(res), Attributes: Canon(rec), ReceivedUnixNano: received,
	}
	if p, _ := sp["parentSpanId"].(string); p != "" {
		lp := strings.ToLower(p)
		row.ParentSpanID = &lp
	}
	return spanTail(sp, row)
}

type spanTimes struct {
	at       int64
	end      *int64
	duration *int64
}

func timesOf(sp map[string]any, received int64) (spanTimes, error) {
	start, err := toIntOrZero(sp["startTimeUnixNano"])
	if err != nil {
		return spanTimes{}, err
	}
	end, err := toIntOrZero(sp["endTimeUnixNano"])
	if err != nil {
		return spanTimes{}, err
	}
	var out spanTimes
	switch {
	case start.Sign() != 0:
		out.at, err = i64(start, "início do span")
	case end.Sign() != 0:
		out.at, err = i64(end, "fim do span")
	default:
		out.at = received
	}
	if err != nil {
		return spanTimes{}, err
	}
	if out.end, err = ptrInt64OrNil(end, "fim do span"); err != nil {
		return spanTimes{}, err
	}
	if start.Sign() != 0 && end.Cmp(start) >= 0 {
		d, err := i64(new(big.Int).Sub(end, start), "duração do span")
		if err != nil {
			return spanTimes{}, err
		}
		out.duration = &d
	}
	return out, nil
}

func spanTail(sp map[string]any, row SpanRow) (SpanRow, error) {
	kind, err := toIntOrZero(sp["kind"])
	if err != nil {
		return SpanRow{}, err
	}
	if row.Kind, err = ptrInt32OrNil(kind, "kind"); err != nil {
		return SpanRow{}, err
	}
	status, ok := asMap(or(sp["status"], map[string]any{}))
	if !ok {
		return SpanRow{}, bad("status não é objeto")
	}
	code, err := toIntOrZero(status["code"])
	if err != nil {
		return SpanRow{}, err
	}
	if row.StatusCode, err = ptrInt32OrNil(code, "status.code"); err != nil {
		return SpanRow{}, err
	}
	row.StatusMessage = textOrNil(status["message"])
	if row.Events, err = spanEvents(sp); err != nil {
		return SpanRow{}, err
	}
	row.Links, err = spanLinks(sp)
	return row, err
}

func spanEvents(sp map[string]any) (string, error) {
	el, err := listOf(sp, "events")
	if err != nil {
		return "", err
	}
	evs, err := objects(el, "evento do span")
	if err != nil {
		return "", err
	}
	out := make([]any, 0, len(evs))
	for _, e := range evs {
		t, err := toIntOrZero(e["timeUnixNano"])
		if err != nil {
			return "", err
		}
		a, err := attrs(e["attributes"])
		if err != nil {
			return "", err
		}
		out = append(out, map[string]any{"time_unix_nano": t, "name": e["name"], "attributes": a})
	}
	return Canon(out), nil
}

func spanLinks(sp map[string]any) (string, error) {
	ll, err := listOf(sp, "links")
	if err != nil {
		return "", err
	}
	lns, err := objects(ll, "link do span")
	if err != nil {
		return "", err
	}
	out := make([]any, 0, len(lns))
	for _, ln := range lns {
		a, err := attrs(ln["attributes"])
		if err != nil {
			return "", err
		}
		out = append(out, map[string]any{"trace_id": ln["traceId"], "span_id": ln["spanId"], "attributes": a})
	}
	return Canon(out), nil
}
