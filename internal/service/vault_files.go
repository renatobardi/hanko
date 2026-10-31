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
// cifrado é o que o driver guarda: postgres nas tabelas secret_field_blobs (o campo) e secret_field_version_blobs (a versão
// arquivada), fs e s3 no BlobStore (ADR-0051), com a chave da empresa e do id do campo ou da versão. O valor só volta em
// claro para quem lê o campo ou restaura a versão.

// fieldBlobKey é a chave do conteúdo de um campo arquivo no BlobStore. storage.Key chama o id de artefato; aqui é o id
// do campo, único do mesmo jeito.
func fieldBlobKey(companyID, fieldID domain.ID) storage.Key {
	return storage.Key{CompanyID: companyID, ArtifactID: fieldID}
}

// fieldVersionBlobKey é a chave do arquivo arquivado de uma versão de campo (ADR-0068 §3.1): o id da versão.
func fieldVersionBlobKey(companyID, versionID domain.ID) storage.Key {
	return storage.Key{CompanyID: companyID, ArtifactID: versionID}
}

// putCipherTx grava o texto cifrado de um campo ou de uma versão na transação que o cria. O postgres grava pela linha
// (pgPut); o driver ativo grava com a transação quando ele participa dela, ou antes do commit com compensação.
func (s *Service) putCipherTx(t *txc, k storage.Key, sealed []byte, pgPut func() error) error {
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		return pgPut()
	}
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

// putFieldBlob grava o texto cifrado de um campo arquivo na transação que cria o campo. Num driver que não participa da
// transação, o objeto é apagado se a transação falhar.
func (s *Service) putFieldBlob(t *txc, companyID, fieldID domain.ID, sealed []byte) error {
	return s.putCipherTx(t, fieldBlobKey(companyID, fieldID), sealed, func() error {
		return mapErr(t.q.CreateSecretFieldBlob(t.ctx, store.CreateSecretFieldBlobParams{FieldID: fieldID, CompanyID: companyID, Content: sealed}), "create secret field blob")
	})
}

// putVersionBlob grava o texto cifrado do arquivo arquivado de uma versão (ADR-0068 §3.1), na transação da troca.
func (s *Service) putVersionBlob(t *txc, companyID, versionID domain.ID, sealed []byte) error {
	return s.putCipherTx(t, fieldVersionBlobKey(companyID, versionID), sealed, func() error {
		return mapErr(t.q.CreateSecretFieldVersionBlob(t.ctx, store.CreateSecretFieldVersionBlobParams{VersionID: versionID, CompanyID: companyID, Content: sealed}), "create secret field version blob")
	})
}

// dropBlobCopiesAfterCommit apaga, depois do commit, as cópias do conteúdo de uma chave em todos os drivers registrados
// (no meio de uma migração entre drivers pode haver cópia em mais de um). O postgres fica de fora: a linha dele sai na
// cascata ou na própria transação. `keep` (vazio = nenhum) é o driver que não se apaga.
func (t *txc) dropBlobCopiesAfterCommit(k storage.Key, keep string) {
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
				t.s.Log.Warn("cofre: apagar arquivo", "id", k.ArtifactID.String(), "err", err)
			}
		})
	}
}

// dropFieldFiles agenda, depois do commit, o apagar do arquivo de um campo e dos arquivos arquivados das versões dele nos
// drivers de blob. A lista de versões se lê aqui, antes de o campo sair: a cascata do postgres não alcança fs e s3.
func (t *txc) dropFieldFiles(companyID, fieldID domain.ID) error {
	versions, err := t.q.ListSecretFieldVersions(t.ctx, store.ListSecretFieldVersionsParams{CompanyID: companyID, FieldID: fieldID})
	if err != nil {
		return mapErr(err, "list secret field versions")
	}
	t.dropBlobCopiesAfterCommit(fieldBlobKey(companyID, fieldID), "")
	t.dropVersionFiles(companyID, versions)
	return nil
}

// dropVersionFiles agenda o apagar dos arquivos arquivados das versões dadas (as que saem do histórico).
func (t *txc) dropVersionFiles(companyID domain.ID, versions []store.SecretFieldVersion) {
	for _, v := range versions {
		if v.BlobKey != "" {
			t.dropBlobCopiesAfterCommit(fieldVersionBlobKey(companyID, v.ID), "")
		}
	}
}

// createFileField cria um campo arquivo: confere o tamanho e os metadados, cifra o conteúdo com o AAD do campo, grava
// o texto cifrado no driver e guarda na linha só os metadados (ADR-0068 §5.1). Quem chama já tem a transação e o segredo.
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

