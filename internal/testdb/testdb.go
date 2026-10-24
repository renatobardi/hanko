// Package testdb gives integration tests a clean Postgres database with the Hanko
// migrations applied. It reads HANKO_TEST_DATABASE_URL and skips the test when unset.
//
// The migrations run once per test process, into a template database; each call to New
// clones that template into a database of its own (CREATE DATABASE ... TEMPLATE) and drops
// it when the test ends (#71). Cloning costs a fraction of re-running every migration.
package testdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// EnvURL is the environment variable holding the test database URL.
const EnvURL = "HANKO_TEST_DATABASE_URL"

var (
	tplOnce sync.Once
	tplName string
	errTpl  error
	seq     atomic.Int64
)

// New returns a pool to a fresh database of the caller's own, with every migration applied.
// The database in HANKO_TEST_DATABASE_URL is only used to create and drop the others, so it
// must be a disposable test database: never point it at real data.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skipf("%s not set; skipping integration test", EnvURL)
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("testdb: parse %s: %v", EnvURL, err)
	}
	base := cfg.ConnConfig.Database
	tplOnce.Do(func() { tplName, errTpl = ensureTemplate(ctx, url, base) })
	if errTpl != nil {
		t.Fatalf("testdb: template: %v", errTpl)
	}

	name := fmt.Sprintf("%s_t%d_%d", base, os.Getpid(), seq.Add(1))
	if err := adminExec(ctx, url, "CREATE DATABASE "+ident(name)+" TEMPLATE "+ident(tplName)); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if err := adminExec(context.Background(), url, "DROP DATABASE IF EXISTS "+ident(name)+" WITH (FORCE)"); err != nil {
			t.Logf("testdb: drop database %s: %v", name, err)
		}
	})
	return pool
}

// ensureTemplate makes sure <base>_tpl_<hash> exists with every migration applied and returns its
// name. The hash covers the migration files, so a new migration gets a new template. An advisory
// lock serializes test processes sharing the server; the template is built under a temporary name
// and renamed only when complete, so a crash never leaves a half-migrated template behind.
func ensureTemplate(ctx context.Context, url, base string) (string, error) {
	sum, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := base + "_tpl_" + sum
	building := name + "_build"

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", name); err != nil {
		return "", fmt.Errorf("lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext($1))", name) }()

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return "", fmt.Errorf("lookup: %w", err)
	}
	if exists {
		return name, nil
	}
	for _, sql := range []string{
		"DROP DATABASE IF EXISTS " + ident(building) + " WITH (FORCE)",
		"CREATE DATABASE " + ident(building),
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return "", err
		}
	}
	if err := migrate(ctx, url, building); err != nil {
		return "", err
	}
	if _, err := conn.Exec(ctx, "ALTER DATABASE "+ident(building)+" RENAME TO "+ident(name)); err != nil {
		return "", err
	}
	return name, nil
}

func migrate(ctx context.Context, url, database string) error {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return err
	}
	cfg.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect %s: %w", database, err)
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// migrationsHash is a short digest of every migration file name and content.
func migrationsHash() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(migrations.FS, path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", path, len(b))
		_, _ = h.Write(b)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash migrations: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// adminExec runs one statement on a short-lived connection to the database in url.
// CREATE/DROP DATABASE cannot run inside a pool transaction or against the target itself.
func adminExec(ctx context.Context, url, sql string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func ident(name string) string { return pgx.Identifier{name}.Sanitize() }
