package service

import (
	"context"
	"fmt"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Persona principal e personas de sistema (ADR-0041).

// primaryPersonaID devolve a persona principal do usuário do contexto (nil sem usuário, sem principal ou desligada).
func (s *Service) primaryPersonaID(ctx context.Context, companyID domain.ID) (*domain.ID, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return nil, nil
	}
	q := s.read()
	pid, err := q.GetPrimaryPersona(ctx, store.GetPrimaryPersonaParams{CompanyID: companyID, UserID: id.User.ID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("primary persona: %w", err)
	}
	p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: pid})
	if isNoRows(err) || (err == nil && domain.PersonaStatus(p.Status) == domain.PersonaOff) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("primary persona: %w", err)
	}
	return &pid, nil
}

// PrimaryPersona devolve a persona principal de quem chama (nil se não escolheu uma ou ela foi desligada). Sem usuário
// no contexto (modo trusted) devolve nil.
func (s *Service) PrimaryPersona(ctx context.Context, companyID domain.ID) (*domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	pid, err := s.primaryPersonaID(ctx, companyID)
	if err != nil || pid == nil {
		return nil, err
	}
	p, err := s.read().GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *pid})
	if err != nil {
		return nil, mapErr(err, "get persona")
	}
	out := personaFromRow(p)
	return &out, nil
}

// SetPrimaryPersona escolhe (ou, com nil, limpa) a persona principal de quem chama: o responsável padrão das tasks que
// a pessoa cria sem responsável e a sugestão do chat. Só vale para uma pessoa autenticada.
func (s *Service) SetPrimaryPersona(ctx context.Context, actor domain.Actor, companyID domain.ID, personaID *domain.ID) (*domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return nil, domain.Invalid("persona", "a persona principal é de uma pessoa autenticada; no modo trusted não há quem escolher")
	}
	var out *domain.Persona
	err := s.withTx(ctx, func(t *txc) error {
		if personaID == nil {
			if err := t.q.DeletePrimaryPersona(t.ctx, store.DeletePrimaryPersonaParams{CompanyID: companyID, UserID: id.User.ID}); err != nil {
				return fmt.Errorf("clear primary persona: %w", err)
			}
			return t.activity(actor, companyID, "member.primary_persona_set", "company", companyID, map[string]any{"user_id": id.User.ID, "persona_id": nil}, "")
		}
		p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *personaID})
		if err != nil {
			return domain.Invalid("persona", "persona not found in company")
		}
		if domain.PersonaStatus(p.Status) == domain.PersonaOff {
			return domain.Invalid("persona", "uma persona desligada não pode ser a principal")
		}
		if err := t.q.UpsertPrimaryPersona(t.ctx, store.UpsertPrimaryPersonaParams{CompanyID: companyID, UserID: id.User.ID, PersonaID: p.ID, Now: t.now}); err != nil {
			return fmt.Errorf("set primary persona: %w", err)
		}
		pp := personaFromRow(p)
		out = &pp
		return t.activity(actor, companyID, "member.primary_persona_set", "persona", p.ID, map[string]any{"user_id": id.User.ID, "persona_id": p.ID}, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetPersonaSystem marca ou desmarca uma persona como de sistema. Só um owner decide: persona de sistema não se desliga
// e só um owner a edita.
func (s *Service) SetPersonaSystem(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, system bool) (domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberOwner); err != nil {
		return domain.Persona{}, err
	}
	if actor.Type == domain.ActorPersona {
		return domain.Persona{}, fmt.Errorf("set persona system: %w", domain.ErrNotAllowed)
	}
	var out domain.Persona
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetPersonaByIDForUpdate(t.ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		if cur.System == system {
			out = personaFromRow(cur)
			return nil
		}
		row, err := t.q.SetPersonaSystem(t.ctx, store.SetPersonaSystemParams{CompanyID: companyID, ID: personaID, System: system, Now: t.now})
		if err != nil {
			return mapErr(err, "set persona system")
		}
		out = personaFromRow(row)
		return t.activity(actor, companyID, "persona.system_changed", "persona", personaID, map[string]any{"system": system, "slug": out.Slug}, "")
	})
	return out, err
}

// RequireHireGate diz se a empresa exige um gate para contratar também quando quem contrata é um humano (ADR-0041).
func (s *Service) RequireHireGate(ctx context.Context, companyID domain.ID) (bool, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return false, err
	}
	pol, err := s.read().GetCompanyPolicies(ctx, companyID)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("company policies: %w", err)
	}
	return pol.RequireHireGate, nil
}

// SetRequireHireGate liga ou desliga a política de contratação por gate. Só owner muda a política da empresa.
func (s *Service) SetRequireHireGate(ctx context.Context, actor domain.Actor, companyID domain.ID, on bool) error {
	if err := s.require(ctx, companyID, domain.MemberOwner); err != nil {
		return err
	}
	if actor.Type == domain.ActorPersona {
		return fmt.Errorf("set hire policy: %w", domain.ErrNotAllowed)
	}
	return s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(t.ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if err := t.q.UpsertCompanyPolicies(t.ctx, store.UpsertCompanyPoliciesParams{CompanyID: companyID, RequireHireGate: on, Now: t.now}); err != nil {
			return fmt.Errorf("set company policies: %w", err)
		}
		return t.activity(actor, companyID, "company.policy_updated", "company", companyID, map[string]any{"require_hire_gate": on}, "")
	})
}

// requireSystemEdit protege uma persona de sistema: só um owner a edita, e ela nunca é desligada.
func (s *Service) requireSystemEdit(ctx context.Context, companyID domain.ID, cur store.Persona, status *domain.PersonaStatus) error {
	if !cur.System {
		return nil
	}
	if status != nil && *status == domain.PersonaOff {
		return fmt.Errorf("a persona de sistema %q não pode ser desligada: %w", cur.Name, domain.ErrConflict)
	}
	return s.require(ctx, companyID, domain.MemberOwner)
}
