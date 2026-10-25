package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Delegação de segredo pessoal às runs de um alvo (ADR-0068 §6). O dono de um segredo de usuário concede o uso a uma
// task ou conversa que ele mesmo pediu (ou a uma run em curso dela). Sem delegação válida, a run não resolve
// ${secret:user/...} nem lê o item com hanko_secret_get.

// UserSecretPrefix abre o item pessoal na tool hanko_secret_get (`user/NOME`), como em ${secret:user/NOME}.
const UserSecretPrefix = "user/"

// CreateDelegationInput delega um segredo pessoal ao alvo. TTL zero vale o padrão (24 h).
type CreateDelegationInput struct {
	TargetType domain.DelegationTarget
	TargetID   domain.ID
	TTL        time.Duration
}

// DelegationView é uma delegação como o dono a vê (nome do segredo, nunca valor).
type DelegationView struct {
	ID         domain.ID
	Secret     string
	TargetType domain.DelegationTarget
	TargetID   domain.ID
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	Active     bool
	CreatedAt  time.Time
}

// SecretAccessView é um uso registrado de um segredo: onde e quando, nunca o valor.
type SecretAccessView struct {
	Field      string
	Via        domain.SecretAccessVia
	RunID      *domain.ID
	Persona    string
	Delegated  bool
	AccessedAt time.Time
}

// MemberExitReport é o que a saída de um humano da empresa apagou (ADR-0068 §6.5).
type MemberExitReport struct {
	UserSecrets int

	// Delegations são as delegações ativas que caíram junto com os itens.
	Delegations []DelegationView
}

// callerOwner devolve o usuário autenticado como dono de segredo pessoal. Sem identidade (modo trusted) não há dono.
func (s *Service) callerOwner(ctx context.Context, companyID domain.ID, actor domain.Actor) (domain.ID, error) {
	if actor.Type == domain.ActorPersona {
		return domain.ID{}, domain.ErrForbidden // um agente nunca delega segredo
	}
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.ID{}, err
	}
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.ID{}, domain.Invalid("scope", "segredos de usuário exigem uma pessoa autenticada")
	}
	return id.User.ID, nil
}

// targetOwner devolve quem pediu o trabalho: o autor humano da task ou da conversa. Nil quando não há um humano
// identificado (task criada por persona ou pelo modo trusted).
func targetOwner(ctx context.Context, q *store.Queries, companyID domain.ID, kind domain.DelegationTarget, id domain.ID) (*uuid.UUID, error) {
	switch kind {
	case domain.DelegationTask:
		tk, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: id})
		if err != nil {
			return nil, mapErr(err, "task")
		}
		return humanCreator(tk.CreatedByType, tk.CreatedByID), nil
	case domain.DelegationConversation:
		cv, err := q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: id})
		if err != nil {
			return nil, mapErr(err, "conversa")
		}
		return humanCreator(cv.CreatedByType, cv.CreatedByID), nil
	case domain.DelegationRun:
		run, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: id})
		if err != nil {
			return nil, mapErr(err, "run")
		}
		return runOwner(ctx, q, companyID, run)
	}
	return nil, domain.Invalid("target_type", "alvo desconhecido")
}

func humanCreator(kind string, id *uuid.UUID) *uuid.UUID {
	if kind != string(domain.ActorHuman) {
		return nil
	}
	return id
}

// runOwner é quem pediu o trabalho da run: o autor da task ou o humano da conversa.
func runOwner(ctx context.Context, q *store.Queries, companyID domain.ID, run store.Run) (*uuid.UUID, error) {
	switch {
	case run.TaskID != nil:
		return targetOwner(ctx, q, companyID, domain.DelegationTask, *run.TaskID)
	case run.ConversationID != nil:
		return targetOwner(ctx, q, companyID, domain.DelegationConversation, *run.ConversationID)
	}
	return nil, nil
}