// ReplaceSecretFieldFile troca o conteúdo de um campo arquivo pelo arquivo novo (#117, ADR-0068 §3.1). O arquivo anterior
// vai para o histórico: a versão sai de secret_field_versions com o arquivo arquivado, e a versão do campo sobe. O campo
// mantém o nome, o id e a marca de visibilidade. Troca, histórico e evento secret.field_updated (sem conteúdo) são da mesma
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

// replaceFileTx faz a troca dentro da transação: trava o campo, confere quem escreve e se é campo arquivo, arquiva a
// versão atual, cifra e grava o conteúdo novo, atualiza os metadados e registra o evento sem conteúdo. `in` já vem com
// nome e tipo conferidos.
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
	if err := s.archiveFileVersionTx(t, actor, companyID, sec, f); err != nil {
		return SecretFieldView{}, err
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

// archiveFileVersionTx guarda a versão atual de um campo arquivo no histórico (ADR-0068 §3.1): a linha de
// secret_field_versions com os metadados e o arquivo, cifrado de novo com o AAD da versão. Quem chama já tem a transação
// e o campo travado, e grava o conteúdo novo depois, porque o arquivo atual é lido aqui.
func (s *Service) archiveFileVersionTx(t *txc, actor domain.Actor, companyID domain.ID, sec store.Secret, f store.SecretField) error {
	cur, err := s.readFieldBlob(t.ctx, t.q, companyID, f.ID)
	if err != nil {
		return err
	}
	plain, err := s.secretBox().Open(string(cur), fieldAADFor(f.AadVersion, sec.ID, f.ID))
	if err != nil {
		return domain.Invalid("secrets", "não foi possível decifrar o arquivo anterior para o histórico: "+err.Error())
	}
	archived, err := s.secretBox().Seal(plain, fieldVersionAAD(sec.ID, f.ID, int(f.Version)))
	if err != nil {
		return fmt.Errorf("seal field version file: %w", err)
	}
	id := domain.NewID()
	if _, err := t.q.CreateSecretFieldVersion(t.ctx, store.CreateSecretFieldVersionParams{
		ID: id, CompanyID: companyID, FieldID: f.ID, Version: f.Version, Provider: string(domain.SecretProviderLocal),
		ValueEnc: "", ExternalRef: "", BlobKey: fieldVersionBlobKey(companyID, id).String(), FileName: f.FileName,
		ContentType: f.ContentType, SizeBytes: f.SizeBytes, AadVersion: 1, RevealedAt: f.RevealedAt,
		ReplacedByType: string(actor.Type), ReplacedByID: actor.ID, CreatedAt: f.RotatedAt, Now: t.now}); err != nil {
		return mapErr(err, "archive secret field file")
	}
	return s.putVersionBlob(t, companyID, id, []byte(archived))
}

// restoreFileVersionTx restaura uma versão de campo arquivo como versão NOVA (ADR-0068 §3.3): o arquivo atual vai para o
// histórico, e o arquivo da versão escolhida volta para o campo, recifrado com o AAD do campo. Nada é apagado.
func (s *Service) restoreFileVersionTx(t *txc, actor domain.Actor, companyID domain.ID, sec store.Secret, f store.SecretField, v store.SecretFieldVersion) (store.SecretField, error) {
	old, err := s.readFieldVersionBlob(t.ctx, t.q, companyID, v.ID)
	if err != nil {
		return store.SecretField{}, err
	}
	plain, err := s.secretBox().Open(string(old), fieldVersionAADFor(v.AadVersion, sec.ID, f.ID, int(v.Version)))
	if err != nil {
		return store.SecretField{}, domain.Invalid("secrets", "não foi possível decifrar a versão: "+err.Error())
	}
	sealed, err := s.secretBox().Seal(plain, fieldAAD(sec.ID, f.ID))
	if err != nil {
		return store.SecretField{}, fmt.Errorf("cifrar arquivo: %w", err)
	}
	if err := s.archiveFileVersionTx(t, actor, companyID, sec, f); err != nil {
		return store.SecretField{}, err
	}
	row, err := t.q.ReplaceSecretFieldFile(t.ctx, store.ReplaceSecretFieldFileParams{ID: f.ID, CompanyID: companyID,
		FileName: v.FileName, ContentType: v.ContentType, SizeBytes: v.SizeBytes, Now: t.now})
	if err != nil {
		return store.SecretField{}, mapErr(err, "restore secret field file")
	}
	if err := s.replaceFieldBlob(t, companyID, f.ID, []byte(sealed)); err != nil {
		return store.SecretField{}, err
	}
	return row, nil
}

// replaceFieldBlob grava o texto cifrado novo de um campo arquivo, na transação da troca. Com o driver postgres ativo,
// atualiza a linha e confere as linhas afetadas; sem linha (o campo foi criado em outro driver), cria. Com outro driver
// ativo, a linha do postgres sai na transação e a cópia do driver ativo é sobrescrita. Depois do commit, as cópias nos
// outros drivers saem também, para o texto cifrado antigo não ficar guardado. Um driver fora da transação não volta
// atrás se a transação falhar. As versões arquivadas não são tocadas: têm chave própria.
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
		t.dropBlobCopiesAfterCommit(fieldBlobKey(companyID, fieldID), storage.DriverPostgres)
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
	t.dropBlobCopiesAfterCommit(fieldBlobKey(companyID, fieldID), drv.Name())
	return nil
}

