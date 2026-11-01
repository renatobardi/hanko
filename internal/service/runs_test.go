package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// runFixture: persona "Eng" with a todo task assigned to it.
func runFixture(t *testing.T) (*env, domain.Persona, service.TaskView) {
	t.Helper()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "Implementar X", PersonaID: &p.ID})
	e.jobs.reset()
	return e, p, tv
}

func wakeOf(p domain.Persona, tv service.TaskView, trig domain.RunTrigger) service.Wake {
	return service.Wake{CompanyID: tv.CompanyID, PersonaID: p.ID, TaskID: tv.ID, Trigger: trig}
}

func TestCheckoutRun(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)

	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunQueued || run.Agent != "fake" || run.Model != "m1" || run.Trigger != domain.TriggerAssign {
		t.Fatalf("run: %+v", run)
	}
	if len(e.jobs.execs) != 1 || e.jobs.execs[0] != run.ID {
		t.Fatalf("execute_run not enqueued: %v", e.jobs.execs)
	}
	got, _ := e.svc.GetTask(ctx, e.co.ID, tv.Key)
	if got.Status != domain.TaskInProgress || got.ActiveRunID == nil || *got.ActiveRunID != run.ID {
		t.Fatalf("task after checkout: %+v", got)
	}

	// occupied
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
	wantErr(t, err, domain.ErrActiveRun)

	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "run"})
	if len(acts) != 1 || acts[0].Verb != "run.queued" {
		t.Fatalf("activity: %+v", acts)
	}
}

func TestCheckoutRunRejections(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	other := e.persona(t, "Other", nil)
	paused := e.persona(t, "Paused", nil)
	_, _ = e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, paused.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaPaused)})
	backlog := e.task(t, service.CreateTaskInput{PersonaID: &p.ID, Status: domain.TaskBacklog})
	unassigned := e.task(t, service.CreateTaskInput{})
	pausedTask := e.task(t, service.CreateTaskInput{PersonaID: &paused.ID})

	tests := []struct {
		name string
		w    service.Wake
		want error
	}{
		{"wrong persona", wakeOf(other, tv, domain.TriggerAssign), domain.ErrInvalid},
		{"not workable", wakeOf(p, backlog, domain.TriggerAssign), domain.ErrInvalid},
		{"unassigned", wakeOf(p, unassigned, domain.TriggerAssign), domain.ErrInvalid},
		{"persona cannot work", wakeOf(paused, pausedTask, domain.TriggerAssign), domain.ErrInvalid},
		{"bad trigger", wakeOf(p, tv, "weird"), domain.ErrInvalid},
		{"unknown task", service.Wake{CompanyID: e.co.ID, PersonaID: p.ID, TaskID: domain.NewID(), Trigger: domain.TriggerManual}, domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.CheckoutRun(ctx, tt.w)
			wantErr(t, err, tt.want)
		})
	}
	if len(e.jobs.execs) != 0 {
		t.Fatalf("rejections must not enqueue execution: %v", e.jobs.execs)
	}
}

func TestCheckoutRunRace(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
		}()
	}
	wg.Wait()
	ok, busy := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrActiveRun):
			busy++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || busy != n-1 {
		t.Fatalf("ok=%d busy=%d, want 1 and %d", ok, busy, n-1)
	}
	if len(e.jobs.execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(e.jobs.execs))
	}
	runs, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{TaskID: &tv.ID})
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
}

