package service_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// spend runs a task of persona p to completion at instant at with the given result.
func (e *env) spend(t *testing.T, p domain.Persona, at time.Time, mod func(*service.CreateTaskInput), res adapter.Result) domain.Run {
	t.Helper()
	ctx := context.Background()
	e.svc.Now = func() time.Time { return at }
	in := service.CreateTaskInput{Title: "gasto", PersonaID: &p.ID}
	if mod != nil {
		mod(&in)
	}
	tv := e.task(t, in)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	done, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: res})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	return done
}

func usd(v float64) adapter.Result { return adapter.Result{CostUSD: &v} }

func TestFinishRunCostSource(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.Model = "claude-sonnet-4-5" })

	r := e.spend(t, p, now, nil, adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{
		InputTokens: 1_000_000, OutputTokens: 200_000, CacheReadTokens: 500_000, CacheCreationTokens: 100_000}})
	// 3 + 3 + 0.15 + 0.375
	if r.CostSource != domain.CostEstimated || r.CostUSD == nil || !approx(*r.CostUSD, 6.525) {
		t.Fatalf("estimated run: source=%s cost=%v", r.CostSource, r.CostUSD)
	}

	r = e.spend(t, p, now, nil, adapter.Result{Model: "claude-opus-4", CostUSD: ptr(1.25), Usage: domain.Usage{InputTokens: 10}})
	if r.CostSource != domain.CostReported || !approx(*r.CostUSD, 1.25) {
		t.Fatalf("reported run: %+v", r)
	}

	r = e.spend(t, p, now, nil, adapter.Result{Model: "claude-haiku-4", Usage: domain.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000}})
	if !approx(*r.CostUSD, 2+5) || r.CostSource != domain.CostEstimated {
		t.Fatalf("haiku: %+v", r)
	}
	r = e.spend(t, p, now, nil, adapter.Result{Model: "gpt-5-codex", Usage: domain.Usage{OutputTokens: 1_000_000}})
	if !approx(*r.CostUSD, 20) {
		t.Fatalf("gpt: %+v", r)
	}

	// unknown model, or no tokens: cost stays unknown
	r = e.spend(t, p, now, nil, adapter.Result{Model: "mistral-x", Usage: domain.Usage{InputTokens: 10}})
	if r.CostSource != domain.CostNone || r.CostUSD != nil {
		t.Fatalf("unknown model: %+v", r)
	}
	r = e.spend(t, p, now, nil, adapter.Result{Model: "claude-sonnet-4-5"})
	if r.CostSource != domain.CostNone || r.CostUSD != nil {
		t.Fatalf("no tokens: %+v", r)
	}
}

