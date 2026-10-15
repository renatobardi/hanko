//go:build integration

package api_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

// noopJobs dispensa o heartbeat: o teste dirige CheckoutRun/AppendRunEvent/FinishRun à mão.
type noopJobs struct{}

func (noopJobs) WakeTx(context.Context, pgx.Tx, service.Wake) error                { return nil }
func (noopJobs) ExecuteRunTx(context.Context, pgx.Tx, domain.ID, domain.ID) error  { return nil }
func (noopJobs) StartReviewTx(context.Context, pgx.Tx, domain.ID, domain.ID) error { return nil }
func (noopJobs) DeliverMemoryTx(context.Context, pgx.Tx, domain.ID) error          { return nil }

func setup(t *testing.T) (*service.Service, *client.Client, *events.Broker) {
	t.Helper()
	url := os.Getenv("HANKO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definido")
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
	sdb, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sdb, "."); err != nil {
		t.Fatal(err)
	}

	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	mux := http.NewServeMux()
	api.Mount(mux, api.Deps{Svc: svc, Broker: broker, Logger: slog.Default(), Version: "it", DB: pool})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return svc, client.New(ts.URL, ""), broker
}

func TestFluxoCompleto(t *testing.T) {
	svc, c, _ := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	co, err := c.CreateCompany(ctx, client.CreateCompanyReq{Name: "Acme Ltda", TaskPrefix: "ACM"})
	if err != nil {
		t.Fatal(err)
	}
	if co.Slug != "acme-ltda" || co.TaskPrefix != "ACM" {
		t.Fatalf("empresa: %+v", co)
	}
	if _, err := c.CreateCompany(ctx, client.CreateCompanyReq{Name: "Acme Ltda"}); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("slug repetido deveria dar 409: %v", err)
	}

	p, err := c.HirePersona(ctx, co.Slug, client.HirePersonaReq{Name: "Marina", Title: "CTO", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Slug != "marina" || p.Agent != "fake" {
		t.Fatalf("persona: %+v", p)
	}
	if _, err := c.HirePersona(ctx, co.Slug, client.HirePersonaReq{Name: "X", Agent: "inexistente"}); err == nil {
		t.Fatal("agente inválido deveria falhar")
	}

	task, err := c.CreateTask(ctx, co.Slug, client.CreateTaskReq{Title: "Implementar login", Persona: "marina", Priority: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Key != "ACM-1" || task.PersonaName != "Marina" {
		t.Fatalf("task: %+v", task)
	}

	ts, err := c.Tasks(ctx, co.Slug, client.TaskQuery{Status: "todo", Persona: "marina"})
	if err != nil || len(ts) != 1 {
		t.Fatalf("lista: %v %v", ts, err)
	}
	if ts, _ = c.Tasks(ctx, co.Slug, client.TaskQuery{Status: "done"}); len(ts) != 0 {
		t.Fatalf("filtro done deveria vir vazio: %v", ts)
	}

	if _, err := c.AddComment(ctx, co.Slug, "ACM-1", client.AddCommentReq{Body: "começa pelo backend"}); err != nil {
		t.Fatal(err)
	}
	cs, err := c.Comments(ctx, co.Slug, "ACM-1")
	if err != nil || len(cs) != 1 || cs[0].Body != "começa pelo backend" {
		t.Fatalf("comentários: %v %v", cs, err)
	}

	st := "in_review"
	if task, err = c.UpdateTask(ctx, co.Slug, "ACM-1", client.UpdateTaskReq{Status: &st}); err != nil || task.Status != "in_review" {
		t.Fatalf("update: %+v %v", task, err)
	}
	if _, err := c.Task(ctx, co.Slug, "ACM-99"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("task inexistente: %v", err)
	}

	// execução dirigida à mão: checkout → eventos → fim, com o SSE aberto no meio
	coID, _ := domain.ParseID(co.ID)
	pID, _ := domain.ParseID(p.ID)
	tID, _ := domain.ParseID(task.ID)
	todo := domain.TaskTodo
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, coID, tID, service.UpdateTaskInput{Status: &todo}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: coID, PersonaID: pID, TaskID: tID, Trigger: domain.TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AppendRunEvent(ctx, coID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: "analisando", OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	got := make(chan string, 16)
	done := make(chan error, 1)
	go func() {
		done <- c.Stream(ctx, co.Slug, run.ID.String(), 0, func(f client.Frame) error { got <- f.Event + ":" + f.ID; return nil })
	}()
	if ev := <-got; ev != "run.event:1" {
		t.Fatalf("replay: %q", ev)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := svc.AppendRunEvent(ctx, coID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: "ao vivo", OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if ev := <-got; ev != "run.event:2" {
		t.Fatalf("ao vivo: %q", ev)
	}
	if _, err := svc.FinishRun(ctx, coID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "pronto"}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("stream: %v", err)
	}
	if ev := <-got; ev != "run.status:" {
		t.Fatalf("status final: %q", ev)
	}

	evs, err := c.AllRunEvents(ctx, co.Slug, run.ID.String(), 0)
	if err != nil || len(evs) < 2 {
		t.Fatalf("eventos: %v %v", evs, err)
	}
	r, err := c.Run(ctx, co.Slug, run.ID.String())
	if err != nil || r.Status != "done" {
		t.Fatalf("run: %+v %v", r, err)
	}
	rs, err := c.Runs(ctx, co.Slug, client.RunQuery{Task: "ACM-1"})
	if err != nil || len(rs) != 1 {
		t.Fatalf("runs: %v %v", rs, err)
	}
}
