package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// gateFixture: persona with a todo task, a run checked out and started (persona running).
func gateFixture(t *testing.T, mod func(*service.HirePersonaInput)) (*env, domain.Persona, service.TaskView, domain.Run) {
	t.Helper()
	e := newEnv(t)
	p := e.persona(t, "Eng", mod)
	tv := e.task(t, service.CreateTaskInput{Title: "Publicar release", PersonaID: &p.ID})
	run, err := e.svc.CheckoutRun(context.Background(), wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(context.Background(), e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	return e, p, tv, run
}

func finishOK(t *testing.T, e *env, run domain.Run) {
	t.Helper()
	if _, err := e.svc.FinishRun(context.Background(), e.co.ID, run.ID, service.FinishRunInput{
		Status: domain.RunDone, Result: adapter.Result{Summary: "feito"}}); err != nil {
		t.Fatal(err)
	}
}

func personaStatus(t *testing.T, e *env, p domain.Persona) domain.PersonaStatus {
	t.Helper()
	got, err := e.svc.GetPersona(context.Background(), e.co.ID, p.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	return got.Status
}

func taskStatus(t *testing.T, e *env, tv service.TaskView) service.TaskView {
	t.Helper()
	got, err := e.svc.GetTask(context.Background(), e.co.ID, tv.Key)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGateLifecycleApproved(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, nil)

	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{
		Title: "Fazer deploy em produção", Body: "Etapa **irreversível**.", TaskID: &tv.ID, RunID: &run.ID, EventID: "gate-1"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != domain.GatePending || g.Kind != domain.GateApproval || g.PersonaID == nil || *g.PersonaID != p.ID ||
		g.TaskKey != tv.Key || g.PersonaName != "Eng" || g.RequestedBy.Type != domain.ActorPersona {
		t.Fatalf("gate: %+v", g)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskBlocked {
		t.Fatalf("task status = %s, want blocked", got.Status)
	}
	if st := personaStatus(t, e, p); st != domain.PersonaAwaitingGate {
		t.Fatalf("persona status = %s, want awaiting_gate", st)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "gate"})
	if len(acts) != 1 || acts[0].Verb != "gate.requested" || acts[0].Actor.Type != domain.ActorPersona {
		t.Fatalf("activity: %+v", acts)
	}
	if st, _ := e.svc.Dashboard(ctx, e.co.ID); st.PendingGates != 1 {
		t.Fatalf("pending gates = %d", st.PendingGates)
	}

	// the run ends; the persona keeps waiting
	finishOK(t, e, run)
	if st := personaStatus(t, e, p); st != domain.PersonaAwaitingGate {
		t.Fatalf("persona after run = %s, want awaiting_gate", st)
	}

	// an idempotent retry returns the same gate
	again, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "outro", TaskID: &tv.ID, EventID: "gate-1"})
	if err != nil || again.ID != g.ID {
		t.Fatalf("retry: %v %+v", err, again)
	}

	e.jobs.reset()
	done, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "Pode ir, janela das 22h."})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.GateApproved || done.DecidedAt == nil || done.DecidedBy == nil || done.DecidedBy.Type != domain.ActorHuman || done.DecisionNote == "" {
		t.Fatalf("decided gate: %+v", done)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskInProgress {
		t.Fatalf("task after approval = %s, want in_progress", got.Status)
	}
	if st := personaStatus(t, e, p); st != domain.PersonaIdle {
		t.Fatalf("persona after approval = %s, want idle", st)
	}

	// the decision is a system comment on the task
	cs, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID)
	last := cs[len(cs)-1]
	if last.Author.Type != domain.ActorSystem || !strings.Contains(last.Body, "Aprovado") || !strings.Contains(last.Body, "janela das 22h") {
		t.Fatalf("decision comment: %+v", last)
	}

	// the persona is woken with trigger gate
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerGate || e.jobs.wakes[0].PersonaID != p.ID || e.jobs.wakes[0].TaskID != tv.ID {
		t.Fatalf("wakes: %+v", e.jobs.wakes)
	}
	if _, err := e.svc.CheckoutRun(ctx, e.jobs.wakes[0]); err != nil {
		t.Fatalf("checkout after decision: %v", err)
	}

	// audit trail
	acts, _ = e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "gate"})
	if len(acts) != 2 || acts[0].Verb != "gate.decided" || acts[0].Actor.Type != domain.ActorHuman {
		t.Fatalf("activity after decision: %+v", acts)
	}
	if st, _ := e.svc.Dashboard(ctx, e.co.ID); st.PendingGates != 0 {
		t.Fatalf("pending gates after decision = %d", st.PendingGates)
	}

	// decision becomes memory
	var fact memory.Fact
	found := false
	for _, id := range e.jobs.outbox {
		it, err := e.svc.GetMemoryItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if it.Kind == "gate.decided" {
			found = true
			if err := json.Unmarshal(it.Payload, &fact); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatalf("no gate.decided fact in outbox %v", e.jobs.outbox)
	}
	wantPath := "hanko/decisions/" + e.svc.Now().Format("2006-01-02") + "-" + tv.Key + "-" + g.ID.String()[:8] + ".md"
	if fact.Path != wantPath || !strings.Contains(fact.Body, "Pode ir") || !strings.Contains(fact.Body, "Aprovado") || !strings.Contains(fact.Body, tv.Key) {
		t.Fatalf("fact: %+v (want path %s)", fact, wantPath)
	}

	// deciding twice conflicts
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateRejected})
	wantErr(t, err, domain.ErrConflict)
}

