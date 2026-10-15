package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Projetos (ADR-0025): agrupam tasks, podem ter um workspace (o diretório das runs das suas tasks) e um orçamento
// mensal próprio. Quem gerencia projetos é admin; member cria e edita tasks neles (inclusive escolhendo o projeto).

// UpdateProjectInput altera um projeto; campo nil fica como está.
type UpdateProjectInput struct {
	Name, Description *string
	WorkspaceID       *domain.ID
	ClearWorkspace    bool
	Status            *domain.ProjectStatus
	// BudgetMonthlyCents define o teto mensal (centavos de USD); ClearBudget o remove.
	BudgetMonthlyCents *int64
	ClearBudget        bool
}

// ProjectPersona é uma persona com tasks no projeto.
type ProjectPersona struct {
	ID        domain.ID `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Tasks     int       `json:"tasks"`
	OpenTasks int       `json:"open_tasks"`
}

// ProjectView é um projeto com o workspace, o progresso das tasks e o custo do mês.
type ProjectView struct {
	domain.Project
	WorkspaceSlug string `json:"workspace_slug,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
	// Tasks conta as tasks por status (todos os status presentes, com zero).
	Tasks      map[domain.TaskStatus]int `json:"tasks"`
	TasksTotal int                       `json:"tasks_total"`
	// Progress é done / (total - canceladas), de 0 a 100; 0 sem tasks.
	Progress float64 `json:"progress_percent"`
	// SpentUSD é o gasto do mês corrente (fuso da empresa) das runs das tasks do projeto.
	SpentUSD float64      `json:"spent_usd"`
	Budget   BudgetStatus `json:"budget"`
	// Personas só vem no detalhe (GetProjectView).
	Personas []ProjectPersona `json:"personas,omitempty"`
}

func newProjectView(p store.Project, ws *store.Workspace, counts map[domain.TaskStatus]int, spent float64) ProjectView {
	v := ProjectView{Project: projectFromRow(p), Tasks: map[domain.TaskStatus]int{}, SpentUSD: spent,
		Budget: budgetStatus(p.BudgetMonthlyCents, spent)}
	for _, st := range domain.TaskStatuses {
		v.Tasks[st] = counts[st]
		v.TasksTotal += counts[st]
	}
	if denom := v.TasksTotal - v.Tasks[domain.TaskCancelled]; denom > 0 {
		v.Progress = float64(v.Tasks[domain.TaskDone]) / float64(denom) * 100
	}
	if ws != nil {
		v.WorkspaceSlug, v.WorkspaceName = ws.Slug, ws.Name
	}
	return v
}

func checkProjectBudget(cents *int64) error {
	if cents != nil && *cents < 0 {
		return domain.Invalid("budget_monthly_cents", "must be >= 0")
	}
	return nil
}

func (s *Service) CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateProjectInput) (domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Project{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Project{}, domain.Invalid("name", "required")
	}
	if len(name) > 120 {
		return domain.Project{}, domain.Invalid("name", "too long (max 120)")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return domain.Project{}, err
	}
	if err := checkProjectBudget(in.BudgetMonthlyCents); err != nil {
		return domain.Project{}, err
	}
	var out domain.Project
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if in.WorkspaceID != nil {
			if _, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *in.WorkspaceID}); err != nil {
				return domain.Invalid("workspace_id", "workspace not found in company")
			}
		}
		row, err := t.q.CreateProject(ctx, store.CreateProjectParams{ID: domain.NewID(), CompanyID: companyID,
			Slug: slug, Name: name, Description: strings.TrimSpace(in.Description), WorkspaceID: in.WorkspaceID,
			Status: string(domain.ProjectActive), BudgetMonthlyCents: in.BudgetMonthlyCents, Now: t.now})
		if err != nil {
			return mapErr(err, "create project")
		}
		out = projectFromRow(row)
		return t.activity(actor, companyID, "project.created", "project", out.ID,
			map[string]any{"slug": out.Slug, "name": out.Name, "workspace_id": out.WorkspaceID, "budget_monthly_cents": out.BudgetMonthlyCents}, "")
	})
	return out, err
}

