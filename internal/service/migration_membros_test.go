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

// A migração 00011 faz backfill: cada usuário existente vira membro de cada empresa existente, com o
// papel que já tinha na instalação (preserva o comportamento anterior).
func TestMigration00011Backfill(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO companies (id, slug, name, task_prefix) VALUES ('11111111-1111-4111-8111-111111111111','a','A','AAA'), ('22222222-2222-4222-8222-222222222222','b','B','BBB')`,
		`INSERT INTO users (id, email, name, provider, password_hash, role) VALUES
		 ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','o@x.com','o','local','x','owner'),
		 ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','a@x.com','a','local','x','admin'),
		 ('cccccccc-cccc-4ccc-8ccc-cccccccccccc','m@x.com','m','local','x','member')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT u.email, m.role, count(*) FROM company_members m JOIN users u ON u.id = m.user_id GROUP BY 1,2 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var email, role string
		var n int
		if err := rows.Scan(&email, &role, &n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("%s deveria estar nas 2 empresas, está em %d", email, n)
		}
		got[email] = role
	}
	want := map[string]string{"o@x.com": "owner", "a@x.com": "admin", "m@x.com": "member"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: papel %q, esperado %q", k, got[k], v)
		}
	}
	var invited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE invited`).Scan(&invited); err != nil || invited != 0 {
		t.Errorf("invited: %d %v", invited, err)
	}
}
