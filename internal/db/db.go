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
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithSessionLocker(locker),
		// Os marcos reservam faixas de numeração (00050, 00080, 00090, 00100, 00120…) e chegam ao main fora da ordem
		// numérica: um banco que já está na 00100 ainda precisa receber a 00050. Sem isto o goose recusa subir.
		goose.WithAllowOutofOrder(true))
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

// CtypeIsC diz se o lc_ctype do banco é C/POSIX: nele lower() e o tokenizador full-text só entendem ASCII, e a busca
// (ADR-0024, hanko_fold) deixa de achar maiúsculas e letras fora do ASCII que não estão na tabela de acentos.
func CtypeIsC(ctype string) bool {
	switch strings.ToUpper(strings.TrimSpace(ctype)) {
	case "C", "POSIX":
		return true
	}
	return false
}

// CheckLocale avisa no log (uma vez, no boot) quando o banco está em lc_ctype=C. Só informa: o Hanko sobe do mesmo jeito.
func CheckLocale(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	var ctype string
	if err := pool.QueryRow(ctx, `SELECT datctype FROM pg_database WHERE datname = current_database()`).Scan(&ctype); err != nil {
		logger.Debug("não foi possível ler o lc_ctype do banco", "err", err.Error())
		return
	}
	if CtypeIsC(ctype) {
		logger.Warn("o banco está com lc_ctype=C: a busca não acha maiúsculas nem letras fora do ASCII; recrie o banco com um locale UTF-8 (ex.: C.UTF-8 ou en_US.UTF-8)", "lc_ctype", ctype)
	}
}
