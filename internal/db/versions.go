package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/db/migrations"
)

// EmbeddedVersions lista as versões das migrations embutidas no binário, em ordem crescente.
func EmbeddedVersions() []int64 {
	return versionsOf(migrations.FS)
}

func versionsOf(fsys fs.FS) []int64 {
	ents, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil
	}
	var out []int64
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		if v, err := strconv.ParseInt(prefix, 10, 64); err == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LatestEmbedded é a maior versão de migration que este binário conhece (0 sem nenhuma).
func LatestEmbedded() int64 {
	v := EmbeddedVersions()
	if len(v) == 0 {
		return 0
	}
	return v[len(v)-1]
}

// AppliedVersions lista as versões aplicadas no banco (tabela do goose). Banco sem a tabela: lista vazia.
func AppliedVersions(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("db: procurar goose_db_version: %w", err)
	}
	if !exists {
		return nil, nil
	}
	rows, err := pool.Query(ctx, `SELECT version_id FROM goose_db_version WHERE is_applied AND version_id > 0 ORDER BY version_id`)
	if err != nil {
		return nil, fmt.Errorf("db: ler versões aplicadas: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MaxApplied é a maior versão aplicada (0 sem nenhuma).
func MaxApplied(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	v, err := AppliedVersions(ctx, pool)
	if err != nil || len(v) == 0 {
		return 0, err
	}
	return v[len(v)-1], nil
}

// PendingVersions são as migrations embutidas que o banco ainda não aplicou (a ordem numérica não importa: as
// migrations das trilhas chegam fora de ordem, ver Migrate).
func PendingVersions(embedded, applied []int64) []int64 {
	have := make(map[int64]bool, len(applied))
	for _, v := range applied {
		have[v] = true
	}
	var out []int64
	for _, v := range embedded {
		if !have[v] {
			out = append(out, v)
		}
	}
	return out
}
