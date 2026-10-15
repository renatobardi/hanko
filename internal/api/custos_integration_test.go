//go:build integration

package api_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// Os quatro custos (real, lista, estimado, pago) saem coerentes do endpoint de custos e batem com o
// que cada execução mostra, em qualquer agrupamento.
func TestAPIQuatroCustos(t *testing.T) {
	e := newAPIEnv(t)
	// US$ 31 por mês: US$ 1 por dia em março
	e.setNow(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))
	var sub api.Subscription
	e.want(201, "POST", e.base()+"/subscriptions", `{"name":"Plano","agent":"fake","monthly_usd":31}`, &sub)
	sid, _ := domain.ParseID(sub.ID)
	p := e.persona(e.co, "Eng", func(in *service.HirePersonaInput) { in.SubscriptionID = &sid; in.Model = "claude-sonnet-4-5" })

	d1 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	// real: o harness informou US$ 0,50 (lista: 1M de entrada = US$ 3)
	a := e.spend(p, d1, adapter.Result{Model: "claude-sonnet-4-5", CostUSD: ptr(0.5), Usage: domain.Usage{InputTokens: 1_000_000}})
	// estimado: 1M de entrada + 200k de saída = US$ 6
	b := e.spend(p, d1, adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 1_000_000, OutputTokens: 200_000}})
	// estimado: US$ 3, no dia 20
	c := e.spend(p, d2, adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 1_000_000}})

	// cada execução expõe real/estimado, lista, origem do custo e a assinatura que a cobriu
	var ra, rb api.Run
	e.want(200, "GET", e.base()+"/runs/"+a.ID.String(), "", &ra)
	e.want(200, "GET", e.base()+"/runs/"+b.ID.String(), "", &rb)
	if ra.CostSource != "reported" || ra.CostUSD == nil || !near(*ra.CostUSD, 0.5) || ra.ListCostUSD == nil || !near(*ra.ListCostUSD, 3) || ra.SubscriptionID != sub.ID {
		t.Fatalf("run real: %+v", ra)
	}
	if rb.CostSource != "estimated" || rb.CostUSD == nil || !near(*rb.CostUSD, 6) || rb.ListCostUSD == nil || !near(*rb.ListCostUSD, 6) || rb.ModelSource != "persona" {
		t.Fatalf("run estimada: %+v", rb)
	}

	e.setNow(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)) // março acabou: o mês fecha com a mensalidade
	q := "?from=2026-03-01&to=2026-03-31&group_by="
	var day api.CostSummary
	e.want(200, "GET", e.base()+"/costs"+q+"day", "", &day)
	if !near(day.ReportedUSD, 0.5) || !near(day.EstimatedUSD, 9) || !near(day.TotalUSD, 9.5) || !near(day.ListUSD, 12) || day.Runs != 3 {
		t.Fatalf("totais: reportado=%v estimado=%v total=%v lista=%v runs=%d", day.ReportedUSD, day.EstimatedUSD, day.TotalUSD, day.ListUSD, day.Runs)
	}
	if !near(day.ReportedUSD+day.EstimatedUSD, day.TotalUSD) {
		t.Fatal("total = real + estimado")
	}
	if !near(day.PaidUSD, 2) || !near(day.IdleUSD, 29) || !near(day.PaidUSD+day.IdleUSD, 31) {
		t.Fatalf("pago=%v ocioso=%v: pago + ocioso fecha a mensalidade", day.PaidUSD, day.IdleUSD)
	}
	if len(day.Subscriptions) != 1 || day.Subscriptions[0].ID != sub.ID || !near(day.Subscriptions[0].PaidUSD, 2) ||
		!near(day.Subscriptions[0].IdleUSD, 29) || !near(day.Subscriptions[0].MonthlyUSD, 31) || day.Subscriptions[0].Runs != 3 || !near(day.Subscriptions[0].ListUSD, 12) {
		t.Fatalf("por assinatura: %+v", day.Subscriptions)
	}
	if len(day.Rows) != 31 {
		t.Fatalf("agrupado por dia traz os 31 dias de março (inclusive os sem uso): %d", len(day.Rows))
	}
	rows := map[string]api.CostRow{}
	var sumPaid, sumIdle, sumList float64
	for _, r := range day.Rows {
		rows[r.Key] = r
		sumPaid += r.PaidUSD
		sumIdle += r.IdleUSD
		sumList += r.ListUSD
		if !near(r.ReportedUSD+r.EstimatedUSD, r.CostUSD) {
			t.Fatalf("dia %s: real + estimado != custo: %+v", r.Key, r)
		}
	}
	if !near(sumPaid, day.PaidUSD) || !near(sumIdle, day.IdleUSD) || !near(sumList, day.ListUSD) {
		t.Fatalf("a soma das linhas fecha com o total: pago %v/%v ocioso %v/%v lista %v/%v", sumPaid, day.PaidUSD, sumIdle, day.IdleUSD, sumList, day.ListUSD)
	}
	// o dia 10 tem as duas execuções (rateio proporcional ao custo de lista: 3/9 e 6/9 de US$ 1)
	if r := rows["2026-03-10"]; !near(r.ReportedUSD, 0.5) || !near(r.EstimatedUSD, 6) || !near(r.ListUSD, 9) || !near(r.PaidUSD, 1) || r.Unused || r.Runs != 2 {
		t.Fatalf("dia 10: %+v", r)
	}
	if r := rows["2026-03-11"]; !r.Unused || !near(r.IdleUSD, 1) || r.PaidUSD != 0 || r.Runs != 0 {
		t.Fatalf("dia sem uso: %+v", r)
	}

	// o mesmo total por persona, modelo e harness (e o pago por linha)
	for _, g := range []string{"persona", "model", "harness", "project"} {
		var s api.CostSummary
		e.want(200, "GET", e.base()+"/costs"+q+g, "", &s)
		if s.GroupBy != g || !near(s.TotalUSD, day.TotalUSD) || !near(s.ListUSD, day.ListUSD) || !near(s.PaidUSD, day.PaidUSD) || !near(s.EstimatedUSD, day.EstimatedUSD) {
			t.Fatalf("group_by=%s diverge do total: %+v", g, s)
		}
		var rowsPaid float64
		for _, r := range s.Rows {
			rowsPaid += r.PaidUSD
		}
		if !near(rowsPaid, s.PaidUSD) {
			t.Fatalf("group_by=%s: pago por linha %v != %v", g, rowsPaid, s.PaidUSD)
		}
	}
	_ = c

	// meio do mês: o rateio para em "hoje" (15 dias da mensalidade)
	e.setNow(time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	var mid api.CostSummary
	e.want(200, "GET", e.base()+"/costs"+q+"day", "", &mid)
	if !near(mid.PaidUSD+mid.IdleUSD, 15) || !near(mid.PaidUSD, 1) {
		t.Fatalf("meio do mês: pago=%v ocioso=%v", mid.PaidUSD, mid.IdleUSD)
	}

	// parâmetros inválidos
	for _, path := range []string{
		"/costs?group_by=banana",
		"/costs?from=ontem&to=hoje",
		"/costs?from=2026-03-31&to=2026-03-01",
		"/costs?from=2024-01-01&to=2026-03-01", // mais de 400 dias
	} {
		if status, body := e.call("GET", e.base()+path, "", nil); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, quer 422: %s", path, status, body)
		}
	}
	// outra empresa não enxerga custo nenhum desta
	e.company("Beta", "beta")
	var other api.CostSummary
	e.want(200, "GET", "/api/v1/companies/beta/costs"+q+"day", "", &other)
	if other.TotalUSD != 0 || other.ListUSD != 0 || other.PaidUSD != 0 || other.Runs != 0 || len(other.Subscriptions) != 0 {
		t.Fatalf("custos vazaram para outra empresa: %+v", other)
	}
}

