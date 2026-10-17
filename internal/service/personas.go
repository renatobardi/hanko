package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/store"
)

func (s *Service) HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in HirePersonaInput) (domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Persona{}, err
	}
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
	icon := strings.TrimSpace(in.Icon)
	if icon == "" {
		icon = domain.DefaultPersonaIcon
	}
	if !domain.ValidPersonaIcon(icon) {
		return domain.Persona{}, domain.Invalid("icon", fmt.Sprintf("unknown icon %q (want one of %v)", icon, domain.PersonaIcons))
	}
	cfg := []byte(in.AdapterConfig)
	if len(cfg) == 0 {
		cfg = []byte(`{}`)
	}
	if !json.Valid(cfg) {
		return domain.Persona{}, domain.Invalid("adapter_config", "not valid JSON")
	}
	if err := s.validateAdapterConfig(in.Agent, cfg); err != nil {
		return domain.Persona{}, err
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
		if in.SubscriptionID != nil {
			if _, err := t.q.GetSubscriptionByID(ctx, store.GetSubscriptionByIDParams{CompanyID: companyID, ID: *in.SubscriptionID}); err != nil {
				return domain.Invalid("subscription_id", "subscription not found in company")
			}
		}
		// o env em claro vira segredos da empresa; a persona guarda só ${secret:...} (ADR-0050)
		if cfg, _, err = s.sealPersonaEnv(t, actor, companyID, slug, cfg); err != nil {
			return err
		}
		row, err := t.q.CreatePersona(ctx, store.CreatePersonaParams{
			ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name, Title: strings.TrimSpace(in.Title), Icon: icon,
			Instructions: in.Instructions, Agent: in.Agent, Model: strings.TrimSpace(in.Model), AdapterConfig: cfg,
			Status: string(domain.PersonaActive), ReportsTo: in.ReportsTo, WorkspaceID: in.WorkspaceID,
			HeartbeatIntervalSec: i32(in.HeartbeatIntervalSec), BudgetMonthlyCents: in.BudgetMonthlyCents,
			SubscriptionID: in.SubscriptionID, Now: t.now})
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
		if in.ReviewerPersona != nil || in.ReviewerAgent != "" || in.ReviewerModel != "" {
			rp, ra, rm, err := t.checkReviewer(companyID, row.ID, row.Agent, row.Model, in.ReviewerPersona, in.ReviewerAgent, in.ReviewerModel)
			if err != nil {
				return err
			}
			row, err = t.q.SetPersonaReviewer(ctx, store.SetPersonaReviewerParams{CompanyID: companyID, ID: row.ID,
				ReviewerPersonaID: rp, ReviewerAgent: ra, ReviewerModel: rm, Now: t.now})
			if err != nil {
				return mapErr(err, "hire persona")
			}
		}
		rev, err := t.recordInstructionRevision(instructionRecord{companyID: companyID, personaID: row.ID, current: 0,
			body: in.Instructions, author: actor, actor: actor, origin: domain.RevisionHire})
		if err != nil {
			return err
		}
		row.InstructionRevision = i32(rev)
		out = personaFromRow(row)
		return t.activity(actor, companyID, "persona.hired", "persona", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name, "agent": out.Agent, "reports_to": out.ReportsTo,
				"require_review": out.RequireReview, "reviewer_persona_id": out.ReviewerPersonaID,
				"reviewer_agent": out.ReviewerAgent, "subscription_id": out.SubscriptionID}, "")
	})
	return out, err
}

// validateAdapterConfig checks the adapter_config of harnesses that need one to run. Only acp does:
// without a command there is nothing to execute, so the mistake is refused when the persona is
// hired, with a message that says what to fix, instead of failing every run later.
func (s *Service) validateAdapterConfig(agent string, cfg []byte) error {
	if err := s.adapterConfigProblem(agent, cfg); err != nil {
		return domain.Invalid("adapter_config", err.Error())
	}
	return nil
}

// acpPolicy é a política efetiva do harness acp: settings da instância (env > banco) ou a configuração fixa.
func (s *Service) acpPolicy() acp.Policy {
	if s.Settings != nil {
		return acp.Policy{Allow: s.Settings.Bool(settings.ACPAllow), Commands: s.Settings.Strings(settings.ACPCommands)}
	}
	return s.ACP
}

