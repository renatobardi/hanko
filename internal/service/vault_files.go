package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
)

// Campo arquivo do cofre (ADR-0068 §5). O conteúdo é cifrado com o AAD do campo antes de sair do processo, e o texto
// cifrado é o que o driver guarda: postgres na tabela secret_field_blobs (artifact_blobs tem FK para artifacts), fs e s3
// no BlobStore (ADR-0051), com a chave da empresa e do campo. O valor só volta em claro para quem lê o campo.

// fieldBlobKey é a chave do conteúdo de um campo arquivo no BlobStore. storage.Key chama o id de artefato; aqui é o id
// do campo, único do mesmo jeito.
func fieldBlobKey(companyID, fieldID domain.ID) storage.Key {
	return storage.Key{CompanyID: companyID, ArtifactID: fieldID}
}

// putFieldBlob grava o texto cifrado de um campo arquivo na transação que cria o campo. Num driver que não participa da
// transação, o objeto é apagado se a transação falhar.
func (s *Service) putFieldBlob(t *txc, companyID, fieldID domain.ID, sealed []byte) error {
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		if err := t.q.CreateSecretFieldBlob(t.ctx, store.CreateSecretFieldBlobParams{FieldID: fieldID, CompanyID: companyID, Content: sealed}); err != nil {
			return mapErr(err, "create secret field blob")
		}
		return nil
	}
	k := fieldBlobKey(companyID, fieldID)
	if drv.Transactional() {
		if err := drv.Put(storage.WithTx(t.ctx, t.q), k, sealed, "application/octet-stream"); err != nil {
			return fmt.Errorf("gravar arquivo do campo: %w", err)
		}
		return nil
	}
	if err := drv.Put(t.ctx, k, sealed, "application/octet-stream"); err != nil {
		return fmt.Errorf("gravar arquivo do campo: %w", err)
	}
	t.onRollback = append(t.onRollback, func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), time.Minute)
		defer cancel()
		_ = drv.Delete(ctx, k)
	})
	return nil
}

// dropFieldBlobAfterCommit apaga, depois do commit, o arquivo de um campo removido. No postgres a linha sai em cascata.
func (t *txc) dropFieldBlobAfterCommit(companyID, fieldID domain.ID) {
	drv := t.s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		return
	}
	k := fieldBlobKey(companyID, fieldID)
	t.after = append(t.after, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := drv.Delete(ctx, k); err != nil && t.s.Log != nil {
			t.s.Log.Warn("cofre: apagar arquivo de campo", "field_id", fieldID.String(), "err", err)
		}
	})
}

// createFileField cria um campo arquivo: confere o tamanho e os metadados, cifra o conteúdo com o AAD do campo, grava o
// texto cifrado no driver e guarda na linha só os metadados (ADR-0068 §5.1). Quem chama já tem a transação e o segredo.
func (s *Service) createFileField(t *txc, companyID domain.ID, sec store.Secret, id domain.ID, in CreateSecretFieldInput, pos int, sensitive bool) (store.SecretField, error) {
	if in.Value != "" || in.Generate || in.ExternalRef != "" || providerOf(in.SecretFieldValueInput) != domain.SecretProviderLocal {
		return store.SecretField{}, domain.Invalid("value", "campo arquivo recebe o conteúdo em file, sem valor de texto nem provider externo")
	}
	size := len(in.File)
	if size == 0 || size > domain.MaxSecretFieldFileBytes {
		return store.SecretField{}, domain.Invalid("file", fmt.Sprintf("o arquivo tem de ter de 1 byte a %d bytes (1 MiB)", domain.MaxSecretFieldFileBytes))
	}
	fileName := strings.TrimSpace(in.FileName)
	if fileName == "" || len(fileName) > 255 {
		return store.SecretField{}, domain.Invalid("file_name", "nome do arquivo: de 1 a 255 caracteres")
	}
	contentType := strings.TrimSpace(in.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if len(contentType) > 255 {
		return store.SecretField{}, domain.Invalid("content_type", "tipo do conteúdo: até 255 caracteres")
	}
	if !s.secretBox().Enabled() {
		return store.SecretField{}, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	sealed, err := s.secretBox().Seal(in.File, fieldAAD(sec.ID, id))
	if err != nil {
		return store.SecretField{}, fmt.Errorf("cifrar arquivo: %w", err)
	}
	// a linha do campo vem antes do blob: no postgres, a tabela do blob referencia o campo
	row, err := t.q.CreateSecretField(t.ctx, store.CreateSecretFieldParams{ID: id, CompanyID: companyID, SecretID: sec.ID,
		Name: in.Name, Kind: string(domain.SecretFieldFile), Sensitive: sensitive, Provider: string(domain.SecretProviderLocal),
		BlobKey: fieldBlobKey(companyID, id).String(), FileName: fileName, ContentType: contentType, SizeBytes: int32(size),
		AadVersion: 1, Position: i32(pos), Now: t.now})
	if err != nil {
		return store.SecretField{}, mapErr(err, "create secret field")
	}
	if err := s.putFieldBlob(t, companyID, id, []byte(sealed)); err != nil {
		return store.SecretField{}, err
	}
	return row, nil
}

// readFieldBlob devolve o texto cifrado de um campo arquivo; quem chama decifra.
func (s *Service) readFieldBlob(ctx context.Context, q *store.Queries, companyID, fieldID domain.ID) ([]byte, error) {
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		b, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: companyID, FieldID: fieldID})
		if err != nil {
			return nil, mapErr(err, "secret field blob")
		}
		return b, nil
	}
	b, err := drv.Get(ctx, fieldBlobKey(companyID, fieldID))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, notFound("arquivo do campo")
	}
	return b, err
}

