package service_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Projetos e workspaces de primeira classe (ADR-0025).

func (e *env) workspace(t *testing.T, name string, mod func(*service.CreateWorkspaceInput)) domain.Workspace {
	t.Helper()
	in := service.CreateWorkspaceInput{Name: name}
	if mod != nil {
		mod(&in)
	}
	ws, err := e.svc.CreateWorkspace(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
	return ws
}

func (e *env) project(t *testing.T, name string, mod func(*service.CreateProjectInput)) domain.Project {
	t.Helper()
	in := service.CreateProjectInput{Name: name}
	if mod != nil {
		mod(&in)
	}
	p, err := e.svc.CreateProject(context.Background(), domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
	return p
}

func TestWorkspacePathValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	e.svc.WorkspacesRoot = root

	bad := map[string]string{
		"relativo":              "api",
		"relativo com ponto":    "./api",
		"travessia":             root + "/../fora",
		"travessia no meio":     root + "/a/../../fora",
		"fora da raiz":          "/etc",
		"a própria raiz":        root,
		"irmão com prefixo":     root + "-irmao/x",
		"barra dupla":           root + "//x",
		"barra final":           root + "/x/",
		"caractere de controle": root + "/x\ny",
		"NUL":                   root + "/x\x00y",
		"longo demais":          root + "/" + strings.Repeat("a", 1100),
	}
	for name, p := range bad {
		t.Run("recusa "+name, func(t *testing.T) {
			_, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "W-" + name, Path: p})
			wantErr(t, err, domain.ErrInvalid)
		})
	}
	t.Run("recusa symlink que escapa da raiz", func(t *testing.T) {
		outside := t.TempDir()
		link := root + "/atalho"
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("sem symlink: %v", err)
		}
		_, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Atalho", Path: link + "/x"})
		wantErr(t, err, domain.ErrInvalid)
	})
	t.Run("aceita dentro da raiz, mesmo sem existir", func(t *testing.T) {
		ws := e.workspace(t, "Dentro", func(in *service.CreateWorkspaceInput) { in.Path = root + "/novo/fundo" })
		if ws.Path != root+"/novo/fundo" {
			t.Fatalf("path = %q", ws.Path)
		}
	})
	t.Run("caminho vazio é só metadado", func(t *testing.T) {
		if ws := e.workspace(t, "Sem caminho", nil); ws.Path != "" {
			t.Fatalf("path = %q", ws.Path)
		}
	})
	t.Run("alterar passa pela mesma validação", func(t *testing.T) {
		ws := e.workspace(t, "Alvo", func(in *service.CreateWorkspaceInput) { in.Path = root + "/alvo" })
		for _, p := range []string{"/etc", "x", root + "/../y"} {
			_, err := e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, service.UpdateWorkspaceInput{Path: ptr(p)})
			wantErr(t, err, domain.ErrInvalid)
		}
		got, err := e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, service.UpdateWorkspaceInput{Path: ptr(root + "/alvo2")})
		if err != nil || got.Path != root+"/alvo2" {
			t.Fatalf("update: %+v %v", got, err)
		}
		got, err = e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, service.UpdateWorkspaceInput{Path: ptr("")})
		if err != nil || got.Path != "" {
			t.Fatalf("limpar: %+v %v", got, err)
		}
	})
}

func TestWorkspaceRepoAndBranch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	good := []string{
		"https://github.com/org/repo", "https://github.com/org/repo.git", "ssh://git@github.com/org/repo.git",
		"ssh://git@example.com:2222/org/repo.git", "git@github.com:org/repo.git", "",
	}
	for i, u := range good {
		ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "G" + string(rune('a'+i)), RepoURL: u})
		if err != nil || ws.RepoURL != u {
			t.Errorf("%q: %+v %v", u, ws, err)
		}
	}
	bad := []string{
		"http://github.com/org/repo", "file:///etc/passwd", "javascript:alert(1)", "ftp://x/y", "github.com/org/repo",
		"https://user:senha@github.com/org/repo", "https://token@github.com/org/repo", "ssh://user:pw@host/repo",
		"https://github.com/org repo", "https://github.com/org\nrepo", "https://" + strings.Repeat("a", 600) + ".com/x", "https://",
	}
	for i, u := range bad {
		_, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "B" + string(rune('a'+i)), RepoURL: u})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%q deveria ser recusada: %v", u, err)
		}
	}

	for _, b := range []string{"main", "feature/x", "release-1.2", "v1_beta"} {
		ws, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Br " + b, DefaultBranch: b})
		if err != nil || ws.DefaultBranch != b {
			t.Errorf("branch %q: %+v %v", b, ws, err)
		}
	}
	for _, b := range []string{"-x", "a b", "a..b", "a;rm", "x\ny", "$(id)", strings.Repeat("a", 200)} {
		_, err := e.svc.CreateWorkspace(ctx, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Bad " + b, DefaultBranch: b})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("branch %q deveria ser recusada: %v", b, err)
		}
	}
}

