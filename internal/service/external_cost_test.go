package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/ingest"
)

func logCostPayload(t *testing.T, requestID string, cost float64) any {
	t.Helper()
	doc, err := ingest.DecodeJSON([]byte(fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[{"key":"host.name","value":{"stringValue":"oute-server"}},{"key":"oute.agent","value":{"stringValue":"claude"}}]},
		"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","eventName":"api_request","attributes":[
		{"key":"session.id","value":{"stringValue":"conv-1"}},{"key":"request_id","value":{"stringValue":%q}},{"key":"cost_usd","value":{"doubleValue":%v}}]}]}]}]}`,
		time.Now().UnixNano()+5_000_000_000, requestID, cost)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// O custo real do Claude Code vem no log `api_request` de mesmo `request_id`, antes ou depois do span; o custo de lista é
// calculado uma vez, na ingestão, com o preço da hora do fato (ADR-0082 itens 1 e 3).
func TestWriteTelemetryCostFromLogs(t *testing.T) {
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
	reqAttr := func(id string) string { return fmt.Sprintf(`,{"key":"request_id","value":{"stringValue":%q}}`, id) }
	// 1) o log chega ANTES do span: o span já entra como real
	logs, err := ingest.LogRows(logCostPayload(t, "req-early", 0.5), time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: logs}); err != nil {
		t.Fatal(err)
	}
	// 2) o span chega ANTES do log: entra como lista e é promovido quando o log chega
	spans, err := ingest.SpanRows(spanPayload(t,
		oneSpan("early", "claude_code.llm_request", 0, reqAttr("req-early")),
		oneSpan("late", "claude_code.llm_request", 1, reqAttr("req-late")),
		oneSpan("zai", "claude_code.llm_request", 2, reqAttr("req-zai")+`,{"key":"oute.subscription","value":{"stringValue":"zai"}},{"key":"oute.cost_usd","value":{"doubleValue":9}}`),
	), time.Now().UnixNano()+10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: spans}); err != nil {
		t.Fatal(err)
	}
	classes := func() string {
		var out string
		if err := e.svc.Pool.QueryRow(ctx, `SELECT string_agg(split_part(dedupe_key, ':', 3) || '=' || cost_class, ',' ORDER BY dedupe_key) FROM model_calls WHERE company_id = $1`, e.co.ID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := classes(); got != "early=real,late=listed,zai=listed" {
		t.Fatalf("classes antes do log tardio = %q; quer early=real,late=listed,zai=listed (a zai descarta o custo informado)", got)
	}

	lateLogs, err := ingest.LogRows(logCostPayload(t, "req-late", 0.75), time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	zaiLogs, err := ingest.LogRows(logCostPayload(t, "req-zai", 3), time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: append(lateLogs, zaiLogs...)}); err != nil {
		t.Fatal(err)
	}
	if got := classes(); got != "early=real,late=real,zai=listed" {
		t.Errorf("classes depois do log tardio = %q; quer early=real,late=real,zai=listed", got)
	}
	var real, list float64
	var unpriced int64
	if err := e.svc.Pool.QueryRow(ctx, `SELECT cost_real_usd::float8, cost_list_usd::float8, unpriced_calls FROM external_conversation WHERE company_id = $1`, e.co.ID).Scan(&real, &list, &unpriced); err != nil {
		t.Fatal(err)
	}
	if real != 1.25 || unpriced != 0 || list < 0.0069 || list > 0.0071 {
		t.Errorf("conversa: real %v (quer 1,25), lista %v (quer ~0,00698), sem preço %d (quer 0)", real, list, unpriced)
	}
	var dailyReal float64
	if err := e.svc.Pool.QueryRow(ctx, `SELECT COALESCE(sum(cost_real_usd), 0)::float8 FROM calls_daily WHERE company_id = $1`, e.co.ID).Scan(&dailyReal); err != nil || dailyReal != 1.25 {
		t.Errorf("calls_daily real = %v (%v), quer 1,25", dailyReal, err)
	}
	// reenviar o mesmo log não soma de novo
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Logs: lateLogs}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Pool.QueryRow(ctx, `SELECT cost_real_usd::float8 FROM external_conversation WHERE company_id = $1`, e.co.ID).Scan(&real); err != nil || real != 1.25 {
		t.Errorf("custo real depois do reenvio = %v (%v), quer 1,25", real, err)
	}
}