// fieldValue devolve o valor em claro de um campo do segredo: o conteúdo do arquivo, o texto do cofre externo ou o
// valor local. Não registra uso: quem chama decide o que fazer com o valor e o que registrar.
func (s *Service) fieldValue(ctx context.Context, companyID domain.ID, sec store.Secret, f store.SecretField) ([]byte, error) {
	switch {
	case f.Kind == string(domain.SecretFieldFile):
		sealed, err := s.readFieldBlob(ctx, s.read(), companyID, f.ID)
		if err != nil {
			return nil, fmt.Errorf("ler arquivo: %w", err)
		}
		plain, err := s.secretBox().Open(string(sealed), fieldAADFor(f.AadVersion, sec.ID, f.ID))
		if err != nil {
			return nil, fmt.Errorf("decifrar arquivo: %w", err)
		}
		return plain, nil
	case f.Provider == string(domain.SecretProviderVault):
		if s.Vault == nil {
			return nil, errors.New("cofre externo não configurado")
		}
		v, err := s.Vault.Get(ctx, f.ExternalRef)
		if err != nil {
			return nil, fmt.Errorf("cofre: %w", err)
		}
		return []byte(v), nil
	}
	plain, err := s.openField(f, sec.ID)
	if err != nil {
		return nil, fmt.Errorf("decifrar: %w", err)
	}
	return plain, nil
}

// rekeyFieldBlobs recifra os arquivos de campo com a chave atual: os das linhas do postgres e os do BlobStore dos
// demais drivers. Sem isso, trocar HANKO_SECRET_KEY deixaria os arquivos ilegíveis.
func (s *Service) rekeyFieldBlobs(ctx context.Context) (int, error) {
	q, w := s.read(), store.New(s.Pool)
	n := 0
	reseal := func(enc string, aad string) (string, bool, error) {
		if !s.secretBox().NeedsReseal(enc) {
			return "", false, nil
		}
		plain, err := s.secretBox().Open(enc, aad)
		if err != nil {
			return "", false, err
		}
		out, err := s.secretBox().Seal(plain, aad)
		return out, err == nil, err
	}
	drv := s.blobs().Active()
	files, err := q.ListSecretFileFieldsForRekey(ctx)
	if err != nil {
		return 0, fmt.Errorf("list secret file fields: %w", err)
	}
	if drv.Name() == storage.DriverPostgres {
		for _, f := range files {
			if f.Content == nil { // campo arquivo sem linha de blob nesta gravação
				continue
			}
			enc, ok, err := reseal(string(f.Content), fieldAAD(f.SecretID, f.ID))
			if err != nil {
				return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
			}
			if !ok {
				continue
			}
			if err := w.SetSecretFieldBlob(ctx, store.SetSecretFieldBlobParams{FieldID: f.ID, Content: []byte(enc)}); err != nil {
				return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
			}
			n++
		}
		return n, nil
	}
	for _, f := range files {
		k := fieldBlobKey(f.CompanyID, f.ID)
		sealed, err := drv.Get(ctx, k)
		if err != nil {
			return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
		}
		enc, ok, err := reseal(string(sealed), fieldAAD(f.SecretID, f.ID))
		if err != nil {
			return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
		}
		if !ok {
			continue
		}
		if err := drv.Put(ctx, k, []byte(enc), "application/octet-stream"); err != nil {
			return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
		}
		n++
	}
	return n, nil
}