func TestUpdateWorkspaceAndView(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ws := e.workspace(t, "Site", func(in *service.CreateWorkspaceInput) {
		in.Path, in.RepoURL, in.DefaultBranch = "/srv/site", "https://github.com/o/site", "main"
	})
	up, err := e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, service.UpdateWorkspaceInput{
		Name: ptr("Site v2"), RepoURL: ptr(""), DefaultBranch: ptr("develop")})
	if err != nil || up.Name != "Site v2" || up.RepoURL != "" || up.DefaultBranch != "develop" || up.Slug != "site" || up.Path != "/srv/site" {
		t.Fatalf("update: %+v %v", up, err)
	}
	if n := countVerb(t, e, "workspace.updated"); n != 1 {
		t.Fatalf("workspace.updated = %d", n)
	}
	// sem mudança não escreve nem gera atividade
	same, err := e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, service.UpdateWorkspaceInput{Name: ptr("Site v2")})
	if err != nil || !same.UpdatedAt.Equal(up.UpdatedAt) || countVerb(t, e, "workspace.updated") != 1 {
		t.Fatalf("no-op: %+v %v", same, err)
	}
	_, err = e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, service.UpdateWorkspaceInput{Name: ptr(" ")})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.UpdateWorkspace(ctx, domain.HumanActor, e.co.ID, domain.NewID(), service.UpdateWorkspaceInput{Name: ptr("x")})
	wantErr(t, err, domain.ErrNotFound)

	// quem usa o workspace
	p := e.persona(t, "Dev", func(in *service.HirePersonaInput) { in.WorkspaceID = &ws.ID })
	pr := e.project(t, "Lançamento", func(in *service.CreateProjectInput) { in.WorkspaceID = &ws.ID })
	e.task(t, service.CreateTaskInput{Title: "aberta", WorkspaceID: &ws.ID})
	done := e.task(t, service.CreateTaskInput{Title: "feita", WorkspaceID: &ws.ID})
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, done.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.GetWorkspaceView(ctx, e.co.ID, "site")
	if err != nil || v.PersonaCount != 1 || v.ProjectCount != 1 || v.OpenTasks != 1 || !v.InUse() {
		t.Fatalf("view: %+v %v", v, err)
	}
	if len(v.Personas) != 1 || v.Personas[0].ID != p.ID || len(v.Projects) != 1 || v.Projects[0].ID != pr.ID {
		t.Fatalf("quem usa: %+v %+v", v.Personas, v.Projects)
	}
	list, err := e.svc.ListWorkspaceViews(ctx, e.co.ID)
	if err != nil || len(list) != 1 || list[0].PersonaCount != 1 || list[0].ProjectCount != 1 || list[0].OpenTasks != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
}

