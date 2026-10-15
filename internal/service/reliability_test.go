package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// ticking makes every clock read one second later, so "created after started_at" is observable.
func ticking(e *env) {
	var mu sync.Mutex
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t0 = t0.Add(time.Second)
		return t0
	}
}

func startRun(t *testing.T, e *env, p domain.Persona, tv service.TaskView) domain.Run {
	t.Helper()
	ctx := context.Background()
	run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, e.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	return run
}

func finish(t *testing.T, e *env, runID domain.ID, st domain.RunStatus, res adapter.Result) {
	t.Helper()
	if _, err := e.svc.FinishRun(context.Background(), e.co.ID, runID, service.FinishRunInput{Status: st, Result: res}); err != nil {
		t.Fatal(err)
	}
}

// Item 1: the result is stored before finishing and cleared once the run is finished.
func TestPendingResultRoundTrip(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	run := startRun(t, e, p, tv)

	rc, err := e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil || rc.PendingResult != nil {
		t.Fatalf("pending before save: %+v %v", rc.PendingResult, err)
	}
	cost := 0.25
	in := service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "feito", SessionRef: "s1", CostUSD: &cost,
		Usage: domain.Usage{InputTokens: 7}}}
	if err := e.svc.SavePendingResult(ctx, e.co.ID, run.ID, in); err != nil {
		t.Fatal(err)
	}
	rc, err = e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if err != nil || rc.PendingResult == nil || rc.PendingResult.Status != domain.RunDone || rc.PendingResult.Result.Summary != "feito" ||
		rc.PendingResult.Result.CostUSD == nil || *rc.PendingResult.Result.CostUSD != 0.25 || rc.PendingResult.Result.Usage.InputTokens != 7 {
		t.Fatalf("pending after save: %+v %v", rc.PendingResult, err)
	}
	finish(t, e, run.ID, in.Status, in.Result)
	rc, _ = e.svc.LoadRunContext(ctx, e.co.ID, run.ID)
	if rc.PendingResult != nil || rc.Run.Status != domain.RunDone {
		t.Fatalf("pending must be cleared: %+v status=%s", rc.PendingResult, rc.Run.Status)
	}
	// saving on a finished run is a no-op
	if err := e.svc.SavePendingResult(ctx, e.co.ID, run.ID, in); err != nil {
		t.Fatal(err)
	}
	if rc, _ = e.svc.LoadRunContext(ctx, e.co.ID, run.ID); rc.PendingResult != nil {
		t.Fatal("finished run must not keep a pending result")
	}
}

// Item 2: a human comment posted while the run was active wakes the persona again at finish.
func TestFinishRunWakesForCommentDuringRun(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	ticking(e)

	// comment before the run started: already in the prompt, no wake
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "antes"}); err != nil {
		t.Fatal(err)
	}
	run := startRun(t, e, p, tv)
	e.jobs.reset()
	finish(t, e, run.ID, domain.RunDone, adapter.Result{Summary: "ok"})
	if e.jobs.wakeCount() != 0 {
		t.Fatalf("no comment during the run → no wake, got %+v", e.jobs.wakes)
	}

	// comment during the run
	run2 := startRun(t, e, p, tv)
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "durante"}); err != nil {
		t.Fatal(err)
	}
	// persona comments do not count
	if _, err := e.svc.AddComment(ctx, domain.PersonaActor(p.ID), e.co.ID, tv.ID, service.AddCommentInput{Body: "eu mesma"}); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	finish(t, e, run2.ID, domain.RunDone, adapter.Result{Summary: "ok"})
	if e.jobs.wakeCount() != 1 || e.jobs.wakes[0].Trigger != domain.TriggerComment || e.jobs.wakes[0].TaskID != tv.ID || e.jobs.wakes[0].PersonaID != p.ID {
		t.Fatalf("wakes = %+v", e.jobs.wakes)
	}

	// a cancelled run is not re-woken by the comment
	run3 := startRun(t, e, p, tv)
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tv.ID, service.AddCommentInput{Body: "de novo"}); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()
	finish(t, e, run3.ID, domain.RunCancelled, adapter.Result{ExitCode: -1})
	if e.jobs.wakeCount() != 0 {
		t.Fatalf("cancelled run must not re-wake: %+v", e.jobs.wakes)
	}
}

