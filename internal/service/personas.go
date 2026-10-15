package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

func (s *Service) HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in HirePersonaInput) (domain.Persona, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Persona{}, domain.Invalid("name", "required")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Persona{}, err
	}
	if !domain.ValidAgent(in.Agent) {
		return domain.Persona{}, domain.Invalid("agent", fmt.Sprintf("unknown agent %q (want one of %v)", in.Agent, domain.KnownAgents))
	}
	if in.HeartbeatIntervalSec < 0 || in.HeartbeatIntervalSec > maxHeartbeatIntervalSec {
		return domain.Persona{}, domain.Invalid("heartbeat_interval_sec", fmt.Sprintf("must be between 0 and %d", maxHeartbeatIntervalSec))
	}
	if in.BudgetMonthlyCents != nil && *in.BudgetMonthlyCents < 0 {
		return domain.Persona{}, domain.Invalid("budget_monthly_cents", "must be >= 0")
	}
	cfg := []byte(in.AdapterConfig)
	if len(cfg) == 0 {
		cfg = []byte(`{}`)
	}
	if !json.Valid(cfg) {
		return domain.Persona{}, domain.Invalid("adapter_config", "not valid JSON")
	}

	var out domain.Persona
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if in.ReportsTo != nil {
			if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.ReportsTo}); err != nil {
				return domain.Invalid("reports_to", "persona not found in company")
			}
		}
		if in.WorkspaceID != nil {
			if _, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *in.WorkspaceID}); err != nil {
				return domain.Invalid("workspace_id", "workspace not found in company")
			}
		}
		row, err := t.q.CreatePersona(ctx, store.CreatePersonaParams{
			ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name, Title: strings.TrimSpace(in.Title),
			Instructions: in.Instructions, Agent: in.Agent, Model: strings.TrimSpace(in.Model), AdapterConfig: cfg,
			Status: string(domain.PersonaActive), ReportsTo: in.ReportsTo, WorkspaceID: in.WorkspaceID,
			HeartbeatIntervalSec: i32(in.HeartbeatIntervalSec), BudgetMonthlyCents: in.BudgetMonthlyCents, Now: t.now})
		if err != nil {
			return mapErr(err, "hire persona")
		}
		if in.RequireReview {
			row, err = t.q.SetPersonaOrg(ctx, store.SetPersonaOrgParams{CompanyID: companyID, ID: row.ID,
				ReportsTo: row.ReportsTo, RequireReview: true, Now: t.now})
			if err != nil {
				return mapErr(err, "hire persona")
			}
		}
		out = personaFromRow(row)
		return t.activity(actor, companyID, "persona.hired", "persona", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name, "agent": out.Agent, "reports_to": out.ReportsTo,
				"require_review": out.RequireReview}, "")
	})
	return out, err
}

func (s *Service) ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error) {
	rows, err := s.read().ListPersonas(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	out := make([]domain.Persona, len(rows))
	for i, r := range rows {
		out[i] = personaFromRow(r)
	}
	return out, nil
}

// GetPersona resolves ref as UUID or slug within the company.
func (s *Service) GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error) {
	q := s.read()
	var row store.Persona
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: id})
	} else {
		row, err = q.GetPersonaBySlug(ctx, store.GetPersonaBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return domain.Persona{}, mapErr(err, "get persona")
	}
	return personaFromRow(row), nil
}