// targetOpen diz se o trabalho que a run atende segue aberto: task não fechada (nem cancelada) ou conversa não arquivada.
// Run sem task nem conversa não tem trabalho aberto.
func targetOpen(ctx context.Context, q *store.Queries, companyID domain.ID, run store.Run) (bool, error) {
	switch {
	case run.TaskID != nil:
		tk, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *run.TaskID})
		if err != nil {
			return false, mapErr(err, "task")
		}
		return domain.TaskStatus(tk.Status).Open(), nil
	case run.ConversationID != nil:
		cv, err := q.GetConversation(ctx, store.GetConversationParams{CompanyID: companyID, ID: *run.ConversationID})
		if err != nil {
			return false, mapErr(err, "conversa")
		}
		return domain.ConversationStatus(cv.Status) == domain.ConversationOpen, nil
	}
	return false, nil
}

// CreateSecretDelegation delega o segredo pessoal `name` do chamador às runs do alvo. O alvo tem de ser trabalho que
// o próprio dono pediu; uma delegação a uma run exige a run em curso.
func (s *Service) CreateSecretDelegation(ctx context.Context, actor domain.Actor, companyID domain.ID, name string, in CreateDelegationInput) (DelegationView, error) {
	owner, err := s.callerOwner(ctx, companyID, actor)
	if err != nil {
		return DelegationView{}, err
	}
	if !in.TargetType.Valid() {
		return DelegationView{}, domain.Invalid("target_type", "use task, conversation ou run")
	}
	var out DelegationView
	err = s.withTx(ctx, func(t *txc) error {
		sec, err := t.q.GetUserSecretByName(t.ctx, store.GetUserSecretByNameParams{CompanyID: companyID, OwnerUserID: &owner, Name: name})
		if err != nil {
			return mapErr(err, "segredo "+name)
		}
		want, err := targetOwner(t.ctx, t.q, companyID, in.TargetType, in.TargetID)
		if err != nil {
			return err
		}
		if want == nil || *want != owner {
			return fmt.Errorf("o alvo não é um trabalho que você pediu: %w", domain.ErrForbidden)
		}
		if in.TargetType == domain.DelegationRun {
			run, err := t.q.GetRunByID(t.ctx, store.GetRunByIDParams{CompanyID: companyID, ID: in.TargetID})
			if err != nil {
				return mapErr(err, "run")
			}
			if !domain.RunStatus(run.Status).Active() {
				return domain.Invalid("target_id", "a run não está em curso")
			}
		}
		expires, err := domain.DelegationExpiry(t.now, in.TTL)
		if err != nil {
			return err
		}
		row, err := t.q.CreateSecretDelegation(t.ctx, store.CreateSecretDelegationParams{ID: domain.NewID(), CompanyID: companyID,
			OwnerUserID: owner, SecretID: sec.ID, TargetType: string(in.TargetType), TargetID: in.TargetID, ExpiresAt: expires, Now: t.now})
		if err != nil {
			return mapErr(err, "delegação")
		}
		out = delegationView(row, name, t.now)
		return t.activity(actor, companyID, "delegation.created", "secret", sec.ID,
			map[string]any{"delegation_id": row.ID, "target_type": in.TargetType, "target_id": in.TargetID, "expires_at": expires}, "")
	})
	return out, err
}

func delegationView(d store.SecretDelegation, secret string, now time.Time) DelegationView {
	dom := domain.SecretDelegation{ExpiresAt: d.ExpiresAt, RevokedAt: d.RevokedAt}
	return DelegationView{ID: d.ID, Secret: secret, TargetType: domain.DelegationTarget(d.TargetType), TargetID: d.TargetID,
		ExpiresAt: d.ExpiresAt, RevokedAt: d.RevokedAt, Active: dom.Active(now), CreatedAt: d.CreatedAt}
}

