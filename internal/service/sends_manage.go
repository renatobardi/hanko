package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
)

// Gerir os envios (ADR-0069, fatia 4, #59): listar, ver, apagar e subir o conteúdo de um envio de arquivo. Quem vê um
// envio: o admin vê todos da empresa; um membro, só os que criou. Quem sobe o conteúdo de um envio de arquivo é quem o
// criou. O conteúdo chega cifrado do cliente: o servidor guarda o blob como veio.

// MaxSendUploadBytes é o tamanho máximo do blob cifrado de um envio de arquivo: o arquivo em claro (25 MiB) mais a
// cifra AES-GCM.
const MaxSendUploadBytes = domain.MaxSendFileBytes + sendCipherOverhead

// sendVisibleTo diz quais envios o ator alcança: nil, todos (admin e dono da instalação); senão, só os que ele criou.
func (s *Service) sendVisibleTo(ctx context.Context, companyID domain.ID, actor domain.Actor) (*uuid.UUID, error) {
	role, err := s.callerRoleOf(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if role.AtLeast(domain.MemberAdmin) {
		return nil, nil
	}
	if actor.ID == nil {
		return nil, domain.ErrForbidden
	}
	return actor.ID, nil
}

// sendVisibleRow confere o alcance de uma linha: visible nil vê tudo; senão, só o envio criado por quem pede.
func sendVisibleRow(row store.SecretSend, visible *uuid.UUID) bool {
	return visible == nil || (row.CreatedByID != nil && *row.CreatedByID == *visible)
}

// ListSends lista os envios que o ator alcança, do mais novo para o mais antigo. limit é o teto (1 a 500).
func (s *Service) ListSends(ctx context.Context, actor domain.Actor, companyID domain.ID, limit int) ([]SendView, error) {
	visible, err := s.sendVisibleTo(ctx, companyID, actor)
	if err != nil {
		return nil, err
	}
	rows, err := s.read().ListSecretSends(ctx, store.ListSecretSendsParams{CompanyID: companyID, CreatedByID: visible,
		Lim: int32(min(max(limit, 1), 500))}) //nolint:gosec // limitado a 500
	if err != nil {
		return nil, mapErr(err, "listar envios")
	}
	out := make([]SendView, 0, len(rows))
	for _, r := range rows {
		out = append(out, SendView{SecretSend: sendFromRow(r)})
	}
	return out, nil
}

// GetSend devolve os metadados de um envio que o ator alcança. Fora do alcance, é "não encontrado" (não se diz que existe).
func (s *Service) GetSend(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (SendView, error) {
	visible, err := s.sendVisibleTo(ctx, companyID, actor)
	if err != nil {
		return SendView{}, err
	}
	row, err := s.read().GetSecretSend(ctx, store.GetSecretSendParams{CompanyID: companyID, ID: id})
	if err != nil {
		return SendView{}, mapErr(err, "envio")
	}
	if !sendVisibleRow(row, visible) {
		return SendView{}, notFound("envio")
	}
	return SendView{SecretSend: sendFromRow(row)}, nil
}

// DeleteSend apaga o conteúdo de um envio antes do prazo, pelo criador ou pelo admin. A linha fica como registro
// (purged_at) e o blob sai junto: na mesma transação no postgres, depois do commit nos demais drivers.
func (s *Service) DeleteSend(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	if actor.Type != domain.ActorHuman {
		return domain.ErrForbidden
	}
	visible, err := s.sendVisibleTo(ctx, companyID, actor)
	if err != nil {
		return err
	}
	row, err := s.read().GetSecretSend(ctx, store.GetSecretSendParams{CompanyID: companyID, ID: id})
	if err != nil {
		return mapErr(err, "envio")
	}
	if !sendVisibleRow(row, visible) {
		return notFound("envio")
	}
	return s.withTx(ctx, func(t *txc) error {
		now := t.now
		purged, err := t.q.PurgeSecretSend(t.ctx, store.PurgeSecretSendParams{Now: &now, CompanyID: companyID, ID: id})
		if isNoRows(err) {
			return ErrSendGone // já apagado: pelo prazo, pelo limite ou por outro pedido
		}
		if err != nil {
			return mapErr(err, "apagar envio")
		}
		if purged.OldBlobKey != "" {
			if err := t.dropSendBlob(companyID, id); err != nil {
				return err
			}
		}
		return t.activity(actor, companyID, "send.deleted", "secret_send", id, map[string]any{"kind": row.Kind}, "")
	})
}

// UploadSendContent grava o blob cifrado de um envio de arquivo. Quem sobe é quem criou o envio (e tem reautenticação
// recente, como na criação), e o blob tem que ter o tamanho declarado mais a cifra. Sobe de novo enquanto o envio não
// expirou: o blob novo troca o anterior.
func (s *Service) UploadSendContent(ctx context.Context, actor domain.Actor, companyID, id domain.ID, sealed []byte) (SendView, error) {
	if err := s.requireSendCreator(ctx, actor, companyID); err != nil {
		return SendView{}, err
	}
	var out SendView
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.GetSecretSend(t.ctx, store.GetSecretSendParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "envio")
		}
		if actor.ID == nil || row.CreatedByID == nil || *row.CreatedByID != *actor.ID {
			return notFound("envio") // quem não criou não fica sabendo que o envio existe
		}
		if row.Kind != string(domain.SendFile) {
			return domain.Invalid("kind", "só envio de arquivo recebe conteúdo em PUT …/content")
		}
		if row.PurgedAt != nil || !row.ExpiresAt.After(t.now) {
			return ErrSendGone
		}
		if want := int(row.SizeBytes) + sendCipherOverhead; len(sealed) != want {
			return domain.Invalid("content", fmt.Sprintf("o conteúdo cifrado tem %d bytes; o envio declarou %d (o arquivo mais %d de cifra)",
				len(sealed), row.SizeBytes, sendCipherOverhead))
		}
		if err := s.putSendBlob(t, companyID, id, sealed); err != nil {
			return err
		}
		out = SendView{SecretSend: sendFromRow(row)}
		return t.activity(actor, companyID, "send.uploaded", "secret_send", id, map[string]any{"size_bytes": int(row.SizeBytes)}, "")
	})
	return out, err
}

