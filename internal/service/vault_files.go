package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

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

// dropFieldBlobAfterCommit apaga, depois do commit, o arquivo de um campo removido, em todos os drivers registrados
// (no meio de uma migração entre drivers pode haver cópia em mais de um). No postgres a linha sai em cascata.
func (t *txc) dropFieldBlobAfterCommit(companyID, fieldID domain.ID) {
	t.dropOtherFieldBlobsAfterCommit(companyID, fieldID, "")
}

// dropOtherFieldBlobsAfterCommit apaga, depois do commit, as cópias do arquivo de um campo em todos os drivers, exceto o
// postgres (a linha dele é tratada na transação) e o driver `keep` (vazio = nenhum é poupado).
func (t *txc) dropOtherFieldBlobsAfterCommit(companyID, fieldID domain.ID, keep string) {
	k := fieldBlobKey(companyID, fieldID)
	for _, name := range t.s.blobs().Names() {
		if name == storage.DriverPostgres || name == keep {
			continue
		}
		drv, err := t.s.blobs().Driver(name)
		if err != nil {
			continue // o nome vem do próprio registro; não falha
		}
		t.after = append(t.after, func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := drv.Delete(ctx, k); err != nil && t.s.Log != nil {
				t.s.Log.Warn("cofre: apagar arquivo de campo", "field_id", fieldID.String(), "err", err)
			}
		})
	}
}

// createFileField cria um campo arquivo: confere o tamanho e os metadados, cifra o conteúdo com o AAD do campo, grava o
// texto cifrado no driver e guarda na linha só os metadados (ADR-0068 §5.1). Quem chama já tem a transação e o segredo.
func (s *Service) createFileField(t *txc, companyID domain.ID, sec store.Secret, id domain.ID, in CreateSecretFieldInput, pos int, sensitive bool) (store.SecretField, error) {
	if in.Value != "" || in.Generate || in.ExternalRef != "" || providerOf(in.SecretFieldValueInput) != domain.SecretProviderLocal {
		return store.SecretField{}, domain.Invalid("value", "campo arquivo recebe o conteúdo em file, sem valor de texto nem provider externo")
	}
	fileName, contentType, err := checkFileContent(in.File, in.FileName, in.ContentType)
	if err != nil {
		return store.SecretField{}, err
	}
	if err := s.requireSecretBox(); err != nil {
		return store.SecretField{}, err
	}
	size, err := fileSize(in.File)
	if err != nil {
		return store.SecretField{}, err
	}
	sealed, err := s.secretBox().Seal(in.File, fieldAAD(sec.ID, id))
	if err != nil {
		return store.SecretField{}, fmt.Errorf("cifrar arquivo: %w", err)
	}
	// a linha do campo vem antes do blob: no postgres, a tabela do blob referencia o campo
	row, err := t.q.CreateSecretField(t.ctx, store.CreateSecretFieldParams{ID: id, CompanyID: companyID, SecretID: sec.ID,
		Name: in.Name, Kind: string(domain.SecretFieldFile), Sensitive: sensitive, Provider: string(domain.SecretProviderLocal),
		BlobKey: fieldBlobKey(companyID, id).String(), FileName: fileName, ContentType: contentType, SizeBytes: size,
		AadVersion: 1, Position: i32(pos), Now: t.now})
	if err != nil {
		return store.SecretField{}, mapErr(err, "create secret field")
	}
	if err := s.putFieldBlob(t, companyID, id, []byte(sealed)); err != nil {
		return store.SecretField{}, err
	}
	return row, nil
}