// ListMyDelegations lista as delegações do chamador (só as dele), das mais novas para as mais antigas.
func (s *Service) ListMyDelegations(ctx context.Context, companyID domain.ID, limit int) ([]DelegationView, error) {
	owner, err := s.callerOwner(ctx, companyID, domain.HumanActor)
	if err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListUserDelegations(ctx, store.ListUserDelegationsParams{CompanyID: companyID, OwnerUserID: owner, Lim: int32(min(max(limit, 1), 500))}) //nolint:gosec // limitado a 500
	if err != nil {
		return nil, fmt.Errorf("listar delegações: %w", err)
	}
	return s.delegationViews(ctx, q, companyID, owner, rows)
}

func (s *Service) delegationViews(ctx context.Context, q *store.Queries, companyID, owner domain.ID, rows []store.SecretDelegation) ([]DelegationView, error) {
	secrets, err := q.ListUserSecrets(ctx, store.ListUserSecretsParams{CompanyID: companyID, OwnerUserID: &owner})
	if err != nil {
		return nil, fmt.Errorf("listar segredos do dono: %w", err)
	}
	names := make(map[uuid.UUID]string, len(secrets))
	for _, sec := range secrets {
		names[sec.ID] = sec.Name
	}
	now := s.Now()
	out := make([]DelegationView, 0, len(rows))
	for _, d := range rows {
		out = append(out, delegationView(d, names[d.SecretID], now))
	}
	return out, nil
}

// RevokeSecretDelegation revoga uma delegação do chamador; a partir daí a run não resolve mais o item.
func (s *Service) RevokeSecretDelegation(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	owner, err := s.callerOwner(ctx, companyID, actor)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		row, err := t.q.RevokeSecretDelegation(t.ctx, store.RevokeSecretDelegationParams{CompanyID: companyID, OwnerUserID: owner, ID: id, Now: ptrTime(t.now)})
		if err != nil {
			return mapErr(err, "delegação")
		}
		return t.activity(actor, companyID, "delegation.revoked", "secret", row.SecretID,
			map[string]any{"delegation_id": row.ID, "target_type": row.TargetType, "target_id": row.TargetID}, "")
	})
}

// ListUserSecretUsage mostra ao dono quando e em que run o segredo pessoal dele foi usado. Só o dono vê (ADR-0050 §7).
func (s *Service) ListUserSecretUsage(ctx context.Context, companyID domain.ID, name string, limit int) ([]SecretAccessView, error) {
	owner, err := s.callerOwner(ctx, companyID, domain.HumanActor)
	if err != nil {
		return nil, err
	}
	q := s.read()
	sec, err := q.GetUserSecretByName(ctx, store.GetUserSecretByNameParams{CompanyID: companyID, OwnerUserID: &owner, Name: name})
	if err != nil {
		return nil, mapErr(err, "segredo "+name)
	}
	rows, err := q.ListSecretAccess(ctx, store.ListSecretAccessParams{CompanyID: companyID, SecretID: sec.ID, Lim: int32(min(max(limit, 1), 500))}) //nolint:gosec // limitado a 500
	if err != nil {
		return nil, fmt.Errorf("listar usos: %w", err)
	}
	return accessViews(ctx, q, companyID, rows), nil
}

// ListSecretUsage mostra onde e quando um segredo da empresa foi usado. Vê o admin, ou o membro com `view` ou `manage`
// numa coleção que contém o segredo (ADR-0068 §2, tabela de permissões).
func (s *Service) ListSecretUsage(ctx context.Context, companyID domain.ID, name string, limit int) ([]SecretAccessView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	q := s.read()
	sec, err := getSecretRow(ctx, q, companyID, domain.SecretScopeCompany, nil, name, false)
	if err != nil {
		return nil, err
	}
	if s.require(ctx, companyID, domain.MemberAdmin) != nil {
		uid, ok := s.identityUserID(ctx)
		if !ok {
			return nil, domain.ErrForbidden
		}
		perms, err := q.UserCollectionPermissions(ctx, store.UserCollectionPermissionsParams{CompanyID: companyID, SecretID: sec.ID, UserID: *uid})
		if err != nil {
			return nil, fmt.Errorf("permissões do segredo: %w", err)
		}
		if !canSeeUsage(perms) {
			return nil, domain.ErrForbidden
		}
	}
	rows, err := q.ListSecretAccess(ctx, store.ListSecretAccessParams{CompanyID: companyID, SecretID: sec.ID, Lim: int32(min(max(limit, 1), 500))}) //nolint:gosec // limitado a 500
	if err != nil {
		return nil, fmt.Errorf("listar usos: %w", err)
	}
	return accessViews(ctx, q, companyID, rows), nil
}