// sendBlobKey é a chave do blob de um envio de arquivo no BlobStore (fs e s3). storage.Key chama o id de artefato; aqui
// é o id do envio, único do mesmo jeito (como nos campos arquivo, vault_files.go).
func sendBlobKey(companyID, sendID domain.ID) storage.Key {
	return storage.Key{CompanyID: companyID, ArtifactID: sendID}
}

// putSendBlob grava o blob cifrado de um envio de arquivo na transação do upload. O postgres grava na tabela
// secret_send_blobs (migração 00206); fs e s3, no BlobStore (ADR-0051).
func (s *Service) putSendBlob(t *txc, companyID, sendID domain.ID, sealed []byte) error {
	drv := s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		if err := t.q.CreateSecretSendBlob(t.ctx, store.CreateSecretSendBlobParams{SendID: sendID, CompanyID: companyID, Content: sealed}); err != nil {
			return mapErr(err, "gravar blob do envio")
		}
		return nil
	}
	k := sendBlobKey(companyID, sendID)
	if drv.Transactional() {
		if err := drv.Put(storage.WithTx(t.ctx, t.q), k, sealed, "application/octet-stream"); err != nil {
			return fmt.Errorf("gravar arquivo do envio: %w", err)
		}
		return nil
	}
	if err := drv.Put(t.ctx, k, sealed, "application/octet-stream"); err != nil {
		return fmt.Errorf("gravar arquivo do envio: %w", err)
	}
	return nil
}

// dropSendBlob apaga o blob de um envio que saiu do ar. No postgres, a linha da tabela sai na mesma transação; nos demais
// drivers, o objeto some depois do commit, e uma falha só vira aviso no log.
func (t *txc) dropSendBlob(companyID, sendID domain.ID) error {
	drv := t.s.blobs().Active()
	if drv.Name() == storage.DriverPostgres {
		if _, err := t.q.DeleteSecretSendBlob(t.ctx, store.DeleteSecretSendBlobParams{CompanyID: companyID, SendID: sendID}); err != nil {
			return mapErr(err, "apagar blob do envio")
		}
		return nil
	}
	k := sendBlobKey(companyID, sendID)
	t.after = append(t.after, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := drv.Delete(ctx, k); err != nil && t.s.Log != nil {
			t.s.Log.Warn("cofre: apagar arquivo de envio", "send_id", sendID.String(), "err", err)
		}
	})
	return nil
}
