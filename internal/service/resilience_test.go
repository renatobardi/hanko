package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// startedRun opens a run for the task and marks it running, like the executor does.
func startedRun(t *testing.T, e *env, p domain.Persona, tv service.TaskView, trig domain.RunTrigger) domain.Run {
	t.Helper()
	ctx := context.Background()
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, trig))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	return run
}

func (e *env) event(t *testing.T, runID domain.ID, kind domain.RunEventKind, text, payload, session string) {
	t.Helper()
	ev := adapter.Event{Kind: kind, Text: text, SessionRef: session}
	if payload != "" {
		ev.Payload = json.RawMessage(payload)
	}
	if _, err := e.svc.AppendRunEvent(context.Background(), e.co.ID, runID, ev); err != nil {
		t.Fatalf("append event: %v", err)
	}
}

func (e *env) crash(t *testing.T) {
	t.Helper()
	if n, err := e.svc.InterruptOrphanRuns(context.Background()); err != nil || n < 1 {
		t.Fatalf("interrupt orphans = %d %v", n, err)
	}
}

func hasWake(e *env, trig domain.RunTrigger) bool {
	e.jobs.mu.Lock()
	defer e.jobs.mu.Unlock()
	for _, w := range e.jobs.wakes {
		if w.Trigger == trig {
			return true
		}
	}
	return false
}

// A queda do processo no meio da run: a run vira interrompida, ganha diagnóstico, e a task é re-acordada; o checkout
// seguinte abre a continuação ligada à antiga, com a sessão anunciada e o briefing do que já tinha sido feito.
func TestInterruptedRunContinuesWithSessionAndBriefing(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	e.event(t, run.ID, domain.EventSystem, "sessão iniciada sess-1", "", "sess-1")
	e.event(t, run.ID, domain.EventAssistant, "Li o README e vou ajustar o parser", "", "")
	e.event(t, run.ID, domain.EventToolUse, "Read: README.md", `{"id":"t1","name":"Read"}`, "")
	e.event(t, run.ID, domain.EventToolResult, "ok", `{"tool_use_id":"t1"}`, "")
	e.jobs.reset()

	e.crash(t)

	old, err := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != domain.RunInterrupted || old.SessionRef != "sess-1" {
		t.Fatalf("old run = %+v", old.Run)
	}
	d := old.Diagnosis
	if d == nil || d.Cause != domain.CauseProcessRestart || d.NextAction != domain.ActionAutoContinue || d.Owner != domain.OwnerSystem || d.Uncertain != nil || d.MaxAttempts != 2 {
		t.Fatalf("diagnosis = %+v", d)
	}
	if old.DecisionPending {
		t.Fatal("an automatic continuation does not wait for a human")
	}
	if !hasWake(e, domain.TriggerHeartbeat) {
		t.Fatalf("assignee not woken: %+v", e.jobs.wakes)
	}

	cont, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
	if err != nil {
		t.Fatal(err)
	}
	if cont.ContinuationOfRunID == nil || *cont.ContinuationOfRunID != run.ID || cont.ContinuationAttempt != 1 || cont.ContinuationReason != domain.ContinueRecovery {
		t.Fatalf("continuation = %+v", cont)
	}
	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, cont.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Continuation == nil || rc.ResumeSession != "sess-1" || rc.Continuation.OfRunID != run.ID {
		t.Fatalf("context = %+v", rc.Continuation)
	}
	joined := strings.Join(rc.Continuation.Tail, "\n")
	if !strings.Contains(joined, "agente: Li o README") || !strings.Contains(joined, "tool: Read: README.md") {
		t.Fatalf("tail = %q", joined)
	}
	// the mark was consumed: a second checkout opens nothing while this one is active, and no continuation is left pending
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat)); err == nil {
		t.Fatal("task already has an active run")
	}
	// the log of the new run knows where it comes from
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "run"})
	found := false
	for _, a := range acts {
		if a.Verb == "run.continued" && a.SubjectID == cont.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no run.continued activity: %+v", acts)
	}
}

