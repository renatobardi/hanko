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

// A migração dos cards de status (ADR-0032) é reversível: o Down apaga as três tabelas e o Up volta a funcionar.
func TestMigrationCardsStatusDownEUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 28); err != nil {
		t.Fatal(err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA')`)
	exec(`INSERT INTO status_cards (id, company_id, interest, created_by_type, created_at, updated_at)
	      VALUES ('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','x','human',now(),now())`)
	if _, err := pool.Exec(ctx, `UPDATE status_cards SET state = 'whatever'`); err == nil {
		t.Error("a constraint de estado deveria recusar")
	}
	if _, err := p.DownTo(ctx, 27); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range []string{"status_cards", "status_card_revisions", "status_card_updates"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tbl).Scan(&ok); err != nil || ok {
			t.Errorf("tabela %s deveria sumir no down (%v)", tbl, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