func TestGateDecisions(t *testing.T) {
	tests := []struct {
		name       string
		decision   domain.GateStatus
		note       string
		wantTask   domain.TaskStatus
		wantErrNil bool
	}{
		{"rejected goes back to todo", domain.GateRejected, "Não faça isso.", domain.TaskTodo, true},
		{"changes requested resumes work", domain.GateChangesRequested, "Inclua rollback.", domain.TaskInProgress, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e, p, tv, run := gateFixture(t, nil)
			g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Merge na main", TaskID: &tv.ID})
			if err != nil {
				t.Fatal(err)
			}
			finishOK(t, e, run)
			e.jobs.reset()
			if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: tc.decision, Note: tc.note}); err != nil {
				t.Fatal(err)
			}
			if got := taskStatus(t, e, tv); got.Status != tc.wantTask {
				t.Fatalf("task = %s, want %s", got.Status, tc.wantTask)
			}
			if st := personaStatus(t, e, p); st != domain.PersonaIdle {
				t.Fatalf("persona = %s, want idle", st)
			}
			if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerGate {
				t.Fatalf("wakes: %+v", e.jobs.wakes)
			}
		})
	}
}

func TestDecideGateRules(t *testing.T) {
	ctx := context.Background()
	e, p, tv, _ := gateFixture(t, nil)
	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Gastar US$ 50", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	// a persona never decides, not even its own gate
	_, err = e.svc.DecideGate(ctx, domain.PersonaActor(p.ID), e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrNotAllowed)
	_, err = e.svc.DecideGate(ctx, domain.SystemActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrNotAllowed)
	// bad input
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GatePending})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateChangesRequested})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, domain.NewID(), service.DecideGateInput{Decision: domain.GateApproved})
	wantErr(t, err, domain.ErrNotFound)
	if got, _ := e.svc.GetGate(ctx, e.co.ID, g.ID); got.Status != domain.GatePending {
		t.Fatalf("gate must stay pending, got %s", got.Status)
	}
	// request validation
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: " "})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "x", Kind: "nope"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "x", TaskID: ptr(domain.NewID())})
	wantErr(t, err, domain.ErrInvalid)
}

