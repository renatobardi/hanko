package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

func activityOf(t *testing.T, e *env, verb string) []domain.ActivityEvent {
	t.Helper()
	acts, err := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.ActivityEvent
	for _, a := range acts {
		if a.Verb == verb {
			out = append(out, a)
		}
	}
	return out
}

func dataOf(t *testing.T, a domain.ActivityEvent) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(a.Data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// ---------- 1. budget reservation ----------

// openRuns tries to check out one run per task and counts how many opened.
func openRuns(t *testing.T, e *env, p domain.Persona, tasks []service.TaskView) (opened int) {
	t.Helper()
	for _, tv := range tasks {
		_, err := e.svc.CheckoutRun(context.Background(), wakeOf(p, tv, domain.TriggerAssign))
		switch {
		case err == nil:
			opened++
		case errors.Is(err, domain.ErrBudgetExceeded):
		default:
			t.Fatalf("checkout: %v", err)
		}
	}
	return opened
}

func fiveTasks(t *testing.T, e *env, p domain.Persona) []service.TaskView {
	t.Helper()
	var tasks []service.TaskView
	for i := 0; i < 5; i++ {
		tasks = append(tasks, e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID}))
	}
	return tasks
}

func TestBudgetReservationCapsOpenRuns(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		spent    float64
		max, min int
	}{
		{"cabe uma reserva", 8.5, 1, 1},
		{"gasto 9,99 e teto 10", 9.99, 1, 0}, // 9,99 + reserva 1,00 já passa do teto
		{"sobra para duas", 7.9, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			spender := e.persona(t, "Gasta", nil)
			worker := e.persona(t, "Trabalha", nil)
			e.spend(t, spender, march, nil, usd(tc.spent))
			if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(1000))); err != nil {
				t.Fatal(err)
			}
			e.svc.Now = func() time.Time { return march }
			got := openRuns(t, e, worker, fiveTasks(t, e, worker))
			if got > tc.max || got < tc.min {
				t.Fatalf("runs abertas = %d, quer entre %d e %d", got, tc.min, tc.max)
			}
		})
	}
}

func TestBudgetReservationConcurrentCheckouts(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	spender := e.persona(t, "Gasta", nil)
	e.spend(t, spender, march, nil, usd(8.5))
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(1000))); err != nil {
		t.Fatal(err)
	}
	// five personas, one task each, all checking out at once: only one reservation fits
	var wg sync.WaitGroup
	var mu sync.Mutex
	opened := 0
	for i := 0; i < 5; i++ {
		p := e.persona(t, "P"+string(rune('a'+i)), nil)
		tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				opened++
			} else if !errors.Is(err, domain.ErrBudgetExceeded) {
				t.Errorf("checkout: %v", err)
			}
		}()
	}
	wg.Wait()
	if opened != 1 {
		t.Fatalf("runs abertas = %d, want 1", opened)
	}
}

func TestBudgetReservationUsesRecentAverage(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	p := e.persona(t, "Cara", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(1000)) })
	e.spend(t, p, march, nil, usd(2.0))
	e.spend(t, p, march, nil, usd(2.0)) // spent 4.00, average cost 2.00 (floor is 1.00)
	e.svc.Now = func() time.Time { return march }
	// 4 + 2 = 6, 4 + 4 = 8, 4 + 6 = 10 fit; 4 + 8 does not. With the floor alone, 6 would fit.
	if got := openRuns(t, e, p, fiveTasks(t, e, p)); got != 3 {
		t.Fatalf("runs abertas = %d, want 3", got)
	}
	// the persona is not paused: a refusal by reservation is transient
	if got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.Status == domain.PersonaPaused {
		t.Fatal("reservation refusal must not pause the persona")
	}
}

func TestBudgetReservationFloorIsConfigurable(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	e.svc.RunReserveUSD = 4
	spender := e.persona(t, "Gasta", nil)
	worker := e.persona(t, "Trabalha", nil)
	e.spend(t, spender, march, nil, usd(1.0))
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(1000))); err != nil {
		t.Fatal(err)
	}
	e.svc.Now = func() time.Time { return march }
	// 1 + 4 = 5, 1 + 8 = 9 fit; 1 + 12 does not
	if got := openRuns(t, e, worker, fiveTasks(t, e, worker)); got != 2 {
		t.Fatalf("runs abertas = %d, want 2", got)
	}
}