// Persona sem assinatura não paga mensalidade: o quarto custo fica zerado, sem derrubar os outros três.
func TestAPICustosSemAssinatura(t *testing.T) {
	e := newAPIEnv(t)
	p := e.persona(e.co, "Eng", nil)
	e.spend(p, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), usdRes(2))
	e.setNow(time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC))
	var s api.CostSummary
	e.want(200, "GET", e.base()+"/costs?from=2026-03-01&to=2026-03-31", "", &s)
	if !near(s.ReportedUSD, 2) || !near(s.TotalUSD, 2) || s.EstimatedUSD != 0 || s.PaidUSD != 0 || s.IdleUSD != 0 || s.Subscriptions == nil || len(s.Subscriptions) != 0 {
		t.Fatalf("%+v", s)
	}
	for _, r := range s.Rows {
		if r.Unused || r.IdleUSD != 0 {
			t.Fatalf("sem assinatura não há dia 'sem uso': %+v", r)
		}
	}
}

// Trava semanal: ao chegar a weekly_guard_pct do teto, novas execuções cobertas são recusadas
// (409, ErrSubscriptionGuard), a persona não é pausada, o aviso sai uma vez por semana e subir o
// teto pela API destrava na hora.
func TestAPITravaSemanal(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	var sub api.Subscription
	e.want(201, "POST", e.base()+"/subscriptions", `{"name":"Plano","agent":"fake","monthly_usd":20,"weekly_limit_usd":10,"weekly_guard_pct":80}`, &sub)
	sid, _ := domain.ParseID(sub.ID)
	p := e.persona(e.co, "Eng", func(in *service.HirePersonaInput) { in.SubscriptionID = &sid })
	free := e.persona(e.co, "Livre", nil)
	tue := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	e.spend(p, tue, usdRes(5))
	e.spend(p, tue, usdRes(2.5)) // 7,5 de 10: abaixo da trava (8): ainda abre
	var st api.Subscription
	e.want(200, "GET", e.base()+"/subscriptions/"+sub.ID, "", &st)
	if st.Blocked || !near(st.WeekUsageUSD, 7.5) || !near(st.WeekPercent, 75) {
		t.Fatalf("abaixo da trava: %+v", st)
	}
	e.spend(p, tue, usdRes(1)) // 8,5: esta ainda abriu; a próxima não
	e.want(200, "GET", e.base()+"/subscriptions/"+sub.ID, "", &st)
	if !st.Blocked || !near(st.WeekUsageUSD, 8.5) || !near(st.WeekPercent, 85) {
		t.Fatalf("na trava: %+v", st)
	}

	// a run é recusada com o erro de trava; nada é aberto e a persona segue ativa
	tv := e.task(e.co, "bloqueada", &p, nil)
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv))
	if !errors.Is(err, domain.ErrSubscriptionGuard) || !errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrBudgetExceeded) {
		t.Fatalf("checkout sob trava: %v", err)
	}
	var runs api.ListBody[api.Run]
	e.want(200, "GET", e.base()+"/runs?task="+tv.Key, "", &runs)
	if len(runs.Items) != 0 {
		t.Fatalf("abriu run sob a trava: %+v", runs.Items)
	}
	var pers api.Persona
	e.want(200, "GET", e.base()+"/personas/"+p.Slug, "", &pers)
	if pers.Status == "paused" {
		t.Fatal("a trava semanal não pausa a persona")
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv)); !errors.Is(err, domain.ErrSubscriptionGuard) {
		t.Fatalf("segunda tentativa: %v", err)
	}
	if n := e.activityVerbs()["subscription.guard_blocked"]; n != 1 {
		t.Fatalf("subscription.guard_blocked = %d, quer 1 por semana", n)
	}

	// sem assinatura, nada trava
	ftv := e.task(e.co, "livre", &free, nil)
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(free, ftv)); err != nil {
		t.Fatalf("persona sem assinatura: %v", err)
	}

	// subir o teto pela API destrava; baixar de novo trava; desligar a assinatura também libera
	e.want(200, "PATCH", e.base()+"/subscriptions/"+sub.ID, `{"weekly_limit_usd":100}`, &st)
	if st.Blocked || !near(st.WeekPercent, 8.5) {
		t.Fatalf("após subir o teto: %+v", st)
	}
	e.want(200, "PATCH", e.base()+"/subscriptions/"+sub.ID, `{"weekly_limit_usd":10}`, &st)
	if !st.Blocked {
		t.Fatalf("após baixar o teto: %+v", st)
	}
	e.want(200, "PATCH", e.base()+"/subscriptions/"+sub.ID, `{"active":false}`, &st)
	if st.Blocked {
		t.Fatalf("assinatura desligada não trava: %+v", st)
	}
	e.want(200, "PATCH", e.base()+"/subscriptions/"+sub.ID, `{"active":true}`, &st)

	// a semana seguinte começa limpa (segunda 16/03 em São Paulo)
	e.setNow(time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC))
	e.want(200, "GET", e.base()+"/subscriptions/"+sub.ID, "", &st)
	if st.Blocked || st.WeekUsageUSD != 0 {
		t.Fatalf("semana nova: %+v", st)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv)); err != nil {
		t.Fatalf("semana nova: %v", err)
	}
}