// Máximo de 2 continuações automáticas: a terceira interrupção esgota, vai para a Caixa com o card e segura a task.
func TestContinuationsAreLimitedAndExhaustionGoesToInbox(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	for attempt := 1; attempt <= domain.MaxContinuations; attempt++ {
		e.event(t, run.ID, domain.EventSystem, "sessão", "", "sess-x")
		e.crash(t)
		next, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if next.ContinuationAttempt != attempt || *next.ContinuationOfRunID != run.ID {
			t.Fatalf("attempt %d: %+v", attempt, next)
		}
		if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, next.ID, "tr", ""); err != nil {
			t.Fatal(err)
		}
		run = next
	}
	e.jobs.reset()
	e.crash(t) // the second continuation dies too

	rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if !rv.DecisionPending || rv.Diagnosis == nil || !rv.Diagnosis.Exhausted || rv.Diagnosis.NextAction != domain.ActionHumanDecision || rv.Diagnosis.Owner != domain.OwnerHuman {
		t.Fatalf("exhausted run = %+v / %+v", rv.Run, rv.Diagnosis)
	}
	if e.jobs.wakeCount() != 0 {
		t.Fatalf("an exhausted chain must not wake the persona: %+v", e.jobs.wakes)
	}
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
	wantErr(t, err, domain.ErrInvalid)
	in, err := e.svc.Inbox(ctx, e.co.ID)
	if err != nil || len(in.StoppedRuns) != 1 || in.StoppedRuns[0].ID != run.ID || in.Count() < 1 {
		t.Fatalf("inbox = %+v %v", in.StoppedRuns, err)
	}

	// Tentar de novo: fresh run, budget restored, no session
	if _, err := e.svc.RunAction(ctx, domain.PersonaActor(p.ID), e.co.ID, run.ID, service.RunActionInput{Action: domain.DecisionRetry}); err == nil {
		t.Fatal("only a human decides")
	}
	if _, err := e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, run.ID, service.RunActionInput{Action: domain.DecisionRetry, Note: "tente outra abordagem"}); err != nil {
		t.Fatal(err)
	}
	if !hasWake(e, domain.TriggerManual) {
		t.Fatalf("the retry wakes the assignee: %+v", e.jobs.wakes)
	}
	fresh, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerManual))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ContinuationReason != domain.ContinueRetry || fresh.ContinuationAttempt != 0 || *fresh.ContinuationOfRunID != run.ID {
		t.Fatalf("retry run = %+v", fresh)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, fresh.ID)
	if rc.ResumeSession != "" || rc.Continuation == nil || rc.Continuation.DecisionNote != "tente outra abordagem" {
		t.Fatalf("a retry starts clean: %+v", rc.Continuation)
	}
	if in, _ := e.svc.Inbox(ctx, e.co.ID); len(in.StoppedRuns) != 0 {
		t.Fatalf("decided run left the inbox: %+v", in.StoppedRuns)
	}
	// a decided run cannot be decided again
	_, err = e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, run.ID, service.RunActionInput{Action: domain.DecisionClose})
	wantErr(t, err, domain.ErrInvalid)
}