func TestCheckoutRunBlockedByPendingGate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "T", PersonaID: &p.ID, Status: domain.TaskBacklog})
	// a human opens a gate on a task; the task stays workable but the gate holds the run
	g, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "Aprovar escopo", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskBacklog {
		t.Fatalf("a human-opened gate must not move the task, got %s", got.Status)
	}
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskTodo)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrInvalid)

	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	// human-opened gate: no persona, so no wake and the task keeps its status
	if got := taskStatus(t, e, tv); got.Status != domain.TaskTodo {
		t.Fatalf("task = %s", got.Status)
	}
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign)); err != nil {
		t.Fatalf("checkout after decision: %v", err)
	}
}

func TestGateDecidedDuringRunWakesAfterFinish(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, nil)
	g, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Deploy", TaskID: &tv.ID})
	if err != nil {
		t.Fatal(err)
	}
	// the human decides while the persona's run is still active
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}
	if st := personaStatus(t, e, p); st != domain.PersonaRunning {
		t.Fatalf("persona with an active run = %s, want running", st)
	}
	e.jobs.reset()
	finishOK(t, e, run)
	if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerGate {
		t.Fatalf("wakes after finish: %+v", e.jobs.wakes)
	}
}

func TestPersonaWithRunElsewhereStaysWorking(t *testing.T) {
	ctx := context.Background()
	e, p, tv, _ := gateFixture(t, nil)
	other := e.task(t, service.CreateTaskInput{Title: "Outra", PersonaID: &p.ID})
	r2, err := e.svc.CheckoutRun(ctx, wakeOf(p, other, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	_ = r2
	if _, err := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Deploy", TaskID: &tv.ID}); err != nil {
		t.Fatal(err)
	}
	if st := personaStatus(t, e, p); st == domain.PersonaAwaitingGate {
		t.Fatalf("persona with a run on another task must not go to awaiting_gate")
	}
	if got := taskStatus(t, e, tv); got.Status != domain.TaskBlocked {
		t.Fatalf("task = %s, want blocked", got.Status)
	}
}

func TestRequireReview(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, func(in *service.HirePersonaInput) { in.RequireReview = true })
	if !p.RequireReview {
		t.Fatalf("persona.RequireReview not stored: %+v", p)
	}
	pa := domain.PersonaActor(p.ID)

	got, err := e.svc.UpdateTask(ctx, pa, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TaskInReview || got.ClosedAt != nil {
		t.Fatalf("task = %s closed=%v, want in_review", got.Status, got.ClosedAt)
	}
	gates, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &tv.ID})
	if len(gates) != 1 || gates[0].Kind != domain.GateTaskReview || gates[0].Status != domain.GatePending ||
		gates[0].PersonaID == nil || *gates[0].PersonaID != p.ID || gates[0].RunID == nil || *gates[0].RunID != run.ID {
		t.Fatalf("gates: %+v", gates)
	}
	// asking again does not duplicate the gate
	if _, err := e.svc.UpdateTask(ctx, pa, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	if gates, _ = e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &tv.ID}); len(gates) != 1 {
		t.Fatalf("duplicate review gate: %+v", gates)
	}
	// a review does not hold the persona
	if st := personaStatus(t, e, p); st == domain.PersonaAwaitingGate {
		t.Fatalf("persona must not wait on a task review")
	}
	finishOK(t, e, run)

	d, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gates[0].ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "Revisado."})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.GateApproved {
		t.Fatalf("gate: %+v", d)
	}
	got = taskStatus(t, e, tv)
	if got.Status != domain.TaskDone || got.ClosedAt == nil {
		t.Fatalf("task after approval = %s closed=%v, want done", got.Status, got.ClosedAt)
	}

	// a human closes tasks directly; a persona without require_review too
	p2 := e.persona(t, "Livre", nil)
	tv2 := e.task(t, service.CreateTaskInput{Title: "Livre", PersonaID: &p2.ID, Status: domain.TaskInProgress})
	if got, err := e.svc.UpdateTask(ctx, domain.PersonaActor(p2.ID), e.co.ID, tv2.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil || got.Status != domain.TaskDone {
		t.Fatalf("persona without review: %v %+v", err, got.Status)
	}
	tv3 := e.task(t, service.CreateTaskInput{Title: "Manual", PersonaID: &p.ID, Status: domain.TaskInProgress})
	if got, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv3.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil || got.Status != domain.TaskDone {
		t.Fatalf("human close: %v %+v", err, got.Status)
	}
}

