package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Incidentes de orçamento (ADR-0041): a decisão humana em cima do alerta/pausa automáticos de ADR-0019/0020.

type incidentManagedKey struct{}

// withIncidentManaged marca o contexto de uma resolução em curso: o setter do teto não resolve o incidente sozinho
// (auto), porque quem resolve é a própria resolução, com o autor e o motivo.
func withIncidentManaged(ctx context.Context) context.Context {
	return context.WithValue(ctx, incidentManagedKey{}, true)
}

func incidentManaged(ctx context.Context) bool {
	v, _ := ctx.Value(incidentManagedKey{}).(bool)
	return v
}

func incidentFromRow(r store.BudgetIncident) domain.BudgetIncident {
	in := domain.BudgetIncident{ID: r.ID, CompanyID: r.CompanyID, Scope: domain.BudgetScope(r.Scope), TargetID: r.TargetID,
		TargetName: r.TargetName, Month: r.Month, Open: r.Status == "open", BudgetCents: r.BudgetCents, SpentUSD: r.SpentUsd,
		ReleaseRuns: int(r.ReleaseRuns), Note: r.Note, OpenedAt: r.OpenedAt, ResolvedAt: r.ResolvedAt}
	if r.Resolution != nil {
		in.Resolution = domain.IncidentResolution(*r.Resolution)
	}
	if r.ResolvedByType != nil {
		a := actorOf(*r.ResolvedByType, r.ResolvedByID)
		in.ResolvedBy = &a
	}
	return in
}

// openBudgetIncident abre (ou reabre) o incidente do alvo no mês, na transação do alerta de teto estourado.
func (t *txc) openBudgetIncident(company store.Company, tg budgetTarget, month string, spent float64) error {
	row, err := t.q.OpenBudgetIncident(t.ctx, store.OpenBudgetIncidentParams{ID: domain.NewID(), CompanyID: company.ID,
		Scope: tg.scope, TargetID: tg.id, TargetName: tg.name, Month: month, BudgetCents: *tg.budget, SpentUsd: spent, Now: t.now})
	if isNoRows(err) {
		return nil // já aberto neste mês
	}
	if err != nil {
		return fmt.Errorf("open budget incident: %w", err)
	}
	in := incidentFromRow(row)
	if err := t.activity(domain.SystemActor, company.ID, "budget.incident_opened", tg.scope, tg.id, map[string]any{
		"incident_id": in.ID, "scope": tg.scope, "name": tg.name, "month": month, "budget_cents": *tg.budget, "spent_usd": spent}, ""); err != nil {
		return err
	}
	t.publish(companyTopic(company.ID), "budget.incident", in)
	return nil
}

// autoResolveIncidents resolve (auto) os incidentes abertos do alvo cujo teto deixou de estourar.
func (t *txc) autoResolveIncidents(companyID domain.ID, scope string, target domain.ID, why string) error {
	if incidentManaged(t.ctx) {
		return nil
	}
	rows, err := t.q.AutoResolveBudgetIncidentsForTarget(t.ctx, store.AutoResolveBudgetIncidentsForTargetParams{Scope: scope, TargetID: target, Note: why, Now: &t.now})
	if err != nil {
		return fmt.Errorf("auto resolve incidents: %w", err)
	}
	for _, r := range rows {
		if err := t.activity(domain.SystemActor, companyID, "budget.incident_resolved", scope, target, map[string]any{
			"incident_id": r.ID, "resolution": domain.ResolveAuto, "reason": why}, ""); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "budget.incident", incidentFromRow(r))
	}
	return nil
}

// consumeRelease gasta um run avulso liberado para o alvo no mês (true = o run passa mesmo com o teto estourado).
func (t *txc) consumeRelease(tg budgetTarget, month string) (bool, error) {
	_, err := t.q.ConsumeBudgetRelease(t.ctx, store.ConsumeBudgetReleaseParams{Scope: tg.scope, TargetID: tg.id, Month: month})
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("consume budget release: %w", err)
	}
	return true, nil
}

