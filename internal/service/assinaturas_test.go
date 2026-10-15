package service_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (e *env) sub(t *testing.T, mod func(*service.CreateSubscriptionInput)) domain.Subscription {
	t.Helper()
	in := service.CreateSubscriptionInput{Name: "Plano", Agent: "fake", MonthlyUSD: 31}
	if mod != nil {
		mod(&in)
	}
	s, err := e.svc.CreateSubscription(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

var (
	// company time zone is America/Sao_Paulo (UTC-3): March 2026 in local time.
	marchFrom = time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)
	marchTo   = time.Date(2026, 4, 1, 3, 0, 0, 0, time.UTC)
)

// Preço novo vale dali em diante: o custo já gravado (estimado e de lista) nunca é recalculado,
// e a tabela de preços não aceita UPDATE nem DELETE.
func TestPriceHistoryIsAppendOnlyAndCostIsNeverRecomputed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	use := adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 1_000_000}}

	first := e.spend(t, p, t0, nil, use)
	if first.ListCostUSD == nil || !near(*first.ListCostUSD, 3) || !near(*first.CostUSD, 3) {
		t.Fatalf("first run: cost=%v list=%v", first.CostUSD, first.ListCostUSD)
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO model_prices (id, model, input_per_mtok, effective_at)
		VALUES (gen_random_uuid(), 'claude-sonnet-4', 100, $1)`, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// reading again, summarizing, even a run finishing later with the old clock: the stored cost stays
	got, err := e.svc.GetRun(ctx, e.co.ID, first.ID)
	if err != nil || got.CostUSD == nil || !near(*got.CostUSD, 3) || !near(*got.ListCostUSD, 3) {
		t.Fatalf("stored cost changed: %+v %v", got, err)
	}
	sum, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: marchFrom, To: marchTo, GroupBy: service.GroupModel})
	if err != nil || !near(sum.TotalUSD, 3) {
		t.Fatalf("summary after the new price: %v %v", sum.TotalUSD, err)
	}
	// a run that finishes before the new price takes effect still uses the old one
	before := e.spend(t, p, t0.Add(30*time.Minute), nil, use)
	after := e.spend(t, p, t0.Add(2*time.Hour), nil, use)
	if !near(*before.CostUSD, 3) || !near(*after.CostUSD, 100) || !near(*after.ListCostUSD, 100) {
		t.Fatalf("before=%v after=%v", *before.CostUSD, *after.CostUSD)
	}

	if _, err := e.svc.Pool.Exec(ctx, `UPDATE model_prices SET input_per_mtok = 1`); err == nil {
		t.Fatal("UPDATE on model_prices must be refused")
	}
	if _, err := e.svc.Pool.Exec(ctx, `DELETE FROM model_prices`); err == nil {
		t.Fatal("DELETE on model_prices must be refused")
	}
}

func TestFourCostsAndPaidProration(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)                                                                       // US$ 31 a month: US$ 1 a day in March
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) } // the plan exists since March
	sub := e.sub(t, nil)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.SubscriptionID = &sub.ID; in.Model = "claude-sonnet-4-5" })

	d1 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	// reported by the harness (list cost still computed: 1M input = 3.0)
	a := e.spend(t, p, d1, nil, adapter.Result{Model: "claude-sonnet-4-5", CostUSD: ptr(0.5), Usage: domain.Usage{InputTokens: 1_000_000}})
	// estimated (3 + 3 = 6.0)
	b := e.spend(t, p, d1, nil, adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 1_000_000, OutputTokens: 200_000}})
	c := e.spend(t, p, d2, nil, adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 1_000_000}})
	if a.SubscriptionID == nil || *a.SubscriptionID != sub.ID {
		t.Fatalf("run not charged to the subscription: %+v", a.SubscriptionID)
	}
	if a.CostSource != domain.CostReported || !near(*a.ListCostUSD, 3) || b.CostSource != domain.CostEstimated || !near(*b.ListCostUSD, 6) {
		t.Fatalf("costs: a=%+v b=%+v", a, b)
	}

	e.svc.Now = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) } // March is over
	sum, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: marchFrom, To: marchTo, GroupBy: service.GroupDay})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]service.CostRow{}
	for _, r := range sum.Rows {
		rows[r.Key] = r
	}
	r1 := rows["2026-03-10"]
	if !near(r1.ReportedUSD, 0.5) || !near(r1.EstimatedUSD, 6) || !near(r1.ListUSD, 9) || !near(r1.PaidUSD, 1) || r1.Unused {
		t.Fatalf("day 10: %+v", r1)
	}
	if r := rows["2026-03-20"]; !near(r.ListUSD, 3) || !near(r.PaidUSD, 1) {
		t.Fatalf("day 20: %+v", r)
	}
	if r := rows["2026-03-11"]; !r.Unused || !near(r.IdleUSD, 1) || r.PaidUSD != 0 {
		t.Fatalf("a day without use: %+v", r)
	}
	if !near(sum.PaidUSD, 2) || !near(sum.IdleUSD, 29) || !near(sum.PaidUSD+sum.IdleUSD, 31) {
		t.Fatalf("the month must close with the fee: paid=%v idle=%v", sum.PaidUSD, sum.IdleUSD)
	}
	if len(sum.Subscriptions) != 1 || !near(sum.Subscriptions[0].PaidUSD, 2) || sum.Subscriptions[0].Runs != 3 {
		t.Fatalf("subscriptions: %+v", sum.Subscriptions)
	}

	// the run's share is proportional to its list cost within the day: a = 1 x 3/9, b = 1 x 6/9
	byPersona, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: marchFrom, To: marchTo, GroupBy: service.GroupPersona})
	if err != nil || len(byPersona.Rows) != 1 || !near(byPersona.Rows[0].PaidUSD, 2) {
		t.Fatalf("by persona: %+v %v", byPersona.Rows, err)
	}
	byModel, _ := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: marchFrom, To: marchTo, GroupBy: service.GroupModel})
	if len(byModel.Rows) != 1 || !near(byModel.Rows[0].PaidUSD, 2) || !near(byModel.Rows[0].ListUSD, 12) {
		t.Fatalf("by model: %+v", byModel.Rows)
	}
	_ = c

	// mid-month the accrual stops at today: 15 days of the fee
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC) }
	mid, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: marchFrom, To: marchTo, GroupBy: service.GroupDay})
	if err != nil || !near(mid.PaidUSD+mid.IdleUSD, 15) || !near(mid.PaidUSD, 1) {
		t.Fatalf("mid month: paid=%v idle=%v %v", mid.PaidUSD, mid.IdleUSD, err)
	}
}

func TestPaidIsZeroWithoutSubscription(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	e.spend(t, p, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), nil, usd(2))
	e.svc.Now = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }
	sum, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: marchFrom, To: marchTo})
	if err != nil || sum.PaidUSD != 0 || sum.IdleUSD != 0 || len(sum.Subscriptions) != 0 {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestSubscriptionCRUD(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bad := []service.CreateSubscriptionInput{
		{Name: " ", Agent: "claude"},
		{Name: "x", Agent: "gemini"},
		{Name: "x", Agent: "claude", MonthlyUSD: -1},
		{Name: "x", Agent: "claude", WeeklyLimitUSD: ptr(0.0)},
		{Name: "x", Agent: "claude", WeeklyGuardPct: 101},
	}
	for i, in := range bad {
		if _, err := e.svc.CreateSubscription(ctx, domain.HumanActor, e.co.ID, in); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		} else {
			wantErr(t, err, domain.ErrInvalid)
		}
	}
	sub := e.sub(t, func(in *service.CreateSubscriptionInput) {
		in.Name, in.Agent, in.ModelPrefixes = "Claude Max", "claude", []string{" claude-opus ", "claude-opus", ""}
	})
	if sub.WeeklyGuardPct != 90 || len(sub.ModelPrefixes) != 1 || sub.ModelPrefixes[0] != "claude-opus" || !sub.Active {
		t.Fatalf("defaults: %+v", sub)
	}
	_, err := e.svc.CreateSubscription(ctx, domain.HumanActor, e.co.ID, service.CreateSubscriptionInput{Name: "Claude Max", Agent: "claude"})
	wantErr(t, err, domain.ErrConflict)
	if st, err := e.svc.GetSubscription(ctx, e.co.ID, "Claude Max"); err != nil || st.ID != sub.ID {
		t.Fatalf("get by name: %+v %v", st, err)
	}

	got, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, sub.ID, service.UpdateSubscriptionInput{
		MonthlyUSD: ptr(200.0), WeeklyLimitUSD: ptr(50.0), WeeklyGuardPct: ptr(80), Active: ptr(false)})
	if err != nil || got.MonthlyUSD != 200 || *got.WeeklyLimitUSD != 50 || got.Active || got.EndedAt == nil {
		t.Fatalf("update: %+v %v", got, err)
	}
	got, err = e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, sub.ID, service.UpdateSubscriptionInput{Active: ptr(true), ClearWeeklyLimit: true})
	if err != nil || !got.Active || got.EndedAt != nil || got.WeeklyLimitUSD != nil {
		t.Fatalf("reactivate: %+v %v", got, err)
	}
	if n := countVerb(t, e, "subscription.created") + countVerb(t, e, "subscription.updated"); n != 3 {
		t.Fatalf("activity events = %d, want 3", n)
	}

	// persona link: other company's subscription is rejected, unlink works
	other, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.svc.CreateSubscription(ctx, domain.HumanActor, other.ID, service.CreateSubscriptionInput{Name: "Plano", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	p := e.persona(t, "Eng", nil)
	_, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{SubscriptionID: &foreign.ID})
	wantErr(t, err, domain.ErrInvalid)
	if p, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{SubscriptionID: &sub.ID}); err != nil || p.SubscriptionID == nil {
		t.Fatalf("link: %+v %v", p, err)
	}
	if p, err = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{ClearSubscription: true}); err != nil || p.SubscriptionID != nil {
		t.Fatalf("unlink: %+v %v", p, err)
	}

	// a subscription with runs charged to it cannot be deleted; an unused one can
	used := e.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Usada" })
	pu := e.persona(t, "Usa", func(in *service.HirePersonaInput) { in.SubscriptionID = &used.ID })
	e.spend(t, pu, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), nil, usd(1))
	wantErr(t, e.svc.DeleteSubscription(ctx, domain.HumanActor, e.co.ID, used.ID), domain.ErrConflict)
	if err := e.svc.DeleteSubscription(ctx, domain.HumanActor, e.co.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSubscription(ctx, e.co.ID, sub.ID.String()); err == nil {
		t.Fatal("deleted subscription still found")
	}
}

func TestWeeklyGuardBlocksNewRuns(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	sub := e.sub(t, func(in *service.CreateSubscriptionInput) { in.WeeklyLimitUSD, in.WeeklyGuardPct = ptr(10.0), 80 }) // trips at US$ 8
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.SubscriptionID = &sub.ID })
	free := e.persona(t, "Livre", nil) // no subscription: never blocked
	tue := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	e.spend(t, p, tue, nil, usd(5))
	e.spend(t, p, tue, nil, usd(2.5)) // 7.5: below the guard (8): still opens
	e.spend(t, p, tue, nil, usd(1.0)) // 8.5: this one opened, the next will not
	st, err := e.svc.GetSubscription(ctx, e.co.ID, sub.ID.String())
	if err != nil || !st.Blocked || !near(st.WeekUsageUSD, 8.5) || !near(st.WeekPercent, 85) {
		t.Fatalf("status: %+v %v", st, err)
	}

	e.svc.Now = func() time.Time { return tue }
	tv := e.task(t, service.CreateTaskInput{Title: "bloqueada", PersonaID: &p.ID})
	runsBefore, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{})
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrSubscriptionGuard)
	wantErr(t, err, domain.ErrConflict)
	if errIsBudget(err) {
		t.Fatal("the guard must have its own reason, not the budget one")
	}
	if runsAfter, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{}); len(runsAfter) != len(runsBefore) {
		t.Fatal("a run was opened under the guard")
	}
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrSubscriptionGuard)
	if n := countVerb(t, e, "subscription.guard_blocked"); n != 1 {
		t.Fatalf("guard events = %d, want 1 (once per week)", n)
	}
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); g.Status == domain.PersonaPaused {
		t.Fatal("the guard must not pause the persona")
	}
	if blocked, err := e.svc.GuardedSubscriptions(ctx, e.co.ID); err != nil || len(blocked) != 1 || blocked[0].ID != sub.ID {
		t.Fatalf("inbox candidates: %+v %v", blocked, err)
	}
	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.GuardedSubscriptions) != 1 || in.Count() < 1 {
		t.Fatalf("inbox: %+v %v", in.GuardedSubscriptions, err)
	}

	// a persona without a subscription is not affected
	ftv := e.task(t, service.CreateTaskInput{Title: "livre", PersonaID: &free.ID})
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(free, ftv, domain.TriggerAssign)); err != nil {
		t.Fatalf("persona without subscription: %v", err)
	}

	// raising the weekly limit lifts the block at once
	if _, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, sub.ID, service.UpdateSubscriptionInput{WeeklyLimitUSD: ptr(100.0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign)); err != nil {
		t.Fatalf("after raising the limit: %v", err)
	}
	if _, err := e.svc.UpdateSubscription(ctx, domain.HumanActor, e.co.ID, sub.ID, service.UpdateSubscriptionInput{WeeklyLimitUSD: ptr(10.0)}); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, mustActiveRun(t, e, p))

	// the next week starts clean (Monday 16/03 in São Paulo)
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC) }
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign)); err != nil {
		t.Fatalf("next week: %v", err)
	}
}

func errIsBudget(err error) bool { return errors.Is(err, domain.ErrBudgetExceeded) }

func TestWeeklyGuardOnlyCoversTheSubscriptionModels(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	sub := e.sub(t, func(in *service.CreateSubscriptionInput) {
		in.WeeklyLimitUSD, in.WeeklyGuardPct, in.ModelPrefixes = ptr(1.0), 100, []string{"claude-opus"}
	})
	covered := e.persona(t, "Opus", func(in *service.HirePersonaInput) { in.SubscriptionID, in.Model = &sub.ID, "claude-opus-4-5" })
	other := e.persona(t, "Sonnet", func(in *service.HirePersonaInput) { in.SubscriptionID, in.Model = &sub.ID, "claude-sonnet-4-5" })
	tue := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	r := e.spend(t, covered, tue, nil, usd(2))
	if r.SubscriptionID == nil || *r.SubscriptionID != sub.ID {
		t.Fatalf("opus run not covered: %+v", r.SubscriptionID)
	}
	// the sonnet persona is linked, but its model is outside the plan: not covered, not blocked, not counted
	r = e.spend(t, other, tue, nil, usd(2))
	if r.SubscriptionID != nil {
		t.Fatalf("sonnet run charged to the plan: %v", r.SubscriptionID)
	}
	tv := e.task(t, service.CreateTaskInput{Title: "mais opus", PersonaID: &covered.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(covered, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrSubscriptionGuard)
	// a manual model on the task moves the run out of the plan
	tv2 := e.task(t, service.CreateTaskInput{Title: "manual", PersonaID: &covered.ID, Model: "claude-sonnet-4-5"})
	run, err := e.svc.CheckoutRun(ctx, wakeOf(covered, tv2, domain.TriggerAssign))
	if err != nil || run.SubscriptionID != nil || run.Model != "claude-sonnet-4-5" {
		t.Fatalf("manual model: %+v %v", run, err)
	}
}

func TestModelSourcePrecedence(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	withModel := e.persona(t, "ComModelo", func(in *service.HirePersonaInput) { in.Model = "m-persona" })
	noModel := e.persona(t, "SemModelo", func(in *service.HirePersonaInput) { in.Model = "" })
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) }

	check := func(p domain.Persona, taskModel, wantModel string, wantSrc domain.ModelSource) {
		t.Helper()
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID, Model: taskModel})
		run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
		if err != nil {
			t.Fatal(err)
		}
		if run.Model != wantModel || run.ModelSource != wantSrc {
			t.Fatalf("persona %s task %q: model=%q source=%q, want %q/%q", p.Name, taskModel, run.Model, run.ModelSource, wantModel, wantSrc)
		}
		got, _ := e.svc.GetRun(ctx, e.co.ID, run.ID)
		if got.ModelSource != wantSrc {
			t.Fatalf("stored source = %q", got.ModelSource)
		}
		if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID); err != nil {
			t.Fatal(err)
		}
	}
	check(withModel, "m-task", "m-task", domain.ModelFromTask)
	check(withModel, "", "m-persona", domain.ModelFromPersona)
	check(noModel, "", "", domain.ModelFromHarnessDefault)
	check(noModel, "m-task", "m-task", domain.ModelFromTask)

	// the manual model can be set and cleared on an existing task
	tv := e.task(t, service.CreateTaskInput{Title: "editável"})
	tv, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Model: ptr("m-x")})
	if err != nil || tv.Model != "m-x" {
		t.Fatalf("set task model: %+v %v", tv, err)
	}
	tv, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Model: ptr("")})
	if err != nil || tv.Model != "" {
		t.Fatalf("clear task model: %+v %v", tv, err)
	}
}
