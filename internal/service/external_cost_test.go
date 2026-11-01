package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
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

// O pago é o rateio único: a mensalidade do dia da assinatura (a do histórico), dividida entre as chamadas pelo custo de
// lista; fecha com o valor do dia (ADR-0082 itens 5 e 6).
func TestExternalPaidFollowsMonthlyHistory(t *testing.T) {
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
	sub := e.sub(t, func(in *service.CreateSubscriptionInput) {
		in.Name = "Claude"
		in.Agent = "claude"
		in.MonthlyUSD = 300
		in.ExternalKey = "claude"
	})
	// o dia é hoje, em UTC, e o relógio fica no meio-dia UTC: com fuso atrás de UTC, entre 21h e 24h local a chamada
	// cai num dia (UTC) que o rateio local ainda não percorreu, e o pago some. Defeito de código aberto à parte
	// (issue a abrir, ligada ao #184); este teste não cobre essa janela.
	now := time.Now().UTC()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return noon }
	// a assinatura só cobra os meses que tocou, então "ontem" cairia no mês anterior no dia 1º
	day := noon.Truncate(24 * time.Hour)
	// a mensalidade vale 600 desde hoje (a linha registrada depois desempata a do cadastro, que também começa hoje)
	if _, err := e.svc.AddSubscriptionMonthlyValue(ctx, actor, e.co.ID, sub.ID.String(), 600, day); err != nil {
		t.Fatal(err)
	}
	at := day.Add(12*time.Hour + time.Minute).UnixNano()
	span := fmt.Sprintf(`{"traceId":"aa","spanId":"p1","name":"claude_code.llm_request","startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[
		{"key":"gen_ai.usage.input_tokens","value":{"intValue":"100"}},{"key":"gen_ai.usage.cache_read_input_tokens","value":{"intValue":"50"}},
		{"key":"gen_ai.usage.output_tokens","value":{"intValue":"10"}},{"key":"gen_ai.response.model","value":{"stringValue":"claude-opus-5-5"}},
		{"key":"oute.subscription","value":{"stringValue":"claude"}}]}`, at, at+1_000_000_000)
	rows, err := ingest.SpanRows(spanPayload(t, span), time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.WriteTelemetry(ctx, key, ingest.Batch{Spans: rows}); err != nil {
		t.Fatal(err)
	}
	daysInMonth := float64(time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
	fee := 600 / daysInMonth

	usage, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, []string{"model"}, nil)
	if err != nil || len(usage) != 1 {
		t.Fatalf("ExternalUsage = %+v (%v)", usage, err)
	}
	if got := usage[0].CostPaidUSD; got < fee-1e-6 || got > fee+1e-6 {
		t.Errorf("pago = %v, quer a mensalidade do dia (%v): só há uma chamada, ela leva tudo", got, fee)
	}

	// uma segunda chamada com o triplo do custo de lista divide o dia em 25% e 75%
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key, calls, cost_list_usd)
		SELECT company_id, day, host_id, agent_id, workspace_id, 'claude-sonnet-x', subscription_key, 1, cost_list_usd * 3 FROM calls_daily WHERE company_id = $1`, e.co.ID); err != nil {
		t.Fatal(err)
	}
	byModel, err := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, []string{"model"}, nil)
	if err != nil || len(byModel) != 2 {
		t.Fatalf("ExternalUsage por modelo = %+v (%v)", byModel, err)
	}
	var total, opus float64
	for _, r := range byModel {
		total += r.CostPaidUSD
		if r.Model == "claude-opus-5-5" {
			opus = r.CostPaidUSD
		}
	}
	if total < fee-1e-6 || total > fee+1e-6 {
		t.Errorf("a soma do pago do dia = %v, quer %v (fecha com a mensalidade do dia)", total, fee)
	}
	if opus < fee*0.25-1e-6 || opus > fee*0.25+1e-6 {
		t.Errorf("a chamada de 1/4 do peso leva %v, quer %v", opus, fee*0.25)
	}
	// sem assinatura ligada pela chave, não há pago
	if _, err := e.svc.UpdateSubscription(ctx, actor, e.co.ID, sub.ID, service.UpdateSubscriptionInput{ClearExternalKey: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.ExternalUsage(ctx, e.co.ID, time.Time{}, time.Time{}, nil, nil); len(got) != 1 || got[0].CostPaidUSD != 0 {
		t.Errorf("sem a chave externa o pago é zero: %+v", got)
	}
}