func TestBudgetExceededCancelsRunningRuns(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for _, scope := range []string{"persona", "company"} {
		t.Run(scope, func(t *testing.T) {
			e := newEnv(t)
			p := e.persona(t, "Eng", nil)
			other := e.persona(t, "Outra", nil)
			if scope == "persona" {
				if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{BudgetMonthlyCents: ptr(int64(500))}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(500))); err != nil {
				t.Fatal(err)
			}
			e.svc.Now = func() time.Time { return march }
			start := func(who domain.Persona) domain.Run {
				tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &who.ID})
				run, err := e.svc.CheckoutRun(ctx, wakeOf(who, tv, domain.TriggerAssign))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
					t.Fatal(err)
				}
				return run
			}
			finishing, sibling, stranger := start(p), start(p), start(other)
			subs := map[domain.ID]<-chan events.Message{}
			for _, r := range []domain.Run{sibling, stranger} {
				ch, unsub := e.broker.Subscribe(events.RunTopic(r.ID.String()), 16)
				defer unsub()
				subs[r.ID] = ch
			}
			if _, err := e.svc.FinishRun(ctx, e.co.ID, finishing.ID, service.FinishRunInput{Status: domain.RunDone,
				Result: adapter.Result{CostUSD: ptr(5.5)}}); err != nil {
				t.Fatal(err)
			}
			cancelled := func(r domain.Run) bool {
				for len(subs[r.ID]) > 0 {
					if m := <-subs[r.ID]; m.Type == "run.cancel" {
						return true
					}
				}
				return false
			}
			if !cancelled(sibling) {
				t.Fatal("o orçamento estourou mas a run em execução do alvo não recebeu run.cancel")
			}
			if got := cancelled(stranger); got != (scope == "company") {
				t.Fatalf("run de outra persona cancelada = %v, em escopo %s", got, scope)
			}
		})
	}
}

// ---------- 3. gates only on own tasks ----------

func TestPersonaRequestsGateOnlyOnOwnTask(t *testing.T) {
	ctx := context.Background()
	e, _, _, eng, qa := orgFixture(t)
	mine := e.task(t, service.CreateTaskInput{Title: "minha", PersonaID: &eng.ID})
	theirs := e.task(t, service.CreateTaskInput{Title: "alheia", PersonaID: &qa.ID})
	unowned := e.task(t, service.CreateTaskInput{Title: "sem dono"})
	ea := domain.PersonaActor(eng.ID)

	_, err := e.svc.RequestGate(ctx, ea, e.co.ID, service.RequestGateInput{Title: "trava a task do colega", TaskID: &theirs.ID})
	wantErr(t, err, domain.ErrNotAllowed)
	_, err = e.svc.RequestGate(ctx, ea, e.co.ID, service.RequestGateInput{Title: "trava a task sem dono", TaskID: &unowned.ID})
	wantErr(t, err, domain.ErrNotAllowed)
	if got := taskStatus(t, e, theirs); got.Status != domain.TaskTodo {
		t.Fatalf("task alheia = %s, deve ficar todo", got.Status)
	}
	if st := personaStatus(t, e, qa); st == domain.PersonaAwaitingGate {
		t.Fatal("a dona da task não pode ser posta em espera por outra persona")
	}
	if _, err := e.svc.RequestGate(ctx, ea, e.co.ID, service.RequestGateInput{Title: "na minha", TaskID: &mine.ID}); err != nil {
		t.Fatalf("na própria task: %v", err)
	}
	if _, err := e.svc.RequestGate(ctx, ea, e.co.ID, service.RequestGateInput{Title: "sem task"}); err != nil {
		t.Fatalf("sem task: %v", err)
	}

	// the task of the persona's own run is allowed even after the task moved to someone else;
	// someone else's run is not
	run, err := e.svc.CheckoutRun(ctx, wakeOf(qa, theirs, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.RequestGate(ctx, ea, e.co.ID, service.RequestGateInput{Title: "run alheia", TaskID: &theirs.ID, RunID: &run.ID})
	wantErr(t, err, domain.ErrNotAllowed)
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, theirs.ID, service.UpdateTaskInput{PersonaID: &eng.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RequestGate(ctx, domain.PersonaActor(qa.ID), e.co.ID, service.RequestGateInput{Title: "minha run", TaskID: &theirs.ID, RunID: &run.ID}); err != nil {
		t.Fatalf("na task da própria run: %v", err)
	}
}

func TestGateDecisionWakesCurrentOwner(t *testing.T) {
	ctx := context.Background()
	e, _, _, eng, qa := orgFixture(t)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &eng.ID})
	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(eng.ID), e.co.ID, service.RequestGateInput{Title: "ok?", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	// while the gate waits, the task is handed to someone else
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{PersonaID: &qa.ID}); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].PersonaID != qa.ID || e.jobs.wakes[0].Trigger != domain.TriggerGate || e.jobs.wakes[0].TaskID != tv.ID {
		t.Fatalf("wakes = %+v, want one for the current owner (qa)", e.jobs.wakes)
	}
}

