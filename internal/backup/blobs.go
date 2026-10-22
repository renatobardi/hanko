package backup

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
)

// PGBlobs entrega ao backup o conteúdo dos artefatos guardados em drivers externos (hoje, s3). O driver postgres
// não entra aqui: o conteúdo dele já está no pg_dump.
type PGBlobs struct {
	Pool *pgxpool.Pool
	Reg  *storage.Registry
}

const blobPage = 500

// List percorre, em ordem estável, as versões de artefato guardadas em cada driver externo configurado.
func (p PGBlobs) List(ctx context.Context) ([]BlobEntry, error) {
	var out []BlobEntry
	q := store.New(p.Pool)
	// conteúdo em um driver que este servidor não tem configurado não pode entrar no backup: melhor falhar alto do
	// que gerar um backup que parece completo
	usage, err := q.CountArtifactsByStorage(ctx)
	if err != nil {
		return nil, fmt.Errorf("contar artefatos por driver: %w", err)
	}
	have := map[string]bool{}
	for _, n := range p.Reg.Names() {
		have[n] = true
	}
	for _, u := range usage {
		if u.Versions > 0 && u.Storage != storage.DriverPostgres && !have[u.Storage] {
			return nil, fmt.Errorf("há %d versão(ões) de artefato no driver %q, que não está configurado neste servidor: configure-o (HANKO_S3_*) para o backup incluir esse conteúdo", u.Versions, u.Storage)
		}
	}
	for _, name := range p.Reg.Names() {
		if name == storage.DriverPostgres {
			continue
		}
		after := uuid.Nil
		for {
			rows, err := q.ListArtifactsForBlobMigration(ctx, store.ListArtifactsForBlobMigrationParams{Storage: name, AfterID: after, Lim: blobPage})
			if err != nil {
				return nil, fmt.Errorf("listar artefatos do driver %s: %w", name, err)
			}
			for _, r := range rows {
				after = r.ID
				out = append(out, BlobEntry{CompanyID: r.CompanyID.String(), ArtifactID: r.ID.String(), Size: r.SizeBytes, SHA256: r.Sha256, Mime: r.Mime, Driver: name})
			}
			if len(rows) < blobPage {
				break
			}
		}
	}
	return out, nil
}

func keyOf(b BlobEntry) (storage.Key, error) {
	c, err := uuid.Parse(b.CompanyID)
	if err != nil {
		return storage.Key{}, fmt.Errorf("company_id inválido: %w", err)
	}
	a, err := uuid.Parse(b.ArtifactID)
	if err != nil {
		return storage.Key{}, fmt.Errorf("artifact_id inválido: %w", err)
	}
	return storage.Key{CompanyID: c, ArtifactID: a}, nil
}

// Get lê o conteúdo no driver do item.
func (p PGBlobs) Get(ctx context.Context, b BlobEntry) ([]byte, error) {
	d, err := p.Reg.Driver(b.Driver)
	if err != nil {
		return nil, err
	}
	k, err := keyOf(b)
	if err != nil {
		return nil, err
	}
	return d.Get(ctx, k)
}

// Put grava o conteúdo de volta no driver do item; o registro do artefato já voltou com o pg_restore.
func (p PGBlobs) Put(ctx context.Context, b BlobEntry, data []byte) error {
	d, err := p.Reg.Driver(b.Driver)
	if err != nil {
		return err
	}
	k, err := keyOf(b)
	if err != nil {
		return err
	}
	return d.Put(ctx, k, data, b.Mime)
}