// checkFileContent confere o conteúdo de um campo arquivo: de 1 byte a 1 MiB, nome de 1 a 255 caracteres e tipo de até
// 255 (vazio vira application/octet-stream). Devolve o nome e o tipo já aparados.
func checkFileContent(file []byte, fileName, contentType string) (string, string, error) {
	if size := len(file); size == 0 || size > domain.MaxSecretFieldFileBytes {
		return "", "", domain.Invalid("file", fmt.Sprintf("o arquivo tem de ter de 1 byte a %d bytes (1 MiB)", domain.MaxSecretFieldFileBytes))
	}
	name := strings.TrimSpace(fileName)
	if name == "" || len(name) > 255 {
		return "", "", domain.Invalid("file_name", "nome do arquivo: de 1 a 255 caracteres")
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	if len(ct) > 255 {
		return "", "", domain.Invalid("content_type", "tipo do conteúdo: até 255 caracteres")
	}
	return name, ct, nil
}

// fileSize devolve o tamanho do conteúdo como int32 (coluna size_bytes), conferido contra o limite antes da conversão.
func fileSize(file []byte) (int32, error) {
	n := len(file)
	if n <= 0 || n > domain.MaxSecretFieldFileBytes {
		return 0, domain.Invalid("file", fmt.Sprintf("o arquivo tem de ter de 1 byte a %d bytes (1 MiB)", domain.MaxSecretFieldFileBytes))
	}
	return int32(n), nil
}

// requireSecretBox recusa guardar um arquivo de campo sem a chave de cifragem do servidor.
func (s *Service) requireSecretBox() error {
	if !s.secretBox().Enabled() {
		return domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	return nil
}

// ReplaceFieldFileInput é o arquivo novo de um campo arquivo, com as mesmas regras de CreateSecretFieldInput.
type ReplaceFieldFileInput struct {
	File        []byte
	FileName    string
	ContentType string
}

// fieldRef identifica o campo a trocar: escopo, dono (usuário, ou nil na empresa), segredo e campo.
type fieldRef struct {
	scope      domain.SecretScope
	owner      *uuid.UUID
	secretName string
	fieldName  string
}

// ReplaceSecretFieldFile troca o conteúdo de um campo arquivo pelo arquivo novo (#117). O campo mantém o nome, o id e a
// marca de visibilidade. O conteúdo anterior é sobrescrito e não vai para o histórico: o histórico de campo arquivo não
// guarda o arquivo de versões anteriores. Troca, metadados e evento secret.field_updated (sem conteúdo) são da mesma
// transação; num driver fora da transação, o arquivo é gravado antes do commit e a falha do commit não o desfaz.
func (s *Service) ReplaceSecretFieldFile(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, in ReplaceFieldFileInput) (SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return SecretFieldView{}, err
	}
	in.FileName, in.ContentType, err = checkFileContent(in.File, in.FileName, in.ContentType)
	if err != nil {
		return SecretFieldView{}, err
	}
	if err := s.requireSecretBox(); err != nil {
		return SecretFieldView{}, err
	}
	ref := fieldRef{scope: scope, owner: owner, secretName: secretName, fieldName: fieldName}
	var out SecretFieldView
	err = s.withTx(ctx, func(t *txc) error {
		v, err := s.replaceFileTx(t, actor, companyID, ref, in)
		out = v
		return err
	})
	return out, err
}

// replaceFileTx faz a troca dentro da transação: trava o campo, confere quem escreve e se é campo arquivo, cifra, grava
// o conteúdo e os metadados, e registra o evento sem conteúdo. `in` já vem com nome e tipo conferidos.
func (s *Service) replaceFileTx(t *txc, actor domain.Actor, companyID domain.ID, ref fieldRef, in ReplaceFieldFileInput) (SecretFieldView, error) {
	size, err := fileSize(in.File)
	if err != nil {
		return SecretFieldView{}, err
	}
	sec, f, err := s.lockField(t, companyID, ref.scope, ref.owner, ref.secretName, ref.fieldName)
	if err != nil {
		return SecretFieldView{}, err
	}
	if err := s.requireFieldWriter(t.ctx, t.q, actor, companyID, sec); err != nil {
		return SecretFieldView{}, err
	}
	if f.Kind != string(domain.SecretFieldFile) {
		return SecretFieldView{}, domain.Invalid("kind", "só campo arquivo recebe arquivo novo; campo de texto troca pelo valor")
	}
	sealed, err := s.secretBox().Seal(in.File, fieldAAD(sec.ID, f.ID))
	if err != nil {
		return SecretFieldView{}, fmt.Errorf("cifrar arquivo: %w", err)
	}
	row, err := t.q.ReplaceSecretFieldFile(t.ctx, store.ReplaceSecretFieldFileParams{ID: f.ID, CompanyID: companyID,
		FileName: in.FileName, ContentType: in.ContentType, SizeBytes: size, Now: t.now})
	if err != nil {
		return SecretFieldView{}, mapErr(err, "replace secret field file")
	}
	if err := s.replaceFieldBlob(t, companyID, f.ID, []byte(sealed)); err != nil {
		return SecretFieldView{}, err
	}
	if err := t.activity(actor, companyID, "secret.field_updated", "secret", sec.ID, fieldEventData(row), ""); err != nil {
		return SecretFieldView{}, err
	}
	return SecretFieldView{SecretField: secretFieldFromRow(row)}, nil
}

// replaceFieldBlob grava o texto cifrado novo de um campo arquivo, na transação da troca. Com o driver postgres ativo,
// atualiza a linha e confere as linhas afetadas; sem linha (o campo foi criado em outro driver), cria. Com outro driver
// ativo, a linha do postgres sai na transação e a cópia do driver ativo é sobrescrita. Depois do commit, as cópias nos
// outros drivers saem também, para o texto cifrado antigo não ficar guardado. Um driver fora da transação não volta
// atrás se a transação falhar.
func (s *Service) replaceFieldBlob(t *txc, companyID, fieldID domain.ID, sealed []byte) error {
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		n, err := t.q.ReplaceSecretFieldBlob(t.ctx, store.ReplaceSecretFieldBlobParams{FieldID: fieldID, CompanyID: companyID, Content: sealed})
		if err != nil {
			return mapErr(err, "replace secret field blob")
		}
		if n == 0 {
			if err := t.q.CreateSecretFieldBlob(t.ctx, store.CreateSecretFieldBlobParams{FieldID: fieldID, CompanyID: companyID, Content: sealed}); err != nil {
				return mapErr(err, "create secret field blob")
			}
		}
		t.dropOtherFieldBlobsAfterCommit(companyID, fieldID, storage.DriverPostgres)
		return nil
	}
	if err := t.q.DeleteSecretFieldBlob(t.ctx, fieldID); err != nil {
		return mapErr(err, "delete secret field blob")
	}
	ctx := t.ctx
	if drv.Transactional() {
		ctx = storage.WithTx(t.ctx, t.q)
	}
	if err := drv.Put(ctx, fieldBlobKey(companyID, fieldID), sealed, "application/octet-stream"); err != nil {
		return fmt.Errorf("gravar arquivo do campo: %w", err)
	}
	t.dropOtherFieldBlobsAfterCommit(companyID, fieldID, drv.Name())
	return nil
}

