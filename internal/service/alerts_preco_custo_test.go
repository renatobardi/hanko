package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
)

func openKinds(t *testing.T, e *env) map[string]int {
	t.Helper()
	rows, err := e.svc.Pool.Query(context.Background(), `SELECT kind, count(*) FROM alerts WHERE company_id = $1 AND closed_at IS NULL GROUP BY kind`, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

// Os alertas de preço e de custo abrem pelo que está gravado e fecham sozinhos quando a condição some (ADR-0083).
func TestPriceAndCostAlerts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	actor := ingestActor(t, e)
	token, _, err := e.svc.CreateIngestKey(ctx, actor, e.co.ID, "oute-server")
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.svc.ResolveIngestKey(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ingest.SpanRows(spanPayload(t,
		strings.Replace(oneSpan("u1", "claude_code.llm_request", 0, ""), `"claude-opus-5-5"`, `"modelo-sem-preco-x"`, 1),
		oneSpan("d1", "claude_code.llm_request", 1, `,{"key":"oute.cost_usd","value":{"doubleValue":5}}`),
	), time.Now().UnixNano()+10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: rows}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	got := openKinds(t, e)
	dump, err := e.svc.Pool.Query(ctx, `SELECT model, cost_class, subscription_key, a.name FROM model_calls m JOIN external_agent a ON a.id = m.agent_id`)
	if err != nil {
		t.Fatal(err)
	}
	for dump.Next() {
		var m, c, sk, an string
		if err := dump.Scan(&m, &c, &sk, &an); err != nil {
			t.Fatal(err)
		}
		t.Logf("model_calls: model=%q class=%q sub=%q agent=%q", m, c, sk, an)
	}
	dump.Close()
	for _, k := range []string{"price_model_unpriced", "cost_subscription_unpriced", "cost_claude_diff"} {
		if got[k] != 1 {
			t.Errorf("alertas abertos %v: falta %s", got, k)
		}
	}

	// a tabela passa a conhecer o modelo: o alerta de modelo sem preço fecha, mesmo com as chamadas antigas gravadas sem preço
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO model_prices (id, model, input_per_mtok, output_per_mtok, effective_at, origin)
		VALUES (gen_random_uuid(), 'modelo-sem-preco-x', 1, 2, now() - interval '1 day', 'sources')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	got = openKinds(t, e)
	for _, k := range []string{"price_model_unpriced", "cost_subscription_unpriced"} {
		if got[k] != 0 {
			t.Errorf("alertas abertos %v: %s devia ter fechado", got, k)
		}
	}

	// fontes que divergem na conferência mais recente, para um modelo em uso: abre; travado, não abre
	run := domain.NewID()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO price_check_run (id, reason, started_at, finished_at) VALUES ($1, 'daily', now(), now())`, run); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"models.dev", "openrouter"} {
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO price_check_source (run_id, source, status) VALUES ($1, $2, 'ok')`, run, s); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO price_check_result (run_id, model, source, status) VALUES ($1, 'claude-opus-5-5', $2, 'diverges')`, run, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got = openKinds(t, e); got["price_sources_diverge"] != 1 {
		t.Errorf("alertas abertos %v: falta price_sources_diverge", got)
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO model_prices (id, model, input_per_mtok, output_per_mtok, effective_at, origin, locked)
		VALUES (gen_random_uuid(), 'claude-opus-5-5', 15, 75, now() - interval '1 hour', 'config', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EvaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if got = openKinds(t, e); got["price_sources_diverge"] != 0 {
		t.Errorf("alertas abertos %v: a divergência não importa para preço travado", got)
	}
}
