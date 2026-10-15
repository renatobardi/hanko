package service_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/testdb"
)

// A migration 00024 (dobra sem caracteres de formatação) é reversível: o Down devolve a hanko_fold da 00015 (que
// mantém o hífen suave) com as colunas e os índices recriados, e o Up volta a removê-lo, com os dados já existentes
// reindexados pela coluna gerada.
func TestMigrationBuscaDobraDownEUp(t *testing.T) {
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
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	fold := func() string {
		t.Helper()
		var out string
		if err := pool.QueryRow(ctx, `SELECT hanko_fold('co'||chr(173)||'operar')`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	idx := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname IN
			('tasks_search_idx','task_comments_search_idx','personas_search_idx','runs_search_idx','flows_search_idx')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := fold(); got != "cooperar" {
		t.Fatalf("com a 00024 o hífen suave some: %q", got)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 23); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := fold(); got != "co­operar" || idx() != 5 {
		t.Errorf("depois do down: fold=%q índices=%d", got, idx())
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
	if got := fold(); got != "cooperar" || idx() != 5 {
		t.Errorf("depois do up: fold=%q índices=%d", got, idx())
	}
}