func TestDeleteWorkspaceInUse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ws := e.workspace(t, "Compartilhado", nil)
	p := e.persona(t, "Dev", func(in *service.HirePersonaInput) { in.WorkspaceID = &ws.ID })
	pr := e.project(t, "Proj", func(in *service.CreateProjectInput) { in.WorkspaceID = &ws.ID })
	tk := e.task(t, service.CreateTaskInput{Title: "usa", WorkspaceID: &ws.ID})

	err := e.svc.DeleteWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, false)
	wantErr(t, err, domain.ErrConflict)
	var inUse *service.WorkspaceInUseError
	if !errors.As(err, &inUse) || inUse.Personas != 1 || inUse.Projects != 1 || inUse.OpenTasks != 1 {
		t.Fatalf("erro: %v", err)
	}
	if _, err := e.svc.GetWorkspace(ctx, e.co.ID, ws.ID.String()); err != nil {
		t.Fatalf("o workspace sumiu apesar da recusa: %v", err)
	}

	if err := e.svc.DeleteWorkspace(ctx, domain.HumanActor, e.co.ID, ws.ID, true); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	_, err = e.svc.GetWorkspace(ctx, e.co.ID, ws.ID.String())
	wantErr(t, err, domain.ErrNotFound)
	if got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.WorkspaceID != nil {
		t.Error("a persona continua apontando para o workspace")
	}
	if got, _ := e.svc.GetProject(ctx, e.co.ID, pr.ID.String()); got.WorkspaceID != nil {
		t.Error("o projeto continua apontando para o workspace")
	}
	if got, _ := e.svc.GetTask(ctx, e.co.ID, tk.ID.String()); got.WorkspaceID != nil {
		t.Error("a task continua apontando para o workspace")
	}
	if countVerb(t, e, "workspace.deleted") != 1 || countVerb(t, e, "project.updated") != 1 {
		t.Error("o desvínculo e a exclusão deveriam virar atividade")
	}

	t.Run("sem uso exclui direto; task concluída não conta", func(t *testing.T) {
		w2 := e.workspace(t, "Livre", nil)
		old := e.task(t, service.CreateTaskInput{Title: "velha", WorkspaceID: &w2.ID})
		if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, old.ID, service.UpdateTaskInput{Status: ptr(domain.TaskDone)}); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.DeleteWorkspace(ctx, domain.HumanActor, e.co.ID, w2.ID, false); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if err := e.svc.DeleteWorkspace(ctx, domain.HumanActor, e.co.ID, w2.ID, false); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("excluir de novo: %v", err)
		}
	})
}

func TestProjectsCRUDAndArchive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ws := e.workspace(t, "Repo", nil)

	pr := e.project(t, "Lançamento", func(in *service.CreateProjectInput) {
		in.WorkspaceID, in.BudgetMonthlyCents = &ws.ID, ptr(int64(5000))
	})
	if pr.Status != domain.ProjectActive || pr.BudgetMonthlyCents == nil || *pr.BudgetMonthlyCents != 5000 {
		t.Fatalf("projeto: %+v", pr)
	}
	_, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Neg", BudgetMonthlyCents: ptr(int64(-1))})
	wantErr(t, err, domain.ErrInvalid)

	up, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, service.UpdateProjectInput{
		Name: ptr("Lançamento v2"), Description: ptr("d"), ClearWorkspace: true, BudgetMonthlyCents: ptr(int64(9000))})
	if err != nil || up.Name != "Lançamento v2" || up.WorkspaceID != nil || up.Slug != "lancamento" || *up.BudgetMonthlyCents != 9000 {
		t.Fatalf("update: %+v %v", up, err)
	}
	up, err = e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, service.UpdateProjectInput{ClearBudget: true, WorkspaceID: &ws.ID})
	if err != nil || up.BudgetMonthlyCents != nil || up.WorkspaceID == nil {
		t.Fatalf("limpar orçamento: %+v %v", up, err)
	}
	bad := []service.UpdateProjectInput{
		{Name: ptr(" ")}, {Status: ptr(domain.ProjectStatus("x"))}, {WorkspaceID: &ws.ID, ClearWorkspace: true},
		{BudgetMonthlyCents: ptr(int64(1)), ClearBudget: true}, {BudgetMonthlyCents: ptr(int64(-5))}, {WorkspaceID: ptr(domain.NewID())},
	}
	for i, in := range bad {
		if _, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("caso %d deveria ser inválido: %v", i, err)
		}
	}
	_, err = e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, domain.NewID(), service.UpdateProjectInput{Name: ptr("x")})
	wantErr(t, err, domain.ErrNotFound)

	// arquivar: o histórico fica, mas o projeto não recebe tasks novas
	tk := e.task(t, service.CreateTaskInput{Title: "antes", ProjectID: &pr.ID})
	other := e.project(t, "Outro", nil)
	loose := e.task(t, service.CreateTaskInput{Title: "solta"})
	if _, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, service.UpdateProjectInput{Status: ptr(domain.ProjectArchived)}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "depois", ProjectID: &pr.ID})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, loose.ID, service.UpdateTaskInput{ProjectID: &pr.ID})
	wantErr(t, err, domain.ErrInvalid)
	if got, err := e.svc.GetTask(ctx, e.co.ID, tk.ID.String()); err != nil || got.ProjectID == nil {
		t.Fatalf("a task antiga perdeu o projeto: %+v %v", got, err)
	}
	// a task do projeto arquivado continua editável e pode sair dele
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Priority: ptr(domain.PriorityHigh)}); err != nil {
		t.Fatalf("editar task de projeto arquivado: %v", err)
	}
	moved, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{ProjectID: &other.ID})
	if err != nil || moved.ProjectID == nil || *moved.ProjectID != other.ID || moved.ProjectName != "Outro" {
		t.Fatalf("mover: %+v %v", moved, err)
	}
	cleared, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{ClearProject: true})
	if err != nil || cleared.ProjectID != nil || cleared.ProjectName != "" {
		t.Fatalf("tirar do projeto: %+v %v", cleared, err)
	}
	_, err = e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{ClearProject: true, ProjectID: &other.ID})
	wantErr(t, err, domain.ErrInvalid)

	// reativar libera de novo
	if _, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, service.UpdateProjectInput{Status: ptr(domain.ProjectActive)}); err != nil {
		t.Fatal(err)
	}
	e.task(t, service.CreateTaskInput{Title: "de novo", ProjectID: &pr.ID})
}

