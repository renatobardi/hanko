package evals

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGInjector derruba o banco "de verdade" para o chaos: proíbe novas conexões (ALLOW_CONNECTIONS false) e
// encerra as conexões abertas (pg_terminate_backend). Restore reabre. Usa uma conexão administrativa a outro
// banco (o "postgres"), então precisa de um papel com esse poder: é para o Postgres de teste do CI/dev, nunca
// para dados reais.
type PGInjector struct {
	// DatabaseURL é a URL do banco que será derrubado (o de teste).
	DatabaseURL string
}

// Name implementa Injector.
func (p PGInjector) Name() string { return "banco fora do ar" }

func (p PGInjector) parts() (adminURL, dbName string, err error) {
	u, err := url.Parse(p.DatabaseURL)
	if err != nil {
		return "", "", err
	}
	dbName = strings.TrimPrefix(u.Path, "/")
	if dbName == "" || !strings.Contains(dbName, "test") {
		// trava de segurança: só derruba bancos cujo nome diz que é de teste
		return "", "", fmt.Errorf("recusado: %q não parece um banco de teste (o nome precisa conter \"test\")", dbName)
	}
	u.Path = "/postgres"
	return u.String(), dbName, nil
}

func (p PGInjector) admin(ctx context.Context, fn func(c *pgx.Conn, db string) error) error {
	adminURL, db, err := p.parts()
	if err != nil {
		return err
	}
	c, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer c.Close(context.WithoutCancel(ctx))
	return fn(c, db)
}

// Inject implementa Injector.
func (p PGInjector) Inject(ctx context.Context) error {
	return p.admin(ctx, func(c *pgx.Conn, db string) error {
		q := pgx.Identifier{db}.Sanitize()
		if _, err := c.Exec(ctx, "ALTER DATABASE "+q+" WITH ALLOW_CONNECTIONS false"); err != nil {
			return err
		}
		_, err := c.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", db)
		return err
	})
}

// Restore implementa Injector (idempotente).
func (p PGInjector) Restore(ctx context.Context) error {
	return p.admin(ctx, func(c *pgx.Conn, db string) error {
		_, err := c.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{db}.Sanitize()+" WITH ALLOW_CONNECTIONS true")
		return err
	})
}

// Ping é a verificação mínima de "o banco responde" que o chaos usa antes, durante e depois da falha.
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	var one int
	return pool.QueryRow(ctx, "SELECT 1").Scan(&one)
}