// ---------- 5. a persona changes only its own tasks ----------

func TestPersonaChangesOnlyOwnedOrSubtreeTasks(t *testing.T) {
	ctx := context.Background()
	e, _, cto, eng, qa := orgFixture(t)
	engT := e.task(t, service.CreateTaskInput{Title: "do eng", PersonaID: &eng.ID})
	qaT := e.task(t, service.CreateTaskInput{Title: "do qa", PersonaID: &qa.ID})
	free := e.task(t, service.CreateTaskInput{Title: "livre"})
	title := "outro"
	done := domain.TaskDone
	up := func(who domain.Persona, tv service.TaskView, in service.UpdateTaskInput) error {
		_, err := e.svc.UpdateTask(ctx, domain.PersonaActor(who.ID), e.co.ID, tv.ID, in)
		return err
	}

	// a peer's task: nothing changes, not the title, not the status, not even unassigning
	wantErr(t, up(eng, qaT, service.UpdateTaskInput{Title: &title}), domain.ErrNotAllowed)
	wantErr(t, up(eng, qaT, service.UpdateTaskInput{Status: &done}), domain.ErrNotAllowed)
	wantErr(t, up(eng, qaT, service.UpdateTaskInput{Unassign: true}), domain.ErrNotAllowed)
	wantErr(t, up(eng, qaT, service.UpdateTaskInput{PersonaID: &eng.ID}), domain.ErrNotAllowed)
	// a superior's task too
	wantErr(t, up(eng, e.task(t, service.CreateTaskInput{Title: "do cto", PersonaID: &cto.ID}), service.UpdateTaskInput{Title: &title}), domain.ErrNotAllowed)
	if got := taskStatus(t, e, qaT); got.Title != "do qa" || got.Status != domain.TaskTodo || got.PersonaID == nil || *got.PersonaID != qa.ID {
		t.Fatalf("task alheia mudou: %+v", got)
	}

	// allowed: its own, a subordinate's, an unowned one
	if err := up(eng, engT, service.UpdateTaskInput{Title: &title}); err != nil {
		t.Fatalf("própria: %v", err)
	}
	if err := up(cto, qaT, service.UpdateTaskInput{Title: &title}); err != nil {
		t.Fatalf("de subordinada: %v", err)
	}
	if err := up(eng, free, service.UpdateTaskInput{Title: &title}); err != nil {
		t.Fatalf("sem dono: %v", err)
	}
	// humans are unaffected
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, qaT.ID, service.UpdateTaskInput{Unassign: true}); err != nil {
		t.Fatalf("humano: %v", err)
	}
}

// ---------- 6. require_review cannot be bypassed with cancelled ----------

