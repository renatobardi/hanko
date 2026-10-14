//go:build integration

package heartbeat_test

import (
	"context"
	"errors"
	"os"
	"strings"
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
func TestFullFlow(t *testing.T) {
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
		t.Fatalf("CreateCompany: %v", err)
	}
	persona, err := svc.HirePersona(ctx, domain.HumanActor, company.ID, service.HirePersonaInput{Name: "Ana", Title: "Engenheira", Agent: "fake", Instructions: "Seja objetiva."})
	if err != nil {
		t.Fatalf("HirePersona: %v", err)
	}
	task, err := svc.CreateTask(ctx, domain.HumanActor, company.ID, service.CreateTaskInput{Title: "Corrigir login", PersonaID: &persona.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	var run service.RunView
	deadline := time.Now().Add(30 * time.Second)
	for {
		runs, err := svc.ListRuns(ctx, company.ID, service.RunFilter{TaskID: &task.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 && runs[0].Status.Terminal() {
			run = runs[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run não terminou: %+v", runs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}

	evs, err := svc.ListRunEvents(ctx, company.ID, run.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) < 5 {
		t.Errorf("eventos = %d", len(evs))
	}
	comments, err := svc.ListComments(ctx, company.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range comments {
		if c.Author.Type == domain.ActorPersona && strings.Contains(c.Body, task.Key) {
			found = true
		}
	}
	if !found {
		t.Errorf("comentário da persona ausente: %+v", comments)
	}
}
