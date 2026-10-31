package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Os alertas operacionais abrem pela telemetria gravada, respeitam o limite da empresa e calam em host parado (ADR-0083).
func TestTelemetryAlerts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	ns := func(ago time.Duration) int64 { return now.Add(-ago).UnixNano() }
	seq := 0
	for _, h := range []string{"h1", "h2"} {
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO external_host (id, company_id, name) VALUES ($1, $2, $3)`, domain.NewID(), e.co.ID, h); err != nil {
			t.Fatal(err)
		}
	}
	metric := func(host, name string, ago time.Duration, v float64, attrs string, start *int64) {
		t.Helper()
		seq++
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO telemetry_metrics (company_id, dedupe_key, time_unix_nano, start_unix_nano, host_name, oute_instance, oute_agent,
			metric_name, metric_type, value, attributes, aggregation_temporality, received_unix_nano) VALUES ($1, $2, $3, $4, $5, 'i', 'codex', $6, 'gauge', $7, $8::jsonb, 2, $3)`,
			e.co.ID, fmt.Sprintf("m%d", seq), ns(ago), start, host, name, v, attrs); err != nil {
			t.Fatal(err)
		}
	}
	logEv := func(host, event, round, attrs string, ago time.Duration) {
		t.Helper()
		seq++
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO telemetry_logs (company_id, dedupe_key, time_unix_nano, host_name, oute_instance, event_name, oute_event_id,
			oute_swarm_round, attributes, received_unix_nano) VALUES ($1, $2, $3, $4, 'i', $5, $2, NULLIF($6, ''), $7::jsonb, $3)`,
			e.co.ID, fmt.Sprintf("l%d", seq), ns(ago), host, event, round, attrs); err != nil {
			t.Fatal(err)
		}
	}
	startRecent := ns(2 * time.Hour)
	// h1 está vivo (dado há 1 min) e tem um problema de cada tipo
	metric("h1", "otelcol_exporter_queue_capacity", 2*time.Minute, 100, `{"exporter":"otlp"}`, nil)
	metric("h1", "otelcol_exporter_queue_size", time.Minute, 80, `{"exporter":"otlp"}`, nil)
	metric("h1", "otelcol_exporter_send_failed_spans", 5*time.Minute, 5, `{"exporter":"otlp"}`, &startRecent)
	metric("h1", "oute.llm_proxy.up", 20*time.Minute, 1, `{}`, nil)
	metric("h1", "oute.memory.capture.hooks_untrusted", time.Minute, 2, `{}`, nil)
	metric("h1", "oute.memory.capture.missing_pct", time.Minute, 50, `{}`, nil)
	metric("h1", "oute.quota.used_pct", time.Minute, 99, `{"oute.quota.window":"7d"}`, nil)
	metric("h1", "oute.quota.reset_in_seconds", time.Minute, 7200, `{"oute.quota.window":"7d"}`, nil)
	logEv("h1", "oute.emit.heartbeat", "", fmt.Sprintf(`{"oute.emit.spool.bytes": %d}`, 50*1024*1024), time.Minute)
	logEv("h1", "oute.swarm.round.opened", "swarm-1", `{}`, 3*time.Hour)
	// h2 está parado (último dado há 2 h): o mesmo problema dele não alerta
	metric("h2", "otelcol_exporter_queue_capacity", 2*time.Hour+time.Minute, 100, `{"exporter":"otlp"}`, nil)
	metric("h2", "otelcol_exporter_queue_size", 2*time.Hour, 90, `{"exporter":"otlp"}`, nil)

	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	got := openKinds(t, e)
	for _, k := range []string{"queue", "destination_refusing", "spool", "quota", "llm_proxy_down", "memory_capture", "memory_hooks_untrusted", "round_stalled"} {
		if got[k] != 1 {
			t.Errorf("alertas abertos %v: %s deveria ter 1", got, k)
		}
	}
	var h2 int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE company_id = $1 AND closed_at IS NULL AND data->>'host' = 'h2'`, e.co.ID).Scan(&h2); err != nil || h2 != 0 {
		t.Errorf("host parado com %d alertas (%v), quer 0", h2, err)
	}

	// o limite da empresa sobe: a fila de 80% deixa de alertar
	if _, err := e.svc.PutAlertSetting(ctx, domain.HumanActor, e.co.ID, "limit.queue_max_ratio", 0.9); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got = openKinds(t, e); got["queue"] != 0 {
		t.Errorf("alertas abertos %v: queue devia ter fechado com o limite em 90%%", got)
	}
}