// Item 3: error only after 3 failures in a row; success resets; reactivation resets.
func TestPersonaErrorOnlyAfterThreeFailures(t *testing.T) {
	ctx := context.Background()
	e, p, tv := runFixture(t)
	status := func() domain.PersonaStatus {
		pp, err := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
		if err != nil {
			t.Fatal(err)
		}
		return pp.Status
	}
	fail := func(msg string) {
		run := startRun(t, e, p, tv)
		finish(t, e, run.ID, domain.RunFailed, adapter.Result{IsError: true, Error: msg})
	}

	fail("um")
	fail("dois")
	if status() != domain.PersonaIdle {
		t.Fatalf("after 2 failures: %s", status())
	}
	// success resets the counter and clears the last error
	ok := startRun(t, e, p, tv)
	finish(t, e, ok.ID, domain.RunDone, adapter.Result{Summary: "ok"})
	h, _ := e.svc.GetPersonaHealth(ctx, e.co.ID, p.ID)
	if h.ConsecutiveFailures != 0 || h.LastError != "" || h.LastErrorAt != nil {
		t.Fatalf("health after success: %+v", h)
	}

	fail("a")
	fail("b")
	fail("c")
	if status() != domain.PersonaError {
		t.Fatalf("after 3 failures in a row: %s", status())
	}
	h, _ = e.svc.GetPersonaHealth(ctx, e.co.ID, p.ID)
	if h.ConsecutiveFailures != 3 || h.LastError != "c" {
		t.Fatalf("health: %+v", h)
	}
	_, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerHeartbeat))
	wantErr(t, err, domain.ErrInvalid)

	// reactivating clears the counter: one more failure does not put it back in error
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaActive)}); err != nil {
		t.Fatal(err)
	}
	if h, _ = e.svc.GetPersonaHealth(ctx, e.co.ID, p.ID); h.ConsecutiveFailures != 0 {
		t.Fatalf("reactivation must reset the counter: %+v", h)
	}
	fail("d")
	if status() != domain.PersonaIdle {
		t.Fatalf("after reactivation + 1 failure: %s", status())
	}
	// cancelled / interrupted runs do not count
	for _, st := range []domain.RunStatus{domain.RunCancelled, domain.RunExpired} {
		run := startRun(t, e, p, tv)
		finish(t, e, run.ID, st, adapter.Result{Error: "x"})
	}
	if h, _ = e.svc.GetPersonaHealth(ctx, e.co.ID, p.ID); h.ConsecutiveFailures != 1 {
		t.Fatalf("neutral outcomes keep the counter: %+v", h)
	}
}