// ListBudgetIncidents lista os incidentes da empresa (abertos primeiro). onlyOpen limita aos abertos.
func (s *Service) ListBudgetIncidents(ctx context.Context, companyID domain.ID, onlyOpen bool, limit int) ([]domain.BudgetIncident, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListBudgetIncidents(ctx, store.ListBudgetIncidentsParams{CompanyID: companyID, OnlyOpen: onlyOpen, Lim: i32(clamp(limit, 100, 500))})
	if err != nil {
		return nil, fmt.Errorf("list budget incidents: %w", err)
	}
	out := make([]domain.BudgetIncident, len(rows))
	for i, r := range rows {
		out[i] = incidentFromRow(r)
	}
	return out, nil
}

// GetBudgetIncident devolve um incidente da empresa.
func (s *Service) GetBudgetIncident(ctx context.Context, companyID, id domain.ID) (domain.BudgetIncident, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.BudgetIncident{}, err
	}
	r, err := s.read().GetBudgetIncident(ctx, store.GetBudgetIncidentParams{CompanyID: companyID, ID: id})
	if err != nil {
		return domain.BudgetIncident{}, mapErr(err, "get budget incident")
	}
	return incidentFromRow(r), nil
}

// ResolveIncidentInput é a decisão sobre um incidente.
type ResolveIncidentInput struct {
	Resolution domain.IncidentResolution
	// BudgetCents é o teto novo (centavos de USD) de ceiling_raised; RemoveCeiling tira o teto de vez.
	BudgetCents   *int64
	RemoveCeiling bool
	Note          string
}

// ResolveBudgetIncident resolve um incidente aberto (humano admin+): sobe o teto, mantém pausado ou libera um run.
func (s *Service) ResolveBudgetIncident(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in ResolveIncidentInput) (domain.BudgetIncident, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.BudgetIncident{}, err
	}
	if actor.Type != domain.ActorHuman {
		return domain.BudgetIncident{}, fmt.Errorf("resolve incident: só um humano resolve um incidente de orçamento: %w", domain.ErrNotAllowed)
	}
	if !in.Resolution.UserChoice() {
		return domain.BudgetIncident{}, domain.Invalid("resolution", "ceiling_raised, kept_paused ou released_once")
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > 500 {
		return domain.BudgetIncident{}, domain.Invalid("note", "até 500 caracteres")
	}
	cur, err := s.GetBudgetIncident(ctx, companyID, id)
	if err != nil {
		return domain.BudgetIncident{}, err
	}
	if !cur.Open {
		return domain.BudgetIncident{}, fmt.Errorf("o incidente já foi resolvido (%s): %w", cur.Resolution, domain.ErrConflict)
	}
	if in.Resolution == domain.ResolveRaiseCeiling {
		if err := s.raiseCeiling(ctx, actor, cur, in); err != nil {
			return domain.BudgetIncident{}, err
		}
	}
	var out domain.BudgetIncident
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.GetBudgetIncidentForUpdate(t.ctx, store.GetBudgetIncidentForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get budget incident")
		}
		if row.Status != "open" {
			return fmt.Errorf("o incidente já foi resolvido (%s): %w", deref(row.Resolution), domain.ErrConflict)
		}
		release := int32(0)
		if in.Resolution == domain.ResolveReleaseOnce {
			release = 1
		}
		res, err := t.q.ResolveBudgetIncident(t.ctx, store.ResolveBudgetIncidentParams{CompanyID: companyID, ID: id,
			Resolution: ptr(string(in.Resolution)), Note: note, ReleaseRuns: release,
			ResolvedByType: ptr(string(actor.Type)), ResolvedByID: actor.ID, Now: &t.now})
		if err != nil {
			return mapErr(err, "resolve budget incident")
		}
		if in.Resolution == domain.ResolveReleaseOnce {
			if err := t.releasePaused(companyID, domain.BudgetScope(row.Scope), row.TargetID); err != nil {
				return err
			}
		}
		out = incidentFromRow(res)
		if err := t.activity(actor, companyID, "budget.incident_resolved", row.Scope, row.TargetID, map[string]any{
			"incident_id": id, "resolution": in.Resolution, "note": note, "budget_cents": in.BudgetCents, "removed": in.RemoveCeiling}, ""); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "budget.incident", out)
		return nil
	})
	return out, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// raiseCeiling muda o teto do alvo do incidente pelo mesmo caminho de sempre (reabre alertas e retoma o que estava