func TestRequireReviewAlsoCoversCancel(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, func(in *service.HirePersonaInput) { in.RequireReview = true })
	pa := domain.PersonaActor(p.ID)
	got, err := e.svc.UpdateTask(ctx, pa, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskCancelled)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskInReview || got.ClosedAt != nil {
		t.Fatalf("cancelar sem gate: task = %s closed=%v, want in_review aberta", got.Status, got.ClosedAt)
	}
	gates, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &tv.ID})
	if len(gates) != 1 || gates[0].Kind != domain.GateTaskReview || gates[0].Status != domain.GatePending || gates[0].RequestedStatus != domain.TaskCancelled {
		t.Fatalf("gates: %+v", gates)
	}
	if run2, err := e.svc.GetRun(ctx, e.co.ID, run.ID); err != nil || !run2.Status.Active() {
		t.Fatalf("a run não deve ser cancelada antes da decisão: %+v %v", run2.Status, err)
	}
	// done after cancelled keeps the first request
	if _, err := e.svc.UpdateTask(ctx, pa, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	if gates, _ = e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &tv.ID}); len(gates) != 1 {
		t.Fatalf("gates duplicados: %+v", gates)
	}
	finishOK(t, e, run)

	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gates[0].ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if got = taskStatus(t, e, tv); got.Status != domain.TaskCancelled || got.ClosedAt == nil {
		t.Fatalf("aprovado: task = %s closed=%v, want cancelled", got.Status, got.ClosedAt)
	}

	// rejected: back to todo, still open
	e2, p2, tv2, run2 := gateFixture(t, func(in *service.HirePersonaInput) { in.RequireReview = true })
	if _, err := e2.svc.UpdateTask(ctx, domain.PersonaActor(p2.ID), e2.co.ID, tv2.ID, service.UpdateTaskInput{Status: ptr(domain.TaskCancelled)}); err != nil {
		t.Fatal(err)
	}
	finishOK(t, e2, run2)
	g2, _ := e2.svc.ListGates(ctx, e2.co.ID, service.GateFilter{TaskID: &tv2.ID})
	if _, err := e2.svc.DecideGate(ctx, domain.HumanActor, e2.co.ID, g2[0].ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, e2, tv2); got.Status != domain.TaskTodo {
		t.Fatalf("rejeitado: task = %s, want todo", got.Status)
	}
}

// ---------- 7. a model without a price still counts ----------

func TestUnknownModelUsesFallbackPriceAndNotesOncePerMonth(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(1000)) })
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	use := adapter.Result{Model: "o3", Usage: domain.Usage{InputTokens: 100_000, OutputTokens: 20_000}}

	r := e.spend(t, p, march, nil, use)
	// opus prices: 100k * 15 + 20k * 75 per million = 1.5 + 1.5
	if r.CostSource != domain.CostEstimated || r.CostUSD == nil || !approx(*r.CostUSD, 3.0) {
		t.Fatalf("o3: %+v", r)
	}
	e.spend(t, p, march, nil, use)
	if n := len(activityOf(t, e, "cost.unknown_model")); n != 1 {
		t.Fatalf("cost.unknown_model = %d, want 1 (once per model and month)", n)
	}
	if spent, _ := e.svc.MonthSpendPersona(ctx, e.co.ID, p.ID); !approx(spent, 6.0) {
		t.Fatalf("o gasto de um modelo sem preço tem de consumir orçamento: %v", spent)
	}
	e.spend(t, p, march, nil, adapter.Result{Model: "gemini-9", Usage: domain.Usage{InputTokens: 10}})
	if n := len(activityOf(t, e, "cost.unknown_model")); n != 2 {
		t.Fatalf("another model: %d, want 2", n)
	}
	e.spend(t, p, time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC), nil, use)
	acts := activityOf(t, e, "cost.unknown_model")
	if len(acts) != 3 {
		t.Fatalf("next month: %d, want 3", len(acts))
	}
	if d := dataOf(t, acts[0]); d["model"] != "o3" || d["month"] != "2026-04" {
		t.Fatalf("data: %v", d)
	}
	// a priced model never raises the notice
	e.spend(t, p, time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC), nil, adapter.Result{Model: "claude-sonnet-4-5", Usage: domain.Usage{InputTokens: 10}})
	if n := len(activityOf(t, e, "cost.unknown_model")); n != 3 {
		t.Fatalf("priced model raised a notice: %d", n)
	}
}

// ---------- 8. budget pause and resume ----------

