package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// Import em empresa existente (ADR-0057): só a configuração entra (workspaces, projetos, assinaturas, skills,
// personas, fluxos e integrações); o histórico (tasks, execuções, artefatos) fica de fora. Cada item que já existe
// segue a ação de conflito: pular, renomear ou substituir.

// importMerge faz a prévia (opts.DryRun) ou o merge em opts.Into. Admin da empresa de destino.
func (s *Service) importMerge(ctx context.Context, actor domain.Actor, doc export.Document, opts ImportOptions) (ImportResult, error) {
	ctx, span := lifecycleSpan(ctx, "company.import_merge")
	defer span.End()
	cid := *opts.Into
	if err := s.require(ctx, cid, domain.MemberAdmin); err != nil {
		return ImportResult{}, err
	}
	p, err := s.planImport(ctx, doc, opts)
	if err != nil {
		return ImportResult{}, err
	}
	warnings := importWarnings(doc, p, opts.Active, true)

	if opts.DryRun {
		q := s.read()
		co, err := q.GetCompanyByID(ctx, cid)
		if err != nil {
			return ImportResult{}, mapErr(err, "get company")
		}
		if co.ArchivedAt != nil {
			return ImportResult{}, domain.ErrCompanyArchived
		}
		ex, _, err := loadExisting(ctx, q, cid)
		if err != nil {
			return ImportResult{}, err
		}
		plan, err := planMerge(doc, ex, opts.OnConflict, opts.Resolve)
		if err != nil {
			return ImportResult{}, err
		}
		if err := s.checkMergeOrg(ctx, q, cid, doc, plan, nil); err != nil {
			return ImportResult{}, err
		}
		return ImportResult{Company: companyFromRow(co), Counts: plan.written(), Items: plan.items, Warnings: warnings, DryRun: true, Merged: true}, nil
	}

	var res ImportResult
	err = s.withTx(ctx, func(t *txc) error {
		co, err := t.q.GetCompanyByIDForUpdate(ctx, cid)
		if err != nil {
			return mapErr(err, "get company")
		}
		if co.ArchivedAt != nil {
			return domain.ErrCompanyArchived
		}
		ex, rows, err := loadExisting(ctx, t.q, cid)
		if err != nil {
			return err
		}
		plan, err := planMerge(doc, ex, opts.OnConflict, opts.Resolve)
		if err != nil {
			return err
		}
		ids, err := t.applyMerge(cid, doc, p, plan, rows, opts)
		if err != nil {
			return err
		}
		if err := s.checkMergeOrg(ctx, t.q, cid, doc, plan, ids.personas); err != nil {
			return err
		}
		res = ImportResult{Company: companyFromRow(co), Counts: plan.written(), Items: plan.items, Warnings: warnings, Merged: true}
		return t.activity(actor, cid, "company.import_merged", "company", cid, map[string]any{
			"source_slug": doc.Company.Slug, "format": export.Format, "paused": !opts.Active, "counts": res.Counts,
			"actions": plan.tally(), "on_conflict": string(plan.defaultAction(opts))}, "")
	})
	return res, err
}

func (m mergePlan) defaultAction(opts ImportOptions) domain.ConflictAction {
	if opts.OnConflict == "" {
		return domain.ConflictSkip
	}
	return opts.OnConflict
}

// existingRows são as linhas do destino, indexadas pela chave de conflito de cada tipo.
type existingRows struct {
	workspaces    map[string]store.Workspace
	projects      map[string]store.Project
	subscriptions map[string]store.Subscription
	skills        map[string]store.Skill
	personas      map[string]store.Persona
	flows         map[string]store.Flow
	integrations  map[string]store.Integration
}

