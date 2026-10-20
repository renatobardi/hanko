//go:build integration

package heartbeat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/heartbeat"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// stackWith is newStack with a custom adapter registry.
func stackWith(t *testing.T, reg adapter.Registry) *stack {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := heartbeat.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	hb, err := heartbeat.New(pool, heartbeat.Options{Workers: 4, PeriodicInterval: time.Hour, RunTimeout: time.Minute, WakeSnooze: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	hb.Attach(svc, reg, memory.Noop{}, broker, auth.RunTokens{Secret: []byte("integration-secret-0123456789")})
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	return &stack{svc: svc, hb: hb, co: co}
}

func (s *stack) hire(t *testing.T, agent string, cfg string) domain.Persona {
	t.Helper()
	in := service.HirePersonaInput{Name: "Ana", Agent: agent}
	if cfg != "" {
		in.AdapterConfig = json.RawMessage(cfg)
	}
	p, err := s.svc.HirePersona(context.Background(), domain.HumanActor, s.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (s *stack) task(t *testing.T, p domain.Persona, title string) service.TaskView {
	t.Helper()
	tv, err := s.svc.CreateTask(context.Background(), domain.HumanActor, s.co.ID, service.CreateTaskInput{Title: title, PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return tv
}

// deadProcess leaves a running run exactly as a killed server would: started, with a log and no queued jobs.
func (s *stack) deadProcess(t *testing.T, p domain.Persona, tv service.TaskView, events ...adapter.Event) domain.Run {
	t.Helper()
	ctx := context.Background()
	run, err := s.svc.CheckoutRun(ctx, service.Wake{CompanyID: s.co.ID, PersonaID: p.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.svc.MarkRunStarted(ctx, s.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if _, err := s.svc.AppendRunEvent(ctx, s.co.ID, run.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.svc.Pool.Exec(ctx, "TRUNCATE river_job"); err != nil {
		t.Fatal(err)
	}
	return run
}

// Demo of M13: kill the process in the middle of a run and the run goes on by itself, as a continuation that resumes
// the harness session.
func TestKilledRunContinuesOnItsOwn(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, -1)
	p := s.hire(t, "fake", "")
	tv := s.task(t, p, "Retomar")
	dead := s.deadProcess(t, p, tv,
		adapter.Event{Kind: domain.EventSystem, Text: "sessão iniciada fake-dead", SessionRef: "fake-dead"},
		adapter.Event{Kind: domain.EventAssistant, Text: "Analisei metade do problema."},
		adapter.Event{Kind: domain.EventToolUse, Text: "Read: a.go", Payload: json.RawMessage(`{"id":"r1","name":"Read"}`)},
		adapter.Event{Kind: domain.EventToolResult, Text: "ok", Payload: json.RawMessage(`{"tool_use_id":"r1"}`)},
	)

	s.start(t) // boot: interrupts the orphan and opens the continuation
	runs := s.waitRuns(t, tv.ID, 20*time.Second, func(r []service.RunView) bool {
		return count(r, domain.RunInterrupted) == 1 && count(r, domain.RunDone) == 1
	})
	var cont service.RunView
	for _, r := range runs {
		if r.Status == domain.RunDone {
			cont = r
		}
	}
	if cont.ContinuationOfRunID == nil || *cont.ContinuationOfRunID != dead.ID || cont.ContinuationAttempt != 1 || cont.ContinuationReason != domain.ContinueRecovery {
		t.Fatalf("continuation = %+v", cont.Run)
	}
	if !strings.Contains(cont.Summary, "sessão retomada") {
		t.Fatalf("the continuation must resume the session: %q", cont.Summary)
	}
	old, _ := s.svc.GetRun(ctx, s.co.ID, dead.ID)
	if old.Diagnosis == nil || old.Diagnosis.NextAction != domain.ActionAutoContinue || old.SessionRef != "fake-dead" {
		t.Fatalf("old run = %+v / %+v", old.Run, old.Diagnosis)
	}
	// the briefing reached the agent: the continuation's log starts a session that resumed (fake echoes it) and the
	// task ends with the persona's comment
	comments, _ := s.svc.ListComments(ctx, s.co.ID, tv.ID)
	if len(comments) == 0 || comments[len(comments)-1].Author.Type != domain.ActorPersona {
		t.Fatalf("comments = %+v", comments)
	}
}

// With a tool of external effect in flight when the process died, nothing restarts on its own: the run waits in the
// inbox for a human decision, and "Continuar" then resumes it.
func TestKilledRunWithUncertainOutcomeWaitsForHuman(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, -1)
	p := s.hire(t, "fake", "")
	tv := s.task(t, p, "Publicar")
	dead := s.deadProcess(t, p, tv,
		adapter.Event{Kind: domain.EventSystem, Text: "sessão", SessionRef: "fake-pub"},
		adapter.Event{Kind: domain.EventToolUse, Text: "Bash: git push", Payload: json.RawMessage(`{"id":"b1","name":"Bash"}`)},
	)
	s.start(t)
	deadline := time.Now().Add(10 * time.Second)
	for {
		in, err := s.svc.Inbox(ctx, s.co.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(in.StoppedRuns) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stopped run never reached the inbox")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond) // no run may appear while the decision is pending
	if runs, _ := s.svc.ListRuns(ctx, s.co.ID, service.RunFilter{TaskID: &tv.ID}); len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	if _, err := s.svc.RunAction(ctx, domain.HumanActor, s.co.ID, dead.ID, service.RunActionInput{Action: domain.DecisionContinue, Note: "conferi o remoto"}); err != nil {
		t.Fatal(err)
	}
	runs := s.waitRuns(t, tv.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunDone) == 1 })
	for _, r := range runs {
		if r.Status == domain.RunDone && (r.ContinuationReason != domain.ContinueManual || !strings.Contains(r.Summary, "sessão retomada")) {
			t.Fatalf("run = %+v", r.Run)
		}
	}
}

// Demo of M13: a message injected into the active run changes its course. The fake adapter holds before its last step
// waiting for the operator, so the proof is deterministic.
func TestSteerChangesTheCourseOfTheActiveRun(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, 20*time.Millisecond)
	s.start(t)
	p := s.hire(t, "fake", `{"hold_for_steer_ms":20000}`)
	tv := s.task(t, p, "Refatorar")
	run := s.waitRuns(t, tv.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunRunning) == 1 })[0]
	// wait until the agent is at the hold point (tool result logged)
	deadline := time.Now().Add(10 * time.Second)
	for {
		evs, _ := s.svc.ListRunEvents(ctx, s.co.ID, run.ID, 0, 100)
		n := 0
		for _, e := range evs {
			if e.Kind == domain.EventToolResult {
				n++
			}
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent never got to the hold point")
		}
		time.Sleep(20 * time.Millisecond)
	}

	res, err := s.svc.SteerRun(ctx, domain.HumanActor, s.co.ID, run.ID, "pare e escreva só os testes")
	if err != nil || res.Mode != domain.SteerInject {
		t.Fatalf("steer = %+v %v", res, err)
	}
	runs := s.waitRuns(t, tv.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunDone) == 1 })
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("the injection must not open another run: %+v", runs)
	}
	if !strings.Contains(runs[0].Summary, "Rumo alterado pelo operador: pare e escreva só os testes") {
		t.Fatalf("the run did not change course: %q", runs[0].Summary)
	}
	evs, _ := s.svc.ListRunEvents(ctx, s.co.ID, run.ID, 0, 100)
	var logged bool
	for _, e := range evs {
		if e.Kind == domain.EventSystem && strings.Contains(e.Text, "mensagem do operador injetada") {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("log lacks the delivery: %+v", evs)
	}
	if fs, _ := s.svc.ListFollowups(ctx, s.co.ID, service.FollowupTarget{TaskID: &tv.ID}); len(fs) != 0 {
		t.Fatalf("followup still pending: %+v", fs)
	}
}

// restartFake behaves like a harness without injection (codex): the executor must fall back to interrupt + continue.
type restartFake struct{ *fake.Adapter }

func (restartFake) Name() string                { return "codex" }
func (restartFake) SteerMode() domain.SteerMode { return domain.SteerRestart }

func TestSteerRestartsAHarnessWithoutInjection(t *testing.T) {
	ctx := context.Background()
	s := stackWith(t, adapter.Registry{"codex": restartFake{fake.New(fake.Options{Delay: 400 * time.Millisecond})}})
	s.start(t)
	p := s.hire(t, "codex", "")
	tv := s.task(t, p, "Longa")
	run := s.waitRuns(t, tv.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunRunning) == 1 })[0]
	time.Sleep(600 * time.Millisecond) // let the wake job that opened the run finish: a wake raised meanwhile would coalesce into it

	res, err := s.svc.SteerRun(ctx, domain.HumanActor, s.co.ID, run.ID, "troque a abordagem")
	if err != nil || res.Mode != domain.SteerRestart {
		t.Fatalf("steer = %+v %v", res, err)
	}
	runs := s.waitRuns(t, tv.ID, 30*time.Second, func(r []service.RunView) bool {
		return count(r, domain.RunCancelled) == 1 && count(r, domain.RunDone) == 1
	})
	for _, r := range runs {
		if r.Status == domain.RunDone && (r.ContinuationReason != domain.ContinueSteer || r.ContinuationOfRunID == nil || *r.ContinuationOfRunID != run.ID) {
			t.Fatalf("continuation = %+v", r.Run)
		}
	}
	comments, _ := s.svc.ListComments(ctx, s.co.ID, tv.ID)
	var steer bool
	for _, c := range comments {
		if c.Author.Type == domain.ActorHuman && c.Body == "troque a abordagem" {
			steer = true
		}
	}
	if !steer {
		t.Fatalf("the message must be in the thread of the continuation: %+v", comments)
	}
}

func TestFollowupsOfTheActiveRunWaitAndRunNext(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, 300*time.Millisecond)
	s.start(t)
	p := s.hire(t, "fake", "")
	tv := s.task(t, p, "Com fila")
	s.waitRuns(t, tv.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunRunning) == 1 })
	f, err := s.svc.QueueTaskFollowup(ctx, domain.HumanActor, s.co.ID, tv.ID, "depois disso, atualize o changelog")
	if err != nil || f.WaitReason != domain.WaitRunActive {
		t.Fatalf("followup = %+v %v", f, err)
	}
	runs := s.waitRuns(t, tv.ID, 30*time.Second, func(r []service.RunView) bool { return count(r, domain.RunDone) == 2 })
	if len(runs) != 2 {
		t.Fatalf("runs = %+v", runs)
	}
	if left, _ := s.svc.ListFollowups(ctx, s.co.ID, service.FollowupTarget{TaskID: &tv.ID}); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
}
