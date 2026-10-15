// Package db abre o pool Postgres (com tracing OTel) e aplica as migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/renatobardi/hanko/db/migrations"
)

const (
	defaultMaxConns = 20
	defaultMinConns = 2
	pingAttempts    = 5
)

// Open cria o pool e confirma conectividade com ping (retry curto com backoff).
// Parâmetros pool_* na URL têm precedência sobre os defaults.
func Open(ctx context.Context, url string, logger *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: url inválida: %w", err)
	}
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = defaultMaxConns
	}
	if !strings.Contains(url, "pool_min_conns") {
		cfg.MinConns = defaultMinConns
	}
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: criar pool: %w", err)
	}

	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pctx)
		cancel()
		if err == nil {
			break
		}
		if attempt >= pingAttempts || ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("db: ping após %d tentativa(s): %w", attempt, err)
		}
		logger.Warn("postgres indisponível, tentando de novo", "tentativa", attempt, "espera", delay.String(), "error", err.Error())
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("db: %w", ctx.Err())
		case <-time.After(delay):
		}
		delay *= 2
	}

	if err := otelpgx.RecordStats(pool); err != nil {
		logger.Warn("métricas do pool indisponíveis", "error", err.Error())
	}
	return pool, nil
}

// MigrateOptions personaliza Migrate.
type MigrateOptions struct {
	// Extra roda depois das migrations goose (ex.: migrations do river, de outro pacote).
	Extra []func(ctx context.Context, pool *pgxpool.Pool) error
}

// Migrate aplica as migrations goose embutidas e depois as Extra, serializado por
// advisory lock (seguro com várias instâncias subindo juntas). Idempotente.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, opts ...MigrateOptions) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close() //nolint:errcheck // wrapper; o pool segue aberto

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("db: locker de migrations: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("db: provider goose: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("db: aplicar migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("migration aplicada", "versao", r.Source.Version, "arquivo", r.Source.Path, "duracao", r.Duration.String())
	}
	if len(results) == 0 {
		logger.Debug("schema já atualizado")
	}

	var errs []error
	for _, o := range opts {
		for _, fn := range o.Extra {
			if err := fn(ctx, pool); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("db: migrations extras: %w", err)
	}
	return nil
}
