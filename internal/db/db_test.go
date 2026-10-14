package db

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("HANKO_TEST_DATABASE_URL")
	if u == "" {
		t.Skip("HANKO_TEST_DATABASE_URL não definido")
	}
	return u
}

func TestMigrateIntegracao(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, testURL(t), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// do zero: banco de teste exclusivo da trilha.
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}

	extraCalls := 0
	opt := MigrateOptions{Extra: []func(context.Context, *pgxpool.Pool) error{
		func(ctx context.Context, p *pgxpool.Pool) error { extraCalls++; return p.Ping(ctx) },
	}}
	for i := 0; i < 2; i++ { // segunda execução = idempotente
		if err := Migrate(ctx, pool, testLogger(), opt); err != nil {
			t.Fatalf("Migrate #%d: %v", i+1, err)
		}
	}
	if extraCalls != 2 {
		t.Fatalf("extras chamadas %d vezes, quer 2", extraCalls)
	}
	for _, tbl := range []string{"companies", "goose_db_version"} {
		var ok bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", tbl).Scan(&ok); err != nil || !ok {
			t.Fatalf("tabela %s ausente (err=%v)", tbl, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id = 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("versão 1 aplicada %d vezes (err=%v)", n, err)
	}
}

func TestMigrateExtraFalha(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, testURL(t), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	err = Migrate(ctx, pool, testLogger(), MigrateOptions{Extra: []func(context.Context, *pgxpool.Pool) error{
		func(context.Context, *pgxpool.Pool) error { return io.ErrUnexpectedEOF },
	}})
	if err == nil || !strings.Contains(err.Error(), "extras") {
		t.Fatalf("erro = %v", err)
	}
}

func TestOpenURLInvalida(t *testing.T) {
	if _, err := Open(context.Background(), "::não é url", testLogger()); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestOpenSemPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	_, err := Open(ctx, "postgres://u@127.0.0.1:1/x?sslmode=disable&connect_timeout=1", testLogger())
	if err == nil || !strings.Contains(err.Error(), "ping") {
		t.Fatalf("erro = %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("retry demorou %v", time.Since(start))
	}
}
