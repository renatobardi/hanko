//go:build integration

package heartbeat_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/heartbeat"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// TestFullFlow assigns a task to a fake persona and waits for the run to finish with a
// persona comment. It needs the real service (track A); against stubs it fails fast.

// TestGateFlow: the persona finishes a run, asks for a gate (task blocked, persona waiting),
// a human approves, and the decision wakes the persona into a new run (trigger gate).
func TestGateFlow(t *testing.T) {
	url := os.Getenv("HANKO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL vazio")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// Fresh schema: this database belongs to the track.
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("goose: %v", err)
	}
	if err := heartbeat.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	broker := events.NewBroker()
	hb, err := heartbeat.New(pool, heartbeat.Options{Workers: 2, PeriodicInterval: 500 * time.Millisecond, RunTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	hb.Attach(svc, adapter.Registry{"fake": fake.New(fake.Options{Delay: 10 * time.Millisecond})}, memory.Noop{}, broker,
		auth.RunTokens{Secret: []byte("integration-secret-0123456789")})
	if err := hb.Start(ctx); err != nil {
		if errors.Is(err, service.ErrNotImplemented) {
			t.Skip("service ainda é stub (trilha A não mesclada)")
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})

	company, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	persona, err := svc.HirePersona(ctx, domain.HumanActor, company.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, domain.HumanActor, company.ID, service.CreateTaskInput{Title: "Publicar release", PersonaID: &persona.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatal(err)
	}
	waitRuns := func(n int) []service.RunView {
		deadline := time.Now().Add(30 * time.Second)
		for {
			runs, err := svc.ListRuns(ctx, company.ID, service.RunFilter{TaskID: &task.ID})
			if err != nil {
				t.Fatal(err)
			}
			done := 0
			for _, r := range runs {
				if r.Status.Terminal() {
					done++
				}
			}
			if len(runs) >= n && done == len(runs) {
				return runs
			}
			if time.Now().After(deadline) {
				t.Fatalf("esperava %d runs terminadas: %+v", n, runs)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitRuns(1)
	// let the first wake job finish: a wake for the same persona and task that arrives while
	// another one is still running is coalesced (see WakeArgs).
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		var busy int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind = 'wake' AND state IN ('available','running','retryable','scheduled','pending')").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if busy == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wake inicial não terminou")
		}
	}

	g, err := svc.RequestGate(ctx, domain.PersonaActor(persona.ID), company.ID, service.RequestGateInput{Title: "Autorizar o deploy", Body: "Irreversível.", TaskID: &task.ID, EventID: "gate-1"})
	if err != nil {
		t.Fatalf("RequestGate: %v", err)
	}
	tv, _ := svc.GetTask(ctx, company.ID, task.Key)
	p, _ := svc.GetPersona(ctx, company.ID, persona.Slug)
	if tv.Status != domain.TaskBlocked || p.Status != domain.PersonaAwaitingGate {
		t.Fatalf("task=%s persona=%s", tv.Status, p.Status)
	}
	if _, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: company.ID, PersonaID: persona.ID, TaskID: task.ID, Trigger: domain.TriggerManual}); err == nil {
		t.Error("CheckoutRun deveria recusar com gate pendente")
	}
	if _, err := svc.DecideGate(ctx, domain.PersonaActor(persona.ID), company.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("persona decidiu: %v", err)
	}
	if _, err := svc.DecideGate(ctx, domain.HumanActor, company.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved, Note: "Pode."}); err != nil {
		t.Fatalf("DecideGate: %v", err)
	}
	runs := waitRuns(2)
	gateRuns := 0
	for _, r := range runs {
		if r.Trigger == domain.TriggerGate {
			gateRuns++
		}
		if r.Status != domain.RunDone {
			t.Errorf("run %s = %s (%s)", r.ID, r.Status, r.Error)
		}
	}
	if gateRuns != 1 {
		t.Errorf("runs com gatilho gate = %d, quer 1", gateRuns)
	}
}
