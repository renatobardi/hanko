// Package testdb gives integration tests a clean Postgres schema with the Hanko
// migrations applied. It reads HANKO_TEST_DATABASE_URL and skips the test when unset.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// EnvURL is the environment variable holding the test database URL.
const EnvURL = "HANKO_TEST_DATABASE_URL"

// New recreates the public schema, applies all migrations and returns a pool.
// The database is exclusive to the caller's package run: never point it at real data.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skipf("%s not set; skipping integration test", EnvURL)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("testdb: reset schema: %v", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatalf("testdb: goose provider: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	return pool
}