// UpdateProject altera nome, descrição, workspace, status (ativo/arquivado) e orçamento mensal do projeto; o slug
// não muda. Mudar o teto reabre os alertas do mês que ele deixou de alcançar.
func (s *Service) UpdateProject(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in UpdateProjectInput) (domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Project{}, err
	}
	if err := checkProjectBudget(in.BudgetMonthlyCents); err != nil {
		return domain.Project{}, err
	}
	if in.Status != nil && !in.Status.Valid() {
		return domain.Project{}, domain.Invalid("status", fmt.Sprintf("unknown status %q", *in.Status))
	}
	if in.WorkspaceID != nil && in.ClearWorkspace {
		return domain.Project{}, domain.Invalid("workspace_id", "cannot set and clear the workspace at once")
	}
	if in.BudgetMonthlyCents != nil && in.ClearBudget {
		return domain.Project{}, domain.Invalid("budget_monthly_cents", "cannot set and clear the budget at once")
	}
	var out domain.Project
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID})
		if err != nil {
			return mapErr(err, "get project")
		}
		p := store.UpdateProjectParams{CompanyID: companyID, ID: projectID, Name: cur.Name, Description: cur.Description,
			WorkspaceID: cur.WorkspaceID, Status: cur.Status, BudgetMonthlyCents: cur.BudgetMonthlyCents, Now: t.now}
		changed := map[string]any{}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" {
				return domain.Invalid("name", "must not be empty")
			}
			if len(n) > 120 {
				return domain.Invalid("name", "too long (max 120)")
			}
			if n != cur.Name {
				p.Name, changed["name"] = n, n
			}
		}
		if in.Description != nil {
			if d := strings.TrimSpace(*in.Description); d != cur.Description {
				p.Description, changed["description"] = d, true
			}
		}
		switch {
		case in.ClearWorkspace && cur.WorkspaceID != nil:
			p.WorkspaceID, changed["workspace_id"] = nil, nil
		case in.WorkspaceID != nil && (cur.WorkspaceID == nil || *cur.WorkspaceID != *in.WorkspaceID):
			if _, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *in.WorkspaceID}); err != nil {
				return domain.Invalid("workspace_id", "workspace not found in company")
			}
			p.WorkspaceID, changed["workspace_id"] = in.WorkspaceID, in.WorkspaceID
		}
		if in.Status != nil && string(*in.Status) != cur.Status {
			p.Status, changed["status"] = string(*in.Status), string(*in.Status)
		}
		budgetChanged := false
		switch {
		case in.ClearBudget && cur.BudgetMonthlyCents != nil:
			p.BudgetMonthlyCents, budgetChanged = nil, true
		case in.BudgetMonthlyCents != nil && !sameCents(cur.BudgetMonthlyCents, in.BudgetMonthlyCents):
			p.BudgetMonthlyCents, budgetChanged = in.BudgetMonthlyCents, true
		}
		if budgetChanged {
			changed["budget_monthly_cents"] = p.BudgetMonthlyCents
		}
		if len(changed) == 0 {
			out = projectFromRow(cur)
			return nil
		}
		row, err := t.q.UpdateProject(ctx, p)
		if err != nil {
			return mapErr(err, "update project")
		}
		out = projectFromRow(row)
		if err := t.activity(actor, companyID, "project.updated", "project", out.ID, changed, ""); err != nil {
			return err
		}
		if budgetChanged {
			co, err := t.q.GetCompanyByID(ctx, companyID)
			if err != nil {
				return mapErr(err, "get company")
			}
			return t.resetBudgetAlerts(co, out.ID, out.BudgetMonthlyCents, nil, &out.ID)
		}
		return nil
	})
	return out, err
}

func (s *Service) ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListProjects(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]domain.Project, len(rows))
	for i, r := range rows {
		out[i] = projectFromRow(r)
	}
	return out, nil
}