func TestRequireReviewRejectedAndChanges(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		d    domain.GateStatus
		want domain.TaskStatus
	}{{domain.GateChangesRequested, domain.TaskInProgress}, {domain.GateRejected, domain.TaskTodo}} {
		e, p, tv, run := gateFixture(t, func(in *service.HirePersonaInput) { in.RequireReview = true })
		if _, err := e.svc.UpdateTask(ctx, domain.PersonaActor(p.ID), e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
			t.Fatal(err)
		}
		finishOK(t, e, run)
		gates, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &tv.ID})
		e.jobs.reset()
		if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gates[0].ID, service.DecideGateInput{Decision: tc.d, Note: "ajuste"}); err != nil {
			t.Fatal(err)
		}
		if got := taskStatus(t, e, tv); got.Status != tc.want {
			t.Fatalf("%s: task = %s, want %s", tc.d, got.Status, tc.want)
		}
		if len(e.jobs.wakes) != 1 || e.jobs.wakes[0].Trigger != domain.TriggerGate {
			t.Fatalf("%s: wakes %+v", tc.d, e.jobs.wakes)
		}
	}
}

func TestListGatesFiltersAndInbox(t *testing.T) {
	ctx := context.Background()
	e, p, tv, run := gateFixture(t, nil)
	finishOK(t, e, run)
	g1, _ := e.svc.RequestGate(ctx, domain.PersonaActor(p.ID), e.co.ID, service.RequestGateInput{Title: "Um", TaskID: &tv.ID})
	other := e.task(t, service.CreateTaskInput{Title: "Outra"})
	g2, _ := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "Dois", Kind: domain.GateHire, TaskID: &other.ID})
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, g2.ID, service.DecideGateInput{Decision: domain.GateRejected}); err != nil {
		t.Fatal(err)
	}

	pending, err := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}})
	if err != nil || len(pending) != 1 || pending[0].ID != g1.ID {
		t.Fatalf("pending: %v %+v", err, pending)
	}
	if hires, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Kinds: []domain.GateKind{domain.GateHire}}); len(hires) != 1 || hires[0].ID != g2.ID {
		t.Fatalf("hire filter: %+v", hires)
	}
	if byTask, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{TaskID: &other.ID}); len(byTask) != 1 || byTask[0].ID != g2.ID {
		t.Fatalf("task filter: %+v", byTask)
	}
	if all, _ := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{}); len(all) != 2 || all[0].ID != g1.ID {
		t.Fatalf("pending gates must come first: %+v", all)
	}
	_, err = e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Statuses: []domain.GateStatus{"x"}})
	wantErr(t, err, domain.ErrInvalid)

	// inbox: the pending gate, plus a blocked task without gate, a failed run and a persona in error
	blocked := e.task(t, service.CreateTaskInput{Title: "Travada", Status: domain.TaskBlocked})
	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Gates) != 1 || in.Gates[0].ID != g1.ID {
		t.Fatalf("inbox gates: %+v", in.Gates)
	}
	if len(in.BlockedTasks) != 1 || in.BlockedTasks[0].ID != blocked.ID { // tv is blocked too, but its gate covers it
		t.Fatalf("inbox blocked: %+v", in.BlockedTasks)
	}
	if in.Count() != 2 {
		t.Fatalf("count = %d", in.Count())
	}
}