// readFieldBlob devolve o texto cifrado de um campo arquivo; quem chama decifra.
func (s *Service) readFieldBlob(ctx context.Context, q *store.Queries, companyID, fieldID domain.ID) ([]byte, error) {
	return s.readCipherBlob(ctx, fieldBlobKey(companyID, fieldID), func() ([]byte, error) {
		return q.GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: companyID, FieldID: fieldID})
	}, "arquivo do campo")
}

// readFieldVersionBlob devolve o texto cifrado do arquivo arquivado de uma versão; quem chama decifra.
func (s *Service) readFieldVersionBlob(ctx context.Context, q *store.Queries, companyID, versionID domain.ID) ([]byte, error) {
	return s.readCipherBlob(ctx, fieldVersionBlobKey(companyID, versionID), func() ([]byte, error) {
		return q.GetSecretFieldVersionBlob(ctx, store.GetSecretFieldVersionBlobParams{CompanyID: companyID, VersionID: versionID})
	}, "arquivo da versão")
}

// readCipherBlob lê o texto cifrado de um campo ou de uma versão: o lugar do driver ativo primeiro; depois a tabela do
// postgres (se não é o ativo) e os demais drivers registrados, porque uma migração entre drivers pode estar no meio
// (ADR-0051 §4). pgGet lê a linha do postgres; sem linha, devolve ErrNoRows. what nomeia o conteúdo na falha.
func (s *Service) readCipherBlob(ctx context.Context, k storage.Key, pgGet func() ([]byte, error), what string) ([]byte, error) {
	drv := s.blobs().Active()
	b, ok, err := s.readCipherActive(ctx, k, drv, pgGet)
	if err != nil || ok {
		return b, err
	}
	if drv.Name() != storage.DriverPostgres {
		if b, ok, err := pgRow(pgGet); err != nil || ok {
			return b, err
		}
	}
	return s.readCipherElsewhere(ctx, k, drv.Name(), what)
}

// readCipherActive lê o conteúdo no driver ativo; ok false quando ele não o tem.
func (s *Service) readCipherActive(ctx context.Context, k storage.Key, drv storage.BlobStore, pgGet func() ([]byte, error)) ([]byte, bool, error) {
	if drv.Name() == storage.DriverPostgres {
		return pgRow(pgGet)
	}
	b, err := drv.Get(ctx, k)
	switch {
	case err == nil:
		return b, true, nil
	case errors.Is(err, storage.ErrNotFound):
		return nil, false, nil
	default:
		return nil, false, err
	}
}

// readCipherElsewhere procura o conteúdo nos drivers registrados que não são o ativo nem o postgres.
func (s *Service) readCipherElsewhere(ctx context.Context, k storage.Key, active, what string) ([]byte, error) {
	for _, name := range s.blobs().Names() {
		if name == active || name == storage.DriverPostgres {
			continue
		}
		d, err := s.blobs().Driver(name)
		if err != nil {
			return nil, err
		}
		b, err := d.Get(ctx, k)
		switch {
		case err == nil:
			return b, nil
		case !errors.Is(err, storage.ErrNotFound):
			return nil, err
		}
	}
	return nil, notFound(what)
}