func (s *Service) UpdatePersona(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in UpdatePersonaInput) (domain.Persona, error) {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return domain.Persona{}, domain.Invalid("name", "must not be empty")
	}
	if in.Status != nil && !in.Status.Valid() {
		return domain.Persona{}, domain.Invalid("status", fmt.Sprintf("unknown status %q", *in.Status))
	}
	if in.HeartbeatIntervalSec != nil && (*in.HeartbeatIntervalSec < 0 || *in.HeartbeatIntervalSec > maxHeartbeatIntervalSec) {
		return domain.Persona{}, domain.Invalid("heartbeat_interval_sec", fmt.Sprintf("must be between 0 and %d", maxHeartbeatIntervalSec))
	}
	if in.ClearWorkspace && in.WorkspaceID != nil {
		return domain.Persona{}, domain.Invalid("workspace_id", "cannot set and clear at once")
	}
	if in.ClearReportsTo && in.ReportsTo != nil {
		return domain.Persona{}, domain.Invalid("reports_to", "cannot set and clear at once")
	}
	if in.ClearBudget && in.BudgetMonthlyCents != nil {
		return domain.Persona{}, domain.Invalid("budget_monthly_cents", "cannot set and clear at once")
	}
	if in.BudgetMonthlyCents != nil && *in.BudgetMonthlyCents < 0 {
		return domain.Persona{}, domain.Invalid("budget_monthly_cents", "must be >= 0")
	}

	var out domain.Persona
	err := s.withTx(ctx, func(t *txc) error {
		if in.ReportsTo != nil {
			if err := t.q.LockOrg(ctx, companyID.String()); err != nil { // serializes concurrent chart edits
				return fmt.Errorf("lock org: %w", err)
			}
		}
		cur, err := t.q.GetPersonaByIDForUpdate(ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		p := store.UpdatePersonaParams{CompanyID: companyID, ID: personaID, Name: cur.Name, Title: cur.Title,
			Instructions: cur.Instructions, Model: cur.Model, Status: cur.Status,
			HeartbeatIntervalSec: cur.HeartbeatIntervalSec, WorkspaceID: cur.WorkspaceID,
			BudgetMonthlyCents: cur.BudgetMonthlyCents, Now: t.now}
		var changed []string
		if in.Name != nil && strings.TrimSpace(*in.Name) != cur.Name {
			p.Name = strings.TrimSpace(*in.Name)
			changed = append(changed, "name")
		}
		if in.Title != nil && *in.Title != cur.Title {
			p.Title = *in.Title
			changed = append(changed, "title")
		}
		if in.Instructions != nil && *in.Instructions != cur.Instructions {
			p.Instructions = *in.Instructions
			changed = append(changed, "instructions")
		}
		if in.Model != nil && *in.Model != cur.Model {
			p.Model = *in.Model
			changed = append(changed, "model")
		}
		if in.Status != nil && string(*in.Status) != cur.Status {
			p.Status = string(*in.Status)
			changed = append(changed, "status")
		}
		if in.Status != nil && actor.Type != domain.ActorPersona && cur.PauseReason != nil {
			// A human who sets the status explicitly takes over a budget pause: without clearing the
			// reason, the budget tick would resume a persona the human just paused on purpose.
			p.ClearPauseReason = true
			if string(*in.Status) == cur.Status {
				changed = append(changed, "pause_reason")
			}
		}
		if in.HeartbeatIntervalSec != nil && i32(*in.HeartbeatIntervalSec) != cur.HeartbeatIntervalSec {
			p.HeartbeatIntervalSec = i32(*in.HeartbeatIntervalSec)
			changed = append(changed, "heartbeat_interval_sec")
		}
		if in.ClearWorkspace && cur.WorkspaceID != nil {
			p.WorkspaceID = nil
			changed = append(changed, "workspace_id")
		} else if in.WorkspaceID != nil && (cur.WorkspaceID == nil || *cur.WorkspaceID != *in.WorkspaceID) {
			if _, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *in.WorkspaceID}); err != nil {
				return domain.Invalid("workspace_id", "workspace not found in company")
			}
			p.WorkspaceID = in.WorkspaceID
			changed = append(changed, "workspace_id")
		}
		reports, review, orgChanged := cur.ReportsTo, cur.RequireReview, false
		switch {
		case in.ClearReportsTo && cur.ReportsTo != nil:
			reports, orgChanged = nil, true
			changed = append(changed, "reports_to")
		case in.ReportsTo != nil && (cur.ReportsTo == nil || *cur.ReportsTo != *in.ReportsTo):
			if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.ReportsTo}); err != nil {
				return domain.Invalid("reports_to", "persona not found in company")
			}
			parents, err := t.orgParents(companyID)
			if err != nil {
				return err
			}
			if parents.WouldCycle(personaID, *in.ReportsTo) {
				return domain.Invalid("reports_to", "would create a cycle in the org chart")
			}
			reports, orgChanged = in.ReportsTo, true
			changed = append(changed, "reports_to")
		}
		if in.RequireReview != nil && *in.RequireReview != cur.RequireReview {
			review, orgChanged = *in.RequireReview, true
			changed = append(changed, "require_review")
		}
		if in.ClearBudget && cur.BudgetMonthlyCents != nil {
			p.BudgetMonthlyCents = nil
			changed = append(changed, "budget_monthly_cents")
		} else if in.BudgetMonthlyCents != nil && (cur.BudgetMonthlyCents == nil || *cur.BudgetMonthlyCents != *in.BudgetMonthlyCents) {
			p.BudgetMonthlyCents = in.BudgetMonthlyCents
			changed = append(changed, "budget_monthly_cents")
		}
		if len(changed) == 0 {
			out = personaFromRow(cur)
			return nil
		}
		row, err := t.q.UpdatePersona(ctx, p)
		if err != nil {
			return mapErr(err, "update persona")
		}
		if orgChanged {
			row, err = t.q.SetPersonaOrg(ctx, store.SetPersonaOrgParams{CompanyID: companyID, ID: personaID,
				ReportsTo: reports, RequireReview: review, Now: t.now})
			if err != nil {
				return mapErr(err, "update persona")
			}
		}
		out = personaFromRow(row)
		if err := t.activity(actor, companyID, "persona.updated", "persona", out.ID, map[string]any{"fields": changed}, ""); err != nil {
			return err
		}
		if in.ClearBudget || in.BudgetMonthlyCents != nil {
			// a new ceiling re-arms the alerts and lifts a budget pause that no longer applies
			co, err := t.q.GetCompanyByID(ctx, companyID)
			if err != nil {
				return mapErr(err, "get company")
			}
			if err := t.resetBudgetAlerts(co, personaID, row.BudgetMonthlyCents, &personaID); err != nil {
				return err
			}
			if err := t.q.ClearPersonaBudgetRefused(ctx, store.ClearPersonaBudgetRefusedParams{PersonaID: &personaID}); err != nil {
				return fmt.Errorf("clear budget refused: %w", err)
			}
			if _, err := t.resumeBudgetPaused(&companyID, &personaID); err != nil {
				return err
			}
			if upd, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err == nil {
				out = personaFromRow(upd)
			}
		}
		return nil
	})
	return out, err
}

// GetPersonaHealth returns the last failure and the consecutive-failure counter of a persona.
func (s *Service) GetPersonaHealth(ctx context.Context, companyID, personaID domain.ID) (PersonaHealth, error) {
	row, err := s.read().GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		return PersonaHealth{}, mapErr(err, "get persona")
	}
	h := PersonaHealth{LastErrorAt: row.LastErrorAt, ConsecutiveFailures: int(row.ConsecutiveFailures)}
	if row.LastError != nil {
		h.LastError = *row.LastError
	}
	return h, nil
}
