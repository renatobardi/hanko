package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

// openMigrations zera o schema do banco de teste, sobe as migrations até `upTo` e devolve o pool e o provider para o
// teste seguir (Up, Down). Fecha tudo no fim do teste.
func openMigrations(t *testing.T, upTo int64) (*pgxpool.Pool, *goose.Provider) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	sdb := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sdb.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, sdb, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, upTo); err != nil {
		t.Fatal(err)
	}
	return pool, p
}