// Tool de efeito externo em andamento ao morrer: desfecho incerto, nada reinicia sozinho; Continuar leva a decisão ao
// briefing.
func TestUncertainOutcomeNeedsHumanDecision(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	e.event(t, run.ID, domain.EventSystem, "sessão", "", "sess-u")
	e.event(t, run.ID, domain.EventToolUse, "Read: a.go", `{"id":"r1","name":"Read"}`, "")
	e.event(t, run.ID, domain.EventToolResult, "ok", `{"tool_use_id":"r1"}`, "")
	e.event(t, run.ID, domain.EventToolUse, "Bash: git push origin main", `{"id":"b1","name":"Bash"}`, "")
	e.jobs.reset()

	e.crash(t)

	rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID)
	d := rv.Diagnosis
	if !rv.DecisionPending || d == nil || d.Uncertain == nil || d.Uncertain.Name != "Bash" || !strings.Contains(d.Uncertain.Input, "git push") || d.NextAction != domain.ActionHumanDecision {
		t.Fatalf("run = %+v diagnosis = %+v", rv.Run, d)
	}
	if e.jobs.wakeCount() != 0 {
		t.Fatalf("uncertain outcome must not restart on its own: %+v", e.jobs.wakes)
	}
	// even a human comment cannot start a run past the pending decision
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerComment))
	wantErr(t, err, domain.ErrInvalid)

	if _, err := e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, run.ID, service.RunActionInput{Action: domain.DecisionContinue, Note: "o push foi conferido no remoto"}); err != nil {
		t.Fatal(err)
	}
	cont, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerManual))
	if err != nil {
		t.Fatal(err)
	}
	if cont.ContinuationReason != domain.ContinueManual || cont.ContinuationAttempt != 0 {
		t.Fatalf("continuation = %+v", cont)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, cont.ID)
	if rc.ResumeSession != "sess-u" || rc.Continuation.Diagnosis == nil || rc.Continuation.Diagnosis.Uncertain == nil || rc.Continuation.DecisionNote != "o push foi conferido no remoto" {
		t.Fatalf("context = %+v", rc.Continuation)
	}
}

func TestCloseDecisionBlocksTaskAndClearsInbox(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	e.event(t, run.ID, domain.EventToolUse, "Write: x.txt", `{"id":"w1","name":"Write"}`, "")
	e.crash(t)

	got, err := e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, run.ID, service.RunActionInput{Action: domain.DecisionClose})
	if err != nil || got.Decision != domain.DecisionClose || got.DecisionPending || got.Diagnosis.NextAction != domain.ActionNone {
		t.Fatalf("closed = %+v %v", got, err)
	}
	task, _ := e.svc.GetTask(ctx, e.co.ID, tv.Key)
	if task.Status != domain.TaskBlocked {
		t.Fatalf("task = %s", task.Status)
	}
	if in, _ := e.svc.Inbox(ctx, e.co.ID); len(in.StoppedRuns) != 0 {
		t.Fatalf("inbox = %+v", in.StoppedRuns)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "run"})
	ok := false
	for _, a := range acts {
		var data struct {
			Decision string `json:"decision"`
		}
		if a.Verb == "run.decision" && a.SubjectID == run.ID && json.Unmarshal(a.Data, &data) == nil && data.Decision == "close" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("no run.decision activity: %+v", acts)
	}
}

// Falha comum: o card é calculado na leitura, e Tentar de novo abre uma run do zero ligada à que falhou.
func TestFailedRunCardAndRetry(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunFailed,
		Result: adapter.Result{ExitCode: 1, IsError: true, Error: "boom do agente", SessionRef: "sess-f"}}); err != nil {
		t.Fatal(err)
	}
	rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if d := rv.Diagnosis; d == nil || d.Cause != domain.CauseAgentError || !strings.Contains(d.CauseDetail, "boom do agente") || d.NextAction != domain.ActionHumanDecision || d.Owner != domain.OwnerHuman {
		t.Fatalf("diagnosis = %+v", d)
	}
	if rv.SteerMode != domain.SteerInject {
		t.Fatalf("steer mode = %q", rv.SteerMode)
	}
	if _, err := e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, run.ID, service.RunActionInput{Action: "x"}); err == nil {
		t.Fatal("unknown action")
	}
	if _, err := e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, run.ID, service.RunActionInput{Action: domain.DecisionContinue}); err != nil {
		t.Fatal(err)
	}
	cont, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerManual))
	if err != nil || cont.ContinuationReason != domain.ContinueManual {
		t.Fatalf("continue after failure = %+v %v", cont, err)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, cont.ID)
	if rc.ResumeSession != "sess-f" {
		t.Fatalf("a manual continue resumes the session: %q", rc.ResumeSession)
	}
	// an active or done run takes no action
	_, err = e.svc.RunAction(ctx, domain.HumanActor, e.co.ID, cont.ID, service.RunActionInput{Action: domain.DecisionClose})
	wantErr(t, err, domain.ErrInvalid)
}