func TestRunLifecycleDone(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	ch, unsub := e.broker.Subscribe(events.RunTopic(run.ID.String()), 32)
	defer unsub()

	started, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "trace-1", "handoff!")
	if err != nil || started.Status != domain.RunRunning || started.StartedAt == nil || started.TraceID != "trace-1" || started.HandoffIn != "handoff!" {
		t.Fatalf("started: %+v %v", started, err)
	}
	if pp, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); pp.Status != domain.PersonaRunning {
		t.Fatalf("persona status = %s", pp.Status)
	}
	// second start is invalid (no longer queued)
	_, err = e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "t", "")
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.MarkRunStarted(ctx, e.co.ID, domain.NewID(), "t", "")
	wantErr(t, err, domain.ErrNotFound)

	// events: seq increments, concurrent appends stay unique
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: "msg"}); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wg.Wait()
	ev, err := e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventToolUse, Text: "ls", Payload: json.RawMessage(`{"cmd":"ls"}`), OccurredAt: time.Now().Add(-time.Minute)})
	if err != nil || ev.Seq != 9 {
		t.Fatalf("event: %+v %v", ev, err)
	}
	evs, err := e.svc.ListRunEvents(ctx, e.co.ID, run.ID, 0, 0)
	if err != nil || len(evs) != 9 {
		t.Fatalf("events = %d %v", len(evs), err)
	}
	for i, x := range evs {
		if x.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %d", i, x.Seq)
		}
	}
	tail, _ := e.svc.ListRunEvents(ctx, e.co.ID, run.ID, 7, 100)
	if len(tail) != 2 || string(tail[1].Payload) != `{"cmd": "ls"}` {
		t.Fatalf("tail: %+v", tail)
	}
	_, err = e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: "bogus"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.AppendRunEvent(ctx, e.co.ID, run.ID, adapter.Event{Kind: domain.EventStdout, Payload: json.RawMessage(`{`)})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.AppendRunEvent(ctx, e.co.ID, domain.NewID(), adapter.Event{Kind: domain.EventStdout})
	wantErr(t, err, domain.ErrNotFound)

	// finish
	cost := 0.0421
	done, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{
		ExitCode: 0, SessionRef: "sess-1", Summary: "Implementei X.", CostUSD: &cost, Model: "m-real",
		Usage: domain.Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 3, CacheCreationTokens: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.RunDone || done.FinishedAt == nil || done.CostUSD == nil || *done.CostUSD != cost ||
		done.SessionRef != "sess-1" || done.Usage.OutputTokens != 20 || done.Model != "m-real" || done.ExitCode == nil {
		t.Fatalf("finished run: %+v", done)
	}

	// comment posted by the persona
	comments, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID)
	if len(comments) != 1 || comments[0].Author.Type != domain.ActorPersona || *comments[0].Author.ID != p.ID ||
		comments[0].RunID == nil || *comments[0].RunID != run.ID || comments[0].Body != "Implementei X." {
		t.Fatalf("comments: %+v", comments)
	}
	// persona back to idle
	if pp, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); pp.Status != domain.PersonaIdle {
		t.Fatalf("persona status = %s", pp.Status)
	}
	// activity
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "run", SubjectID: &run.ID})
	verbs := map[string]bool{}
	for _, a := range acts {
		verbs[a.Verb] = true
	}
	if !verbs["run.queued"] || !verbs["run.started"] || !verbs["run.finished"] {
		t.Fatalf("verbs = %v", verbs)
	}
	// outbox: task.created + run.finished
	var fact memory.Fact
	found := false
	for _, id := range e.jobs.outbox {
		item, err := e.svc.GetMemoryItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if item.Kind == "run.finished" {
			found = true
			if err := json.Unmarshal(item.Payload, &fact); err != nil {
				t.Fatal(err)
			}
			if item.EventID != "run.finished:"+run.ID.String() || item.CompanySlug != e.co.Slug {
				t.Errorf("item: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("no run.finished outbox row; outbox=%v", e.jobs.outbox)
	}
	wantPath := "hanko/runs/" + time.Now().UTC().Format("2006-01-02") + "-" + tv.Key + "-" + run.ID.String()[:8] + ".md"
	if fact.Path != wantPath {
		t.Errorf("path = %q, want %q", fact.Path, wantPath)
	}
	for _, sub := range []string{tv.Key, "Eng", "done", "Implementei X.", "0.0421"} {
		if !strings.Contains(fact.Body, sub) {
			t.Errorf("body lacks %q:\n%s", sub, fact.Body)
		}
	}

	// finish is idempotent
	again, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunFailed})
	if err != nil || again.Status != domain.RunDone {
		t.Fatalf("second finish: %+v %v", again, err)
	}
	if c, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID); len(c) != 1 {
		t.Fatalf("duplicate summary comment: %d", len(c))
	}

	// broker saw status updates
	got := map[string]int{}
	for len(ch) > 0 {
		got[(<-ch).Type]++
	}
	if got["run.event"] != 9 || got["run.status"] != 2 {
		t.Fatalf("broker messages = %v", got)
	}

	// views
	rv, err := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if err != nil || rv.TaskKey != tv.Key || rv.TaskTitle != "Implementar X" || rv.PersonaName != "Eng" {
		t.Fatalf("run view: %+v %v", rv, err)
	}
	_, err = e.svc.GetRun(ctx, e.co.ID, domain.NewID())
	wantErr(t, err, domain.ErrNotFound)

	// dashboard month cost
	d, err := e.svc.Dashboard(ctx, e.co.ID)
	if err != nil || d.MonthCostUSD < 0.0420 || d.MonthCostUSD > 0.0422 || d.OpenTasks != 1 || d.TasksInProgress != 1 || d.ActivePersonas != 1 {
		t.Fatalf("dashboard: %+v %v", d, err)
	}
}

