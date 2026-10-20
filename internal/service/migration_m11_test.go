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

// A migração 00070 é aditiva: ambientes, personas e projetos anteriores seguem intactos, o Down desfaz o que ela criou
// (inclusive as linhas dos drivers novos) e o Up volta a aplicar.
func TestMigration00070IsAdditiveAndReversible(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 69); err != nil {
		t.Fatal(err)
	}
	const co = "11111111-1111-4111-8111-111111111111"
	for _, q := range []string{
		`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('` + co + `','a','A','AAA')`,
		`INSERT INTO environments (id, company_id, slug, name, driver, created_by_type) VALUES
		 ('eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee','` + co + `','caixa','Caixa','sandbox','human')`,
		`INSERT INTO projects (id, company_id, slug, name) VALUES ('cccccccc-cccc-4ccc-8ccc-cccccccccccc','` + co + `','site','Site')`,
		`INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at)
		 VALUES ('dddddddd-dddd-4ddd-8ddd-dddddddddddd','` + co + `','run_stuck','company','` + co + `','warning','m', now(), now())`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.UpTo(ctx, 70); err != nil {
		t.Fatal(err)
	}
	// dados antigos intactos; colunas e tabelas novas existem
	var driver string
	if err := pool.QueryRow(ctx, `SELECT driver FROM environments WHERE slug='caixa'`).Scan(&driver); err != nil || driver != "sandbox" {
		t.Fatalf("ambiente anterior: %q %v", driver, err)
	}
	var envID *string
	if err := pool.QueryRow(ctx, `SELECT environment_id::text FROM projects WHERE slug='site'`).Scan(&envID); err != nil || envID != nil {
		t.Fatalf("projeto anterior sem ambiente: %v %v", envID, err)
	}
	for _, tbl := range []string{"persona_readiness", "model_reports"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatalf("tabela %s: %v", tbl, err)
		}
	}
	// os valores novos passam nos checks; os antigos continuam passando
	for _, q := range []string{
		`INSERT INTO environments (id, company_id, slug, name, driver, created_by_type) VALUES
		 ('ffffffff-ffff-4fff-8fff-ffffffffffff','` + co + `','nuvem','Nuvem','cloud','human')`,
		`INSERT INTO environments (id, company_id, slug, name, driver, created_by_type) VALUES
		 ('abababab-abab-4bab-8bab-abababababab','` + co + `','servidor','Servidor','local','human')`,
		`INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, opened_at, last_seen_at)
		 VALUES ('12121212-1212-4212-8212-121212121212','` + co + `','persona_not_ready','company','` + co + `','critical','m', now(), now())`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO environments (id, company_id, slug, name, driver, created_by_type) VALUES
		 ('99999999-9999-4999-8999-999999999999','`+co+`','ruim','Ruim','docker','human')`); err == nil {
		t.Fatal("driver desconhecido deveria ser recusado pelo check")
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET environment_id='ffffffff-ffff-4fff-8fff-ffffffffffff' WHERE slug='site'`); err != nil {
		t.Fatal(err)
	}

	if _, err := p.DownTo(ctx, 69); err != nil {
		t.Fatalf("down: %v", err)
	}
	// o Down apaga o que a migração criou, inclusive as linhas dos drivers novos e o alerta novo; o resto fica
	for _, tbl := range []string{"persona_readiness", "model_reports"} {
		var reg *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.`+tbl+`')::text`).Scan(&reg); err != nil || reg != nil {
			t.Fatalf("o down deveria remover %s (%v %v)", tbl, reg, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM environments`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("só o sandbox antigo deveria ficar: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alerts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("só o alerta antigo deveria ficar: %d %v", n, err)
	}
	var col int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('projects','runs') AND column_name IN ('environment_id','environment_source')`).Scan(&col); err != nil || col != 0 {
		t.Fatalf("o down deveria remover as colunas: %d %v", col, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