// Item 4: orphan runs wake the assignee of still-open tasks, in the same tx.
func TestInterruptOrphansWakesOpenTasks(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	open := e.task(t, service.CreateTaskInput{Title: "aberta", PersonaID: &p.ID})
	p2 := e.persona(t, "Eng2", nil)
	closed := e.task(t, service.CreateTaskInput{Title: "fechada", PersonaID: &p2.ID})
	startRun(t, e, p, open)
	startRun(t, e, p2, closed)
	// the second task is closed behind the run's back (no run cancellation: straight in the DB)
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE tasks SET status='done' WHERE id=$1`, closed.ID); err != nil {
		t.Fatal(err)
	}
	e.jobs.reset()

	n, err := e.svc.InterruptOrphanRuns(ctx)
	if err != nil || n != 2 {
		t.Fatalf("orphans = %d %v", n, err)
	}
	if e.jobs.wakeCount() != 1 {
		t.Fatalf("wakes = %+v", e.jobs.wakes)
	}
	if w := e.jobs.wakes[0]; w.TaskID != open.ID || w.PersonaID != p.ID || w.Trigger != domain.TriggerHeartbeat {
		t.Fatalf("wake = %+v", w)
	}
}

// Item 5: closing, unassigning or reassigning cancels the active run.
func TestUpdateTaskCancelsActiveRun(t *testing.T) {
	ctx := context.Background()
	other := func(e *env) domain.Persona { return e.persona(t, "Outra", nil) }

	t.Run("queued run is cancelled in the tx on close", func(t *testing.T) {
		e, p, tv := runFixture(t)
		run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskCancelled)}); err != nil {
			t.Fatal(err)
		}
		rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID)
		if rv.Status != domain.RunCancelled || rv.FinishedAt == nil {
			t.Fatalf("run = %+v", rv.Run)
		}
		got, _ := e.svc.GetTask(ctx, e.co.ID, tv.ID.String())
		if got.ActiveRunID != nil {
			t.Fatalf("task still has an active run: %v", got.ActiveRunID)
		}
	})

	t.Run("running run gets run.cancel after commit on done", func(t *testing.T) {
		e, p, tv := runFixture(t)
		run := startRun(t, e, p, tv)
		ch, unsub := e.broker.Subscribe(events.RunTopic(run.ID.String()), 8)
		defer unsub()
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
			t.Fatal(err)
		}
		select {
		case m := <-ch:
			if m.Type != "run.cancel" {
				t.Fatalf("message = %+v", m)
			}
		default:
			t.Fatal("run.cancel not published")
		}
		// the executor finishes it as cancelled: no summary comment
		finish(t, e, run.ID, domain.RunCancelled, adapter.Result{ExitCode: -1, Summary: "parcial", Error: "cancelada"})
		if c, _ := e.svc.ListComments(ctx, e.co.ID, tv.ID); len(c) != 0 {
			t.Fatalf("cancelled run summary became a comment: %+v", c)
		}
	})

	t.Run("unassign", func(t *testing.T) {
		e, p, tv := runFixture(t)
		run := startRun(t, e, p, tv)
		ch, unsub := e.broker.Subscribe(events.RunTopic(run.ID.String()), 8)
		defer unsub()
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Unassign: true}); err != nil {
			t.Fatal(err)
		}
		if m := <-ch; m.Type != "run.cancel" {
			t.Fatalf("message = %+v", m)
		}
	})

	t.Run("reassign", func(t *testing.T) {
		e, p, tv := runFixture(t)
		o := other(e)
		run, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{PersonaID: &o.ID}); err != nil {
			t.Fatal(err)
		}
		if rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID); rv.Status != domain.RunCancelled {
			t.Fatalf("run = %s", rv.Status)
		}
		// the new assignee is woken
		var woke bool
		for _, w := range e.jobs.wakes {
			woke = woke || w.PersonaID == o.ID
		}
		if !woke {
			t.Fatalf("new assignee not woken: %+v", e.jobs.wakes)
		}
	})

	t.Run("unrelated update keeps the run", func(t *testing.T) {
		e, p, tv := runFixture(t)
		run, _ := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{Priority: ptr(domain.PriorityHigh)}); err != nil {
			t.Fatal(err)
		}
		if rv, _ := e.svc.GetRun(ctx, e.co.ID, run.ID); rv.Status != domain.RunQueued {
			t.Fatalf("run = %s", rv.Status)
		}
	})
}

// Item 6: transient failures keep the fact pending with backoff; only permanent ones fail.
func TestMemoryOutboxBackoff(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.task(t, service.CreateTaskInput{Title: "fato"})
	if len(e.jobs.outbox) != 1 {
		t.Fatalf("outbox = %d", len(e.jobs.outbox))
	}
	id := e.jobs.outbox[0]
	// relógio a partir de agora: o created_at do fato vem do banco (now()), e uma data fixa
	// no passado deixaria o fato "novo demais" para o sweep.
	now := time.Now().UTC().Truncate(time.Second)
	e.svc.Now = func() time.Time { return now }
	sweep := now.Add(time.Hour) // far past memoryMinAge

	wantWaits := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}
	for i, wait := range wantWaits {
		if err := e.svc.MarkMemoryFailed(ctx, id, "rede", false); err != nil {
			t.Fatal(err)
		}
		item, err := e.svc.GetMemoryItem(ctx, id)
		if err != nil || item.Status != "pending" || item.Attempts != i+1 || item.NextAttempt == nil || !item.NextAttempt.Equal(now.Add(wait)) {
			t.Fatalf("attempt %d: %+v %v (want next %v)", i+1, item, err, now.Add(wait))
		}
	}
	// not due yet → not swept; due → swept
	e.svc.Now = func() time.Time { return now.Add(7 * time.Minute) }
	if ids, _ := e.svc.PendingMemory(ctx, 10); len(ids) != 0 {
		t.Fatalf("not due yet but swept: %v", ids)
	}
	e.svc.Now = func() time.Time { return sweep }
	if ids, _ := e.svc.PendingMemory(ctx, 10); len(ids) != 1 {
		t.Fatalf("due but not swept: %v", ids)
	}

	// cap: 6 h
	for i := 0; i < 12; i++ {
		if err := e.svc.MarkMemoryFailed(ctx, id, "rede", false); err != nil {
			t.Fatal(err)
		}
	}
	item, _ := e.svc.GetMemoryItem(ctx, id)
	if item.Status != "pending" || item.NextAttempt == nil || !item.NextAttempt.Equal(sweep.Add(6*time.Hour)) {
		t.Fatalf("cap: %+v", item)
	}

	// permanent failure ends in failed and is no longer swept
	if err := e.svc.MarkMemoryFailed(ctx, id, "payload inválido", true); err != nil {
		t.Fatal(err)
	}
	item, _ = e.svc.GetMemoryItem(ctx, id)
	if item.Status != "failed" || item.NextAttempt != nil {
		t.Fatalf("permanent: %+v", item)
	}
	e.svc.Now = func() time.Time { return sweep.Add(48 * time.Hour) }
	if ids, _ := e.svc.PendingMemory(ctx, 10); len(ids) != 0 {
		t.Fatalf("failed item swept: %v", ids)
	}
}

func TestMemoryBackoff(t *testing.T) {
	for attempts, want := range map[int]time.Duration{-1: time.Minute, 0: time.Minute, 1: 2 * time.Minute, 5: 32 * time.Minute,
		8: 256 * time.Minute, 9: 6 * time.Hour, 100: 6 * time.Hour} {
		if got := service.MemoryBackoff(attempts); got != want {
			t.Errorf("MemoryBackoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}
