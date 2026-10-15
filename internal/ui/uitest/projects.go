package uitest

import (
	"context"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Projetos e workspaces do Fake (ADR-0025): as visões (contagens, progresso, uso) são calculadas das tasks e
// dos vínculos em memória; as validações de caminho ficam no service real (aqui só o essencial).

func (f *Fake) projectView(p domain.Project, detail bool) service.ProjectView {
	if p.Status == "" {
		p.Status = domain.ProjectActive
	}
	counts := map[domain.TaskStatus]int{}
	spent := 0.0
	personas := map[domain.ID]*service.ProjectPersona{}
	for _, t := range f.Tasks {
		if t.ProjectID == nil || *t.ProjectID != p.ID {
			continue
		}
		counts[t.Status]++
		if detail && t.PersonaID != nil {
			pp := personas[*t.PersonaID]
			if pp == nil {
				pp = &service.ProjectPersona{ID: *t.PersonaID, Name: f.personaName(t.PersonaID)}
				for _, pe := range f.Personas {
					if pe.ID == *t.PersonaID {
						pp.Slug = pe.Slug
					}
				}
				personas[*t.PersonaID] = pp
			}
			pp.Tasks++
			if t.Status != domain.TaskDone && t.Status != domain.TaskCancelled {
				pp.OpenTasks++
			}
		}
	}
	for _, e := range f.Ledger {
		if e.Project == p.Name && !e.At.Before(time.Date(Now().Year(), Now().Month(), 1, 0, 0, 0, 0, time.UTC)) {
			spent += e.CostUSD
		}
	}
	v := service.ProjectView{Project: p, Tasks: map[domain.TaskStatus]int{}, SpentUSD: spent}
	for _, st := range domain.TaskStatuses {
		v.Tasks[st] = counts[st]
		v.TasksTotal += counts[st]
	}
	if denom := v.TasksTotal - v.Tasks[domain.TaskCancelled]; denom > 0 {
		v.Progress = float64(v.Tasks[domain.TaskDone]) / float64(denom) * 100
	}
	v.Budget = service.BudgetStatus{BudgetCents: p.BudgetMonthlyCents, SpentUSD: spent, State: service.BudgetNone}
	if p.BudgetMonthlyCents != nil {
		v.Budget.State = service.BudgetOK
		if *p.BudgetMonthlyCents > 0 {
			v.Budget.Percent = spent * 100 / (float64(*p.BudgetMonthlyCents) / 100)
		}
		switch {
		case *p.BudgetMonthlyCents <= 0 || v.Budget.Percent >= 100:
			v.Budget.State = service.BudgetExceeded
		case v.Budget.Percent >= 80:
			v.Budget.State = service.BudgetWarning
		}
	}
	if p.WorkspaceID != nil {
		for _, w := range f.Workspaces {
			if w.ID == *p.WorkspaceID {
				v.WorkspaceSlug, v.WorkspaceName = w.Slug, w.Name
			}
		}
	}
	for _, pp := range personas {
		v.Personas = append(v.Personas, *pp)
	}
	return v
}

func (f *Fake) ListProjectViews(context.Context, domain.ID) ([]service.ProjectView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListProjectViews"); err != nil {
		return nil, err
	}
	out := make([]service.ProjectView, 0, len(f.Projects))
	for _, p := range f.Projects {
		out = append(out, f.projectView(p, false))
	}
	return out, nil
}

func (f *Fake) GetProjectView(_ context.Context, _ domain.ID, ref string) (service.ProjectView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetProjectView"); err != nil {
		return service.ProjectView{}, err
	}
	for _, p := range f.Projects {
		if p.Slug == ref || p.ID.String() == ref {
			return f.projectView(p, true), nil
		}
	}
	return service.ProjectView{}, domain.ErrNotFound
}

func (f *Fake) CreateProject(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateProjectInput) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateProject")
	if err := f.fail("CreateProject"); err != nil {
		return domain.Project{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return domain.Project{}, domain.Invalid("name", "obrigatório")
	}
	slug := in.Slug
	if slug == "" {
		slug = domain.Slugify(in.Name)
	}
	for _, p := range f.Projects {
		if p.Slug == slug {
			return domain.Project{}, domain.ErrConflict
		}
	}
	now := time.Now().UTC()
	p := domain.Project{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: in.Name, Description: in.Description, WorkspaceID: in.WorkspaceID,
		Status: domain.ProjectActive, BudgetMonthlyCents: in.BudgetMonthlyCents, CreatedAt: now, UpdatedAt: now}
	f.Projects = append(f.Projects, p)
	return p, nil
}

func (f *Fake) UpdateProject(_ context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateProjectInput) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateProject")
	if err := f.fail("UpdateProject"); err != nil {
		return domain.Project{}, err
	}
	for i := range f.Projects {
		p := &f.Projects[i]
		if p.ID != id {
			continue
		}
		if in.Name != nil {
			p.Name = *in.Name
		}
		if in.Description != nil {
			p.Description = *in.Description
		}
		if in.ClearWorkspace {
			p.WorkspaceID = nil
		}
		if in.WorkspaceID != nil {
			p.WorkspaceID = in.WorkspaceID
		}
		if in.Status != nil {
			p.Status = *in.Status
		}
		if in.ClearBudget {
			p.BudgetMonthlyCents = nil
		}
		if in.BudgetMonthlyCents != nil {
			p.BudgetMonthlyCents = in.BudgetMonthlyCents
		}
		p.UpdatedAt = time.Now().UTC()
		return *p, nil
	}
	return domain.Project{}, domain.ErrNotFound
}