func TestBudgetPauseIsRecordedAndLiftedWhenCeilingRises(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.BudgetMonthlyCents = ptr(int64(1000)) })
	for i := 0; i < 9; i++ {
		e.spend(t, p, march, nil, usd(1.0))
	}
	e.spend(t, p, march, nil, usd(2.5)) // 11.5 of 10
	open := e.task(t, service.CreateTaskInput{Title: "aberta", PersonaID: &p.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, open, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)

	got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if got.Status != domain.PersonaPaused || got.PauseReason == "" {
		t.Fatalf("persona: %s %q", got.Status, got.PauseReason)
	}
	paused := activityOf(t, e, "persona.paused")
	if len(paused) != 1 || paused[0].SubjectID != p.ID || dataOf(t, paused[0])["reason"] == "" || dataOf(t, paused[0])["scope"] != "persona" {
		t.Fatalf("persona.paused: %+v", paused)
	}

	// still over: raising to a value below the spend changes nothing
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{BudgetMonthlyCents: ptr(int64(1100))}); err != nil {
		t.Fatal(err)
	}
	if got, _ = e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.Status != domain.PersonaPaused {
		t.Fatalf("ainda estourada, mas status = %s", got.Status)
	}

	e.jobs.reset()
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{BudgetMonthlyCents: ptr(int64(5000))}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if got.Status == domain.PersonaPaused || got.PauseReason != "" {
		t.Fatalf("depois de subir o teto: %s %q", got.Status, got.PauseReason)
	}
	if len(activityOf(t, e, "persona.resumed")) != 1 {
		t.Fatal("persona.resumed ausente")
	}
	woken := false
	for _, w := range e.jobs.wakes {
		woken = woken || (w.TaskID == open.ID && w.PersonaID == p.ID)
	}
	if !woken {
		t.Fatalf("a task aberta deveria ter sido acordada: %+v", e.jobs.wakes)
	}
	// the exceeded alert is re-armed: crossing the new ceiling alerts again
	e.spend(t, p, march, nil, usd(40))
	if n := countVerb(t, e, "budget.exceeded"); n != 2 {
		t.Fatalf("budget.exceeded = %d, want 2 (once per crossing)", n)
	}
}

func TestBudgetPauseByCompanyLiftedByRaisingCeilingOrNewMonth(t *testing.T) {
	ctx := context.Background()
	march := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e := newEnv(t)
	a := e.persona(t, "A", nil)
	b := e.persona(t, "B", nil)
	manual := e.persona(t, "Manual", nil)
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(1000))); err != nil {
		t.Fatal(err)
	}
	e.spend(t, a, march, nil, usd(1.0))
	e.spend(t, a, march, nil, usd(10.0)) // 11 of 10
	tb := e.task(t, service.CreateTaskInput{Title: "b", PersonaID: &b.ID})
	_, err := e.svc.CheckoutRun(ctx, wakeOf(b, tb, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	if st := personaStatus(t, e, b); st != domain.PersonaPaused {
		t.Fatalf("b = %s", st)
	}
	// a human pause (no budget reason) is never lifted automatically
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, manual.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)}); err != nil {
		t.Fatal(err)
	}

	// next month, the periodic scan resumes b and wakes its task
	e.svc.Now = func() time.Time { return time.Date(2026, 4, 1, 3, 0, 1, 0, time.UTC) }
	e.jobs.reset()
	n, err := e.svc.ResumeBudgetPaused(ctx)
	if err != nil || n != 1 {
		t.Fatalf("resumed = %d %v", n, err)
	}
	if st := personaStatus(t, e, b); st == domain.PersonaPaused {
		t.Fatal("b should be back in the new month")
	}
	if st := personaStatus(t, e, manual); st != domain.PersonaPaused {
		t.Fatalf("manual pause lifted: %s", st)
	}
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].TaskID != tb.ID || e.jobs.wakes[0].PersonaID != b.ID {
		t.Fatalf("wakes: %+v", e.jobs.wakes)
	}
	if n, _ := e.svc.ResumeBudgetPaused(ctx); n != 0 {
		t.Fatalf("second scan resumed %d", n)
	}

	// raising the company ceiling in the same month does it too
	e.svc.Now = func() time.Time { return march }
	c := e.persona(t, "C", nil)
	tc := e.task(t, service.CreateTaskInput{Title: "c", PersonaID: &c.ID})
	_, err = e.svc.CheckoutRun(ctx, wakeOf(c, tc, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	e.jobs.reset()
	if _, err := e.svc.SetCompanyBudget(ctx, domain.HumanActor, e.co.ID, ptr(int64(5000))); err != nil {
		t.Fatal(err)
	}
	if st := personaStatus(t, e, c); st == domain.PersonaPaused {
		t.Fatal("c should be back after raising the ceiling")
	}
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].TaskID != tc.ID {
		t.Fatalf("wakes: %+v", e.jobs.wakes)
	}
}

