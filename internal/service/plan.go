package service

import (
	"context"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Plan-review (ADR-0041): a política por task, projeto ou persona, a fase derivada e o plano aprovado no briefing.

// PlanContext é o que o run sabe do plan-review: a fase e, na fase de execução, o plano aprovado. nil quando a política
// não vale para a task.
type PlanContext struct {
	Phase  domain.PlanPhase
	Policy domain.PlanPolicy
	// ApprovedPlan é o texto do plano aprovado (fase execute); GateID e Revision dizem de qual gate e versão.
	ApprovedPlan string
	GateID       domain.ID
	Revision     int
}

// PlanPolicyView mostra a política por camada, o resultado da precedência e a fase atual da task.
type PlanPolicyView struct {
	Task, Project, Persona *bool
	Effective              domain.PlanPolicy
	Phase                  domain.PlanPhase
	// PendingPlan: há um plano esperando decisão.
	PendingPlan bool
}

func planOverride(ctx context.Context, q *store.Queries, scope domain.PlanScope, target domain.ID) (*bool, error) {
	v, err := q.GetPlanPolicy(ctx, store.GetPlanPolicyParams{Scope: string(scope), TargetID: target})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plan policy: %w", err)
	}
	return &v, nil
}

// planLayers lê as três camadas para a task e a persona dadas.
func planLayers(ctx context.Context, q *store.Queries, taskID domain.ID, projectID, personaID *domain.ID) (task, project, persona *bool, err error) {
	if task, err = planOverride(ctx, q, domain.PlanScopeTask, taskID); err != nil {
		return
	}
	if projectID != nil {
		if project, err = planOverride(ctx, q, domain.PlanScopeProject, *projectID); err != nil {
			return
		}
	}
	if personaID != nil {
		persona, err = planOverride(ctx, q, domain.PlanScopePersona, *personaID)
	}
	return
}

// planContext monta o PlanContext de uma task trabalhada por uma persona (nil sem política).
func planContext(ctx context.Context, q *store.Queries, taskID domain.ID, projectID, personaID *domain.ID) (*PlanContext, error) {
	to, po, pe, err := planLayers(ctx, q, taskID, projectID, personaID)
	if err != nil {
		return nil, err
	}
	pol := domain.ResolvePlanPolicy(to, po, pe)
	if !pol.Required {
		return nil, nil
	}
	pc := &PlanContext{Policy: pol}
	g, err := q.LatestApprovedPlanGate(ctx, &taskID)
	switch {
	case err == nil:
		pc.Phase, pc.ApprovedPlan, pc.GateID, pc.Revision = domain.PlanPhaseExecute, g.Body, g.ID, int(g.Revision)
	case isNoRows(err):
		pc.Phase = domain.PlanPhasePlan
	default:
		return nil, fmt.Errorf("approved plan: %w", err)
	}
	return pc, nil
}

// PlanPhaseFor devolve a fase de plano da task para a persona do run (usada pelo gateway MCP para recusar tools de
// escrita na fase de planejamento). PlanPhaseNone quando a política não vale ou a task não existe.
func (s *Service) PlanPhaseFor(ctx context.Context, companyID, taskID, personaID domain.ID) (domain.PlanPhase, error) {
	q := s.read()
	task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
	if isNoRows(err) {
		return domain.PlanPhaseNone, nil
	}
	if err != nil {
		return "", fmt.Errorf("get task: %w", err)
	}
	pc, err := planContext(ctx, q, task.ID, task.ProjectID, &personaID)
	if err != nil || pc == nil {
		return domain.PlanPhaseNone, err
	}
	return pc.Phase, nil
}

// TaskPlanPolicy mostra a política de plano da task por camada (a camada da persona é a do responsável atual).
func (s *Service) TaskPlanPolicy(ctx context.Context, companyID, taskID domain.ID) (PlanPolicyView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return PlanPolicyView{}, err
	}
	q := s.read()
	task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
	if err != nil {
		return PlanPolicyView{}, mapErr(err, "get task")
	}
	to, po, pe, err := planLayers(ctx, q, task.ID, task.ProjectID, task.PersonaID)
	if err != nil {
		return PlanPolicyView{}, err
	}
	v := PlanPolicyView{Task: to, Project: po, Persona: pe, Effective: domain.ResolvePlanPolicy(to, po, pe)}
	if v.Effective.Required {
		_, aerr := q.LatestApprovedPlanGate(ctx, &task.ID)
		if aerr != nil && !isNoRows(aerr) {
			return PlanPolicyView{}, fmt.Errorf("approved plan: %w", aerr)
		}
		v.Phase = v.Effective.PhaseFor(aerr == nil)
	}
	if v.PendingPlan, err = q.HasPendingPlanGate(ctx, &task.ID); err != nil {
		return PlanPolicyView{}, fmt.Errorf("pending plan: %w", err)
	}
	return v, nil
}

// SetPlanPolicy liga (true), desliga (false) ou limpa (nil, volta a herdar) a política de plano de uma camada.
// Task: member ou mais; projeto e persona: admin.
func (s *Service) SetPlanPolicy(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.PlanScope, targetID domain.ID, require *bool) error {
	if err := scope.Check(); err != nil {
		return err
	}
	min := domain.MemberAdmin
	if scope == domain.PlanScopeTask {
		min = domain.MemberMember
	}
	if err := s.require(ctx, companyID, min); err != nil {
		return err
	}
	if actor.Type == domain.ActorPersona {
		return fmt.Errorf("set plan policy: a persona não muda a política de plano: %w", domain.ErrNotAllowed)
	}
	return s.withTx(ctx, func(t *txc) error {
		var err error
		switch scope {
		case domain.PlanScopeTask:
			_, err = t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: targetID})
		case domain.PlanScopeProject:
			_, err = t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: targetID})
		default:
			_, err = t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: targetID})
		}
		if err != nil {
			return mapErr(err, string(scope))
		}
		if require == nil {
			err = t.q.DeletePlanPolicy(t.ctx, store.DeletePlanPolicyParams{CompanyID: companyID, Scope: string(scope), TargetID: targetID})
		} else {
			err = t.q.UpsertPlanPolicy(t.ctx, store.UpsertPlanPolicyParams{CompanyID: companyID, Scope: string(scope), TargetID: targetID,
				RequirePlan: *require, UpdatedByType: string(actor.Type), UpdatedByID: actor.ID, Now: t.now})
		}
		if err != nil {
			return fmt.Errorf("set plan policy: %w", err)
		}
		return t.activity(actor, companyID, "plan_policy.updated", string(scope), targetID, map[string]any{"scope": scope, "require_plan": require}, "")
	})
}