// canSeeUsage diz se alguma permissão da coleção deixa ver o uso: view ou manage.
func canSeeUsage(perms []string) bool {
	for _, p := range perms {
		if p == string(domain.CollectionView) || p == string(domain.CollectionManage) {
			return true
		}
	}
	return false
}

// accessViews traduz os usos registrados: a persona pelo slug, a delegação como flag. Nunca o valor.
func accessViews(ctx context.Context, q *store.Queries, companyID domain.ID, rows []store.SecretAccessLog) []SecretAccessView {
	slugs := map[uuid.UUID]string{}
	out := make([]SecretAccessView, 0, len(rows))
	for _, r := range rows {
		v := SecretAccessView{Field: r.FieldName, Via: domain.SecretAccessVia(r.Via), RunID: r.RunID, Delegated: r.DelegationID != nil, AccessedAt: r.AccessedAt}
		if r.PersonaID != nil {
			slug, ok := slugs[*r.PersonaID]
			if !ok {
				if p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *r.PersonaID}); err == nil {
					slug = p.Slug
				}
				slugs[*r.PersonaID] = slug
			}
			v.Persona = slug
		}
		out = append(out, v)
	}
	return out
}

// delegatedUserSecret acha o segredo pessoal `ref` de quem pediu o trabalho da run e a delegação válida dele para a
// run, a task ou a conversa dela. Sem dono, sem item ou sem delegação, recusa com o nome da referência, nunca o valor.
func (s *Service) delegatedUserSecret(ctx context.Context, companyID, runID domain.ID, r secretref.Ref) (store.Secret, store.SecretDelegation, uuid.UUID, error) {
	q := s.read()
	denied := fmt.Errorf("%s: sem delegação válida do dono para esta run (ADR-0068 §6)", r.String())
	run, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
	if err != nil {
		return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, mapErr(err, "run")
	}
	owner, err := runOwner(ctx, q, companyID, run)
	if err != nil {
		return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, err
	}
	if owner == nil {
		return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, denied
	}
	// a delegação só vale enquanto o trabalho (task ou conversa) está aberto (ADR-0068 §6.3)
	open, err := targetOpen(ctx, q, companyID, run)
	if err != nil {
		return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, err
	}
	if !open {
		return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, fmt.Errorf("%s: o trabalho desta run já está fechado ou arquivado (ADR-0068 §6.3)", r.String())
	}
	sec, err := q.GetUserSecretByName(ctx, store.GetUserSecretByNameParams{CompanyID: companyID, OwnerUserID: owner, Name: r.Name})
	if err != nil {
		if isNoRows(err) {
			return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, denied // não revela se o dono tem o item
		}
		return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, fmt.Errorf("ler segredo: %w", err)
	}
	targets := []struct {
		kind domain.DelegationTarget
		id   *uuid.UUID
	}{{domain.DelegationRun, &runID}, {domain.DelegationTask, run.TaskID}, {domain.DelegationConversation, run.ConversationID}}
	now := s.Now().UTC()
	for _, tg := range targets {
		if tg.id == nil {
			continue
		}
		d, err := q.ActiveDelegationForTarget(ctx, store.ActiveDelegationForTargetParams{CompanyID: companyID, SecretID: sec.ID,
			TargetType: string(tg.kind), TargetID: *tg.id, Now: now})
		if err == nil {
			return sec, d, *owner, nil
		}
		if !isNoRows(err) {
			return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, fmt.Errorf("ler delegação: %w", err)
		}
	}
	return store.Secret{}, store.SecretDelegation{}, uuid.UUID{}, denied
}

