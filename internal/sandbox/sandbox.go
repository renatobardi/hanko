// Package sandbox cria e apaga as instâncias descartáveis de test-drive (uma por branch ou worktree, ADR-0061): um banco
// próprio no Postgres que o desenvolvedor já tem, uma porta determinística e um contexto da CLI apontando para ela.
// Nada aqui sobe o servidor; quem chama (o comando `hanko sandbox`) decide como.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// DBPrefix marca os bancos de sandbox: só estes são listados e apagados.
const DBPrefix = "hanko_sbx_"

// maxName limita o nome (o identificador de banco do Postgres vai a 63 bytes).
const maxName = 40

// PortBase e PortSpan definem a faixa das portas determinísticas.
const (
	PortBase = 18000
	PortSpan = 1000
)

// Instance descreve uma sandbox.
type Instance struct {
	Name        string `json:"name"`
	Database    string `json:"database"`
	DatabaseURL string `json:"-"`
	Port        int    `json:"port"`
}

// Slug reduz um nome de branch ou diretório a [a-z0-9-] (até 40 caracteres, sem hífen nas pontas). Vazio vira "dev".
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxName {
		out = strings.Trim(out[:maxName], "-")
	}
	if out == "" {
		return "dev"
	}
	return out
}

// DBName é o nome do banco da sandbox.
func DBName(name string) string { return DBPrefix + strings.ReplaceAll(Slug(name), "-", "_") }

// NameOfDB devolve o nome da sandbox de um banco ("" quando o banco não é de sandbox).
func NameOfDB(db string) string {
	n, ok := strings.CutPrefix(db, DBPrefix)
	if !ok || n == "" {
		return ""
	}
	return strings.ReplaceAll(n, "_", "-")
}

// Port é a porta determinística do nome (a mesma branch cai sempre na mesma porta).
func Port(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(Slug(name)))
	return PortBase + int(h.Sum32()%PortSpan) //nolint:gosec // G115: o resto é menor que PortSpan
}

// WithDatabase troca o banco de uma URL postgres://, preservando usuário, host e parâmetros.
func WithDatabase(rawURL, db string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return "", errors.New("a URL do banco precisa ser postgres://usuario@host:porta/banco")
	}
	u.Path = "/" + db
	u.RawPath = ""
	return u.String(), nil
}

// Branch devolve o nome da branch de dir (git), ou o nome do diretório quando não há git ou a HEAD está solta.
func Branch(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		if b := strings.TrimSpace(string(out)); b != "" && b != "HEAD" {
			return b
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "dev"
	}
	return filepath.Base(abs)
}

func connect(ctx context.Context, adminURL string) (*pgx.Conn, error) {
	c, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("conectar ao Postgres: %w", err)
	}
	return c, nil
}

// Create cria o banco da sandbox (se ainda não existe) e devolve a instância. adminURL é a URL de um banco do mesmo
// servidor onde o usuário pode criar bancos (HANKO_DATABASE_URL do desenvolvedor).
func Create(ctx context.Context, adminURL, name string) (Instance, error) {
	name = Slug(name)
	db := DBName(name)
	dbURL, err := WithDatabase(adminURL, db)
	if err != nil {
		return Instance{}, err
	}
	c, err := connect(ctx, adminURL)
	if err != nil {
		return Instance{}, err
	}
	defer c.Close(ctx)
	var exists bool
	if err := c.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, db).Scan(&exists); err != nil {
		return Instance{}, fmt.Errorf("consultar bancos: %w", err)
	}
	if !exists {
		if _, err := c.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
			return Instance{}, fmt.Errorf("criar o banco %s (o usuário precisa de CREATEDB): %w", db, err)
		}
	}
	return Instance{Name: name, Database: db, DatabaseURL: dbURL, Port: Port(name)}, nil
}

// Drop apaga o banco da sandbox, derrubando conexões abertas. Apagar o que não existe não é erro; bancos fora do prefixo
// nunca são tocados.
func Drop(ctx context.Context, adminURL, name string) error {
	db := DBName(name)
	if !strings.HasPrefix(db, DBPrefix) {
		return errors.New("recusado: não é um banco de sandbox")
	}
	c, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("apagar o banco %s: %w", db, err)
	}
	return nil
}

// List devolve as sandboxes do servidor (bancos com o prefixo), por nome.
func List(ctx context.Context, adminURL string) ([]Instance, error) {
	c, err := connect(ctx, adminURL)
	if err != nil {
		return nil, err
	}
	defer c.Close(ctx)
	rows, err := c.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1 ORDER BY datname`, strings.ReplaceAll(DBPrefix, "_", `\_`)+"%")
	if err != nil {
		return nil, fmt.Errorf("listar bancos: %w", err)
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, err
		}
		n := NameOfDB(db)
		if n == "" {
			continue
		}
		dbURL, err := WithDatabase(adminURL, db)
		if err != nil {
			return nil, err
		}
		out = append(out, Instance{Name: n, Database: db, DatabaseURL: dbURL, Port: Port(n)})
	}
	return out, rows.Err()
}