// ---------------- follow-ups ----------------

func TestTaskFollowupQueueEditDiscardAndConsume(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)

	f1, err := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "  primeiro  ")
	if err != nil {
		t.Fatal(err)
	}
	if f1.Position != 1 || f1.WaitReason != domain.WaitRunActive || f1.WaitRunID == nil || *f1.WaitRunID != run.ID || f1.Body != "primeiro" || f1.Kind != domain.FollowupTask {
		t.Fatalf("f1 = %+v", f1)
	}
	f2, _ := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "segundo")
	f3, _ := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "terceiro")
	if f2.Position != 2 || f3.Position != 3 {
		t.Fatalf("positions: %d %d", f2.Position, f3.Position)
	}
	edited, err := e.svc.EditFollowup(ctx, domain.HumanActor, e.co.ID, f1.ID, "primeiro (editado)")
	if err != nil || edited.Body != "primeiro (editado)" || edited.EditedAt == nil {
		t.Fatalf("edit = %+v %v", edited, err)
	}
	if err := e.svc.DiscardFollowup(ctx, domain.HumanActor, e.co.ID, f2.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{TaskID: &tv.ID})
	if len(list) != 2 || list[0].ID != f1.ID || list[1].ID != f3.ID || list[1].Position != 2 {
		t.Fatalf("list = %+v", list)
	}
	if _, err := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "   "); err == nil {
		t.Fatal("empty follow-up")
	}
	if _, err := e.svc.QueueTaskFollowup(ctx, domain.PersonaActor(p.ID), e.co.ID, tv.ID, "x"); err == nil {
		t.Fatal("only a human queues")
	}

	// the run ends: the pending follow-ups wake the persona and the next run collects them, in order
	e.jobs.reset()
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "feito"}}); err != nil {
		t.Fatal(err)
	}
	if !hasWake(e, domain.TriggerComment) {
		t.Fatalf("pending follow-ups must wake the persona: %+v", e.jobs.wakes)
	}
	next, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerComment))
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{TaskID: &tv.ID}); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, next.ID)
	var bodies []string
	for _, c := range rc.Comments {
		if c.Author.Type == domain.ActorHuman {
			bodies = append(bodies, c.Body)
		}
	}
	if strings.Join(bodies, "|") != "primeiro (editado)|terceiro" {
		t.Fatalf("human comments = %v", bodies)
	}
	// consumed: no more edit or discard
	_, err = e.svc.EditFollowup(ctx, domain.HumanActor, e.co.ID, f1.ID, "tarde")
	wantErr(t, err, domain.ErrInvalid)
	wantErr(t, e.svc.DiscardFollowup(ctx, domain.HumanActor, e.co.ID, f3.ID), domain.ErrInvalid)
}

func TestTaskFollowupWithoutActiveRunWakesAndExplainsWait(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	f, err := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "olhe isto")
	if err != nil {
		t.Fatal(err)
	}
	if f.WaitReason != domain.WaitStarting || f.WaitRunID != nil {
		t.Fatalf("f = %+v", f)
	}
	if !hasWake(e, domain.TriggerComment) {
		t.Fatalf("no wake: %+v", e.jobs.wakes)
	}
	// a paused persona is the visible reason
	paused := domain.PersonaPaused
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: &paused}); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{TaskID: &tv.ID})
	if len(list) != 1 || list[0].WaitReason != domain.WaitPersonaUnavailable || !strings.Contains(list[0].WaitDetail, "pausada") {
		t.Fatalf("list = %+v", list)
	}
}