func TestFinishRunFailureAndValidation(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}

	_, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunRunning})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: "zzz"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.FinishRun(ctx, e.co.ID, domain.NewID(), service.FinishRunInput{Status: domain.RunDone})
	wantErr(t, err, domain.ErrNotFound)

	failed, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunFailed, Result: adapter.Result{ExitCode: 2, IsError: true, Error: "boom", Summary: "não deve virar comentário"}})
	if err != nil || failed.Status != domain.RunFailed || failed.Error != "boom" || failed.CostUSD != nil {
		t.Fatalf("failed: %+v %v", failed, err)
	}
	if c, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID); len(c) != 0 {
		t.Fatalf("failed run must not comment: %+v", c)
	}
	// one failure is not enough for error: back to idle, with the last error recorded
	if pp, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); pp.Status != domain.PersonaIdle {
		t.Fatalf("persona = %s, want idle", pp.Status)
	}
	h, err := e.svc.GetPersonaHealth(ctx, e.co.ID, p.ID)
	if err != nil || h.LastError != "boom" || h.LastErrorAt == nil || h.ConsecutiveFailures != 1 {
		t.Fatalf("health = %+v %v", h, err)
	}
}

func TestCancelRun(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)

	// queued → cancelled
	run, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	rv, err := e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID)
	if err != nil || rv.Status != domain.RunCancelled || rv.FinishedAt == nil {
		t.Fatalf("cancel queued: %+v %v", rv, err)
	}
	_, err = e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run.ID)
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", "")
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, domain.NewID())
	wantErr(t, err, domain.ErrNotFound)

	// the slot is free again; running → publishes run.cancel, stays running
	run2, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerManual))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run2.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	ch, unsub := e.broker.Subscribe(events.RunTopic(run2.ID.String()), 8)
	defer unsub()
	rv, err = e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, run2.ID)
	if err != nil || rv.Status != domain.RunRunning {
		t.Fatalf("cancel running: %+v %v", rv, err)
	}
	select {
	case m := <-ch:
		if m.Type != "run.cancel" {
			t.Fatalf("message = %+v", m)
		}
	default:
		t.Fatal("run.cancel not published")
	}
	// the executor then finishes it as cancelled
	fin, err := e.svc.FinishRun(ctx, e.co.ID, run2.ID, service.FinishRunInput{Status: domain.RunCancelled, Result: adapter.Result{ExitCode: -1}})
	if err != nil || fin.Status != domain.RunCancelled {
		t.Fatalf("finish cancelled: %+v %v", fin, err)
	}
}