// pausado por orçamento). O novo teto precisa ficar acima do que já se gastou, senão o incidente voltaria na hora.
func (s *Service) raiseCeiling(ctx context.Context, actor domain.Actor, inc domain.BudgetIncident, in ResolveIncidentInput) error {
	if in.RemoveCeiling == (in.BudgetCents != nil) {
		return domain.Invalid("budget_cents", "informe o novo teto (budget_cents) ou remove_ceiling, um dos dois")
	}
	var st BudgetStatus
	var err error
	switch inc.Scope {
	case domain.BudgetScopeCompany:
		st, err = s.CompanyBudget(ctx, inc.CompanyID)
	case domain.BudgetScopeProject:
		st, err = s.ProjectBudget(ctx, inc.CompanyID, inc.TargetID)
	default:
		st, err = s.PersonaBudget(ctx, inc.CompanyID, inc.TargetID)
	}
	if err != nil {
		return err
	}
	if in.BudgetCents != nil && float64(*in.BudgetCents)/100 <= st.SpentUSD {
		return domain.Invalid("budget_cents", fmt.Sprintf("o novo teto (US$ %.2f) não passa do que já foi gasto no mês (US$ %.2f)", float64(*in.BudgetCents)/100, st.SpentUSD))
	}
	mctx := withIncidentManaged(ctx)
	switch inc.Scope {
	case domain.BudgetScopeCompany:
		_, err = s.SetCompanyBudget(mctx, actor, inc.CompanyID, in.BudgetCents)
	case domain.BudgetScopeProject:
		_, err = s.UpdateProject(mctx, actor, inc.CompanyID, inc.TargetID, UpdateProjectInput{BudgetMonthlyCents: in.BudgetCents, ClearBudget: in.RemoveCeiling})
	default:
		_, err = s.UpdatePersona(mctx, actor, inc.CompanyID, inc.TargetID, UpdatePersonaInput{BudgetMonthlyCents: in.BudgetCents, ClearBudget: in.RemoveCeiling})
	}
	return err
}

// releasePaused retoma as personas pausadas por orçamento que o incidente cobre (a persona do incidente, ou as pausadas
// pelo teto da empresa), sem mexer no teto: o crédito de um run avulso (release_runs) é o que as deixa trabalhar.
func (t *txc) releasePaused(companyID domain.ID, scope domain.BudgetScope, target domain.ID) error {
	if scope == domain.BudgetScopeProject {
		return nil // o teto de projeto recusa o run sem pausar ninguém: o crédito basta
	}
	var only *domain.ID
	if scope == domain.BudgetScopePersona {
		only = &target
	}
	rows, err := t.q.ListBudgetPausedPersonas(t.ctx, store.ListBudgetPausedPersonasParams{CompanyID: &companyID, PersonaID: only})
	if err != nil {
		return fmt.Errorf("list budget-paused personas: %w", err)
	}
	co, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "get company")
	}
	for _, p := range rows {
		if scope == domain.BudgetScopeCompany && (p.PauseReason == nil || !strings.Contains(*p.PauseReason, "empresa")) {
			continue
		}
		if _, err := t.resumeBudgetPausedPersona(co, p); err != nil {
			return err
		}
	}
	return nil
}

// sweepBudgetIncidents resolve (auto) os incidentes abertos de meses que já viraram.
func (t *txc) sweepBudgetIncidents() (int, error) {
	rows, err := t.q.AutoResolveStaleBudgetIncidents(t.ctx, &t.now)
	if err != nil {
		return 0, fmt.Errorf("sweep budget incidents: %w", err)
	}
	for _, r := range rows {
		if err := t.activity(domain.SystemActor, r.CompanyID, "budget.incident_resolved", r.Scope, r.TargetID, map[string]any{
			"incident_id": r.ID, "resolution": domain.ResolveAuto, "reason": "month_rolled"}, ""); err != nil {
			return 0, err
		}
		t.publish(companyTopic(r.CompanyID), "budget.incident", incidentFromRow(r))
	}
	return len(rows), nil
}