func TestCancelRunWithPendingFollowupsStartsNextRun(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	if _, err := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "mude de ideia"); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	// the executor finishes the cancelled run; the pending follow-up then starts the next one
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunCancelled, Result: adapter.Result{ExitCode: -1}}); err != nil {
		t.Fatal(err)
	}
	if !hasWake(e, domain.TriggerComment) {
		t.Fatalf("cancel must trigger the next run: %+v", e.jobs.wakes)
	}
	next, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerComment))
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, next.ID)
	last := rc.Comments[len(rc.Comments)-1]
	if last.Body != "mude de ideia" {
		t.Fatalf("thread = %+v", rc.Comments)
	}
}

func TestCancelQueuedRunWithPendingFollowupsWakes(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "antes de começar"); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	if !hasWake(e, domain.TriggerComment) {
		t.Fatalf("no wake: %+v", e.jobs.wakes)
	}
}

// ---------------- steer ----------------

// Mensagem injetada na run viva (harness com injeção): o broker leva o evento ao executor; o ack do adapter consome o
// follow-up, que vira comentário da task e linha do log; nenhuma run extra nasce por causa dele.
func TestSteerInjectsIntoActiveRun(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	ch, unsub := e.broker.Subscribe(events.RunTopic(run.ID.String()), 8)
	defer unsub()

	res, err := e.svc.SteerRun(ctx, domain.HumanActor, e.co.ID, run.ID, "agora foque nos testes")
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != domain.SteerInject || res.Followup.Delivery != domain.DeliverySteer || res.Followup.WaitReason != domain.WaitSteerPending {
		t.Fatalf("res = %+v", res)
	}
	m := <-ch
	ev, ok := m.Data.(service.SteerEvent)
	if m.Type != service.RunSteerMsg || !ok || ev.FollowupID != res.Followup.ID || ev.Text != "agora foque nos testes" {
		t.Fatalf("message = %+v", m)
	}
	if list, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{TaskID: &tv.ID}); len(list) != 1 || list[0].WaitReason != domain.WaitSteerPending {
		t.Fatalf("before the ack: %+v", list)
	}

	e.jobs.reset()
	if err := e.svc.MarkSteerDelivered(ctx, e.co.ID, run.ID, res.Followup.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{TaskID: &tv.ID}); len(list) != 0 {
		t.Fatalf("after the ack: %+v", list)
	}
	evs, _ := e.svc.ListRunEvents(ctx, e.co.ID, run.ID, 0, 50)
	logged := false
	for _, ev := range evs {
		if ev.Kind == domain.EventSystem && strings.Contains(ev.Text, "mensagem do operador injetada") && strings.Contains(ev.Text, "foque nos testes") {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("the log must show the injection: %+v", evs)
	}
	// delivered twice is refused, not duplicated
	wantErr(t, e.svc.MarkSteerDelivered(ctx, e.co.ID, run.ID, res.Followup.ID), domain.ErrInvalid)

	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "feito"}}); err != nil {
		t.Fatal(err)
	}
	if hasWake(e, domain.TriggerComment) {
		t.Fatalf("a steered message was seen by the run: no re-wake expected: %+v", e.jobs.wakes)
	}
	// finished runs cannot be steered
	_, err = e.svc.SteerRun(ctx, domain.HumanActor, e.co.ID, run.ID, "tarde")
	wantErr(t, err, domain.ErrInvalid)
}