// pgRow lê a linha do postgres de um conteúdo cifrado: ok false quando a linha não existe.
func pgRow(get func() ([]byte, error)) ([]byte, bool, error) {
	b, err := get()
	switch {
	case err == nil:
		return b, true, nil
	case isNoRows(err):
		return nil, false, nil
	default:
		return nil, false, mapErr(err, "ler arquivo")
	}
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

// rekeyFieldBatch é o tamanho da página do rekey dos arquivos de campo e de versão.
const rekeyFieldBatch = 100

// cipherRow é um texto cifrado de campo ou de versão a reescrever com a chave atual: a chave (o id do campo ou da versão,
// no BlobStore ou na linha do postgres), se é versão, e o AAD que o amarra.
type cipherRow struct {
	key     storage.Key
	version bool
	aad     string
}

// rekeyFieldBlobs recifra os arquivos de campo e as versões arquivadas com a chave atual: as linhas do postgres e o
// BlobStore dos demais drivers. Sem isso, trocar HANKO_SECRET_KEY deixaria os arquivos ilegíveis. As listas vêm em
// páginas por id e o conteúdo de cada um entra e sai de memória um por vez, nunca o de todos de uma vez (achado 6 da
// auditoria do PR #80).
func (s *Service) rekeyFieldBlobs(ctx context.Context) (int, error) {
	q := s.read()
	n, err := s.rekeyCipherRows(ctx, func(ctx context.Context, after uuid.UUID) ([]cipherRow, error) {
		fields, err := q.ListSecretFileFieldsAfter(ctx, store.ListSecretFileFieldsAfterParams{AfterID: after, Lim: rekeyFieldBatch})
		if err != nil {
			return nil, fmt.Errorf("list secret file fields: %w", err)
		}
		rows := make([]cipherRow, len(fields))
		for i, f := range fields {
			rows[i] = cipherRow{key: storage.Key{CompanyID: f.CompanyID, ArtifactID: f.ID}, aad: fieldAAD(f.SecretID, f.ID)}
		}
		return rows, nil
	})
	if err != nil {
		return n, err
	}
	m, err := s.rekeyCipherRows(ctx, func(ctx context.Context, after uuid.UUID) ([]cipherRow, error) {
		versions, err := q.ListSecretFileVersionsAfter(ctx, store.ListSecretFileVersionsAfterParams{AfterID: after, Lim: rekeyFieldBatch})
		if err != nil {
			return nil, fmt.Errorf("list secret file versions: %w", err)
		}
		rows := make([]cipherRow, len(versions))
		for i, v := range versions {
			rows[i] = cipherRow{key: storage.Key{CompanyID: v.CompanyID, ArtifactID: v.ID}, version: true,
				aad: fieldVersionAADFor(v.AadVersion, v.SecretID, v.FieldID, int(v.Version))}
		}
		return rows, nil
	})
	return n + m, err
}

// rekeyCipherRows percorre as páginas de `next` (cada uma começa depois do último id da anterior) e recifra as linhas
// cifradas com a chave atual. Devolve quantas foram reescritas.
func (s *Service) rekeyCipherRows(ctx context.Context, next func(context.Context, uuid.UUID) ([]cipherRow, error)) (int, error) {
	n := 0
	after := uuid.Nil
	for {
		rows, err := next(ctx, after)
		if err != nil {
			return n, err
		}
		if len(rows) == 0 {
			return n, nil
		}
		for _, r := range rows {
			after = r.key.ArtifactID
			changed, err := s.rekeyCipherRow(ctx, r)
			if err != nil {
				return n, fmt.Errorf("arquivo do campo %s: %w", r.key.ArtifactID, err)
			}
			if changed {
				n++
			}
		}
	}
}

// rekeyCipherRow recifra uma linha cifrada que não está com a chave atual. Sem conteúdo no lugar, não faz nada.
func (s *Service) rekeyCipherRow(ctx context.Context, r cipherRow) (bool, error) {
	enc, err := s.getCipherRow(ctx, r)
	if err != nil || enc == "" {
		return false, err
	}
	if !s.secretBox().NeedsReseal(enc) {
		return false, nil
	}
	plain, err := s.secretBox().Open(enc, r.aad)
	if err != nil {
		return false, err
	}
	out, err := s.secretBox().Seal(plain, r.aad)
	if err != nil {
		return false, err
	}
	return true, s.saveCipherRow(ctx, r, out)
}

// getCipherRow lê o texto cifrado de uma linha no lugar do driver ativo; "" quando não há conteúdo.
func (s *Service) getCipherRow(ctx context.Context, r cipherRow) (string, error) {
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		var b []byte
		var err error
		if r.version {
			b, err = s.read().GetSecretFieldVersionBlob(ctx, store.GetSecretFieldVersionBlobParams{CompanyID: r.key.CompanyID, VersionID: r.key.ArtifactID})
		} else {
			b, err = s.read().GetSecretFieldBlob(ctx, store.GetSecretFieldBlobParams{CompanyID: r.key.CompanyID, FieldID: r.key.ArtifactID})
		}
		if isNoRows(err) {
			return "", nil // sem linha de blob nesta gravação
		}
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	b, err := drv.Get(ctx, r.key)
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// saveCipherRow grava o texto recifrado no lugar do driver ativo.
func (s *Service) saveCipherRow(ctx context.Context, r cipherRow, enc string) error {
	drv := s.blobs().Active()
	if drv.Name() != storage.DriverPostgres {
		return drv.Put(ctx, r.key, []byte(enc), "application/octet-stream")
	}
	w := store.New(s.Pool)
	if r.version {
		return w.SetSecretFieldVersionBlob(ctx, store.SetSecretFieldVersionBlobParams{VersionID: r.key.ArtifactID, Content: []byte(enc)})
	}
	return w.SetSecretFieldBlob(ctx, store.SetSecretFieldBlobParams{FieldID: r.key.ArtifactID, Content: []byte(enc)})
}