// readFieldBlob devolve o texto cifrado de um campo arquivo; quem chama decifra. O conteúdo vive no lugar do driver
// ativo; se ele não tem (migração entre drivers em andamento, ADR-0051 §4), procura na tabela do postgres e nos demais
// drivers registrados antes de declarar que o arquivo não existe.
func (s *Service) readFieldBlob(ctx context.Context, q *store.Queries, companyID, fieldID domain.ID) ([]byte, error) {
	k := fieldBlobKey(companyID, fieldID)
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		b, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: companyID, FieldID: fieldID})
		switch {
		case err == nil:
			return b, nil
		case !isNoRows(err):
			return nil, mapErr(err, "secret field blob")
		}
	} else if b, err := drv.Get(ctx, k); err == nil {
		return b, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}
	if drv.Name() != storage.DriverPostgres {
		if b, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: companyID, FieldID: fieldID}); err == nil {
			return b, nil
		} else if !isNoRows(err) {
			return nil, mapErr(err, "secret field blob")
		}
	}
	for _, name := range s.blobs().Names() {
		if name == drv.Name() || name == storage.DriverPostgres {
			continue
		}
		d, err := s.blobs().Driver(name)
		if err != nil {
			return nil, err
		}
		if b, err := d.Get(ctx, k); err == nil {
			return b, nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
	}
	return nil, notFound("arquivo do campo")
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

// rekeyFieldBatch é o tamanho da página do rekey dos arquivos de campo.
const rekeyFieldBatch = 100

// rekeyFieldBlobs recifra os arquivos de campo com a chave atual: os das linhas do postgres e os do BlobStore dos
// demais drivers. Sem isso, trocar HANKO_SECRET_KEY deixaria os arquivos ilegíveis. Os campos vêm em páginas por id e o
// conteúdo de cada um entra e sai de memória um por vez, nunca o de todos de uma vez (achado 6 da auditoria do PR #80).
func (s *Service) rekeyFieldBlobs(ctx context.Context) (int, error) {
	q, w := s.read(), store.New(s.Pool)
	drv := s.blobs().Active()
	pg := drv.Name() == storage.DriverPostgres
	get := func(f store.ListSecretFileFieldsAfterRow) (string, error) {
		if pg {
			b, err := q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: f.CompanyID, FieldID: f.ID})
			if isNoRows(err) {
				return "", nil // campo arquivo sem linha de blob nesta gravação
			}
			if err != nil {
				return "", fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
			}
			return string(b), nil
		}
		b, err := drv.Get(ctx, fieldBlobKey(f.CompanyID, f.ID))
		if errors.Is(err, storage.ErrNotFound) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
		}
		return string(b), nil
	}
	save := func(f store.ListSecretFileFieldsAfterRow, enc string) error {
		if pg {
			return w.SetSecretFieldBlob(ctx, store.SetSecretFieldBlobParams{FieldID: f.ID, Content: []byte(enc)})
		}
		return drv.Put(ctx, fieldBlobKey(f.CompanyID, f.ID), []byte(enc), "application/octet-stream")
	}
	n := 0
	after := uuid.Nil
	for {
		fields, err := q.ListSecretFileFieldsAfter(ctx, store.ListSecretFileFieldsAfterParams{AfterID: after, Lim: rekeyFieldBatch})
		if err != nil {
			return n, fmt.Errorf("list secret file fields: %w", err)
		}
		if len(fields) == 0 {
			return n, nil
		}
		for _, f := range fields {
			after = f.ID
			enc, err := get(f)
			if err != nil {
				return n, err
			}
			if enc == "" {
				continue
			}
			if !s.secretBox().NeedsReseal(enc) {
				continue
			}
			plain, err := s.secretBox().Open(enc, fieldAAD(f.SecretID, f.ID))
			if err != nil {
				return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
			}
			out, err := s.secretBox().Seal(plain, fieldAAD(f.SecretID, f.ID))
			if err != nil {
				return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
			}
			if err := save(f, out); err != nil {
				return n, fmt.Errorf("arquivo do campo %s: %w", f.ID, err)
			}
			n++
		}
	}
}