// Harness sem injeção (codex): a run é interrompida e a mensagem inicia a continuação, com a sessão.
func TestSteerRestartsHarnessWithoutInjection(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Cod", func(in *service.HirePersonaInput) { in.Agent = "codex" })
	tv := e.task(t, service.CreateTaskInput{Title: "t", PersonaID: &p.ID})
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	e.event(t, run.ID, domain.EventSystem, "sessão iniciada thr-1", "", "thr-1")
	ch, unsub := e.broker.Subscribe(events.RunTopic(run.ID.String()), 8)
	defer unsub()
	e.jobs.reset()

	res, err := e.svc.SteerRun(ctx, domain.HumanActor, e.co.ID, run.ID, "troque a abordagem")
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != domain.SteerRestart || res.Followup.WaitReason != domain.WaitRestartPending {
		t.Fatalf("res = %+v", res)
	}
	if m := <-ch; m.Type != "run.cancel" {
		t.Fatalf("the run must be cancelled, got %+v", m)
	}
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunCancelled, Result: adapter.Result{ExitCode: -1}}); err != nil {
		t.Fatal(err)
	}
	if !hasWake(e, domain.TriggerComment) {
		t.Fatalf("no wake: %+v", e.jobs.wakes)
	}
	next, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerComment))
	if err != nil {
		t.Fatal(err)
	}
	if next.ContinuationReason != domain.ContinueSteer || next.ContinuationAttempt != 0 || *next.ContinuationOfRunID != run.ID {
		t.Fatalf("next = %+v", next)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, next.ID)
	if rc.ResumeSession != "thr-1" || rc.Comments[len(rc.Comments)-1].Body != "troque a abordagem" {
		t.Fatalf("context: session=%q comments=%+v", rc.ResumeSession, rc.Comments)
	}
}

// ---------------- chat ----------------

func TestChatFollowupQueueEditDiscardAndCancelStartsNext(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	m1, err := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "segunda")
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "terceira")
	m3, _ := e.svc.SendMessage(ctx, domain.HumanActor, e.co.ID, cv.ID, "quarta")

	fs, err := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{ConversationID: &cv.ID})
	if err != nil || len(fs) != 3 {
		t.Fatalf("followups = %+v %v", fs, err)
	}
	if fs[0].ID != m1.ID || fs[0].Position != 1 || fs[0].WaitReason != domain.WaitRunActive || fs[0].WaitRunID == nil || *fs[0].WaitRunID != run.ID || fs[2].Kind != domain.FollowupChat {
		t.Fatalf("followups = %+v", fs)
	}
	ed, err := e.svc.EditFollowup(ctx, domain.HumanActor, e.co.ID, m1.ID, "segunda (editada)")
	if err != nil || ed.Body != "segunda (editada)" || ed.EditedAt == nil {
		t.Fatalf("edit = %+v %v", ed, err)
	}
	if err := e.svc.DiscardFollowup(ctx, domain.HumanActor, e.co.ID, m2.ID); err != nil {
		t.Fatal(err)
	}
	_ = m3

	// cancelling the active run starts the next one with what is pending (ADR-0039)
	if _, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunCancelled, Result: adapter.Result{ExitCode: -1}}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if got.ActiveRunID == nil || *got.ActiveRunID == run.ID || got.PendingMessages != 0 || len(e.jobs.execs) != 1 {
		t.Fatalf("conversation = %+v execs=%v", got, e.jobs.execs)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, *got.ActiveRunID)
	if rc.Chat == nil || len(rc.Chat.New) != 2 || rc.Chat.New[0].Body != "segunda (editada)" || rc.Chat.New[1].Body != "quarta" {
		t.Fatalf("new messages = %+v", rc.Chat)
	}
	// consumed: no more edits
	_, err = e.svc.EditFollowup(ctx, domain.HumanActor, e.co.ID, m1.ID, "tarde")
	wantErr(t, err, domain.ErrInvalid)
}

