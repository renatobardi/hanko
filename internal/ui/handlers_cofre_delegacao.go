package ui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Configurações › Segredos › delegação de um segredo pessoal (ADR-0068 §6, #117 fatia 2). Só o dono delega, lista e
// revoga, e só para task, conversa ou run que ele pediu. A tela mostra alvo, prazo e estado, nunca o valor. Criar e
// revogar não pedem reautenticação: o service não a exige para delegações.

// delegationPageLimit é quantas delegações do usuário a tela lê; a lista é filtrada pelo nome do segredo.
const delegationPageLimit = 100

// delegationPath é o caminho da tela de campos de um segredo pessoal, com o escopo do usuário.
func delegationPath(co domain.Company, name string) string {
	return "/c/" + co.Slug + "/configuracoes/segredos/" + url.PathEscape(name) + "/campos" + scopeQuery(true)
}

// delegationRows monta as delegações do usuário para este segredo; delegações de outros segredos ficam de fora.
func delegationRows(ctx context.Context, co domain.Company, be vaultUIBackend, name string) ([]views.DelegationRowVM, error) {
	list, err := be.ListMyDelegations(ctx, co.ID, delegationPageLimit)
	if err != nil {
		return nil, err
	}
	loc := loadLoc(co.Timezone)
	base := "/c/" + co.Slug + "/configuracoes/segredos/" + url.PathEscape(name) + "/delegacoes/"
	var rows []views.DelegationRowVM
	for _, d := range list {
		if d.Secret != name {
			continue
		}
		rows = append(rows, views.DelegationRowVM{ID: d.ID.String(), Target: string(d.TargetType) + " " + d.TargetID.String(),
			ExpiresAt: fmtDateTime(ctx, d.ExpiresAt, loc), State: delegationStateText(ctx, d), Active: d.Active && d.RevokedAt == nil,
			RevokeHref: base + url.PathEscape(d.ID.String()) + "/revogar" + scopeQuery(true)})
	}
	return rows, nil
}

// delegationStateText diz em uma palavra se a delegação vale agora: revogada, ativa ou inativa (vencida ou com o trabalho fechado).
func delegationStateText(ctx context.Context, d service.DelegationView) string {
	switch {
	case d.RevokedAt != nil:
		return i18n.T(ctx, "revogada")
	case d.Active:
		return i18n.T(ctx, "ativa")
	default:
		return i18n.T(ctx, "inativa")
	}
}

// delegationTTL lê o prazo do formulário em segundos. Vazio, negativo ou acima de 7 dias vira erro de validação
// antes de chegar ao service.
func delegationTTL(raw string) (time.Duration, error) {
	sec, err := strconv.Atoi(raw)
	if err != nil || sec < 0 || sec > int(domain.MaxDelegationTTL/time.Second) {
		return 0, domain.Invalid("expires_in", "prazo inválido")
	}
	return time.Duration(sec) * time.Second, nil
}

// handleDelegate delega o segredo pessoal do dono à task, conversa ou run que ele pediu.
func (s *server) handleDelegate(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	typ, target := trimForm(r, "alvo_tipo"), trimForm(r, "alvo")
	fail := func(err error) {
		s.fieldsFail(w, r, co, be, err, func(vm *views.SecretFieldsVM) {
			vm.DelegateType, vm.DelegateTarget = typ, target // o id do alvo não é segredo
		})
	}
	ttl, err := delegationTTL(r.PostFormValue("prazo"))
	if err != nil {
		fail(err)
		return
	}
	id, err := domain.ParseID(target)
	if err != nil {
		fail(domain.Invalid("target_id", "não é um uuid"))
		return
	}
	_, err = be.CreateSecretDelegation(ctx, actorFrom(ctx), co.ID, name, service.CreateDelegationInput{
		TargetType: domain.DelegationTarget(typ), TargetID: id, TTL: ttl})
	if err != nil {
		fail(err)
		return
	}
	redirect(w, r, delegationPath(co, name)+"&ok=delegacao")
}

// handleRevokeDelegation revoga uma delegação do usuário; a run que a usava deixa de resolver o segredo.
func (s *server) handleRevokeDelegation(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ctx, ok := s.vaultReq(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		s.fieldsFail(w, r, co, be, domain.Invalid("id", "não é um uuid"), nil)
		return
	}
	if err := be.RevokeSecretDelegation(ctx, actorFrom(ctx), co.ID, id); err != nil {
		s.fieldsFail(w, r, co, be, err, nil)
		return
	}
	redirect(w, r, delegationPath(co, name)+"&ok=delegacao-revogada")
}
