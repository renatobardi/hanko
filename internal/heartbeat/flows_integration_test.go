//go:build integration

package heartbeat_test

import (
	"context"
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

// TestFlowTickCreatesTaskAndRun: a due Fluxo is fired by the periodic flow_tick, creates a
// task for its fake persona, and the run executes with trigger flow. A persona whose budget
// is already spent gets the task but no run (hard stop), and ends paused.
func TestFlowTickCreatesTaskAndRun(t *testing.T) {
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

	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	ana, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	broke, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Sem Verba", Agent: "fake", BudgetMonthlyCents: ptr(int64(0))})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string, p domain.Persona) service.FlowView {
		f, err := svc.CreateFlow(ctx, domain.HumanActor, co.ID, service.CreateFlowInput{Name: name, Cron: "0 9 * * *", PersonaID: p.ID,
			TaskTitle: name + " {{data}}"})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	fa, fb := mk("Diário da Ana", ana), mk("Diário sem verba", broke)
	// make both due now
	if _, err := pool.Exec(ctx, "UPDATE flows SET next_fire_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}

	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})

	deadline := time.Now().Add(40 * time.Second)
	var anaRun service.RunView
	for {
		runs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{PersonaID: &ana.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 && runs[0].Status.Terminal() {
			anaRun = runs[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run do fluxo não terminou: %+v", runs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if anaRun.Status != domain.RunDone || anaRun.Trigger != domain.TriggerFlow {
		t.Fatalf("run: status=%s trigger=%s err=%q", anaRun.Status, anaRun.Trigger, anaRun.Error)
	}
	if anaRun.CostUSD == nil || anaRun.CostSource != domain.CostReported {
		t.Fatalf("custo da run fake: %v %s", anaRun.CostUSD, anaRun.CostSource)
	}

	// idempotent: more ticks do not create more tasks, and the schedule moved on
	time.Sleep(1500 * time.Millisecond)
	for _, f := range []service.FlowView{fa, fb} {
		fires, err := svc.ListFlowFires(ctx, co.ID, f.ID, 10)
		if err != nil || len(fires) != 1 || fires[0].TaskID == nil {
			t.Fatalf("fires of %s: %+v %v", f.Slug, fires, err)
		}
		got, _ := svc.GetFlow(ctx, co.ID, f.ID.String())
		if got.NextFireAt == nil || !got.NextFireAt.After(time.Now()) || got.LastFiredAt == nil {
			t.Fatalf("schedule of %s: last=%v next=%v", f.Slug, got.LastFiredAt, got.NextFireAt)
		}
	}

	// hard stop: the task exists, no run was opened, the persona is paused
	runs, _ := svc.ListRuns(ctx, co.ID, service.RunFilter{PersonaID: &broke.ID})
	if len(runs) != 0 {
		t.Fatalf("persona sem verba abriu run: %+v", runs)
	}
	p, _ := svc.GetPersona(ctx, co.ID, broke.ID.String())
	if p.Status != domain.PersonaPaused || p.PauseReason == "" {
		t.Fatalf("persona: %s %q", p.Status, p.PauseReason)
	}
	tasks, _ := svc.ListTasks(ctx, co.ID, service.TaskFilter{PersonaID: &broke.ID})
	if len(tasks) != 1 {
		t.Fatalf("tasks da persona sem verba = %d", len(tasks))
	}
}
