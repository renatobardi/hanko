package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/store"
)

type txKey struct{}

// WithTx liga as queries de uma transação ao contexto: o driver postgres grava e lê por elas, de modo que o
// conteúdo entra (ou não) junto com a linha do artefato.
func WithTx(ctx context.Context, q *store.Queries) context.Context {
	return context.WithValue(ctx, txKey{}, q)
}

// Postgres é o driver padrão: a tabela artifact_blobs (bytea).
type Postgres struct {
	Pool *pgxpool.Pool
}

// NewPostgres monta o driver sobre o pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{Pool: pool} }

func (p *Postgres) queries(ctx context.Context) *store.Queries {
	if q, ok := ctx.Value(txKey{}).(*store.Queries); ok && q != nil {
		return q
	}
	return store.New(p.Pool)
}

// Name implementa BlobStore.
func (p *Postgres) Name() string { return DriverPostgres }

// Transactional implementa BlobStore.
func (p *Postgres) Transactional() bool { return true }

// Put implementa BlobStore. A linha do artefato já precisa existir (chave estrangeira).
func (p *Postgres) Put(ctx context.Context, k Key, data []byte, _ string) error {
	if err := p.queries(ctx).CreateArtifactBlob(ctx, store.CreateArtifactBlobParams{ArtifactID: k.ArtifactID, CompanyID: k.CompanyID, Content: data}); err != nil {
		return fmt.Errorf("storage postgres: gravar: %w", err)
	}
	return nil
}

// Get implementa BlobStore.
func (p *Postgres) Get(ctx context.Context, k Key) ([]byte, error) {
	b, err := p.queries(ctx).GetArtifactContent(ctx, store.GetArtifactContentParams{CompanyID: k.CompanyID, ArtifactID: k.ArtifactID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage postgres: ler: %w", err)
	}
	return b, nil
}

// Delete implementa BlobStore.
func (p *Postgres) Delete(ctx context.Context, k Key) error {
	if _, err := p.queries(ctx).DeleteArtifactBlob(ctx, store.DeleteArtifactBlobParams{CompanyID: k.CompanyID, ArtifactID: k.ArtifactID}); err != nil {
		return fmt.Errorf("storage postgres: apagar: %w", err)
	}
	return nil
}

// Stat implementa BlobStore.
func (p *Postgres) Stat(ctx context.Context, k Key) (int64, error) {
	b, err := p.Get(ctx, k)
	if err != nil {
		return 0, err
	}
	return int64(len(b)), nil
}