func loadExisting(ctx context.Context, q *store.Queries, cid domain.ID) (existingKeys, existingRows, error) {
	var r existingRows
	ex := existingKeys{}
	var err error
	ws, err := q.ListWorkspaces(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list workspaces: %w", err)
	}
	r.workspaces, ex[KindWorkspaces] = indexRows(ws, func(x store.Workspace) string { return x.Slug })
	pr, err := q.ListProjects(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list projects: %w", err)
	}
	r.projects, ex[KindProjects] = indexRows(pr, func(x store.Project) string { return x.Slug })
	su, err := q.ListSubscriptions(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list subscriptions: %w", err)
	}
	r.subscriptions, ex[KindSubscriptions] = indexRows(su, func(x store.Subscription) string { return x.Name })
	sk, err := q.ListSkills(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list skills: %w", err)
	}
	r.skills, ex[KindSkills] = indexRows(sk, func(x store.Skill) string { return x.Slug })
	pe, err := q.ListPersonas(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list personas: %w", err)
	}
	r.personas, ex[KindPersonas] = indexRows(pe, func(x store.Persona) string { return x.Slug })
	fl, err := q.ListFlows(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list flows: %w", err)
	}
	r.flows, ex[KindFlows] = indexRows(fl, func(x store.Flow) string { return x.Slug })
	in, err := q.ListIntegrations(ctx, cid)
	if err != nil {
		return nil, r, fmt.Errorf("list integrations: %w", err)
	}
	r.integrations, ex[KindIntegrations] = indexRows(in, func(x store.Integration) string { return x.Slug })
	return ex, r, nil
}

func indexRows[T any](rows []T, key func(T) string) (map[string]T, map[string]bool) {
	m, set := make(map[string]T, len(rows)), make(map[string]bool, len(rows))
	for _, r := range rows {
		m[key(r)] = r
		set[key(r)] = true
	}
	return m, set
}

// mergedIDs são os ids de destino de cada item do arquivo (novos, existentes pulados/substituídos).
type mergedIDs struct {
	ws, pr, sub, sk, personas, integ map[string]domain.ID
}