func TestInterruptOrphansAndWakeCandidates(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return now }

	hb := e.persona(t, "HB", func(in *service.HirePersonaInput) { in.HeartbeatIntervalSec = 600 })
	nohb := e.persona(t, "NoHB", nil)
	a := e.task(t, service.CreateTaskInput{Title: "a", PersonaID: &hb.ID})
	e.task(t, service.CreateTaskInput{Title: "b", PersonaID: &nohb.ID})
	e.task(t, service.CreateTaskInput{Title: "c", PersonaID: &hb.ID, Status: domain.TaskBacklog})

	cands, err := e.svc.WakeCandidates(ctx, 10)
	if err != nil || len(cands) != 1 || cands[0].TaskID != a.ID || cands[0].PersonaID != hb.ID {
		t.Fatalf("candidates: %+v %v", cands, err)
	}

	run, _ := e.svc.CheckoutRun(ctx, wakeOf(hb, a, domain.TriggerHeartbeat))
	if cands, _ = e.svc.WakeCandidates(ctx, 10); len(cands) != 0 {
		t.Fatalf("active run excludes candidate: %+v", cands)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.InterruptOrphanRuns(ctx)
	if err != nil || n != 1 {
		t.Fatalf("orphans = %d %v", n, err)
	}
	rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID)
	if rv.Status != domain.RunInterrupted || rv.FinishedAt == nil {
		t.Fatalf("run: %+v", rv.Run)
	}
	if n, _ := e.svc.InterruptOrphanRuns(ctx); n != 0 {
		t.Fatalf("second pass = %d", n)
	}
	if pp, _ := e.svc.GetPersona(ctx, e.co.ID, hb.ID.String()); pp.Status != domain.PersonaIdle {
		t.Fatalf("persona = %s", pp.Status)
	}

	// interval not elapsed yet → not a candidate; elapsed → candidate
	if cands, _ = e.svc.WakeCandidates(ctx, 10); len(cands) != 0 {
		t.Fatalf("interval not elapsed: %+v", cands)
	}
	now = now.Add(11 * time.Minute)
	// a: its interval elapsed. b: no interval at all, but untouched for over a minute with no run
	// (the safety net for lost wakes). c is a backlog task: never.
	cands, _ = e.svc.WakeCandidates(ctx, 10)
	got := map[domain.ID]bool{}
	for _, c := range cands {
		got[c.TaskID] = true
	}
	if len(cands) != 2 || !got[a.ID] {
		t.Fatalf("interval elapsed: %+v", cands)
	}
}

func TestListRunsFilters(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	r1, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	_, _ = e.svc.CancelRun(ctx, domain.HumanActor, e.co.ID, r1.ID)
	r2, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerManual))

	all, err := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{})
	if err != nil || len(all) != 2 || all[0].ID != r2.ID {
		t.Fatalf("all: %+v %v", all, err)
	}
	q, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{Statuses: []domain.RunStatus{domain.RunQueued}, PersonaID: &p.ID, TaskID: &tv.ID})
	if len(q) != 1 || q[0].ID != r2.ID || q[0].TaskKey != tv.Key {
		t.Fatalf("filtered: %+v", q)
	}
	if one, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{Limit: 1}); len(one) != 1 {
		t.Fatalf("limit: %d", len(one))
	}
	_, err = e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{Statuses: []domain.RunStatus{"x"}})
	wantErr(t, err, domain.ErrInvalid)
}

func TestLoadRunContext(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ws, _ := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "W", Path: "/tmp/w"})
	p := e.persona(t, "Eng", func(in *service.HirePersonaInput) { in.WorkspaceID = &ws.ID })
	tv := e.task(t, service.CreateTaskInput{PersonaID: &p.ID})
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "olá"}); err != nil {
		t.Fatal(err)
	}
	run, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))

	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Run.ID != run.ID || rc.Company.ID != e.co.ID || rc.Persona.ID != p.ID || rc.Task.Key != tv.Key ||
		len(rc.Comments) != 1 || rc.Workspace == nil || rc.Workspace.Path != "/tmp/w" {
		t.Fatalf("context: %+v", rc)
	}
	_, err = e.svc.LoadRunContext(ctx, e.co.ID, domain.NewID())
	wantErr(t, err, domain.ErrNotFound)

	// no workspace
	p2 := e.persona(t, "Solo", nil)
	tv2 := e.task(t, service.CreateTaskInput{PersonaID: &p2.ID})
	run2, _ := e.svc.CheckoutRun(ctx, wakeOf(p2, tv2, domain.TriggerAssign))
	rc2, err := e.svc.LoadRunContext(ctx, e.co.ID, run2.ID)
	if err != nil || rc2.Workspace != nil {
		t.Fatalf("no workspace expected: %+v %v", rc2.Workspace, err)
	}
}