// ListProjectViews lista os projetos com workspace, progresso e custo do mês (ativos primeiro).
func (s *Service) ListProjectViews(ctx context.Context, companyID domain.ID) ([]ProjectView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return nil, mapErr(err, "get company")
	}
	projects, err := q.ListProjects(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	workspaces, err := q.ListWorkspaces(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	wsByID := make(map[domain.ID]store.Workspace, len(workspaces))
	for _, w := range workspaces {
		wsByID[w.ID] = w
	}
	counts, err := q.ProjectTaskCountsAll(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("project task counts: %w", err)
	}
	byProject := map[domain.ID]map[domain.TaskStatus]int{}
	for _, c := range counts {
		if c.ProjectID == nil {
			continue
		}
		m := byProject[*c.ProjectID]
		if m == nil {
			m = map[domain.TaskStatus]int{}
			byProject[*c.ProjectID] = m
		}
		m[domain.TaskStatus(c.Status)] = int(c.N)
	}
	from, to, _ := monthWindow(co.Timezone, s.Now())
	spend, err := q.SpendByProject(ctx, store.SpendByProjectParams{CompanyID: companyID, FromTs: from, ToTs: to})
	if err != nil {
		return nil, fmt.Errorf("project spend: %w", err)
	}
	spentBy := map[domain.ID]float64{}
	for _, r := range spend {
		if r.ProjectID != nil {
			spentBy[*r.ProjectID] = r.Spend
		}
	}
	active := make([]ProjectView, 0, len(projects))
	var archived []ProjectView
	for _, p := range projects {
		var ws *store.Workspace
		if p.WorkspaceID != nil {
			if w, ok := wsByID[*p.WorkspaceID]; ok {
				ws = &w
			}
		}
		v := newProjectView(p, ws, byProject[p.ID], spentBy[p.ID])
		if v.Status == domain.ProjectArchived {
			archived = append(archived, v)
		} else {
			active = append(active, v)
		}
	}
	return append(active, archived...), nil
}

// GetProjectView é o detalhe do projeto: workspace, progresso, custo do mês e as personas envolvidas.
func (s *Service) GetProjectView(ctx context.Context, companyID domain.ID, ref string) (ProjectView, error) {
	pr, err := s.GetProject(ctx, companyID, ref)
	if err != nil {
		return ProjectView{}, err
	}
	q := s.read()
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return ProjectView{}, mapErr(err, "get company")
	}
	row, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: pr.ID})
	if err != nil {
		return ProjectView{}, mapErr(err, "get project")
	}
	var ws *store.Workspace
	if row.WorkspaceID != nil {
		w, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *row.WorkspaceID})
		if err == nil {
			ws = &w
		} else if !isNoRows(err) {
			return ProjectView{}, fmt.Errorf("get workspace: %w", err)
		}
	}
	counts, err := q.ProjectTaskCounts(ctx, store.ProjectTaskCountsParams{CompanyID: companyID, ProjectID: &pr.ID})
	if err != nil {
		return ProjectView{}, fmt.Errorf("project task counts: %w", err)
	}
	m := map[domain.TaskStatus]int{}
	for _, c := range counts {
		m[domain.TaskStatus(c.Status)] = int(c.N)
	}
	from, to, _ := monthWindow(co.Timezone, s.Now())
	spent, err := q.SpendBetweenProject(ctx, store.SpendBetweenProjectParams{CompanyID: companyID, ProjectID: &pr.ID, FromTs: from, ToTs: to})
	if err != nil {
		return ProjectView{}, fmt.Errorf("project spend: %w", err)
	}
	v := newProjectView(row, ws, m, spent)
	ps, err := q.ProjectPersonas(ctx, store.ProjectPersonasParams{CompanyID: companyID, ProjectID: &pr.ID})
	if err != nil {
		return ProjectView{}, fmt.Errorf("project personas: %w", err)
	}
	for _, p := range ps {
		v.Personas = append(v.Personas, ProjectPersona{ID: p.ID, Slug: p.Slug, Name: p.Name, Tasks: int(p.Tasks), OpenTasks: int(p.OpenTasks)})
	}
	return v, nil
}

// GetProject resolves ref as UUID or slug within the company.
func (s *Service) GetProject(ctx context.Context, companyID domain.ID, ref string) (domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Project{}, err
	}
	q := s.read()
	var row store.Project
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: id})
	} else {
		row, err = q.GetProjectBySlug(ctx, store.GetProjectBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return domain.Project{}, mapErr(err, "get project")
	}
	return projectFromRow(row), nil
}

// ProjectBudget é o gasto do mês do projeto contra o teto dele.
func (s *Service) ProjectBudget(ctx context.Context, companyID, projectID domain.ID) (BudgetStatus, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return BudgetStatus{}, err
	}
	q := s.read()
	pr, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID})
	if err != nil {
		return BudgetStatus{}, mapErr(err, "get project")
	}
	co, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return BudgetStatus{}, mapErr(err, "get company")
	}
	from, to, _ := monthWindow(co.Timezone, s.Now())
	spent, err := q.SpendBetweenProject(ctx, store.SpendBetweenProjectParams{CompanyID: companyID, ProjectID: &projectID, FromTs: from, ToTs: to})
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("project spend: %w", err)
	}
	return budgetStatus(pr.BudgetMonthlyCents, spent), nil
}