// applyMerge grava o plano na transação aberta. O arquivo já foi validado por planImport.
func (t *txc) applyMerge(cid domain.ID, doc export.Document, p importPlan, plan mergePlan, ex existingRows, opts ImportOptions) (mergedIDs, error) {
	ctx := t.ctx
	ids := mergedIDs{ws: map[string]domain.ID{}, pr: map[string]domain.ID{}, sub: map[string]domain.ID{}, sk: map[string]domain.ID{},
		personas: map[string]domain.ID{}, integ: map[string]domain.ID{}}

	// workspaces
	for _, w := range doc.Workspaces {
		d := plan.decision(KindWorkspaces, w.Slug)
		path := strings.TrimSpace(w.Path)
		if p.clearedPaths[w.Slug] {
			path = ""
		}
		switch d.Action {
		case domain.ItemSkip:
			ids.ws[w.Slug] = ex.workspaces[w.Slug].ID
		case domain.ItemReplace:
			cur := ex.workspaces[w.Slug]
			if path == "" {
				path = cur.Path // um caminho que não vale aqui não apaga o que já funciona
			}
			if _, err := t.q.UpdateWorkspace(ctx, store.UpdateWorkspaceParams{Name: strings.TrimSpace(w.Name), Path: path,
				RepoUrl: strings.TrimSpace(w.RepoURL), DefaultBranch: strings.TrimSpace(w.DefaultBranch), Now: t.now, CompanyID: cid, ID: cur.ID}); err != nil {
				return ids, mapErr(err, "replace workspace "+w.Slug)
			}
			ids.ws[w.Slug] = cur.ID
		default:
			id := domain.NewID()
			if _, err := t.q.ImportWorkspace(ctx, store.ImportWorkspaceParams{ID: id, CompanyID: cid, Slug: d.Target, Name: strings.TrimSpace(w.Name),
				Path: path, RepoUrl: strings.TrimSpace(w.RepoURL), DefaultBranch: strings.TrimSpace(w.DefaultBranch),
				CreatedAt: t.now, UpdatedAt: t.now}); err != nil {
				return ids, mapErr(err, "import workspace "+w.Slug)
			}
			ids.ws[w.Slug] = id
		}
	}

	// projetos
	for _, x := range doc.Projects {
		d := plan.decision(KindProjects, x.Slug)
		status := x.Status
		if status == "" {
			status = string(domain.ProjectActive)
		}
		switch d.Action {
		case domain.ItemSkip:
			ids.pr[x.Slug] = ex.projects[x.Slug].ID
		case domain.ItemReplace:
			cur := ex.projects[x.Slug]
			if _, err := t.q.UpdateProject(ctx, store.UpdateProjectParams{Name: strings.TrimSpace(x.Name), Description: x.Description,
				WorkspaceID: optID(ids.ws, x.Workspace), Status: status, BudgetMonthlyCents: x.BudgetMonthlyCents, Now: t.now, CompanyID: cid, ID: cur.ID}); err != nil {
				return ids, mapErr(err, "replace project "+x.Slug)
			}
			ids.pr[x.Slug] = cur.ID
		default:
			id := domain.NewID()
			if _, err := t.q.ImportProject(ctx, store.ImportProjectParams{ID: id, CompanyID: cid, Slug: d.Target, Name: strings.TrimSpace(x.Name),
				Description: x.Description, WorkspaceID: optID(ids.ws, x.Workspace), Status: status, BudgetMonthlyCents: x.BudgetMonthlyCents,
				CreatedAt: t.now, UpdatedAt: t.now}); err != nil {
				return ids, mapErr(err, "import project "+x.Slug)
			}
			ids.pr[x.Slug] = id
		}
	}

	// assinaturas
	for _, x := range doc.Subscriptions {
		name := strings.TrimSpace(x.Name)
		d := plan.decision(KindSubscriptions, name)
		pct := x.WeeklyGuardPct
		if pct == 0 {
			pct = defaultGuardPct
		}
		switch d.Action {
		case domain.ItemSkip:
			ids.sub[name] = ex.subscriptions[name].ID
		case domain.ItemReplace:
			cur := ex.subscriptions[name]
			if _, err := t.q.UpdateSubscription(ctx, store.UpdateSubscriptionParams{Name: name, Agent: x.Agent, MonthlyUsd: x.MonthlyUSD,
				ModelPrefixes: normalizePrefixes(x.ModelPrefixes), WeeklyLimitUsd: x.WeeklyLimitUSD, WeeklyGuardPct: i32(pct), Active: x.Active,
				EndedAt: x.EndedAt, Now: t.now, CompanyID: cid, ID: cur.ID}); err != nil {
				return ids, mapErr(err, "replace subscription "+name)
			}
			ids.sub[name] = cur.ID
		default:
			id := domain.NewID()
			if _, err := t.q.ImportSubscription(ctx, store.ImportSubscriptionParams{ID: id, CompanyID: cid, Name: d.Target, Agent: x.Agent,
				MonthlyUsd: x.MonthlyUSD, ModelPrefixes: normalizePrefixes(x.ModelPrefixes), WeeklyLimitUsd: x.WeeklyLimitUSD,
				WeeklyGuardPct: i32(pct), Active: x.Active, EndedAt: x.EndedAt, CreatedAt: t.now, UpdatedAt: t.now}); err != nil {
				return ids, mapErr(err, "import subscription "+name)
			}
			for _, per := range x.Periods {
				if err := t.q.ImportSubscriptionPeriod(ctx, store.ImportSubscriptionPeriodParams{SubscriptionID: id,
					StartedAt: per.StartedAt, EndedAt: per.EndedAt}); err != nil {
					return ids, mapErr(err, "import subscription period")
				}
			}
			ids.sub[name] = id
		}
	}

	// skills (antes das personas: elas as ligam)
	for i, k := range doc.Skills {
		d := plan.decision(KindSkills, k.Slug)
		ps := p.si.skills[i]
		switch d.Action {
		case domain.ItemSkip:
			ids.sk[k.Slug] = ex.skills[k.Slug].ID
		case domain.ItemReplace:
			cur := ex.skills[k.Slug]
			if cur.Name == ps.name && cur.Description == ps.desc && cur.Body == ps.body {
				ids.sk[k.Slug] = cur.ID // igual: não sobe versão
				continue
			}
			row, err := t.q.UpdateSkill(ctx, store.UpdateSkillParams{CompanyID: cid, ID: cur.ID, Name: ps.name, Description: ps.desc,
				Body: ps.body, Version: cur.Version + 1, ActorType: string(domain.ActorSystem), Now: t.now})
			if err != nil {
				return ids, mapErr(err, "replace skill "+k.Slug)
			}
			if row.SourceID != nil && !row.SourceModified {
				if err := t.q.SetSkillSourceModified(ctx, store.SetSkillSourceModifiedParams{CompanyID: cid, ID: row.ID, Modified: true}); err != nil {
					return ids, fmt.Errorf("mark skill modified: %w", err)
				}
			}
			if err := t.recordSkillVersion(row); err != nil {
				return ids, err
			}
			ids.sk[k.Slug] = cur.ID
		default:
			id := domain.NewID()
			row, err := t.q.CreateSkill(ctx, store.CreateSkillParams{ID: id, CompanyID: cid, Slug: d.Target, Name: ps.name,
				Description: ps.desc, Body: ps.body, ActorType: string(domain.ActorSystem), Now: t.now})
			if err != nil {
				return ids, mapErr(err, "import skill "+k.Slug)
			}
			if err := t.recordSkillVersion(row); err != nil {
				return ids, err
			}
			ids.sk[k.Slug] = id
		}
	}

	// personas
	touched := map[string]bool{} // criadas, renomeadas ou substituídas: ganham organograma e skills do arquivo
	for i, x := range doc.Personas {
		d := plan.decision(KindPersonas, x.Slug)
		status := x.Status
		if !opts.Active && (status == string(domain.PersonaActive) || status == string(domain.PersonaIdle)) {
			status = string(domain.PersonaPaused)
		}
		switch d.Action {
		case domain.ItemSkip:
			ids.personas[x.Slug] = ex.personas[x.Slug].ID
			continue
		case domain.ItemReplace:
			cur := ex.personas[x.Slug]
			if _, err := t.q.ReplaceImportedPersona(ctx, store.ReplaceImportedPersonaParams{CompanyID: cid, ID: cur.ID, Name: strings.TrimSpace(x.Name),
				Title: strings.TrimSpace(x.Title), Instructions: x.Instructions, Agent: x.Agent, Model: strings.TrimSpace(x.Model),
				AdapterConfig: p.personaCfg[i], WorkspaceID: optID(ids.ws, x.Workspace), HeartbeatIntervalSec: i32(x.HeartbeatIntervalSec),
				BudgetMonthlyCents: x.BudgetMonthlyCents, SubscriptionID: optID(ids.sub, strings.TrimSpace(x.Subscription)),
				RequireReview: x.RequireReview, ReviewerAgent: strings.TrimSpace(x.ReviewerAgent), ReviewerModel: strings.TrimSpace(x.ReviewerModel),
				Icon: importIcon(x.Icon), Now: t.now}); err != nil {
				return ids, mapErr(err, "replace persona "+x.Slug)
			}
			ids.personas[x.Slug] = cur.ID
		default:
			id := domain.NewID()
			if _, err := t.q.ImportPersona(ctx, store.ImportPersonaParams{ID: id, CompanyID: cid, Slug: d.Target, Name: strings.TrimSpace(x.Name),
				Title: strings.TrimSpace(x.Title), Instructions: x.Instructions, Agent: x.Agent, Model: strings.TrimSpace(x.Model),
				AdapterConfig: p.personaCfg[i], Status: exportStatus(status), WorkspaceID: optID(ids.ws, x.Workspace),
				HeartbeatIntervalSec: i32(x.HeartbeatIntervalSec), BudgetMonthlyCents: x.BudgetMonthlyCents,
				SubscriptionID: optID(ids.sub, strings.TrimSpace(x.Subscription)), RequireReview: x.RequireReview,
				ReviewerAgent: strings.TrimSpace(x.ReviewerAgent), ReviewerModel: strings.TrimSpace(x.ReviewerModel),
				Icon: importIcon(x.Icon), CreatedAt: t.now, UpdatedAt: t.now}); err != nil {
				return ids, mapErr(err, "import persona "+x.Slug)
			}
			ids.personas[x.Slug] = id
		}
		touched[x.Slug] = true
	}
	for _, x := range doc.Personas {
		if !touched[x.Slug] {
			continue
		}
		if err := t.q.LinkImportedPersona(ctx, store.LinkImportedPersonaParams{ID: ids.personas[x.Slug],
			ReportsTo: optID(ids.personas, x.ReportsTo), ReviewerPersonaID: optID(ids.personas, x.Reviewer)}); err != nil {
			return ids, mapErr(err, "link persona "+x.Slug)
		}
		for _, sl := range x.Skills {
			if err := t.q.SetPersonaSkill(ctx, store.SetPersonaSkillParams{CompanyID: cid, PersonaID: ids.personas[x.Slug],
				SkillID: ids.sk[sl], Enabled: true, Now: t.now}); err != nil {
				return ids, mapErr(err, "import persona skill")
			}
		}
	}

	// fluxos
	for _, x := range doc.Flows {
		d := plan.decision(KindFlows, x.Slug)
		if d.Action == domain.ItemSkip {
			continue
		}
		enabled := x.Enabled && opts.Active
		next, err := NextCron(x.Cron, x.Timezone, t.now)
		if err != nil {
			return ids, err
		}
		if d.Action == domain.ItemReplace {
			cur := ex.flows[x.Slug]
			if _, err := t.q.UpdateFlow(ctx, store.UpdateFlowParams{Name: strings.TrimSpace(x.Name), Description: x.Description,
				Cron: strings.TrimSpace(x.Cron), Timezone: x.Timezone, Enabled: enabled, PersonaID: ids.personas[x.Persona],
				ProjectID: optID(ids.pr, x.Project), Priority: x.Priority, TaskTitle: x.TaskTitle, TaskDescription: x.TaskDescription,
				NextFireAt: &next, Now: t.now, CompanyID: cid, ID: cur.ID}); err != nil {
				return ids, mapErr(err, "replace flow "+x.Slug)
			}
			continue
		}
		if _, err := t.q.ImportFlow(ctx, store.ImportFlowParams{ID: domain.NewID(), CompanyID: cid, Slug: d.Target, Name: strings.TrimSpace(x.Name),
			Description: x.Description, Cron: strings.TrimSpace(x.Cron), Timezone: x.Timezone, Enabled: enabled, PersonaID: ids.personas[x.Persona],
			ProjectID: optID(ids.pr, x.Project), Priority: x.Priority, TaskTitle: x.TaskTitle, TaskDescription: x.TaskDescription,
			NextFireAt: &next, CreatedAt: t.now, UpdatedAt: t.now}); err != nil {
			return ids, mapErr(err, "import flow "+x.Slug)
		}
	}

	// integrações: entram desligadas e sem segredos (substituir também desliga e zera o segredo do arquivo, que não o traz)
	for i, x := range doc.Integrations {
		d := plan.decision(KindIntegrations, x.Slug)
		if d.Action == domain.ItemSkip {
			ids.integ[x.Slug] = ex.integrations[x.Slug].ID
			continue
		}
		events := []string{}
		if x.Kind == string(domain.IntegrationWebhook) {
			events, _ = validateEvents(x.Events)
		}
		allPersonas := x.AllPersonas && x.Kind == string(domain.IntegrationMCP)
		var id domain.ID
		if d.Action == domain.ItemReplace {
			cur := ex.integrations[x.Slug]
			if cur.Kind != x.Kind {
				return ids, domain.Invalid("integrations."+x.Slug+".kind", fmt.Sprintf("existing integration is %q, file has %q: rename it instead", cur.Kind, x.Kind))
			}
			if _, err := t.q.UpdateIntegration(ctx, store.UpdateIntegrationParams{Name: strings.TrimSpace(x.Name), Enabled: false,
				AllPersonas: allPersonas, Config: p.si.integrations[i].cfg, Events: events, SecretEnc: cur.SecretEnc, SecretHint: cur.SecretHint,
				Now: t.now, CompanyID: cid, ID: cur.ID}); err != nil {
				return ids, mapErr(err, "replace integration "+x.Slug)
			}
			if err := t.q.ClearIntegrationPersonas(ctx, cur.ID); err != nil {
				return ids, mapErr(err, "clear integration personas")
			}
			id = cur.ID
		} else {
			id = domain.NewID()
			if _, err := t.q.CreateIntegration(ctx, store.CreateIntegrationParams{ID: id, CompanyID: cid, Kind: x.Kind, Slug: d.Target,
				Name: strings.TrimSpace(x.Name), Enabled: false, AllPersonas: allPersonas, Config: p.si.integrations[i].cfg, Events: events,
				ActorType: string(domain.ActorSystem), Now: t.now}); err != nil {
				return ids, mapErr(err, "import integration "+x.Slug)
			}
		}
		ids.integ[x.Slug] = id
		if x.Kind != string(domain.IntegrationMCP) {
			continue
		}
		for _, sl := range x.Personas {
			if err := t.q.SetIntegrationPersona(ctx, store.SetIntegrationPersonaParams{CompanyID: cid, IntegrationID: id, PersonaID: ids.personas[sl]}); err != nil {
				return ids, mapErr(err, "import integration persona")
			}
		}
	}
	return ids, nil
}

