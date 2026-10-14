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
	if in.HeartbeatIntervalSec < 0 {
		return domain.Persona{}, domain.Invalid("heartbeat_interval_sec", "must be >= 0")
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
			HeartbeatIntervalSec: int32(in.HeartbeatIntervalSec), BudgetMonthlyCents: in.BudgetMonthlyCents, Now: t.now})
		if err != nil {
			return mapErr(err, "hire persona")
		}
		out = personaFromRow(row)
		return t.activity(actor, companyID, "persona.hired", "persona", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name, "agent": out.Agent}, "")
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
	if in.HeartbeatIntervalSec != nil && *in.HeartbeatIntervalSec < 0 {
		return domain.Persona{}, domain.Invalid("heartbeat_interval_sec", "must be >= 0")
	}
	if in.ClearWorkspace && in.WorkspaceID != nil {
		return domain.Persona{}, domain.Invalid("workspace_id", "cannot set and clear at once")
	}

	var out domain.Persona
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetPersonaByIDForUpdate(ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		p := store.UpdatePersonaParams{CompanyID: companyID, ID: personaID, Name: cur.Name, Title: cur.Title,
			Instructions: cur.Instructions, Model: cur.Model, Status: cur.Status,
			HeartbeatIntervalSec: cur.HeartbeatIntervalSec, WorkspaceID: cur.WorkspaceID, Now: t.now}
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
		if in.HeartbeatIntervalSec != nil && int32(*in.HeartbeatIntervalSec) != cur.HeartbeatIntervalSec {
			p.HeartbeatIntervalSec = int32(*in.HeartbeatIntervalSec)
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
		if len(changed) == 0 {
			out = personaFromRow(cur)
			return nil
		}
		row, err := t.q.UpdatePersona(ctx, p)
		if err != nil {
			return mapErr(err, "update persona")
		}
		out = personaFromRow(row)
		return t.activity(actor, companyID, "persona.updated", "persona", out.ID, map[string]any{"fields": changed}, "")
	})
	return out, err
}