func TestMemoryFactsAndOutbox(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	tv := e.task(t, service.CreateTaskInput{Title: "Fato"})
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "nota humana"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddComment(ctx, domain.SystemActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "sistema"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}

	kinds := map[string]memory.Fact{}
	for _, id := range e.jobs.outbox {
		item, err := e.svc.GetMemoryItem(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var f memory.Fact
		_ = json.Unmarshal(item.Payload, &f)
		kinds[item.Kind] = f
	}
	if len(e.jobs.outbox) != 3 {
		t.Fatalf("outbox rows = %d, want 3 (created, human comment, closed): %v", len(e.jobs.outbox), kinds)
	}
	for _, k := range []string{"task.created", "task.closed", "comment.human"} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("missing fact %s", k)
		}
	}
	if kinds["task.created"].Path != "hanko/tasks/"+tv.Key+".md" || kinds["task.closed"].Path != "hanko/tasks/"+tv.Key+".md" {
		t.Errorf("paths: %+v", kinds)
	}

	// delivery state machine
	id := e.jobs.outbox[0]
	now := time.Now().UTC()
	e.svc.Now = func() time.Time { return now.Add(time.Hour) }
	pend, err := e.svc.PendingMemory(ctx, 10)
	if err != nil || len(pend) != 3 {
		t.Fatalf("pending: %v %v", pend, err)
	}
	if err := e.svc.MarkMemoryFailed(ctx, id, "provider down", false); err != nil {
		t.Fatal(err)
	}
	item, _ := e.svc.GetMemoryItem(ctx, id)
	if item.Attempts != 1 {
		t.Fatalf("attempts = %d", item.Attempts)
	}
	if err := e.svc.MarkMemorySent(ctx, id); err != nil {
		t.Fatal(err)
	}
	if pend, _ = e.svc.PendingMemory(ctx, 10); len(pend) != 2 {
		t.Fatalf("pending after sent = %d", len(pend))
	}
	e.svc.Now = func() time.Time { return now.Add(-time.Hour) }
	if pend, _ = e.svc.PendingMemory(ctx, 10); len(pend) != 0 {
		t.Fatalf("fresh rows are not swept: %d", len(pend))
	}
	_, err = e.svc.GetMemoryItem(ctx, domain.NewID())
	wantErr(t, err, domain.ErrNotFound)
}

func TestActivityFilterAndDashboardEmpty(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	tv := e.task(t, service.CreateTaskInput{})
	e.task(t, service.CreateTaskInput{})

	all, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{})
	if err != nil || len(all) != 5 { // company.created + 2 access_template.seeded (ADR-0089) + 2 task.created
		t.Fatalf("all = %d %v", len(all), err)
	}
	if all[0].Verb != "task.created" {
		t.Errorf("newest first expected, got %s", all[0].Verb)
	}
	byTask, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "task", SubjectID: &tv.ID})
	if len(byTask) != 1 {
		t.Fatalf("by task = %d", len(byTask))
	}
	before := all[1].OccurredAt
	older, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{Before: &before})
	if len(older) != 3 {
		t.Fatalf("before: %+v", older)
	}
	for _, a := range older {
		if a.Verb == "task.created" {
			t.Fatalf("before devolveu task: %+v", older)
		}
	}
	if lim, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{Limit: 1}); len(lim) != 1 {
		t.Fatalf("limit = %d", len(lim))
	}

	d, err := e.svc.Dashboard(ctx, e.co.ID)
	if err != nil || d.OpenTasks != 2 || d.MonthCostUSD != 0 || d.RunningRuns != 0 || d.PendingGates != 0 {
		t.Fatalf("dashboard: %+v %v", d, err)
	}
	_, err = e.svc.Dashboard(ctx, domain.NewID())
	wantErr(t, err, domain.ErrNotFound)
}

// Comentário humano em in_review/blocked acorda a persona e reabre a task (in_progress); heartbeat e
// atribuição continuam exigindo todo/in_progress.
func TestCheckoutRunComentarioReabreInReviewEBlocked(t *testing.T) {
	ctx := context.Background()
	for _, st := range []domain.TaskStatus{domain.TaskInReview, domain.TaskBlocked} {
		t.Run(string(st), func(t *testing.T) {
			e, p, _ := runFixture(t)
			tv := e.task(t, service.CreateTaskInput{Title: "Revisar", PersonaID: &p.ID, Status: st})
			e.jobs.reset()
			if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat)); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("heartbeat deveria recusar %s: %v", st, err)
			}
			run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerComment))
			if err != nil {
				t.Fatalf("comment em %s: %v", st, err)
			}
			got, _ := e.svc.GetTask(ctx, e.co.ID, tv.Key)
			if got.Status != domain.TaskInProgress || got.ActiveRunID == nil || *got.ActiveRunID != run.ID {
				t.Fatalf("task após reabrir: %+v", got)
			}
		})
	}
}