func (f *Fake) workspaceView(w domain.Workspace, detail bool) service.WorkspaceView {
	v := service.WorkspaceView{Workspace: w, OutsideRoot: w.Path != "" && !strings.HasPrefix(w.Path, f.companyRoot()+"/")}
	for _, p := range f.Personas {
		if p.WorkspaceID != nil && *p.WorkspaceID == w.ID {
			v.PersonaCount++
			if detail {
				v.Personas = append(v.Personas, service.UsageRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
			}
		}
	}
	for _, p := range f.Projects {
		if p.WorkspaceID != nil && *p.WorkspaceID == w.ID {
			v.ProjectCount++
			if detail {
				v.Projects = append(v.Projects, service.UsageRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
			}
		}
	}
	for _, t := range f.Tasks {
		if t.WorkspaceID != nil && *t.WorkspaceID == w.ID && t.Status != domain.TaskDone && t.Status != domain.TaskCancelled {
			v.OpenTasks++
		}
	}
	return v
}

func (f *Fake) ListWorkspaceViews(context.Context, domain.ID) ([]service.WorkspaceView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("ListWorkspaceViews"); err != nil {
		return nil, err
	}
	out := make([]service.WorkspaceView, 0, len(f.Workspaces))
	for _, w := range f.Workspaces {
		out = append(out, f.workspaceView(w, false))
	}
	return out, nil
}

func (f *Fake) GetWorkspaceView(_ context.Context, _ domain.ID, ref string) (service.WorkspaceView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("GetWorkspaceView"); err != nil {
		return service.WorkspaceView{}, err
	}
	for _, w := range f.Workspaces {
		if w.Slug == ref || w.ID.String() == ref {
			return f.workspaceView(w, true), nil
		}
	}
	return service.WorkspaceView{}, domain.ErrNotFound
}

// WorkspacesRootPath é a raiz fictícia dos testes; os caminhos válidos ficam em <raiz>/hanko/ (pasta da empresa).
func (f *Fake) WorkspacesRootPath() string { return "/srv/hanko/workspaces" }

func (f *Fake) companyRoot() string { return f.WorkspacesRootPath() + "/hanko" }

func (f *Fake) CreateWorkspace(_ context.Context, _ domain.Actor, companyID domain.ID, in service.CreateWorkspaceInput) (domain.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("CreateWorkspace")
	if err := f.fail("CreateWorkspace"); err != nil {
		return domain.Workspace{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return domain.Workspace{}, domain.Invalid("name", "obrigatório")
	}
	if !strings.HasPrefix(in.Path, f.companyRoot()+"/") {
		return domain.Workspace{}, domain.Invalid("path", "fora da raiz de workspaces")
	}
	slug := in.Slug
	if slug == "" {
		slug = domain.Slugify(in.Name)
	}
	for _, w := range f.Workspaces {
		if w.Slug == slug {
			return domain.Workspace{}, domain.ErrConflict
		}
	}
	now := time.Now().UTC()
	w := domain.Workspace{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: in.Name, Path: in.Path, RepoURL: in.RepoURL,
		DefaultBranch: in.DefaultBranch, CreatedAt: now, UpdatedAt: now}
	f.Workspaces = append(f.Workspaces, w)
	return w, nil
}

func (f *Fake) UpdateWorkspace(_ context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateWorkspaceInput) (domain.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("UpdateWorkspace")
	if err := f.fail("UpdateWorkspace"); err != nil {
		return domain.Workspace{}, err
	}
	for i := range f.Workspaces {
		w := &f.Workspaces[i]
		if w.ID != id {
			continue
		}
		if in.Name != nil {
			w.Name = *in.Name
		}
		if in.Path != nil {
			if *in.Path != "" && !strings.HasPrefix(*in.Path, f.companyRoot()+"/") {
				return domain.Workspace{}, domain.Invalid("path", "fora da raiz de workspaces")
			}
			w.Path = *in.Path
		}
		if in.RepoURL != nil {
			w.RepoURL = *in.RepoURL
		}
		if in.DefaultBranch != nil {
			w.DefaultBranch = *in.DefaultBranch
		}
		w.UpdatedAt = time.Now().UTC()
		return *w, nil
	}
	return domain.Workspace{}, domain.ErrNotFound
}

func (f *Fake) DeleteWorkspace(_ context.Context, _ domain.Actor, _, id domain.ID, unlink bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note("DeleteWorkspace")
	if err := f.fail("DeleteWorkspace"); err != nil {
		return err
	}
	for i, w := range f.Workspaces {
		if w.ID != id {
			continue
		}
		v := f.workspaceView(w, false)
		if v.InUse() && !unlink {
			return &service.WorkspaceInUseError{Slug: w.Slug, Personas: v.PersonaCount, Projects: v.ProjectCount, OpenTasks: v.OpenTasks}
		}
		for j := range f.Personas {
			if f.Personas[j].WorkspaceID != nil && *f.Personas[j].WorkspaceID == id {
				f.Personas[j].WorkspaceID = nil
			}
		}
		for j := range f.Projects {
			if f.Projects[j].WorkspaceID != nil && *f.Projects[j].WorkspaceID == id {
				f.Projects[j].WorkspaceID = nil
			}
		}
		f.Workspaces = append(f.Workspaces[:i], f.Workspaces[i+1:]...)
		return nil
	}
	return domain.ErrNotFound
}
