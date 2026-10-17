package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
	"github.com/renatobardi/hanko/internal/testdb"
)

// A migração das versões, fontes e estúdio de skills (ADR-0035) faz o backfill da versão corrente de cada skill e é
// reversível.
func TestMigrationSkillsStudioBackfillDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 30); err != nil {
		t.Fatal(err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA')`)
	exec(`INSERT INTO skills (id, company_id, slug, name, description, body, version, created_by_type, updated_by_type)
	      VALUES ('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','deploy','Deploy','d','# corpo',3,'human','human')`)
	if _, err := p.UpTo(ctx, 31); err != nil {
		t.Fatalf("up 31: %v", err)
	}
	var rev int
	var sha string
	if err := pool.QueryRow(ctx, `SELECT revision, sha256 FROM skill_versions WHERE skill_id = '22222222-2222-4222-8222-222222222222'`).Scan(&rev, &sha); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("# corpo"))
	if rev != 3 || sha != hex.EncodeToString(want[:]) {
		t.Fatalf("backfill: rev=%d sha=%s", rev, sha)
	}
	if _, err := p.DownTo(ctx, 30); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range []string{"skill_versions", "skill_sources", "skill_source_entries", "skill_test_inputs", "skill_test_runs"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, tbl).Scan(&ok); err != nil || ok {
			t.Errorf("tabela %s deveria sumir no down (%v)", tbl, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'skills' AND column_name LIKE 'source_%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("colunas source_* sobraram: %d %v", n, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
