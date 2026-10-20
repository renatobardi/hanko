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

// As migrations 00120–00124 (ADR-0042) são aditivas: metas, tasks e gates anteriores seguem intactos, o Down desfaz
// tudo o que criaram e o Up volta a aplicar.
func TestMigrations00120to00124AreAdditiveAndReversible(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 119); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA')`,
		`INSERT INTO goals (id, company_id, slug, title, start_value, target_value, current_value, created_by_type)
		 VALUES ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','11111111-1111-4111-8111-111111111111','meta','Meta de busca',0,10,0,'human')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	// a meta antiga ganha o tsvector sem perder dados e entra na busca
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM goals WHERE search_tsv @@ to_tsquery('simple','busca') AND parent_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("meta anterior na busca: %d %v", n, err)
	}
	tables := []string{"inbox_state", "comment_mentions", "instance_secrets", "push_subscriptions", "notification_prefs"}
	for _, tbl := range tables {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatalf("tabela %s: %v", tbl, err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_participation('11111111-1111-4111-8111-111111111111','aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("task_participation: %d %v", n, err)
	}
	// o trigger recusa o próprio pai e um pai de outra empresa
	if _, err := pool.Exec(ctx, `UPDATE goals SET parent_id = id`); err == nil {
		t.Fatal("meta sob si mesma deveria ser recusada")
	}

	if _, err := p.DownTo(ctx, 119); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, tbl := range tables {
		var reg *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.`+tbl+`')::text`).Scan(&reg); err != nil || reg != nil {
			t.Fatalf("o down deveria remover %s (%v %v)", tbl, reg, err)
		}
	}
	var cols int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('goals','projects','gates','artifacts') AND column_name IN ('search_tsv','parent_id')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("colunas restantes: %d %v", cols, err)
	}
	var fn int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('goals_parent_guard','task_participation')`).Scan(&fn); err != nil || fn != 0 {
		t.Fatalf("funções restantes: %d %v", fn, err)
	}
	var title string
	if err := pool.QueryRow(ctx, `SELECT title FROM goals`).Scan(&title); err != nil || title != "Meta de busca" {
		t.Fatalf("o down não mexe nos dados anteriores: %q %v", title, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
