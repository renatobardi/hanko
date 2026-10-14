//go:build integration

package heartbeat_test

import (
	"context"
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

type stack struct {
	svc *service.Service
	hb  *heartbeat.Heartbeat
	co  domain.Company
}

// newStack wires a real service, river and the fake adapter over a clean database.
// The heartbeat is attached but not started.
func newStack(t *testing.T, delay time.Duration) *stack {
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
	hb.Attach(svc, adapter.Registry{"fake": fake.New(fake.Options{Delay: delay})}, memory.Noop{}, broker,
		auth.RunTokens{Secret: []byte("integration-secret-0123456789")})
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	return &stack{svc: svc, hb: hb, co: co}
}

func (s *stack) start(t *testing.T) {
	t.Helper()
	if err := s.hb.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.hb.Stop(sctx)
	})
}

func (s *stack) waitRuns(t *testing.T, taskID domain.ID, within time.Duration, ok func([]service.RunView) bool) []service.RunView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		runs, err := s.svc.ListRuns(context.Background(), s.co.ID, service.RunFilter{TaskID: &taskID})
		if err != nil {
			t.Fatal(err)
		}
		if ok(runs) {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("condição não atingida em %v: %+v", within, runs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func count(runs []service.RunView, st domain.RunStatus) (n int) {
	for _, r := range runs {
		if r.Status == st {
			n++
		}
	}
	return n
}

// A comment posted while a run is active must not be lost: a new run happens afterwards.
func TestCommentDuringRunProducesNewRun(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, 400*time.Millisecond)
	s.start(t)
	p, err := s.svc.HirePersona(ctx, domain.HumanActor, s.co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.svc.CreateTask(ctx, domain.HumanActor, s.co.ID, service.CreateTaskInput{Title: "Corrigir", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.waitRuns(t, task.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunRunning) == 1 })
	if _, err := s.svc.AddComment(ctx, domain.HumanActor, s.co.ID, task.ID, service.AddCommentInput{Body: "mais um detalhe"}); err != nil {
		t.Fatal(err)
	}
	runs := s.waitRuns(t, task.ID, 30*time.Second, func(r []service.RunView) bool { return count(r, domain.RunDone) >= 2 })
	if len(runs) < 2 {
		t.Fatalf("runs = %+v", runs)
	}
}

// Closing a task cancels its running run end to end (broker → executor → FinishRun).
func TestClosingTaskCancelsRunningRun(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, 2*time.Second)
	s.start(t)
	p, err := s.svc.HirePersona(ctx, domain.HumanActor, s.co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.svc.CreateTask(ctx, domain.HumanActor, s.co.ID, service.CreateTaskInput{Title: "Longa", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.waitRuns(t, task.ID, 20*time.Second, func(r []service.RunView) bool { return count(r, domain.RunRunning) == 1 })
	if _, err := s.svc.UpdateTask(ctx, domain.HumanActor, s.co.ID, task.ID, service.UpdateTaskInput{Status: ptr(domain.TaskCancelled)}); err != nil {
		t.Fatal(err)
	}
	runs := s.waitRuns(t, task.ID, 15*time.Second, func(r []service.RunView) bool { return len(r) == 1 && r[0].Status.Terminal() })
	if runs[0].Status != domain.RunCancelled {
		t.Fatalf("run = %s (%s)", runs[0].Status, runs[0].Error)
	}
	if c, _ := s.svc.ListComments(ctx, s.co.ID, task.ID); len(c) != 0 {
		t.Fatalf("resumo de run cancelada virou comentário: %+v", c)
	}
}

// After a crash, boot interrupts the orphan run and wakes the assignee: the task resumes.
func TestBootWakesOrphanedTask(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, -1)
	p, err := s.svc.HirePersona(ctx, domain.HumanActor, s.co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.svc.CreateTask(ctx, domain.HumanActor, s.co.ID, service.CreateTaskInput{Title: "Retomar", PersonaID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	// simulate the dead process: a running run and no queued jobs
	run, err := s.svc.CheckoutRun(ctx, service.Wake{CompanyID: s.co.ID, PersonaID: p.ID, TaskID: task.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.svc.MarkRunStarted(ctx, s.co.ID, run.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.svc.Pool.Exec(ctx, "TRUNCATE river_job"); err != nil {
		t.Fatal(err)
	}

	s.start(t)
	runs := s.waitRuns(t, task.ID, 20*time.Second, func(r []service.RunView) bool {
		return count(r, domain.RunInterrupted) == 1 && count(r, domain.RunDone) == 1
	})
	if len(runs) != 2 {
		t.Fatalf("runs = %+v", runs)
	}
}

func ptr[T any](v T) *T { return &v }