// ---------- 10. orphan gates ----------

func TestClosingTaskExpiresPendingGates(t *testing.T) {
	ctx := context.Background()
	for _, to := range []domain.TaskStatus{domain.TaskCancelled, domain.TaskDone} {
		t.Run(string(to), func(t *testing.T) {
			e, p, tv, run := gateFixture(t, nil)
			finishOK(t, e, run)
			g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "ok?", TaskID: &tv.ID})
			if err != nil {
				t.Fatal(err)
			}
			if st := personaStatus(t, e, p); st != domain.PersonaAwaitingGate {
				t.Fatalf("persona = %s", st)
			}
			if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: &to}); err != nil {
				t.Fatal(err)
			}
			got, err := e.svc.GetGate(ctx, e.co.ID, g.ID)
			if err != nil || got.Status != domain.GateExpired || got.DecidedAt == nil {
				t.Fatalf("gate: %+v %v", got, err)
			}
			acts := activityOf(t, e, "gate.expired")
			if len(acts) != 1 || acts[0].SubjectID != g.ID || dataOf(t, acts[0])["task_id"] != tv.ID.String() {
				t.Fatalf("gate.expired: %+v", acts)
			}
			if st := personaStatus(t, e, p); st != domain.PersonaIdle {
				t.Fatalf("persona kept waiting for an expired gate: %s", st)
			}
			if pending, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}}); len(pending) != 0 {
				t.Fatalf("pending gates left: %+v", pending)
			}
			// it can no longer be decided
			_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
			wantErr(t, err, domain.ErrConflict)
		})
	}
}

func TestApprovingReviewExpiresOtherPendingGates(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, func(in *service.HirePersonaInput) { in.RequireReview = true })
	if _, err := e.svc.UpdateTask(ctx, domain.PersonaActor(p.ID), e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	finishOK(t, e, run)
	other, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "outra", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	gates, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &tv.ID, Kinds: []domain.GateKind{domain.GateTaskReview}})
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gates[0].ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.GetGate(ctx, e.co.ID, other.ID); got.Status != domain.GateExpired {
		t.Fatalf("outro gate = %s, want expired", got.Status)
	}
}

// ---------- 2 and 9. flows ----------

func TestRunNowDoesNotCollideWithScheduledOccurrence(t *testing.T) {
	ctx := context.Background()
	e, _, f, _ := flowFixture(t)
	occ := *f.NextFireAt // 2026-03-11 12:00Z
	e.svc.Now = func() time.Time { return occ.Add(20 * time.Second) }

	manual, err := e.svc.RunFlowNow(ctx, domain.HumanActor, e.co.ID, f.ID)
	if err != nil || manual.Task == nil || !manual.Fire.Manual || !manual.Fire.ScheduledFor.Equal(occ) {
		t.Fatalf("manual: %+v %v", manual, err)
	}
	sched, err := e.svc.FireFlow(ctx, f.ID, occ)
	if err != nil || sched.Duplicate || sched.Task == nil || sched.Fire.Manual {
		t.Fatalf("o disparo agendado colidiu com o manual: %+v %v", sched, err)
	}
	if sched.Task.ID == manual.Task.ID {
		t.Fatal("mesma task nos dois disparos")
	}
	got, _ := e.svc.GetFlow(ctx, e.co.ID, f.ID.String())
	if got.NextFireAt == nil || !got.NextFireAt.Equal(occ.Add(24*time.Hour)) {
		t.Fatalf("o fluxo travou: next_fire_at = %v, want %v", got.NextFireAt, occ.Add(24*time.Hour))
	}
	if due, _ := e.svc.DueFlows(ctx, occ.Add(time.Minute)); len(due) != 0 {
		t.Fatalf("fluxo ainda vencido: %v", due)
	}
}