// checkMergeOrg revalida o organograma da empresa depois do merge: religar personas a personas que já existiam pode
// fechar um ciclo (ids != nil: já gravado, lê o banco; nil: prévia, simula com o arquivo sobre o que existe).
func (s *Service) checkMergeOrg(ctx context.Context, q *store.Queries, cid domain.ID, doc export.Document, plan mergePlan, ids map[string]domain.ID) error {
	rows, err := q.ListPersonas(ctx, cid)
	if err != nil {
		return fmt.Errorf("list personas: %w", err)
	}
	parents := map[domain.ID]domain.ID{}
	if ids != nil {
		for _, r := range rows {
			if r.ReportsTo != nil {
				parents[r.ID] = *r.ReportsTo
			}
		}
	} else {
		bySlug := map[string]domain.ID{}
		for _, r := range rows {
			bySlug[r.Slug] = r.ID
			if r.ReportsTo != nil {
				parents[r.ID] = *r.ReportsTo
			}
		}
		// ids simulados para o que o arquivo cria; o que ele pula ou substitui usa o id existente
		sim := map[string]domain.ID{}
		for _, x := range doc.Personas {
			d := plan.decision(KindPersonas, x.Slug)
			switch d.Action {
			case domain.ItemSkip, domain.ItemReplace:
				sim[x.Slug] = bySlug[x.Slug]
			default:
				sim[x.Slug] = domain.NewID()
			}
		}
		for _, x := range doc.Personas {
			d := plan.decision(KindPersonas, x.Slug)
			if d.Action == domain.ItemSkip {
				continue
			}
			if x.ReportsTo == "" {
				delete(parents, sim[x.Slug])
				continue
			}
			parents[sim[x.Slug]] = sim[x.ReportsTo]
		}
	}
	if orgCycle(parents) != "" {
		return fmt.Errorf("o organograma resultante teria um ciclo (reports_to): %w", domain.ErrConflict)
	}
	return nil
}
