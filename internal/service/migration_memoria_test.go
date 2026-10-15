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

// A migração da consolidação de memória (ADR-0030) é reversível: o Down apaga o que depende do novo gatilho e
// do novo tipo de gate, devolve as constraints de antes e o Up volta a funcionar.
func TestMigrationMemoriaDownEUp(t *testing.T) {
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
	var v int64
	if v, err = p.GetDBVersion(ctx); err != nil || v < 21 {
		t.Fatalf("versão = %d %v", v, err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA')`)
	const insertGate = `INSERT INTO gates (id, company_id, kind, status, title, body, requested_by_type)
	      VALUES ('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','memory','pending','t','b','system')`
	exec(insertGate)

	// volta até antes da 00021 (as migrations posteriores, como a 00024 da busca, descem junto)
	if _, err := p.DownTo(ctx, 20); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range []string{"memory_settings", "memory_consolidations", "memory_proposals", "memory_grants"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tbl).Scan(&ok); err != nil || ok {
			t.Errorf("tabela %s deveria sumir no down (%v)", tbl, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gates WHERE kind = 'memory'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("gates memory sobraram: %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, insertGate); err == nil {
		t.Error("depois do down, a constraint não deveria aceitar o kind memory")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