func TestDuplicateScheduledFireStillAdvancesSchedule(t *testing.T) {
	ctx := context.Background()
	e, _, f, _ := flowFixture(t)
	occ := *f.NextFireAt
	e.svc.Now = func() time.Time { return occ.Add(time.Second) }
	if _, err := e.svc.FireFlow(ctx, f.ID, occ); err != nil {
		t.Fatal(err)
	}
	// the schedule is left behind (as an older bug would): the repeat must move it forward
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE flows SET next_fire_at = $1 WHERE id = $2`, occ, f.ID); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.FireFlow(ctx, f.ID, occ)
	if err != nil || !r.Duplicate {
		t.Fatalf("repeat: %+v %v", r, err)
	}
	got, _ := e.svc.GetFlow(ctx, e.co.ID, f.ID.String())
	if got.NextFireAt == nil || !got.NextFireAt.Equal(occ.Add(24*time.Hour)) {
		t.Fatalf("next_fire_at = %v", got.NextFireAt)
	}
}

func TestFlowSkipsWhileEarlierTaskIsOpen(t *testing.T) {
	ctx := context.Background()
	e, _, f, _ := flowFixture(t)
	occ := *f.NextFireAt
	e.svc.Now = func() time.Time { return occ.Add(time.Second) }
	first, err := e.svc.FireFlow(ctx, f.ID, occ)
	if err != nil || first.Task == nil {
		t.Fatal(err)
	}

	occ2 := occ.Add(24 * time.Hour)
	e.svc.Now = func() time.Time { return occ2.Add(time.Second) }
	sk, err := e.svc.FireFlow(ctx, f.ID, occ2)
	if err != nil || !sk.Skipped || sk.SkipReason != "open_task" || sk.Task != nil {
		t.Fatalf("skip: %+v %v", sk, err)
	}
	if tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{}); len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	acts := activityOf(t, e, "flow.skipped")
	if len(acts) != 1 || acts[0].SubjectID != f.ID || dataOf(t, acts[0])["task_id"] != first.Task.ID.String() {
		t.Fatalf("flow.skipped: %+v", acts)
	}
	got, _ := e.svc.GetFlow(ctx, e.co.ID, f.ID.String())
	if !got.NextFireAt.Equal(occ2.Add(24 * time.Hour)) {
		t.Fatalf("a skip must still advance the schedule: %v", got.NextFireAt)
	}
	// a manual run is explicit and ignores the open task
	if r, err := e.svc.RunFlowNow(ctx, domain.HumanActor, e.co.ID, f.ID); err != nil || r.Task == nil {
		t.Fatalf("manual: %+v %v", r, err)
	}
	// once the task is closed the next occurrence fires again
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, first.Task.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	occ3 := occ2.Add(24 * time.Hour)
	e.svc.Now = func() time.Time { return occ3.Add(time.Second) }
	if r, err := e.svc.FireFlow(ctx, f.ID, occ3); err != nil || r.Task == nil {
		t.Fatalf("after close: %+v %v", r, err)
	}
}

func TestFlowAfterDowntimeFiresOnlyTheLatestOccurrence(t *testing.T) {
	ctx := context.Background()
	e, _, f, _ := flowFixture(t)
	first := *f.NextFireAt // 03-11 12:00Z; daily at 09:00 in São Paulo
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 15, 13, 0, 0, 0, time.UTC) }
	r, err := e.svc.FireFlow(ctx, f.ID, first)
	if err != nil || r.Task == nil {
		t.Fatalf("fire: %+v %v", r, err)
	}
	latest := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	if !r.Fire.ScheduledFor.Equal(latest) || r.Task.Title != "Resumo de 2026-03-15 (#1)" {
		t.Fatalf("fire: scheduled_for=%v title=%q", r.Fire.ScheduledFor, r.Task.Title)
	}
	if tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{}); len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	missed := activityOf(t, e, "flow.missed")
	if len(missed) != 1 || missed[0].SubjectID != f.ID || dataOf(t, missed[0])["missed"] != float64(4) {
		t.Fatalf("flow.missed: %+v", missed)
	}
	got, _ := e.svc.GetFlow(ctx, e.co.ID, f.ID.String())
	if !got.NextFireAt.Equal(latest.Add(24 * time.Hour)) {
		t.Fatalf("next = %v", got.NextFireAt)
	}
	// the same call again changes nothing
	if again, err := e.svc.FireFlow(ctx, f.ID, first); err != nil || !again.Duplicate {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if n := len(activityOf(t, e, "flow.missed")); n != 2 && n != 1 {
		t.Fatalf("flow.missed = %d", n)
	}
}

// The documented DST behaviour of the cron parser (see ParseCron).
func TestCronDaylightSavingBehaviour(t *testing.T) {
	// spring forward 2026-03-08 in New York: 02:30 does not exist that day and is skipped
	n, err := service.NextCron("30 2 * * *", "America/New_York", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC))
	if err != nil || !n.Equal(time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("spring forward: %v %v", n, err)
	}
	// fall back 2026-11-01: 01:30 happens twice, 05:30Z (EDT) and 06:30Z (EST); both fire
	n, _ = service.NextCron("30 1 * * *", "America/New_York", time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC))
	if !n.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("first 01:30: %v", n)
	}
	n, _ = service.NextCron("30 1 * * *", "America/New_York", n)
	if !n.Equal(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("second 01:30: %v", n)
	}
}

// ---------- 4. wake counter and the heartbeat safety net ----------

func TestWakeSeqCountsEveryRequest(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	w := wakeOf(p, tv, domain.TriggerManual)
	if n, err := e.svc.WakeSeq(ctx, p.ID, tv.ID); err != nil || n != 0 {
		t.Fatalf("seq = %d %v", n, err)
	}
	for i := 1; i <= 3; i++ {
		tx, err := e.svc.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.BumpWakeRequestTx(ctx, tx, w); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if n, _ := e.svc.WakeSeq(ctx, p.ID, tv.ID); n != int64(i) {
			t.Fatalf("seq = %d, want %d", n, i)
		}
	}
	// a rolled-back request leaves no trace
	tx, _ := e.svc.Pool.Begin(ctx)
	_ = service.BumpWakeRequestTx(ctx, tx, w)
	_ = tx.Rollback(ctx)
	if n, _ := e.svc.WakeSeq(ctx, p.ID, tv.ID); n != 3 {
		t.Fatalf("seq after rollback = %d", n)
	}
	_ = pgx.Tx(tx)
}

func TestWakeCandidatesSafetyNet(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return t0 }
	p := e.persona(t, "Eng", nil) // heartbeat_interval_sec = 0: only the safety net can find its tasks
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	ids := func(at time.Time) map[domain.ID]bool {
		e.svc.Now = func() time.Time { return at }
		cs, err := e.svc.WakeCandidates(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		m := map[domain.ID]bool{}
		for _, c := range cs {
			m[c.TaskID] = true
		}
		return m
	}
	if ids(t0.Add(30 * time.Second))[tv.ID] {
		t.Fatal("task mexida há menos de 1 minuto não é candidata")
	}
	if !ids(t0.Add(2 * time.Minute))[tv.ID] {
		t.Fatal("task atribuída, parada há mais de 1 minuto e sem run deveria ser candidata (wake perdido)")
	}
	// a pending gate holds it
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "g", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ids(t0.Add(5 * time.Minute))[tv.ID] {
		t.Fatal("task com gate pendente não é candidata")
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	// a run opened after the last change means the wake was not lost
	e.svc.Now = func() time.Time { return t0.Add(10 * time.Minute) }
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if ids(t0.Add(20 * time.Minute))[tv.ID] {
		t.Fatal("task com run ativa não é candidata")
	}
	e.svc.Now = func() time.Time { return t0.Add(21 * time.Minute) }
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	finishOK(t, e, run)
	if ids(t0.Add(40 * time.Minute))[tv.ID] {
		t.Fatal("task já tentada depois da última mudança não pode virar laço de runs")
	}
	// a human comment after the run is a new reason to wake
	e.svc.Now = func() time.Time { return t0.Add(41 * time.Minute) }
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "e agora?"}); err != nil {
		t.Fatal(err)
	}
	if !ids(t0.Add(43 * time.Minute))[tv.ID] {
		t.Fatal("comentário humano sem run depois dele deveria tornar a task candidata")
	}
	// personas that cannot work are skipped
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)}); err != nil {
		t.Fatal(err)
	}
	if ids(t0.Add(50 * time.Minute))[tv.ID] {
		t.Fatal("persona pausada não é candidata")
	}
	_ = strings.TrimSpace
}
