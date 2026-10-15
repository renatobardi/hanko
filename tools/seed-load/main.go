// Command seed-load enche um banco Postgres com uma carga sintética reprodutível (empresas, personas, tasks,
// runs, run_events, activity_events, comentários, gates e alertas) para medir consultas com EXPLAIN.
//
// Uso (-reset apaga o schema public do banco apontado):
//
//	go run ./tools/seed-load -db 'postgres://postgres:postgres@127.0.0.1:5432/hanko_perf?sslmode=disable' -reset
//
// -scale 1 = 3 empresas, 30 personas, 50k tasks, ~200k runs, ~5M run_events, 1M activity_events.
// O mesmo -scale com o mesmo -ref produz o mesmo banco. -migrate-to N aplica só as migrations até a versão N
// (útil para medir o "antes" de uma migration nova e só depois aplicar o resto).
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/renatobardi/hanko/db/migrations"
)

//go:embed seed.sql
var seedSQL string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed-load:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dbURL     = flag.String("db", os.Getenv("HANKO_TEST_DATABASE_URL"), "URL do Postgres (default: HANKO_TEST_DATABASE_URL)")
		scale     = flag.Float64("scale", 1, "fator de escala (1 = 50k tasks, ~5M run_events)")
		refStr    = flag.String("ref", "", "instante de referência RFC3339 (default: agora, truncado à hora)")
		reset     = flag.Bool("reset", false, "apaga o schema public e migra antes de semear")
		migrateTo = flag.Int64("migrate-to", 0, "com -reset: aplica as migrations até esta versão (0 = todas)")
	)
	flag.Parse()
	if *dbURL == "" {
		return fmt.Errorf("informe -db ou HANKO_TEST_DATABASE_URL")
	}
	ref := time.Now().UTC().Truncate(time.Hour)
	if *refStr != "" {
		t, err := time.Parse(time.RFC3339, *refStr)
		if err != nil {
			return fmt.Errorf("-ref: %w", err)
		}
		ref = t.UTC()
	}
	fmt.Println("ref:", ref.Format(time.RFC3339), "scale:", *scale)
	ctx := context.Background()

	if *reset {
		if err := resetAndMigrate(ctx, *dbURL, *migrateTo); err != nil {
			return err
		}
	}

	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	// Carga única: o banco é descartável, então não espera o flush do WAL a cada commit.
	if _, err := conn.Exec(ctx, `SET synchronous_commit = off; SET work_mem = '64MB'`); err != nil {
		return err
	}

	n := func(base int) string { return strconv.Itoa(max(1, int(float64(base)**scale))) }
	repl := strings.NewReplacer(
		"@REF@", ref.Format(time.RFC3339),
		"@N_TASKS@", n(50000),
		"@N_ACTIVITY@", n(1000000),
		"@N_COMMENTS@", n(100000),
		"@N_GATES@", n(8000),
	)

	start := time.Now()
	for _, st := range splitSteps(seedSQL) {
		t0 := time.Now()
		sql := repl.Replace(st.sql)
		var err error
		if strings.Contains(sql, "@CHUNK_LO@") {
			err = chunked(ctx, conn, sql, st.name)
		} else {
			_, err = conn.Exec(ctx, sql)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
		fmt.Printf("%-40s %8.1fs\n", st.name, time.Since(t0).Seconds())
	}
	// VACUUM fora de transação: marca páginas como all-visible (index-only scans) e deixa as estatísticas como as de um banco maduro.
	t0 := time.Now()
	if _, err := conn.Exec(ctx, `VACUUM (ANALYZE)`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	fmt.Printf("%-40s %8.1fs\n", "vacuum analyze", time.Since(t0).Seconds())
	fmt.Printf("%-40s %8.1fs\n", "total", time.Since(start).Seconds())
	return report(ctx, conn)
}

func resetAndMigrate(ctx context.Context, url string, to int64) error {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		return fmt.Errorf("reset schema: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return err
	}
	if to > 0 {
		_, err = p.UpTo(ctx, to)
	} else {
		_, err = p.Up(ctx)
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	fmt.Println("schema recriado e migrado")
	return nil
}

type step struct{ name, sql string }

func splitSteps(src string) []step {
	var out []step
	for _, line := range strings.Split(src, "\n") {
		if name, ok := strings.CutPrefix(line, "-- step: "); ok {
			out = append(out, step{name: strings.TrimSpace(name)})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].sql += line + "\n"
		}
	}
	return out
}

// chunked repete o INSERT de run_events por faixas de páginas da heap de runs: cada comando fica pequeno
// (memória e WAL limitados) e o progresso aparece.
func chunked(ctx context.Context, conn *pgx.Conn, sql, name string) error {
	var pages int64
	if err := conn.QueryRow(ctx, `SELECT pg_relation_size('runs') / current_setting('block_size')::int + 1`).Scan(&pages); err != nil {
		return err
	}
	const stride = 400
	var total int64
	for lo := int64(0); lo < pages; lo += stride {
		q := strings.NewReplacer("@CHUNK_LO@", strconv.FormatInt(lo, 10), "@CHUNK_HI@", strconv.FormatInt(lo+stride, 10)).Replace(sql)
		tag, err := conn.Exec(ctx, q)
		if err != nil {
			return err
		}
		total += tag.RowsAffected()
		fmt.Printf("  %s: %d/%d páginas, %d linhas\r", name, min(lo+stride, pages), pages, total)
	}
	fmt.Println()
	return nil
}

func report(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx, `
SELECT c.relname, c.reltuples::bigint, pg_size_pretty(pg_table_size(c.oid)), pg_size_pretty(pg_indexes_size(c.oid))
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'r'
  AND c.relname IN ('companies','personas','tasks','task_comments','runs','run_events','activity_events','gates','alerts','reviews','subscriptions')
ORDER BY c.reltuples DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	fmt.Printf("\n%-18s %12s %10s %10s\n", "tabela", "linhas", "heap", "índices")
	for rows.Next() {
		var name, heap, idx string
		var n int64
		if err := rows.Scan(&name, &n, &heap, &idx); err != nil {
			return err
		}
		fmt.Printf("%-18s %12d %10s %10s\n", name, n, heap, idx)
	}
	return rows.Err()
}
