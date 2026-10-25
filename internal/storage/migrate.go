package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// MigrateOptions define uma migração de conteúdo entre drivers (ADR-0051).
type MigrateOptions struct {
	From, To string
	// CompanyID restringe a uma empresa; nil = todas.
	CompanyID *domain.ID
	// KeepSource mantém o conteúdo na origem depois de copiar (padrão: apaga).
	KeepSource bool
	// DryRun só conta o que seria movido.
	DryRun bool
	// Batch é o tamanho do lote lido do banco (padrão 100).
	Batch int
	Log   *slog.Logger
}

// MigrateReport é o resultado.
type MigrateReport struct {
	Scanned  int      // versões encontradas no driver de origem
	Moved    int      // copiadas, conferidas e com o registro trocado
	Failed   int      // não puderam ser movidas (ficam na origem)
	Leftover int      // movidas, mas o conteúdo na origem não pôde ser apagado
	Bytes    int64    // bytes movidos
	Errors   []string // primeiras falhas (id: motivo), sem conteúdo
}

const maxReportedErrors = 20

// Migrate move o conteúdo dos artefatos de um driver para outro, em lotes, e depois o dos campos arquivo do cofre
// (migrateSecretFieldBlobs). É retomável e idempotente: cada versão é copiada, conferida (tamanho e sha256 registrados,
// depois um Stat no destino) e só então o registro troca de driver; reexecutar continua de onde parou. Uma falha em uma
// versão é contada e a migração segue.
func Migrate(ctx context.Context, pool *pgxpool.Pool, reg *Registry, o MigrateOptions) (MigrateReport, error) {
	var rep MigrateReport
	if o.From == o.To {
		return rep, errors.New("storage: origem e destino são o mesmo driver")
	}
	src, err := reg.Driver(o.From)
	if err != nil {
		return rep, err
	}
	dst, err := reg.Driver(o.To)
	if err != nil {
		return rep, err
	}
	if o.Batch <= 0 {
		o.Batch = 100
	}
	o.Batch = min(o.Batch, 1000)
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	q := store.New(pool)
	after := uuid.Nil
	fail := func(id uuid.UUID, why string) {
		rep.Failed++
		if len(rep.Errors) < maxReportedErrors {
			rep.Errors = append(rep.Errors, id.String()+": "+why)
		}
		log.Warn("blobs migrate: falha", "artifact_id", id, "why", why)
	}
	for {
		rows, err := q.ListArtifactsForBlobMigration(ctx, store.ListArtifactsForBlobMigrationParams{
			Storage: o.From, CompanyID: o.CompanyID, AfterID: after, Lim: int32(o.Batch)}) //nolint:gosec // limitado a 1000 logo acima
		if err != nil {
			return rep, fmt.Errorf("storage: listar artefatos: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			after = r.ID
			rep.Scanned++
			if o.DryRun {
				rep.Moved++
				rep.Bytes += r.SizeBytes
				continue
			}
			key := Key{CompanyID: r.CompanyID, ArtifactID: r.ID}
			data, err := src.Get(ctx, key)
			switch {
			case errors.Is(err, ErrNotFound):
				// retomada: o conteúdo pode já ter sido copiado e a origem apagada antes de o registro trocar
				if n, serr := dst.Stat(ctx, key); serr == nil && n == r.SizeBytes {
					if flipped, ferr := flip(ctx, q, r, o); ferr != nil {
						fail(r.ID, ferr.Error())
					} else if flipped {
						rep.Moved++
						rep.Bytes += r.SizeBytes
					}
					continue
				}
				fail(r.ID, "conteúdo ausente na origem")
				continue
			case err != nil:
				fail(r.ID, "ler origem: "+err.Error())
				continue
			}
			sum := sha256.Sum256(data)
			if int64(len(data)) != r.SizeBytes || hex.EncodeToString(sum[:]) != r.Sha256 {
				fail(r.ID, "o conteúdo na origem não bate com o tamanho/sha256 registrados")
				continue
			}
			if dst.Transactional() {
				_ = dst.Delete(ctx, key) // sobra de uma execução interrompida (chave primária)
			}
			if err := dst.Put(ctx, key, data, r.Mime); err != nil {
				fail(r.ID, "gravar destino: "+err.Error())
				continue
			}
			if n, err := dst.Stat(ctx, key); err != nil || n != r.SizeBytes {
				fail(r.ID, "o destino não confirmou o tamanho")
				continue
			}
			flipped, err := flip(ctx, q, r, o)
			if err != nil {
				fail(r.ID, err.Error())
				continue
			}
			if !flipped {
				continue // mudou no meio (apagado ou já movido): nada a fazer
			}
			rep.Moved++
			rep.Bytes += r.SizeBytes
			if !o.KeepSource {
				if err := src.Delete(ctx, key); err != nil {
					rep.Leftover++
					log.Warn("blobs migrate: conteúdo movido, mas a origem não foi apagada", "artifact_id", r.ID, "err", err.Error())
				}
			}
		}
	}
	if err := migrateSecretFieldBlobs(ctx, q, src, dst, o, &rep, log); err != nil {
		return rep, err
	}
	return rep, nil
}

// errFieldBlobConflict: o destino já guarda outro conteúdo para o campo.
var errFieldBlobConflict = errors.New("storage: o destino já tem outro conteúdo para o campo")

// migrateSecretFieldBlobs move o conteúdo cifrado dos campos arquivo do cofre entre drivers, um campo por vez
// (ADR-0068 §5.1). No driver postgres o conteúdo vive na tabela secret_field_blobs, fora do BlobStore; nos drivers fs e
// s3, sob a chave do campo. O texto cifrado é copiado sem decifrar: a conferência compara a cópia com a origem byte a
// byte. Como o campo não registra o driver (quem decide onde ler é o driver ativo, ADR-0072), migrar é esvaziar a
// origem; retomar reconhece o campo cuja origem já não tem conteúdo e o destino tem.
func migrateSecretFieldBlobs(ctx context.Context, q *store.Queries, src, dst BlobStore, o MigrateOptions, rep *MigrateReport, log *slog.Logger) error {
	pgSrc, pgDst := o.From == DriverPostgres, o.To == DriverPostgres
	fail := func(id uuid.UUID, why string) {
		rep.Failed++
		if len(rep.Errors) < maxReportedErrors {
			rep.Errors = append(rep.Errors, id.String()+": "+why)
		}
		log.Warn("blobs migrate: falha", "field_id", id, "why", why)
	}
	after := uuid.Nil
	for {
		rows, err := q.ListSecretFileFieldsAfter(ctx, store.ListSecretFileFieldsAfterParams{
			CompanyID: o.CompanyID, AfterID: after, Lim: int32(o.Batch)}) //nolint:gosec // limitado a 1000 em Migrate
		if err != nil {
			return fmt.Errorf("storage: listar campos arquivo: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, f := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			after = f.ID
			key := Key{CompanyID: f.CompanyID, ArtifactID: f.ID} // o id do campo no papel do id de artefato
			data, err := readFieldBlob(ctx, q, src, pgSrc, key)
			if err != nil {
				fail(f.ID, "ler origem: "+err.Error())
				continue
			}
			if data == nil {
				// origem sem conteúdo: retomada (já copiado e origem apagada) ou campo sem gravação
				if has, err := fieldBlobInDst(ctx, q, dst, pgDst, key); err == nil && has {
					continue
				}
				fail(f.ID, "conteúdo ausente na origem")
				continue
			}
			rep.Scanned++
			if o.DryRun {
				rep.Moved++
				rep.Bytes += int64(len(data))
				continue
			}
			if err := writeFieldBlob(ctx, q, dst, pgDst, key, data); err != nil {
				fail(f.ID, err.Error())
				continue
			}
			back, err := readFieldBlob(ctx, q, dst, pgDst, key)
			if err != nil || !bytes.Equal(back, data) {
				fail(f.ID, "o destino não confirmou a cópia")
				continue
			}
			rep.Moved++
			rep.Bytes += int64(len(data))
			if !o.KeepSource {
				if err := deleteFieldBlob(ctx, q, src, pgSrc, key); err != nil {
					rep.Leftover++
					log.Warn("blobs migrate: conteúdo movido, mas a origem não foi apagada", "field_id", f.ID, "err", err.Error())
				}
			}
		}
	}
}

// readFieldBlob lê o conteúdo cifrado de um campo arquivo no lugar dado; nil, nil quando o lugar não o tem.
func readFieldBlob(ctx context.Context, q *store.Queries, drv BlobStore, pg bool, key Key) ([]byte, error) {
	if pg {
		b, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: key.CompanyID, FieldID: key.ArtifactID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return b, err
	}
	b, err := drv.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return b, err
}

// fieldBlobInDst diz se o destino já tem conteúdo para a chave (retomada de uma execução interrompida).
func fieldBlobInDst(ctx context.Context, q *store.Queries, dst BlobStore, pgDst bool, key Key) (bool, error) {
	if pgDst {
		_, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: key.CompanyID, FieldID: key.ArtifactID})
		return err == nil, err
	}
	_, err := dst.Stat(ctx, key)
	return err == nil, err
}