func TestProjectViewProgress(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pa := e.persona(t, "Ana", nil)
	pb := e.persona(t, "Beto", nil)
	pr := e.project(t, "Proj", nil)
	empty := e.project(t, "Vazio", nil)

	mk := func(title string, persona *domain.Persona, st domain.TaskStatus) {
		in := service.CreateTaskInput{Title: title, ProjectID: &pr.ID, Status: st}
		if persona != nil {
			in.PersonaID = &persona.ID
		}
		e.task(t, in)
	}
	// 2 feitas, 1 em andamento, 1 a fazer, 2 canceladas (não contam no denominador): 2/4 = 50%
	mk("a", &pa, domain.TaskDone)
	mk("b", &pa, domain.TaskDone)
	mk("c", &pb, domain.TaskInProgress)
	mk("d", nil, domain.TaskTodo)
	mk("e", &pb, domain.TaskCancelled)
	mk("f", nil, domain.TaskCancelled)
	e.task(t, service.CreateTaskInput{Title: "fora do projeto"})

	v, err := e.svc.GetProjectView(ctx, e.co.ID, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if v.TasksTotal != 6 || v.Tasks[domain.TaskDone] != 2 || v.Tasks[domain.TaskInProgress] != 1 || v.Tasks[domain.TaskTodo] != 1 || v.Tasks[domain.TaskCancelled] != 2 {
		t.Fatalf("contagens: %+v", v.Tasks)
	}
	if !approx(v.Progress, 50) {
		t.Fatalf("progresso = %v, quer 50", v.Progress)
	}
	if len(v.Personas) != 2 {
		t.Fatalf("personas: %+v", v.Personas)
	}
	for _, pe := range v.Personas {
		if pe.Slug == "ana" && (pe.Tasks != 2 || pe.OpenTasks != 0) || pe.Slug == "beto" && (pe.Tasks != 2 || pe.OpenTasks != 1) {
			t.Errorf("persona %s: %+v", pe.Slug, pe)
		}
	}
	list, err := e.svc.ListProjectViews(ctx, e.co.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	for _, lv := range list {
		switch lv.ID {
		case pr.ID:
			if lv.TasksTotal != 6 || !approx(lv.Progress, 50) {
				t.Errorf("lista: %+v", lv)
			}
		case empty.ID:
			if lv.TasksTotal != 0 || lv.Progress != 0 {
				t.Errorf("projeto vazio: %+v", lv)
			}
		}
	}

	// filtro de tasks por projeto
	got, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{ProjectID: &pr.ID})
	if err != nil || len(got) != 6 {
		t.Fatalf("filtro por projeto: %d %v", len(got), err)
	}
}

func TestRunWorkspaceResolution(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wsTask := e.workspace(t, "Da task", nil)
	wsProj := e.workspace(t, "Do projeto", nil)
	wsPers := e.workspace(t, "Da persona", nil)
	withPers := e.persona(t, "ComWS", func(in *service.HirePersonaInput) { in.WorkspaceID = &wsPers.ID })
	noWS := e.persona(t, "SemWS", nil)
	pr := e.project(t, "Com ws", func(in *service.CreateProjectInput) { in.WorkspaceID = &wsProj.ID })
	prNoWS := e.project(t, "Sem ws", nil)

	checkout := func(p domain.Persona, in service.CreateTaskInput) domain.Run {
		t.Helper()
		in.PersonaID = &p.ID
		tv := e.task(t, in)
		run, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
		if err != nil {
			t.Fatalf("checkout: %v", err)
		}
		if _, err := e.svc.FinishRun(ctx, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone}); err != nil {
			t.Fatalf("finish: %v", err)
		}
		return run
	}
	want := func(name string, run domain.Run, ws *domain.Workspace, src domain.WorkspaceSource) {
		t.Helper()
		got, err := e.svc.GetRun(ctx, e.co.ID, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.WorkspaceSource != src {
			t.Errorf("%s: origem = %q, quer %q", name, got.WorkspaceSource, src)
		}
		if (ws == nil) != (got.WorkspaceID == nil) || ws != nil && *got.WorkspaceID != ws.ID {
			t.Errorf("%s: workspace = %v, quer %v", name, got.WorkspaceID, ws)
		}
		if run.WorkspaceSource != src {
			t.Errorf("%s: o CheckoutRun devolveu origem %q, quer %q", name, run.WorkspaceSource, src)
		}
	}

	// a task escolhe o workspace de propósito: vence o projeto e a persona
	want("task", checkout(withPers, service.CreateTaskInput{Title: "t1", ProjectID: &pr.ID, WorkspaceID: &wsTask.ID}), &wsTask, domain.WorkspaceFromTask)
	// task com projeto que tem workspace: o do projeto vence o da persona
	want("projeto", checkout(withPers, service.CreateTaskInput{Title: "t2", ProjectID: &pr.ID}), &wsProj, domain.WorkspaceFromProject)
	// projeto sem workspace cai no da persona
	want("persona (projeto sem ws)", checkout(withPers, service.CreateTaskInput{Title: "t3", ProjectID: &prNoWS.ID}), &wsPers, domain.WorkspaceFromPersona)
	// sem projeto: o da persona
	want("persona", checkout(withPers, service.CreateTaskInput{Title: "t4"}), &wsPers, domain.WorkspaceFromPersona)
	// nada: comportamento atual (diretório temporário), registrado como none
	want("nenhum", checkout(noWS, service.CreateTaskInput{Title: "t5", ProjectID: &prNoWS.ID}), nil, domain.WorkspaceNone)
	// persona sem workspace, mas o projeto tem
	want("projeto (persona sem ws)", checkout(noWS, service.CreateTaskInput{Title: "t6", ProjectID: &pr.ID}), &wsProj, domain.WorkspaceFromProject)

	// a task não guarda cópia do workspace do projeto: trocar o workspace do projeto vale já na próxima run
	tv := e.task(t, service.CreateTaskInput{Title: "t7", ProjectID: &pr.ID, PersonaID: &noWS.ID})
	if tv.WorkspaceID != nil {
		t.Fatalf("a task copiou o workspace do projeto: %v", tv.WorkspaceID)
	}
	if _, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, service.UpdateProjectInput{WorkspaceID: &wsPers.ID}); err != nil {
		t.Fatal(err)
	}
	run, err := e.svc.CheckoutRun(ctx, wakeOf(noWS, tv, domain.TriggerAssign))
	if err != nil {
		t.Fatal(err)
	}
	want("projeto trocado", run, &wsPers, domain.WorkspaceFromProject)
}