// Alertas operacionais não têm endpoint próprio: aparecem na atividade da empresa (alert.opened,
// alert.closed), no Painel e na Caixa de entrada. O orçamento perto do limite abre um alerta e
// subir o teto pela API o fecha.
func TestAPIAlertaDeOrcamento(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	p := e.persona(e.co, "Eng", nil)
	e.want(200, "PATCH", e.base(), `{"budget_monthly_cents":1000}`, nil) // US$ 10
	e.spend(p, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), usdRes(9))

	ev, err := e.svc.EvaluateAlerts(ctx)
	if err != nil || ev.Opened != 1 || ev.Open != 1 {
		t.Fatalf("avaliação: %+v %v", ev, err)
	}
	var dash api.Dashboard
	e.want(200, "GET", e.base()+"/dashboard", "", &dash)
	if dash.BudgetState != "warning" || !near(dash.BudgetPercent, 90) {
		t.Fatalf("painel: %+v", dash)
	}
	var acts api.ListBody[api.Activity]
	e.want(200, "GET", e.base()+"/activity?limit=500", "", &acts)
	var opened *api.Activity
	for i, a := range acts.Items {
		if a.Verb == "alert.opened" {
			opened = &acts.Items[i]
		}
	}
	if opened == nil || opened.SubjectType != "alert" && opened.SubjectType != "company" {
		t.Fatalf("alert.opened não apareceu na atividade: %+v", acts.Items)
	}
	if !bytes.Contains(opened.Data, []byte("budget_near_limit")) {
		t.Fatalf("o alerta é de orçamento: %s", opened.Data)
	}
	// avaliar de novo não duplica
	if ev, err = e.svc.EvaluateAlerts(ctx); err != nil || ev.Opened != 0 || ev.Open != 1 {
		t.Fatalf("reavaliação: %+v %v", ev, err)
	}
	// subir o teto resolve a condição e o alerta fecha sozinho
	e.want(200, "PATCH", e.base(), `{"budget_monthly_cents":100000}`, nil)
	if ev, err = e.svc.EvaluateAlerts(ctx); err != nil || ev.Closed != 1 || ev.Open != 0 {
		t.Fatalf("fechamento: %+v %v", ev, err)
	}
	if v := e.activityVerbs(); v["alert.opened"] != 1 || v["alert.closed"] != 1 {
		t.Fatalf("atividade: %v", v)
	}
	e.want(200, "GET", e.base()+"/dashboard", "", &dash)
	if dash.BudgetState != "ok" {
		t.Fatalf("painel após subir o teto: %+v", dash)
	}
}
