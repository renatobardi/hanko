package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Rascunhos no servidor (ADR-0052): por usuário + empresa + alvo (task ou conversa). Sobrevive à troca de dispositivo
// e é apagado quando o texto é enviado, na mesma transação do envio. Não é fato de atividade (é ruído de edição).

// localDraftUser é o dono dos rascunhos quando não há usuário no contexto (modo trusted).
const localDraftUser = "local"

// draftOwner é a chave do dono dos rascunhos da chamada: o usuário da sessão ou da API key, ou "local".
func draftOwner(ctx context.Context) string {
	if id, ok := auth.IdentityFrom(ctx); ok {
		return id.User.ID.String()
	}
	return localDraftUser
}

// GetDraft devolve o rascunho do chamador para o alvo; sem rascunho, um Draft vazio (Exists() falso). Member.
func (s *Service) GetDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID) (domain.Draft, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.Draft{}, err
	}
	if !target.Valid() {
		return domain.Draft{}, domain.Invalid("target_type", fmt.Sprintf("unknown target %q", target))
	}
	q := s.read()
	if err := checkDraftTarget(ctx, q, companyID, target, targetID); err != nil {
		return domain.Draft{}, err
	}
	empty := domain.Draft{CompanyID: companyID, TargetType: target, TargetID: targetID}
	var row store.Draft
	var err error
	if target == domain.DraftTask {
		row, err = q.GetTaskDraft(ctx, store.GetTaskDraftParams{CompanyID: companyID, UserKey: draftOwner(ctx), TaskID: &targetID})
	} else {
		row, err = q.GetConversationDraft(ctx, store.GetConversationDraftParams{CompanyID: companyID, UserKey: draftOwner(ctx), ConversationID: &targetID})
	}
	if isNoRows(err) {
		return empty, nil
	}
	if err != nil {
		return domain.Draft{}, fmt.Errorf("get draft: %w", err)
	}
	empty.Body, empty.UpdatedAt = row.Body, row.UpdatedAt
	return empty, nil
}

func checkDraftTarget(ctx context.Context, q *store.Queries, companyID domain.ID, target domain.DraftTarget, id domain.ID) error {
	var err error
	if target == domain.DraftTask {
		_, err = q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: id})
	} else {
		_, err = q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: id})
	}
	if err != nil {
		return mapErr(err, string(target))
	}
	return nil
}

// SaveDraft grava (ou troca) o rascunho do chamador para o alvo. Um texto em branco apaga o rascunho. Member.
func (s *Service) SaveDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID, body string) (domain.Draft, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.Draft{}, err
	}
	if !target.Valid() {
		return domain.Draft{}, domain.Invalid("target_type", fmt.Sprintf("unknown target %q", target))
	}
	if !utf8.ValidString(body) || utf8.RuneCountInString(body) > domain.MaxDraftRunes {
		return domain.Draft{}, domain.Invalid("body", fmt.Sprintf("must be valid UTF-8 with at most %d characters", domain.MaxDraftRunes))
	}
	if strings.TrimSpace(body) == "" {
		return domain.Draft{CompanyID: companyID, TargetType: target, TargetID: targetID}, s.DeleteDraft(ctx, companyID, target, targetID)
	}
	out := domain.Draft{CompanyID: companyID, TargetType: target, TargetID: targetID}
	err := s.withTx(ctx, func(t *txc) error {
		if err := checkDraftTarget(t.ctx, t.q, companyID, target, targetID); err != nil {
			return err
		}
		var row store.Draft
		var err error
		if target == domain.DraftTask {
			row, err = t.q.UpsertTaskDraft(t.ctx, store.UpsertTaskDraftParams{ID: domain.NewID(), CompanyID: companyID,
				UserKey: draftOwner(ctx), TaskID: &targetID, Body: body, Now: t.now})
		} else {
			row, err = t.q.UpsertConversationDraft(t.ctx, store.UpsertConversationDraftParams{ID: domain.NewID(), CompanyID: companyID,
				UserKey: draftOwner(ctx), ConversationID: &targetID, Body: body, Now: t.now})
		}
		if err != nil {
			return mapErr(err, "save draft")
		}
		out.Body, out.UpdatedAt = row.Body, row.UpdatedAt
		return nil
	})
	return out, err
}

// DeleteDraft apaga o rascunho do chamador para o alvo (não existir não é erro). Member.
func (s *Service) DeleteDraft(ctx context.Context, companyID domain.ID, target domain.DraftTarget, targetID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	if !target.Valid() {
		return domain.Invalid("target_type", fmt.Sprintf("unknown target %q", target))
	}
	return s.withTx(ctx, func(t *txc) error { return t.clearDraft(companyID, target, targetID) })
}

// clearDraft apaga o rascunho do usuário da chamada para o alvo, na transação corrente. É a regra "limpa ao enviar":
// quem envia o comentário ou a mensagem chama isto na mesma transação do envio. Ator que não é humano não tem rascunho.
func (t *txc) clearDraft(companyID domain.ID, target domain.DraftTarget, targetID domain.ID) error {
	owner := draftOwner(t.ctx)
	var err error
	if target == domain.DraftTask {
		err = t.q.DeleteTaskDraft(t.ctx, store.DeleteTaskDraftParams{CompanyID: companyID, UserKey: owner, TaskID: &targetID})
	} else {
		err = t.q.DeleteConversationDraft(t.ctx, store.DeleteConversationDraftParams{CompanyID: companyID, UserKey: owner, ConversationID: &targetID})
	}
	if err != nil {
		return fmt.Errorf("clear draft: %w", err)
	}
	return nil
}