// adapterConfigProblem is the reason an adapter_config cannot run on agent (nil = fine). For acp that
// includes the operator's gate (HANKO_ACP_ALLOW / HANKO_ACP_COMMANDS, ADR-0023): the command is chosen
// by a company admin, so it is checked here and again by the adapter when the run starts.
func (s *Service) adapterConfigProblem(agent string, cfg []byte) error {
	if agent != "acp" {
		return nil
	}
	c, err := acp.ParseConfig(cfg)
	if err != nil {
		return err
	}
	return s.acpPolicy().Check(c.Command)
}

func (s *Service) ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
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
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Persona{}, err
	}
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
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Persona{}, err
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return domain.Persona{}, domain.Invalid("name", "must not be empty")
	}
	if in.Status != nil && !in.Status.Valid() {
		return domain.Persona{}, domain.Invalid("status", fmt.Sprintf("unknown status %q", *in.Status))
	}
	if in.Icon != nil && !domain.ValidPersonaIcon(*in.Icon) {
		return domain.Persona{}, domain.Invalid("icon", fmt.Sprintf("unknown icon %q (want one of %v)", *in.Icon, domain.PersonaIcons))
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
	if in.ClearReviewer && (in.ReviewerPersona != nil || in.ReviewerAgent != nil || in.ReviewerModel != nil) {
		return domain.Persona{}, domain.Invalid("reviewer", "cannot set and clear at once")
	}
	if in.ReviewerPersona != nil && (in.ReviewerAgent != nil || in.ReviewerModel != nil) {
		// the hire path refuses this combination too; silently dropping the harness would hide the mistake
		return domain.Persona{}, domain.Invalid("reviewer", "choose a reviewer persona or an alternate harness, not both")
	}
	if in.BudgetMonthlyCents != nil && *in.BudgetMonthlyCents < 0 {
		return domain.Persona{}, domain.Invalid("budget_monthly_cents", "must be >= 0")
	}
	if in.ClearSubscription && in.SubscriptionID != nil {
		return domain.Persona{}, domain.Invalid("subscription_id", "cannot set and clear at once")
	}
	reason, err := cleanReason(in.InstructionsReason)
	if err != nil {
		return domain.Persona{}, err
	}
	if in.Instructions != nil && (!utf8.ValidString(*in.Instructions) || utf8.RuneCountInString(*in.Instructions) > maxInstructionRunes) {
		return domain.Persona{}, domain.Invalid("instructions", fmt.Sprintf("must be valid UTF-8 with at most %d characters", maxInstructionRunes))
	}

	var out domain.Persona
	err = s.withTx(ctx, func(t *txc) error {
		if in.ReportsTo != nil {
			if err := t.q.LockOrg(ctx, companyID.String()); err != nil { // serializes concurrent chart edits
				return fmt.Errorf("lock org: %w", err)
			}
		}
		cur, err := t.q.GetPersonaByIDForUpdate(ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		p := store.UpdatePersonaParams{CompanyID: companyID, ID: personaID, Name: cur.Name, Title: cur.Title, Icon: cur.Icon,
			Instructions: cur.Instructions, Model: cur.Model, Status: cur.Status,
			HeartbeatIntervalSec: cur.HeartbeatIntervalSec, WorkspaceID: cur.WorkspaceID,
			BudgetMonthlyCents: cur.BudgetMonthlyCents, SubscriptionID: cur.SubscriptionID, Now: t.now}
		var changed []string
		if in.Name != nil && strings.TrimSpace(*in.Name) != cur.Name {
			p.Name = strings.TrimSpace(*in.Name)
			changed = append(changed, "name")
		}
		if in.Title != nil && *in.Title != cur.Title {
			p.Title = *in.Title
			changed = append(changed, "title")
		}
		if in.Icon != nil && *in.Icon != cur.Icon {
			p.Icon = *in.Icon
			changed = append(changed, "icon")
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
		rvPersona, rvAgent, rvModel := cur.ReviewerPersonaID, cur.ReviewerAgent, cur.ReviewerModel
		switch {
		case in.ClearReviewer:
			rvPersona, rvAgent, rvModel = nil, "", ""
		case in.ReviewerPersona != nil: // a reviewer persona replaces an alternate harness
			rvPersona, rvAgent, rvModel = in.ReviewerPersona, "", ""
		case in.ReviewerAgent != nil || in.ReviewerModel != nil: // an alternate harness replaces a reviewer persona
			if in.ReviewerAgent != nil {
				rvAgent = *in.ReviewerAgent
			}
			if in.ReviewerModel != nil {
				rvModel = *in.ReviewerModel
			}
			rvPersona = nil
		}
		reviewerChanged := !sameID(rvPersona, cur.ReviewerPersonaID) || rvAgent != cur.ReviewerAgent || rvModel != cur.ReviewerModel
		// An alternate harness must stay different from the author's: also re-check when only the author's model moved.
		if reviewerChanged || (p.Model != cur.Model && rvPersona == nil && rvAgent != "") {
			if rvPersona, rvAgent, rvModel, err = t.checkReviewer(companyID, personaID, cur.Agent, p.Model, rvPersona, rvAgent, rvModel); err != nil {
				return err
			}
			if reviewerChanged {
				changed = append(changed, "reviewer")
			}
		}
		if in.ClearSubscription && cur.SubscriptionID != nil {
			p.SubscriptionID = nil
			changed = append(changed, "subscription_id")
		} else if in.SubscriptionID != nil && (cur.SubscriptionID == nil || *cur.SubscriptionID != *in.SubscriptionID) {
			if _, err := t.q.GetSubscriptionByID(ctx, store.GetSubscriptionByIDParams{CompanyID: companyID, ID: *in.SubscriptionID}); err != nil {
				return domain.Invalid("subscription_id", "subscription not found in company")
			}
			p.SubscriptionID = in.SubscriptionID
			changed = append(changed, "subscription_id")
		}
		if len(changed) == 0 {
			out = personaFromRow(cur)
			return nil
		}
		row, err := t.q.UpdatePersona(ctx, p)
		if err != nil {
			return mapErr(err, "update persona")
		}
		if p.Instructions != cur.Instructions { // ADR-0052: toda mudança de instruções vira uma revisão, na mesma transação
			rev, err := t.recordInstructionRevision(instructionRecord{companyID: companyID, personaID: personaID, current: int(cur.InstructionRevision),
				body: p.Instructions, author: actor, actor: actor, reason: reason, origin: domain.RevisionEdit})
			if err != nil {
				return err
			}
			row.InstructionRevision = i32(rev)
		}
		if reviewerChanged {
			row, err = t.q.SetPersonaReviewer(ctx, store.SetPersonaReviewerParams{CompanyID: companyID, ID: personaID,
				ReviewerPersonaID: rvPersona, ReviewerAgent: rvAgent, ReviewerModel: rvModel, Now: t.now})
			if err != nil {
				return mapErr(err, "update persona")
			}
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
			if err := t.resetBudgetAlerts(co, personaID, row.BudgetMonthlyCents, &personaID, nil); err != nil {
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
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return PersonaHealth{}, err
	}
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

func sameID(a, b *domain.ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// checkReviewer validates a reviewer configuration: either another persona of the company or an
// alternate harness (agent, optionally a model) for the same role, never both and never itself. An
// alternate harness identical to the author's own (same agent and same model) is no second opinion.
func (t *txc) checkReviewer(companyID, personaID domain.ID, authorAgent, authorModel string, reviewer *domain.ID, agent, model string) (*domain.ID, string, string, error) {
	agent, model = strings.TrimSpace(agent), strings.TrimSpace(model)
	switch {
	case reviewer != nil && (agent != "" || model != ""):
		return nil, "", "", domain.Invalid("reviewer", "choose a reviewer persona or an alternate harness, not both")
	case reviewer != nil:
		if *reviewer == personaID {
			return nil, "", "", domain.Invalid("reviewer", "a persona cannot review itself; use an alternate harness instead")
		}
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *reviewer}); err != nil {
			return nil, "", "", domain.Invalid("reviewer", "persona not found in company")
		}
		return reviewer, "", "", nil
	case agent == "" && model != "":
		return nil, "", "", domain.Invalid("reviewer_agent", "required when reviewer_model is set")
	case agent == "acp":
		return nil, "", "", domain.Invalid("reviewer_agent", "acp cannot be an alternate reviewer harness (it needs its own adapter_config); use a reviewer persona that runs on acp instead")
	case agent != "" && !domain.ValidReviewerAgent(agent):
		return nil, "", "", domain.Invalid("reviewer_agent", fmt.Sprintf("unknown agent %q (want one of %v)", agent, domain.ReviewerAgents))
	}
	if agent != "" && agent == authorAgent && model == strings.TrimSpace(authorModel) {
		return nil, "", "", domain.Invalid("reviewer_agent", "the alternate harness is the same as the author's (same agent and model); change the agent or the model")
	}
	return nil, agent, model, nil
}