// ResolveRunUserSecrets expande as referências ${secret:user/...} do texto que a run recebe (a task, os comentários, a
// conversa) com os segredos pessoais de quem pediu o trabalho, mas só os que ele delegou a esta run. Cada uso entra no
// registro de uso do item. Texto sem referência pessoal volta como está. Devolve também os valores inseridos, para a
// redação do log. O erro nomeia a referência, nunca o valor.
func (s *Service) ResolveRunUserSecrets(ctx context.Context, companyID, runID domain.ID, text string) (string, []string, error) {
	if !strings.Contains(text, "${secret:"+UserSecretPrefix) && !strings.Contains(text, "${secretfile:"+UserSecretPrefix) {
		return text, nil, nil
	}
	c, err := s.RunToolCaller(ctx, companyID, runID)
	if err != nil {
		return text, nil, err
	}
	return secretref.Expand(text, func(r secretref.Ref) (string, error) {
		if !r.User {
			return r.String(), nil // referência da empresa: vale a configuração da persona, não o texto da task
		}
		if r.File {
			return "", errors.New("${secretfile:} de segredo de usuário ainda não é aceito (ADR-0068 §6)")
		}
		sec, d, owner, err := s.delegatedUserSecret(ctx, companyID, runID, r)
		if err != nil {
			return "", err
		}
		f, err := s.read().GetSecretField(ctx, store.GetSecretFieldParams{CompanyID: companyID, SecretID: sec.ID, Name: r.FieldName()})
		if err != nil {
			if isNoRows(err) {
				return "", fmt.Errorf("o campo %q não existe em %s", r.FieldName(), r.Name)
			}
			return "", fmt.Errorf("ler campo: %w", err)
		}
		if err := refKindErr(r, domain.SecretFieldKind(f.Kind)); err != nil {
			return "", err
		}
		val, err := s.resolveRef(ctx, companyID, r, &owner)
		if err != nil {
			return "", err
		}
		if err := s.recordSecretUse(ctx, c, companyID, sec.ID, f, domain.SecretAccessDispatch, &d.ID); err != nil {
			return "", err
		}
		return val, nil
	})
}

// purgeMemberSecrets apaga os segredos pessoais de quem saiu da empresa, na transação da saída (ADR-0068 §6.5). As
// delegações caem em cascata; as ativas são listadas antes, para o relatório e para o fato secret.user_purged, que não
// leva nome nem valor.
func (t *txc) purgeMemberSecrets(actor domain.Actor, companyID, userID domain.ID) (MemberExitReport, error) {
	rows, err := t.q.ListUserDelegations(t.ctx, store.ListUserDelegationsParams{CompanyID: companyID, OwnerUserID: userID, Lim: 10000})
	if err != nil {
		return MemberExitReport{}, fmt.Errorf("listar delegações do membro: %w", err)
	}
	var rep MemberExitReport
	for _, d := range rows {
		dom := domain.SecretDelegation{ExpiresAt: d.ExpiresAt, RevokedAt: d.RevokedAt}
		if !dom.Active(t.now) {
			continue
		}
		rep.Delegations = append(rep.Delegations, delegationView(d, "", t.now))
	}
	n, err := t.q.DeleteUserSecrets(t.ctx, store.DeleteUserSecretsParams{CompanyID: companyID, OwnerUserID: &userID})
	if err != nil {
		return MemberExitReport{}, fmt.Errorf("apagar segredos pessoais: %w", err)
	}
	rep.UserSecrets = int(n)
	if n == 0 && len(rep.Delegations) == 0 {
		return rep, nil
	}
	targets := make([]map[string]any, 0, len(rep.Delegations))
	for _, d := range rep.Delegations {
		targets = append(targets, map[string]any{"delegation_id": d.ID, "target_type": d.TargetType, "target_id": d.TargetID, "expires_at": d.ExpiresAt})
	}
	err = t.activity(actor, companyID, "secret.user_purged", "user", userID,
		map[string]any{"items": n, "active_delegations": len(rep.Delegations), "delegations": targets}, "")
	return rep, err
}