// O orçamento do projeto trava só as execuções das tasks dele (ADR-0025): a persona não é pausada.
func TestProjectBudgetHardStop(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return at }
	pr := e.project(t, "Capado", func(in *service.CreateProjectInput) { in.BudgetMonthlyCents = ptr(int64(1000)) }) // US$ 10
	inProject := func(in *service.CreateTaskInput) { in.ProjectID = &pr.ID }

	for i := 0; i < 7; i++ {
		e.spend(t, p, at, inProject, usd(1.0))
	}
	if countVerb(t, e, "budget.warning") != 0 {
		t.Fatal("alerta abaixo de 80%")
	}
	e.spend(t, p, at, inProject, usd(1.2)) // 8.2 = 82%
	e.spend(t, p, at, inProject, usd(0.1)) // continua em alerta
	if n := countVerb(t, e, "budget.warning"); n != 1 {
		t.Fatalf("alertas = %d, quer 1 (uma vez por mês)", n)
	}
	b, err := e.svc.ProjectBudget(ctx, e.co.ID, pr.ID)
	if err != nil || b.State != service.BudgetWarning || b.Percent < 80 {
		t.Fatalf("budget: %+v %v", b, err)
	}
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{SubjectType: "project", Limit: 100})
	sawProjectAlert := false
	for _, a := range acts {
		sawProjectAlert = sawProjectAlert || a.Verb == "budget.warning"
	}
	if !sawProjectAlert {
		t.Error("o alerta deveria ser do projeto")
	}

	e.spend(t, p, at, inProject, usd(2.0)) // 10.3: estourou; a execução termina normalmente
	if n := countVerb(t, e, "budget.exceeded"); n != 1 {
		t.Fatalf("exceeded = %d", n)
	}

	// próxima task do projeto: recusada, com o motivo do projeto e sem abrir execução
	tv := e.task(t, service.CreateTaskInput{Title: "mais uma", PersonaID: &p.ID, ProjectID: &pr.ID})
	before, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{})
	_, err = e.svc.CheckoutRun(ctx, wakeOf(p, tv, domain.TriggerAssign))
	wantErr(t, err, domain.ErrBudgetExceeded)
	wantErr(t, err, domain.ErrConflict)
	var blocked *service.BudgetBlockedError
	if !errors.As(err, &blocked) || blocked.Scope != "project" || blocked.Name != "Capado" || !strings.Contains(err.Error(), "projeto") {
		t.Fatalf("erro sem o motivo do projeto: %v", err)
	}
	after, _ := e.svc.ListRuns(ctx, e.co.ID, service.RunFilter{})
	if len(after) != len(before) {
		t.Fatalf("abriu execução: %d -> %d", len(before), len(after))
	}

	// a persona NÃO foi pausada: o resto do trabalho dela segue
	if g, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); g.Status == domain.PersonaPaused {
		t.Fatalf("a persona foi pausada pelo orçamento do projeto: %q", g.PauseReason)
	}
	free := e.task(t, service.CreateTaskInput{Title: "sem projeto", PersonaID: &p.ID})
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, free, domain.TriggerAssign)); err != nil {
		t.Fatalf("task fora do projeto: %v", err)
	}
	other := e.project(t, "Livre", nil)
	tv2 := e.task(t, service.CreateTaskInput{Title: "outro projeto", PersonaID: &p.ID, ProjectID: &other.ID})
	e.svc.Now = func() time.Time { return at.Add(time.Minute) }
	if _, err := e.svc.CheckoutRun(ctx, wakeOf(p, tv2, domain.TriggerAssign)); err != nil {
		// a persona já tem uma run ativa (a da task solta): ErrActiveRun não é falha de orçamento
		if errors.Is(err, domain.ErrBudgetExceeded) {
			t.Fatalf("o orçamento de um projeto travou outro: %v", err)
		}
	}

	// mover a task para fora do projeto a libera; subir o teto libera o projeto
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{ClearProject: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateTask(ctx, domain.HumanActor, e.co.ID, tv.ID, service.UpdateTaskInput{ProjectID: &pr.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateProject(ctx, domain.HumanActor, e.co.ID, pr.ID, service.UpdateProjectInput{BudgetMonthlyCents: ptr(int64(5000))}); err != nil {
		t.Fatal(err)
	}
	b, _ = e.svc.ProjectBudget(ctx, e.co.ID, pr.ID)
	if b.State != service.BudgetOK {
		t.Fatalf("depois de subir o teto: %+v", b)
	}
}

// O gasto do projeto é o das execuções das tasks dele; o filtro e o agrupamento de custos usam isso.
func TestCostSummaryByProject(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := e.persona(t, "Eng", nil)
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	a := e.project(t, "A", nil)
	b := e.project(t, "B", nil)
	e.spend(t, p, at, func(in *service.CreateTaskInput) { in.ProjectID = &a.ID }, usd(1.5))
	e.spend(t, p, at, func(in *service.CreateTaskInput) { in.ProjectID = &a.ID }, usd(0.5))
	e.spend(t, p, at, func(in *service.CreateTaskInput) { in.ProjectID = &b.ID }, usd(4))
	e.spend(t, p, at, nil, usd(10))

	from, to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	all, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: from, To: to, GroupBy: service.GroupProject})
	if err != nil || !approx(all.TotalUSD, 16) {
		t.Fatalf("total: %+v %v", all, err)
	}
	by := map[string]float64{}
	for _, r := range all.Rows {
		by[r.Label] = r.CostUSD
	}
	if !approx(by["A"], 2) || !approx(by["B"], 4) || !approx(by[""], 10) && !approx(by["Sem projeto"], 10) {
		t.Fatalf("agrupado: %+v", by)
	}

	only, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: from, To: to, GroupBy: service.GroupPersona, ProjectID: &a.ID})
	if err != nil || !approx(only.TotalUSD, 2) || only.Runs != 2 {
		t.Fatalf("filtrado: %+v %v", only, err)
	}
	days, err := e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: from, To: to, GroupBy: service.GroupDay, ProjectID: &b.ID})
	if err != nil || !approx(days.TotalUSD, 4) {
		t.Fatalf("filtrado por dia: %+v %v", days, err)
	}
	if days.PaidUSD != 0 {
		t.Errorf("mensalidade não é rateada por projeto: %v", days.PaidUSD)
	}
	// gasto do projeto no mês corrente da empresa
	e.svc.Now = func() time.Time { return at }
	v, err := e.svc.GetProjectView(ctx, e.co.ID, "a")
	if err != nil || !approx(v.SpentUSD, 2) {
		t.Fatalf("view: %+v %v", v, err)
	}
	_, err = e.svc.CostSummary(ctx, e.co.ID, service.CostFilter{From: from, To: to, GroupBy: service.GroupDay, ProjectID: ptr(domain.NewID())})
	wantErr(t, err, domain.ErrNotFound)
}