func TestEstimateUsesPriceInForceAtFinish(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	// a longer prefix with a later validity overrides the seeded sonnet price
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO model_prices (id, model, input_per_mtok, effective_at)
		VALUES (gen_random_uuid(), 'claude-sonnet-4', 100, $1)`, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	use := adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 1_000_000}}
	before := e.spend(t, p, t0, nil, use)
	after := e.spend(t, p, t0.Add(2*time.Hour), nil, use)
	if !approx(*before.CostUSD, 3) || !approx(*after.CostUSD, 100) {
		t.Fatalf("before=%v after=%v", *before.CostUSD, *after.CostUSD)
	}
	prices, err := e.svc.ListModelPrices(ctx)
	if err != nil || len(prices) < 5 {
		t.Fatalf("prices: %d %v", len(prices), err)
	}
}

func TestCostSummaryDimensionsAndTimezone(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t) // company tz America/Sao_Paulo (UTC-3)
	eng := e.persona(t, "Eng", nil)
	rev := e.persona(t, "Rev", nil)
	proj, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Infra"})
	if err != nil {
		t.Fatal(err)
	}
	withProj := func(in *service.CreateTaskInput) { in.ProjectID = &proj.ID }

	// 02:00Z on 10/03 is 23:00 on 09/03 in São Paulo; 03:30Z is 00:30 on 10/03.
	e.spend(t, eng, time.Date(2026, 3, 10, 2, 0, 0, 0, time.UTC), withProj, adapter.Result{CostUSD: ptr(1.0), Model: "m-a"})
	e.spend(t, eng, time.Date(2026, 3, 10, 3, 30, 0, 0, time.UTC), nil, adapter.Result{CostUSD: ptr(2.0), Model: "m-a"})
	e.spend(t, rev, time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC), withProj, adapter.Result{CostUSD: ptr(4.0), Model: "m-b"})
	e.spend(t, rev, time.Date(2026, 3, 12, 15, 0, 0, 0, time.UTC), nil, adapter.Result{Model: "claude-sonnet-4-5",
		Usage: domain.Usage{OutputTokens: 100_000}}) // estimated: 1.5
	e.spend(t, rev, time.Date(2026, 4, 2, 15, 0, 0, 0, time.UTC), nil, adapter.Result{CostUSD: ptr(9.0)}) // other month

	e.svc.Now = func() time.Time { return time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) }
	sum, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{GroupBy: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.GroupBy != "day" || len(sum.Rows) != 31 || !approx(sum.TotalUSD, 8.5) || !approx(sum.EstimatedUSD, 1.5) ||
		!approx(sum.ReportedUSD, 7.0) || sum.Runs != 4 {
		t.Fatalf("summary: total=%v est=%v rep=%v runs=%d rows=%d", sum.TotalUSD, sum.EstimatedUSD, sum.ReportedUSD, sum.Runs, len(sum.Rows))
	}
	byKey := map[string]float64{}
	for _, r := range sum.Rows {
		byKey[r.Key] = r.CostUSD
	}
	if !approx(byKey["2026-03-09"], 1.0) || !approx(byKey["2026-03-10"], 6.0) || !approx(byKey["2026-03-12"], 1.5) || byKey["2026-03-11"] != 0 {
		t.Fatalf("days: %v", byKey)
	}
	if sum.Rows[0].Key != "2026-03-01" || sum.Rows[30].Key != "2026-03-31" {
		t.Fatalf("first/last day: %s %s", sum.Rows[0].Key, sum.Rows[30].Key)
	}

	check := func(group string, want map[string]float64) {
		t.Helper()
		s, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{GroupBy: group})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]float64{}
		for _, r := range s.Rows {
			got[r.Label] = r.CostUSD
		}
		if len(got) != len(want) {
			t.Fatalf("%s: rows %v, want %v", group, got, want)
		}
		for k, v := range want {
			if !approx(got[k], v) {
				t.Fatalf("%s: %q = %v, want %v (%v)", group, k, got[k], v, got)
			}
		}
	}
	check("persona", map[string]float64{"Eng": 3.0, "Rev": 5.5})
	check("project", map[string]float64{"Infra": 5.0, "Sem projeto": 3.5})
	check("model", map[string]float64{"m-a": 3.0, "m-b": 4.0, "claude-sonnet-4-5": 1.5})
	check("harness", map[string]float64{"fake": 8.5})

	// explicit range: one day in the company zone
	s, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{GroupBy: "persona",
		From: time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 11, 3, 0, 0, 0, time.UTC)})
	if err != nil || !approx(s.TotalUSD, 6.0) {
		t.Fatalf("range: %+v %v", s.TotalUSD, err)
	}
	// largest spender first
	if s.Rows[0].Label != "Rev" {
		t.Fatalf("order: %+v", s.Rows)
	}

	_, err = e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{GroupBy: "weather"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: time.Now(), To: time.Now().Add(-time.Hour)})
	wantErr(t, err, domain.ErrInvalid)

	ms, err := e.svc.MonthSpend(ctx, e.co.ID)
	if err != nil || !approx(ms, 8.5) {
		t.Fatalf("month spend = %v %v", ms, err)
	}
	mp, err := e.svc.MonthSpendPersona(ctx, e.co.ID, eng.ID)
	if err != nil || !approx(mp, 3.0) {
		t.Fatalf("persona month spend = %v %v", mp, err)
	}
	st, err := e.svc.Dashboard(ctx, e.co.ID)
	if err != nil || !approx(st.MonthCostUSD, 8.5) || st.BudgetState != "none" {
		t.Fatalf("dashboard: %+v %v", st, err)
	}
}

func countVerb(t *testing.T, e *env, verb string) int {
	t.Helper()
	acts, err := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range acts {
		if a.Verb == verb {
			n++
		}
	}
	return n
}

func TestPersonaBudgetHardStopAndAlertsOncePerMonth(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(1000)) }) // US$ 10
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	ch, unsub := e.broker.Subscribe(events.CompanyTopic(e.co.ID.String()), 128)
	defer unsub()

	e.spend(t, p, march, nil, usd(7.9))
	if countVerb(t, e, "budget.warning") != 0 {
		t.Fatal("warning below 80%")
	}
	e.spend(t, p, march, nil, usd(0.2)) // 8.1 = 81%
	e.spend(t, p, march, nil, usd(0.1)) // still warning
	if n := countVerb(t, e, "budget.warning"); n != 1 {
		t.Fatalf("warnings = %d, want 1 (once per month)", n)
	}
	b, err := e.svc.PersonaBudget(ctx, e.co.ID, p.ID)
	if err != nil || b.State != service.BudgetWarning || b.Percent < 80 || b.Percent > 85 {
		t.Fatalf("budget: %+v %v", b, err)
	}

	// the run that blows the budget finishes normally; the next attribution does not open a run
	e.spend(t, p, march, nil, usd(2.0)) // 10.3
	if n := countVerb(t, e, "budget.exceeded"); n != 1 {
		t.Fatalf("exceeded = %d", n)
	}
	e.svc.Now = func() time.Time { return march }
	tv := e.task(t, service.CreateTaskInput{Title: "mais uma", PersonaID: &p.ID})
	runsBefore, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{})
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	wantErr(t, err, domain.ErrConflict)
	runsAfter, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{})
	if len(runsAfter) != len(runsBefore) {
		t.Fatalf("a run was opened: %d -> %d", len(runsBefore), len(runsAfter))
	}
	got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if got.Status != domain.PersonaPaused || got.PauseReason == "" {
		t.Fatalf("persona: %s %q", got.Status, got.PauseReason)
	}
	if n := countVerb(t, e, "budget.exceeded"); n != 1 {
		t.Fatalf("exceeded after hard stop = %d, want 1", n)
	}
	sawExceeded := false
	for len(ch) > 0 {
		if m := <-ch; m.Type == "budget.exceeded" {
			sawExceeded = true
		}
	}
	if !sawExceeded {
		t.Fatal("no budget.exceeded event on the broker")
	}

	// resume with budget still blown: still stopped, still a single alert
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaActive)}); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); g.PauseReason != "" {
		t.Fatalf("pause reason kept after resume: %q", g.PauseReason)
	}
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if n := countVerb(t, e, "budget.exceeded"); n != 1 {
		t.Fatalf("exceeded = %d, want 1", n)
	}

	// raising the budget releases it
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{
		Status: ptr(domain.PersonaActive), BudgetMonthlyCents: ptr(int64(5000))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign)); err != nil {
		t.Fatalf("after raising budget: %v", err)
	}

	// a new month starts clean and alerts again
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{BudgetMonthlyCents: ptr(int64(1000))}); err != nil {
		t.Fatal(err)
	}
	e.svc.Now = func() time.Time { return march }
	_, _ = e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, mustActiveRun(t, e, p))
	april := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	e.spend(t, p, april, nil, usd(8.5))
	if n := countVerb(t, e, "budget.warning"); n != 2 {
		t.Fatalf("warnings after new month = %d, want 2", n)
	}
}

func mustActiveRun(t *testing.T, e *env, p domain.Persona) domain.ID {
	t.Helper()
	runs, err := e.svc.ListRuns(context.Background(), e.co.ID, service.RunFilter{PersonaID: &p.ID,
		Statuses: []domain.RunStatus{domain.RunQueued, domain.RunRunning}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("active runs: %d %v", len(runs), err)
	}
	return runs[0].ID
}

func TestCompanyBudgetHardStop(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := e.persona(t, "A", nil)
	b := e.persona(t, "B", nil)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return at }

	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(-1))); err == nil {
		t.Fatal("negative budget accepted")
	}
	co, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(500)))
	if err != nil || co.BudgetMonthlyCents == nil || *co.BudgetMonthlyCents != 500 {
		t.Fatalf("set budget: %+v %v", co, err)
	}
	e.spend(t, a, at, nil, usd(6.0))
	if countVerb(t, e, "budget.exceeded") != 1 {
		t.Fatal("company exceeded not recorded")
	}
	tv := e.task(t, service.CreateTaskInput{PersonaID: &b.ID}) // another persona, no budget of its own
	_, err = e.svc.CheckoutRun(ctx, wakeOf(b, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, b.ID.String()); g.Status != domain.PersonaPaused {
		t.Fatalf("persona %s", g.Status)
	}
	st, err := e.svc.CompanyBudget(ctx, e.co.ID)
	if err != nil || st.State != service.BudgetExceeded || st.Percent < 119 {
		t.Fatalf("company budget: %+v %v", st, err)
	}
	d, _ := e.svc.Dashboard(ctx, e.co.ID)
	if d.BudgetState != service.BudgetExceeded || d.MonthBudgetCents == nil {
		t.Fatalf("dashboard: %+v", d)
	}

	// removing the ceiling frees everyone
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, b.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaActive)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(b, tv, domain.TriggerAssign)); err != nil {
		t.Fatal(err)
	}
}