// writeFieldBlob grava o conteúdo cifrado no destino. Na tabela do postgres, uma linha de outra cópia é conflito; a
// mesma cópia (execução interrompida antes de apagar a origem) segue em frente.
func writeFieldBlob(ctx context.Context, q *store.Queries, dst BlobStore, pgDst bool, key Key, data []byte) error {
	if !pgDst {
		return dst.Put(ctx, key, data, "application/octet-stream")
	}
	if cur, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: key.CompanyID, FieldID: key.ArtifactID}); err == nil {
		if bytes.Equal(cur, data) {
			return nil
		}
		return errFieldBlobConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return q.CreateSecretFieldBlob(ctx, store.CreateSecretFieldBlobParams{FieldID: key.ArtifactID, CompanyID: key.CompanyID, Content: data})
}

// deleteFieldBlob apaga o conteúdo cifrado do lugar dado.
func deleteFieldBlob(ctx context.Context, q *store.Queries, drv BlobStore, pg bool, key Key) error {
	if pg {
		return q.DeleteSecretFieldBlob(ctx, key.ArtifactID)
	}
	return drv.Delete(ctx, key)
}

func flip(ctx context.Context, q *store.Queries, r store.ListArtifactsForBlobMigrationRow, o MigrateOptions) (bool, error) {
	n, err := q.SetArtifactStorage(ctx, store.SetArtifactStorageParams{CompanyID: r.CompanyID, ID: r.ID, FromStorage: o.From, ToStorage: o.To})
	if err != nil {
		return false, fmt.Errorf("trocar o driver do registro: %w", err)
	}
	return n == 1, nil
}

// Usage é o total de versões e bytes de artefato guardados em um driver.
type Usage struct {
	Driver   string
	Versions int64
	Bytes    int64
}

// Count soma as versões de artefato por driver (hanko blobs status).
func Count(ctx context.Context, pool *pgxpool.Pool) ([]Usage, error) {
	rows, err := store.New(pool).CountArtifactsByStorage(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: contar artefatos: %w", err)
	}
	out := make([]Usage, len(rows))
	for i, r := range rows {
		out[i] = Usage{Driver: r.Storage, Versions: r.Versions, Bytes: r.Bytes}
	}
	return out, nil
}