func TestChatRunInterruptedContinuesOnItsOwn(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	e.event(t, run.ID, domain.EventSystem, "sessão", "", "chat-sess")
	e.jobs.reset()
	e.crash(t)

	got, _ := e.svc.GetConversation(ctx, e.co.ID, cv.ID)
	if got.ActiveRunID == nil || *got.ActiveRunID == run.ID || len(e.jobs.execs) != 1 {
		t.Fatalf("conversation = %+v execs = %v", got, e.jobs.execs)
	}
	next, _ := e.svc.GetRun(ctx, e.co.ID, *got.ActiveRunID)
	if next.ContinuationOfRunID == nil || *next.ContinuationOfRunID != run.ID || next.ContinuationAttempt != 1 || next.Trigger != domain.TriggerChat {
		t.Fatalf("next = %+v", next.Run)
	}
	rc, _ := e.svc.LoadRunContext(ctx, e.co.ID, next.ID)
	if rc.Chat == nil || rc.Chat.ResumeSession != "chat-sess" || len(rc.Chat.New) != 1 || rc.Continuation == nil {
		t.Fatalf("context = %+v / %+v", rc.Chat, rc.Continuation)
	}
	msgs, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	if last := msgs[len(msgs)-1]; last.Kind != domain.MessageSystem || !strings.Contains(last.Body, "Retomando sozinha") {
		t.Fatalf("notice = %+v", last)
	}
}

func TestChatSteerInjectedMessageBelongsToTheRun(t *testing.T) {
	ctx := context.Background()
	e, _, cv, run := chatFixture(t)
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "tr", ""); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.SteerRun(ctx, domain.HumanActor, e.co.ID, run.ID, "responda só em uma linha")
	if err != nil || res.Mode != domain.SteerInject || res.Followup.Kind != domain.FollowupChat {
		t.Fatalf("res = %+v %v", res, err)
	}
	if fs, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{ConversationID: &cv.ID}); len(fs) != 1 || fs[0].WaitReason != domain.WaitSteerPending {
		t.Fatalf("followups = %+v", fs)
	}
	if err := e.svc.MarkSteerDelivered(ctx, e.co.ID, run.ID, res.Followup.ID); err != nil {
		t.Fatal(err)
	}
	if fs, _ := e.svc.ListFollowups(ctx, e.co.ID, service.FollowupTarget{ConversationID: &cv.ID}); len(fs) != 0 {
		t.Fatalf("followups = %+v", fs)
	}
	msgs, _ := e.svc.ListConversationMessages(ctx, e.co.ID, cv.ID, 0)
	last := msgs[len(msgs)-1]
	if last.Body != "responda só em uma linha" || last.RunID == nil || *last.RunID != run.ID {
		t.Fatalf("last = %+v", last)
	}
}

// Rede de segurança: se o wake que deveria recolher um follow-up se perdeu, o heartbeat enxerga o follow-up parado há
// mais de um minuto e a task volta a ser candidata.
func TestPendingFollowupIsAWakeCandidateWhenStale(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return now }

	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	if _, err := e.svc.QueueTaskFollowup(ctx, domain.HumanActor, e.co.ID, tv.ID, "mais uma coisa"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunFailed, Result: adapter.Result{IsError: true, Error: "x"}}); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.svc.WakeCandidates(ctx, 10); len(c) != 0 {
		t.Fatalf("too early: %+v", c)
	}
	now = now.Add(2 * time.Minute)
	if c, _ := e.svc.WakeCandidates(ctx, 10); len(c) != 1 || c[0].TaskID != tv.ID {
		t.Fatalf("stale follow-up must wake the task: %+v", c)
	}
}

// A continuation never dispatches a held task (ADR-0040): the wake is recorded and, once the task resumes, the
// continuation still resumes the session of the interrupted run.
func TestContinuationRespectsAHeldTask(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startedRun(t, e, p, tv, domain.TriggerAssign)
	e.event(t, run.ID, domain.EventSystem, "sess", "", "sess-h")
	e.crash(t)
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, tv.ID, "congelada"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
	wantErr(t, err, domain.ErrTaskHeld)
	if _, err := e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, tv.ID); err != nil {
		t.Fatal(err)
	}
	cont, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
	if err != nil || cont.ContinuationOfRunID == nil || *cont.ContinuationOfRunID != run.ID {
		t.Fatalf("cont = %+v %v", cont, err)
	}
}