// Papéis (ADR-0021): viewer lê; member cria e edita tasks (inclusive num projeto); admin/owner gerenciam
// projetos e workspaces.
func TestProjectsRoleMatrix(t *testing.T) {
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	ws := e.workspace(t, "Base", nil)
	pr := e.project(t, "Base", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})

	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		users[r] = u
	}
	type op struct {
		name string
		min  domain.MemberRole
		run  func(ctx context.Context, u domain.User) error
	}
	ops := []op{
		{"ListProjectViews", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.ListProjectViews(ctx, e.co.ID)
			return err
		}},
		{"GetProjectView", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.GetProjectView(ctx, e.co.ID, pr.Slug)
			return err
		}},
		{"ListWorkspaceViews", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.ListWorkspaceViews(ctx, e.co.ID)
			return err
		}},
		{"GetWorkspaceView", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.GetWorkspaceView(ctx, e.co.ID, ws.Slug)
			return err
		}},
		{"CreateTask no projeto", domain.MemberMember, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateTask(ctx, u.Actor(), e.co.ID, service.CreateTaskInput{Title: "x", ProjectID: &pr.ID})
			return err
		}},
		{"UpdateTask projeto", domain.MemberMember, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.UpdateTask(ctx, u.Actor(), e.co.ID, tk.ID, service.UpdateTaskInput{ProjectID: &pr.ID})
			return err
		}},
		{"CreateProject", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateProject(ctx, u.Actor(), e.co.ID, service.CreateProjectInput{Name: "P-" + u.Email})
			return err
		}},
		{"UpdateProject", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.UpdateProject(ctx, u.Actor(), e.co.ID, pr.ID, service.UpdateProjectInput{Description: ptr("d-" + u.Email)})
			return err
		}},
		{"CreateWorkspace", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateWorkspace(ctx, u.Actor(), e.co.ID, service.CreateWorkspaceInput{Name: "W-" + u.Email})
			return err
		}},
		{"UpdateWorkspace", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.UpdateWorkspace(ctx, u.Actor(), e.co.ID, ws.ID, service.UpdateWorkspaceInput{Name: ptr("n-" + u.Email)})
			return err
		}},
		{"DeleteWorkspace", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			w, err := e.svc.CreateWorkspace(context.Background(), domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "D-" + u.Email})
			if err != nil {
				return err
			}
			return e.svc.DeleteWorkspace(ctx, u.Actor(), e.co.ID, w.ID, false)
		}},
	}
	for _, o := range ops {
		for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
			u := users[r]
			err := o.run(as(u), u)
			if r.AtLeast(o.min) {
				if err != nil {
					t.Errorf("%s como %s deveria passar: %v", o.name, r, err)
				}
			} else if !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s como %s deveria ser ErrForbidden: %v", o.name, r, err)
			}
		}
	}
}
