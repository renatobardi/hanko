package service_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

// A migração 00100 é aditiva: dados anteriores (inclusive a árvore de tasks) seguem intactos, o Down desfaz tudo o
// que ela criou e o Up volta a aplicar.
func TestMigration00100IsAdditiveAndReversible(t *testing.T) {
	url := os.Getenv(testdb.EnvURL)
	if url == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definida")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 99); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA')`,
		`INSERT INTO tasks (id, company_id, number, title, created_by_type) VALUES
		 ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','11111111-1111-4111-8111-111111111111',1,'pai','human'),
		 ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','11111111-1111-4111-8111-111111111111',2,'filha','human')`,
		`UPDATE tasks SET parent_id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' WHERE number = 2`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var parent string
	if err := pool.QueryRow(ctx, `SELECT parent_id::text FROM tasks WHERE number = 2`).Scan(&parent); err != nil || parent != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("a árvore anterior sobrevive: %q %v", parent, err)
	}
	for _, tbl := range []string{"task_dependencies", "task_holds", "held_wakes", "task_review_overrides", "project_review_policies", "run_policy_origins"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatalf("tabela %s: %v", tbl, err)
		}
	}
	var held bool
	if err := pool.QueryRow(ctx, `SELECT task_effectively_held('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb')`).Scan(&held); err != nil || held {
		t.Fatalf("task_effectively_held: %v %v", held, err)
	}
	if _, err := p.DownTo(ctx, 99); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range []string{"task_dependencies", "task_holds", "held_wakes", "task_review_overrides", "project_review_policies", "run_policy_origins"} {
		var reg *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.`+tbl+`')::text`).Scan(&reg); err != nil || reg != nil {
			t.Fatalf("o down deveria remover %s (%v %v)", tbl, reg, err)
		}
	}
	var fn int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('task_effectively_held','tasks_parent_guard','task_dependencies_guard')`).Scan(&fn); err != nil {
		t.Fatal(err)
	}
	if fn != 0 {
		t.Fatalf("funções restantes: %d", fn)
	}
	if err := pool.QueryRow(ctx, `SELECT parent_id::text FROM tasks WHERE number = 2`).Scan(&parent); err != nil || parent == "" {
		t.Fatalf("o down não mexe nos dados anteriores: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}

// O heartbeat não considera candidata a task bloqueada por dependência nem a pausada (própria ou herdada).
func TestWakeCandidatesSkipBlockedAndHeldTasks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", func(in *service.HirePersonaInput) { in.HeartbeatIntervalSec = 1 })
	free := e.task(t, service.CreateTaskInput{Title: "livre", PersonaID: &p.ID})
	dep := e.task(t, service.CreateTaskInput{Title: "dep"})
	blocked := e.task(t, service.CreateTaskInput{Title: "bloqueada", PersonaID: &p.ID, DependsOn: []domain.ID{dep.ID}})
	root := e.task(t, service.CreateTaskInput{Title: "raiz"})
	held := e.task(t, service.CreateTaskInput{Title: "pausada por herança", PersonaID: &p.ID, ParentID: &root.ID})
	if _, err := e.svc.HoldTask(ctx, domain.HumanActor, e.co.ID, root.ID, "x"); err != nil {
		t.Fatal(err)
	}
	// o intervalo de 1 s passa: sobram só as livres
	cs, err := e.svc.WakeCandidates(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[domain.ID]bool{}
	for _, c := range cs {
		ids[c.TaskID] = true
	}
	if ids[blocked.ID] || ids[held.ID] {
		t.Fatalf("bloqueada/pausada não são candidatas: %v", ids)
	}
	if !ids[free.ID] {
		t.Fatalf("a livre é candidata: %v", ids)
	}
	e.closeAs(t, domain.HumanActor, dep, domain.TaskDone)
	if _, err := e.svc.ResumeTask(ctx, domain.HumanActor, e.co.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	cs, _ = e.svc.WakeCandidates(ctx, 100)
	ids = map[domain.ID]bool{}
	for _, c := range cs {
		ids[c.TaskID] = true
	}
	if !ids[blocked.ID] || !ids[held.ID] {
		t.Fatalf("liberadas e retomadas voltam a ser candidatas: %v", ids)
	}
}
