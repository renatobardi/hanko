package mcp

import (
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Projetos pelo MCP (ADR-0025): a persona lista os projetos e cria task num deles.
func TestProjectListAndTasksInProject(t *testing.T) {
	e := setup(t, false)
	cid := e.f.company.ID
	mk := func(slug, name string, st domain.ProjectStatus) service.ProjectView {
		return service.ProjectView{Project: domain.Project{ID: domain.NewID(), CompanyID: cid, Slug: slug, Name: name, Status: st},
			WorkspaceSlug: "site", Tasks: map[domain.TaskStatus]int{domain.TaskDone: 1, domain.TaskTodo: 2}, TasksTotal: 3, Progress: 100.0 / 3,
			Budget: service.BudgetStatus{State: service.BudgetWarning}}
	}
	e.f.projects = []service.ProjectView{mk("lancamento", "Lançamento", domain.ProjectActive), mk("velho", "Velho", domain.ProjectArchived)}
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	var out projectListOut
	structured(t, call(t, cs, "hanko_project_list", map[string]any{}), &out)
	if len(out.Projects) != 1 || out.Projects[0].Slug != "lancamento" || out.Projects[0].OpenTasks != 2 || out.Projects[0].BudgetState != "warning" ||
		out.Projects[0].Workspace != "site" || out.Projects[0].Status != "active" {
		t.Fatalf("projetos (só ativos): %+v", out.Projects)
	}
	structured(t, call(t, cs, "hanko_project_list", map[string]any{"all": true}), &out)
	if len(out.Projects) != 2 {
		t.Fatalf("projetos com arquivados: %+v", out.Projects)
	}

	// criar uma task num projeto
	res := call(t, cs, "hanko_task_create", map[string]any{"title": "No projeto", "project": "lancamento"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if in := e.f.lastCreate; in.ProjectID == nil || *in.ProjectID != e.f.projects[0].ID {
		t.Errorf("project ignorado: %+v", in.ProjectID)
	}
	if r := call(t, cs, "hanko_task_create", map[string]any{"title": "x", "project": "fantasma"}); !r.IsError {
		t.Error("projeto inexistente deveria falhar")
	}

	// listar as tasks de um projeto
	if r := call(t, cs, "hanko_task_list", map[string]any{"project": "lancamento"}); r.IsError {
		t.Fatal(text(r))
	}
	if f := e.f.lastFilter; f.ProjectID == nil || *f.ProjectID != e.f.projects[0].ID {
		t.Errorf("filtro por projeto: %+v", f.ProjectID)
	}
	if r := call(t, cs, "hanko_task_list", map[string]any{"project": "fantasma"}); !r.IsError {
		t.Error("projeto inexistente no filtro deveria falhar")
	}
}
