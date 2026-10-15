package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Projetos e workspaces (ADR-0025). A barreira de papel é o service (admin cria, altera, arquiva, exclui);
// a UI só esconde os controles que o papel não tem.

var projectNotices = map[string]string{
	"criado":     i18n.N("Projeto criado."),
	"salvo":      i18n.N("Projeto atualizado."),
	"arquivado":  i18n.N("Projeto arquivado: não recebe tasks novas, e o histórico fica."),
	"reativado":  i18n.N("Projeto reativado."),
	"orcamento":  i18n.N("Orçamento do projeto atualizado."),
	"w-criado":   i18n.N("Workspace criado."),
	"w-salvo":    i18n.N("Workspace atualizado."),
	"w-excluido": i18n.N("Workspace excluído."),
}

// dirExists diz se o caminho de um workspace existe neste servidor. É uma variável para os testes.
var dirExists = func(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// taskProjectOptions são as opções do campo projeto de uma task: "Nenhum", os projetos ativos e, se a task já
// está num projeto arquivado, esse também (para o formulário não desvinculá-la sem querer).
func (s *server) taskProjectOptions(r *http.Request, co domain.Company, sel *domain.ID) []views.Option {
	ctx := r.Context()
	projects, _ := s.be.ListProjects(ctx, co.ID) // opcional: o formulário funciona sem projetos
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Nenhum"), Selected: sel == nil}}
	for _, p := range projects {
		cur := sel != nil && *sel == p.ID
		if p.Status == domain.ProjectArchived && !cur {
			continue
		}
		label := p.Name
		if p.Status == domain.ProjectArchived {
			label += " (" + i18n.T(ctx, "arquivado") + ")"
		}
		out = append(out, views.Option{Value: p.ID.String(), Label: label, Selected: cur, Slug: p.Slug})
	}
	return out
}

func (s *server) workspaceOptions(r *http.Request, co domain.Company, sel *domain.ID) []views.Option {
	ctx := r.Context()
	list, _ := s.be.ListWorkspaceViews(ctx, co.ID) // opcional
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Nenhum (usa o da persona)"), Selected: sel == nil}}
	for _, w := range list {
		out = append(out, views.Option{Value: w.ID.String(), Label: w.Name, Selected: sel != nil && *sel == w.ID})
	}
	return out
}

func projectFormError(ctx context.Context, vm *views.ProjectFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := ve.Field
		msg := ""
		switch field {
		case "name":
			msg = i18n.T(ctx, "Dê um nome ao projeto (até 120 caracteres).")
		case "workspace_id":
			field, msg = "workspace", i18n.T(ctx, "Workspace não encontrado nesta empresa.")
		case "budget", "budget_monthly_cents":
			field, msg = "budget", i18n.T(ctx, "Informe um valor em dólares, como 400 ou 400,50.")
		case "status":
			msg = i18n.T(ctx, "Status desconhecido.")
		default:
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func projectFormValues(r *http.Request, vm *views.ProjectFormVM) {
	vm.Name = trimForm(r, "name")
	vm.Description = trimForm(r, "description")
	vm.Budget = trimForm(r, "budget")
	selectOption(vm.WorkspaceOptions, trimForm(r, "workspace"))
}

// ---------- projetos ----------

type projectTab struct{ key, label string }

var projectTabs = []projectTab{
	{"ativos", i18n.N("Ativos")},
	{"arquivados", i18n.N("Arquivados")},
	{"todos", i18n.N("Todos")},
}

func percentInt(v float64) int { return int(math.Min(100, math.Round(v))) }

func (s *server) handleProjects(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	list, err := s.be.ListProjectViews(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	cur := projectTabs[0].key
	for _, t := range projectTabs {
		if t.key == r.URL.Query().Get("aba") {
			cur = t.key
		}
	}
	vm := views.ProjectsVM{NewHref: base + "/projetos/novo", Empty: len(list) == 0,
		Notice:   i18n.T(ctx, projectNotices[r.URL.Query().Get("ok")]),
		Subtitle: i18n.T(ctx, "Agrupe tasks por projeto, com workspace e orçamento próprios.")}
	spent := 0.0
	counts := map[string]int{}
	for _, p := range list {
		archived := p.Status == domain.ProjectArchived
		counts["todos"]++
		if archived {
			vm.Archived++
			counts["arquivados"]++
		} else {
			vm.Active++
			counts["ativos"]++
			vm.OpenTask += p.TasksTotal - p.Tasks[domain.TaskDone] - p.Tasks[domain.TaskCancelled]
		}
		spent += p.SpentUSD
		if (cur == "ativos" && archived) || (cur == "arquivados" && !archived) {
			continue
		}
		row := views.ProjectRow{Name: p.Name, Href: base + "/projetos/" + p.Slug, Description: p.Description, Archived: archived,
			Workspace: p.WorkspaceName, Open: p.TasksTotal - p.Tasks[domain.TaskDone] - p.Tasks[domain.TaskCancelled], Total: p.TasksTotal,
			Progress: percentInt(p.Progress), ProgressTxt: fmt.Sprintf("%d%% · %d/%d", int(math.Round(p.Progress)), p.Tasks[domain.TaskDone], p.TasksTotal),
			Spent: fmtUSDCtx(ctx, p.SpentUSD), BudgetState: p.Budget.State}
		if p.BudgetMonthlyCents != nil {
			row.Limit = fmtUSDCtx(ctx, centsToUSD(*p.BudgetMonthlyCents))
			row.BudgetPct = fmt.Sprintf("%d%%", int(math.Round(p.Budget.Percent)))
		}
		vm.Rows = append(vm.Rows, row)
	}
	vm.Spent = fmtUSDCtx(ctx, spent)
	for _, t := range projectTabs {
		href := base + "/projetos"
		if t.key != "ativos" {
			href += "?aba=" + t.key
		}
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: t.key, Label: i18n.T(ctx, t.label), Href: href, Count: counts[t.key], On: t.key == cur})
	}
	sh := s.shell(r, co, "projetos", i18n.T(ctx, "Projetos"), views.Crumb{Label: i18n.T(ctx, "Projetos")})
	s.render(w, r, http.StatusOK, views.ProjectsPage(sh, vm))
}

func (s *server) blankProjectForm(r *http.Request, co domain.Company) views.ProjectFormVM {
	base := "/c/" + co.Slug
	return views.ProjectFormVM{Action: base + "/projetos/novo", CancelHref: base + "/projetos", FieldErr: map[string]string{},
		WorkspaceOptions: s.workspaceOptions(r, co, nil)}
}

func (s *server) renderNewProject(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.ProjectFormVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "projetos", i18n.T(ctx, "Novo projeto"), views.Crumb{Label: i18n.T(ctx, "Projetos"), Href: base + "/projetos"}, views.Crumb{Label: i18n.T(ctx, "Novo")})
	s.render(w, r, status, views.NewProjectPage(sh, vm))
}

func (s *server) handleNewProject(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderNewProject(w, r, co, http.StatusOK, s.blankProjectForm(r, co))
}

func (s *server) handleCreateProject(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	vm := s.blankProjectForm(r, co)
	projectFormValues(r, &vm)
	bad := func(err error) {
		projectFormError(ctx, &vm, err)
		s.renderNewProject(w, r, co, statusFor(err), vm)
	}
	ws, ok := parseID(trimForm(r, "workspace"))
	if !ok {
		bad(domain.Invalid("workspace_id", "invalid"))
		return
	}
	cents, err := parseUSD(vm.Budget)
	if err != nil {
		bad(domain.Invalid("budget", "invalid"))
		return
	}
	p, err := s.be.CreateProject(ctx, actorFrom(ctx), co.ID, service.CreateProjectInput{Name: vm.Name, Description: vm.Description, WorkspaceID: ws, BudgetMonthlyCents: cents})
	if err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/projetos/"+p.Slug+"?ok=criado")
}

var statusTab = map[domain.TaskStatus]string{
	domain.TaskBacklog: "backlog", domain.TaskDone: "concluidas", domain.TaskCancelled: "concluidas",
}

func (s *server) buildProjectVM(r *http.Request, co domain.Company, p service.ProjectView) views.ProjectVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	loc := loadLoc(co.Timezone)
	now := s.now()
	archived := p.Status == domain.ProjectArchived
	vm := views.ProjectVM{Name: p.Name, Description: p.Description, Archived: archived, Workspace: p.WorkspaceName,
		Notice: i18n.T(ctx, projectNotices[r.URL.Query().Get("ok")]),
		Total:  p.TasksTotal, Progress: percentInt(p.Progress),
		ProgressTxt: fmt.Sprintf("%d%% · %d/%d", int(math.Round(p.Progress)), p.Tasks[domain.TaskDone], p.TasksTotal),
		Spent:       fmtUSDCtx(ctx, p.SpentUSD),
		Budget:      budgetBox(ctx, i18n.T(ctx, "Orçamento do projeto"), p.Budget, "", ""),
		TasksHref:   base + "/tasks?aba=todas&projeto=" + url.QueryEscape(p.Slug),
		NewTaskHref: base + "/tasks/nova?projeto=" + url.QueryEscape(p.Slug),
		CostHref:    base + "/custos?projeto=" + url.QueryEscape(p.Slug),
		ToggleHref:  base + "/projetos/" + p.Slug + "/arquivar"}
	if archived {
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Arquivado"), Class: "soft"}
		vm.ToggleHref = base + "/projetos/" + p.Slug + "/reativar"
	} else {
		vm.Badge = views.Badge{Label: i18n.T(ctx, "Ativo"), Class: "run"}
	}
	if p.WorkspaceSlug != "" {
		vm.WorkspaceURL = base + "/workspaces/" + p.WorkspaceSlug
	}
	for _, st := range domain.TaskStatuses {
		tab := statusTab[st]
		if tab == "" {
			tab = "ativas"
		}
		vm.Statuses = append(vm.Statuses, views.StatusCount{Label: taskStatusLabel(ctx, st), Count: p.Tasks[st],
			Href: base + "/tasks?aba=" + tab + "&projeto=" + url.QueryEscape(p.Slug)})
	}
	sort.SliceStable(p.Personas, func(i, j int) bool { return p.Personas[i].OpenTasks > p.Personas[j].OpenTasks })
	for _, pe := range p.Personas {
		vm.Personas = append(vm.Personas, views.ProjectPersonaRow{Name: pe.Name, Href: base + "/personas/" + pe.Slug, Open: pe.OpenTasks, Total: pe.Tasks})
	}
	if tasks, err := s.be.ListTasks(ctx, co.ID, service.TaskFilter{ProjectID: &p.ID, Limit: 8}); err == nil {
		for _, tv := range tasks {
			vm.Tasks = append(vm.Tasks, taskRow(ctx, tv, base, now, loc))
		}
	}
	if goals, err := s.be.ListGoalViews(ctx, co.ID, service.GoalFilter{ProjectID: &p.ID}); err == nil {
		for _, g := range goals {
			if g.Status == domain.GoalAbandoned || len(vm.Goals) >= 5 {
				continue
			}
			vm.Goals = append(vm.Goals, goalMini(ctx, g, base, now, loc))
		}
	}
	vm.GoalsHref = base + "/metas?aba=todas&projeto=" + url.QueryEscape(p.Slug)
	if arts, err := s.be.ListArtifacts(ctx, co.ID, service.ArtifactFilter{ProjectID: &p.ID, Limit: 5}); err == nil {
		vm.Artifacts = artifactMinis(ctx, arts, base)
	}
	vm.ArtifactsHref = base + "/artefatos?projeto=" + url.QueryEscape(p.Slug)
	ws := p.WorkspaceID
	vm.Form = views.ProjectFormVM{Action: base + "/projetos/" + p.Slug, CancelHref: base + "/projetos", Edit: true, FieldErr: map[string]string{},
		Name: p.Name, Description: p.Description, Budget: usdField(ctx, p.BudgetMonthlyCents), WorkspaceOptions: s.workspaceOptions(r, co, ws)}
	return vm
}

func (s *server) renderProject(w http.ResponseWriter, r *http.Request, co domain.Company, status int, p service.ProjectView, vm views.ProjectVM) {
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "projetos", p.Name, views.Crumb{Label: i18n.T(r.Context(), "Projetos"), Href: base + "/projetos"}, views.Crumb{Label: p.Name})
	s.render(w, r, status, views.ProjectPage(sh, vm))
}

func (s *server) handleProject(w http.ResponseWriter, r *http.Request, co domain.Company) {
	p, err := s.be.GetProjectView(r.Context(), co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderProject(w, r, co, http.StatusOK, p, s.buildProjectVM(r, co, p))
}

func (s *server) handleUpdateProject(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := s.buildProjectVM(r, co, p)
	projectFormValues(r, &vm.Form)
	vm.Notice = ""
	bad := func(err error) {
		projectFormError(ctx, &vm.Form, err)
		s.renderProject(w, r, co, statusFor(err), p, vm)
	}
	ws, ok := parseID(trimForm(r, "workspace"))
	if !ok {
		bad(domain.Invalid("workspace_id", "invalid"))
		return
	}
	cents, err := parseUSD(vm.Form.Budget)
	if err != nil {
		bad(domain.Invalid("budget", "invalid"))
		return
	}
	f := vm.Form
	in := service.UpdateProjectInput{Name: &f.Name, Description: &f.Description, WorkspaceID: ws, ClearWorkspace: ws == nil,
		BudgetMonthlyCents: cents, ClearBudget: cents == nil}
	if _, err := s.be.UpdateProject(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		bad(err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/projetos/"+p.Slug+"?ok=salvo")
}

// handleProjectArchive arquiva (ou reativa) o projeto e volta para a página dele.
func (s *server) handleProjectArchive(archive bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		ctx := r.Context()
		p, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		st, ok := domain.ProjectActive, "reativado"
		if archive {
			st, ok = domain.ProjectArchived, "arquivado"
		}
		if _, err := s.be.UpdateProject(ctx, actorFrom(ctx), co.ID, p.ID, service.UpdateProjectInput{Status: &st}); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		redirect(w, r, "/c/"+co.Slug+"/projetos/"+p.Slug+"?ok="+ok)
	}
}

// handleSetProjectBudget grava só o orçamento (formulário da tela de Custos).
func (s *server) handleSetProjectBudget(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	cents, err := parseUSDCtx(ctx, r.PostFormValue("budget"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	in := service.UpdateProjectInput{BudgetMonthlyCents: cents, ClearBudget: cents == nil}
	if _, err := s.be.UpdateProject(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if r.URL.Query().Get("next") == "custos" {
		redirect(w, r, "/c/"+co.Slug+"/custos?ok=projeto")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/projetos/"+p.Slug+"?ok=orcamento")
}

// ---------- workspaces ----------

// workspacesRoot é a pasta da empresa dentro da raiz dos workspaces (<raiz>/<slug>); vazia sem raiz configurada.
func (s *server) workspacesRoot(co domain.Company) string {
	root := s.be.WorkspacesRootPath()
	if root == "" {
		return ""
	}
	return strings.TrimRight(root, "/") + "/" + co.Slug
}

func (s *server) handleWorkspaces(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	list, err := s.be.ListWorkspaceViews(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	base := "/c/" + co.Slug
	vm := views.WorkspacesVM{NewHref: base + "/workspaces/novo", Root: s.workspacesRoot(co),
		Notice:   i18n.T(ctx, projectNotices[r.URL.Query().Get("ok")]),
		Subtitle: i18n.T(ctx, "Diretórios e repositórios onde as personas trabalham.")}
	for _, wv := range list {
		if wv.InUse() {
			vm.InUse++
		}
		vm.Rows = append(vm.Rows, views.WorkspaceRow{Name: wv.Name, Href: base + "/workspaces/" + wv.Slug, Path: wv.Path, RepoURL: wv.RepoURL,
			Branch: wv.DefaultBranch, Personas: wv.PersonaCount, Projects: wv.ProjectCount, OpenTasks: wv.OpenTasks, InUse: wv.InUse(),
			Missing: wv.Path != "" && !dirExists(wv.Path), Outside: wv.OutsideRoot})
	}
	sh := s.shell(r, co, "workspaces", i18n.T(ctx, "Workspaces"), views.Crumb{Label: i18n.T(ctx, "Workspaces")})
	s.render(w, r, http.StatusOK, views.WorkspacesPage(sh, vm))
}

func (s *server) blankWorkspaceForm(co domain.Company, root string) views.WorkspaceFormVM {
	base := "/c/" + co.Slug
	return views.WorkspaceFormVM{Action: base + "/workspaces/novo", CancelHref: base + "/workspaces", Root: root, FieldErr: map[string]string{}}
}

func workspaceFormValues(r *http.Request, vm *views.WorkspaceFormVM) {
	vm.Name = trimForm(r, "name")
	vm.Path = trimForm(r, "path")
	vm.RepoURL = trimForm(r, "repo_url")
	vm.Branch = trimForm(r, "branch")
}

func workspaceFormError(ctx context.Context, vm *views.WorkspaceFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := ve.Field
		var msg string
		switch field {
		case "name":
			msg = i18n.T(ctx, "Dê um nome ao workspace (até 120 caracteres).")
		case "path":
			msg = i18n.T(ctx, "Caminho inválido: use um caminho absoluto e limpo (sem .. nem //).")
			if vm.Root != "" {
				msg = i18n.Tf(ctx, "Caminho inválido: use um caminho absoluto, limpo (sem .. nem //) e dentro de %s.", vm.Root)
			}
		case "repo_url":
			msg = i18n.T(ctx, "URL do repositório inválida: use https://, ssh:// ou formato scp, sem usuário e senha.")
		case "default_branch":
			field, msg = "branch", i18n.T(ctx, "Nome de branch inválido.")
		default:
			msg = ve.Field + ": " + ve.Reason + "."
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func (s *server) renderNewWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.WorkspaceFormVM) {
	base := "/c/" + co.Slug
	ctx := r.Context()
	sh := s.shell(r, co, "workspaces", i18n.T(ctx, "Novo workspace"), views.Crumb{Label: i18n.T(ctx, "Workspaces"), Href: base + "/workspaces"}, views.Crumb{Label: i18n.T(ctx, "Novo")})
	s.render(w, r, status, views.NewWorkspacePage(sh, vm))
}

func (s *server) handleNewWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderNewWorkspace(w, r, co, http.StatusOK, s.blankWorkspaceForm(co, s.workspacesRoot(co)))
}

func (s *server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	vm := s.blankWorkspaceForm(co, s.workspacesRoot(co))
	workspaceFormValues(r, &vm)
	ws, err := s.be.CreateWorkspace(ctx, actorFrom(ctx), co.ID, service.CreateWorkspaceInput{Name: vm.Name, Path: vm.Path, RepoURL: vm.RepoURL, DefaultBranch: vm.Branch})
	if err != nil {
		workspaceFormError(ctx, &vm, err)
		s.renderNewWorkspace(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/workspaces/"+ws.Slug+"?ok=w-criado")
}

func (s *server) buildWorkspaceVM(r *http.Request, co domain.Company, wv service.WorkspaceView) views.WorkspaceVM {
	base := "/c/" + co.Slug
	vm := views.WorkspaceVM{Name: wv.Name, Path: wv.Path, Missing: wv.Path != "" && !dirExists(wv.Path), Outside: wv.OutsideRoot, Root: s.workspacesRoot(co),
		Notice:    i18n.T(r.Context(), projectNotices[r.URL.Query().Get("ok")]),
		OpenTasks: wv.OpenTasks, InUse: wv.InUse(), DeleteAction: base + "/workspaces/" + wv.Slug + "/excluir",
		Form: views.WorkspaceFormVM{Action: base + "/workspaces/" + wv.Slug, CancelHref: base + "/workspaces", Edit: true, Root: s.workspacesRoot(co),
			FieldErr: map[string]string{}, Name: wv.Name, Path: wv.Path, RepoURL: wv.RepoURL, Branch: wv.DefaultBranch}}
	for _, p := range wv.Personas {
		vm.Personas = append(vm.Personas, views.UsageLink{Name: p.Name, Href: base + "/personas/" + p.Slug})
	}
	for _, p := range wv.Projects {
		vm.Projects = append(vm.Projects, views.UsageLink{Name: p.Name, Href: base + "/projetos/" + p.Slug})
	}
	return vm
}

func (s *server) renderWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company, status int, wv service.WorkspaceView, vm views.WorkspaceVM) {
	base := "/c/" + co.Slug
	sh := s.shell(r, co, "workspaces", wv.Name, views.Crumb{Label: i18n.T(r.Context(), "Workspaces"), Href: base + "/workspaces"}, views.Crumb{Label: wv.Name})
	s.render(w, r, status, views.WorkspacePage(sh, vm))
}

func (s *server) handleWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company) {
	wv, err := s.be.GetWorkspaceView(r.Context(), co.ID, r.PathValue("workspace"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.renderWorkspace(w, r, co, http.StatusOK, wv, s.buildWorkspaceVM(r, co, wv))
}

func (s *server) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	wv, err := s.be.GetWorkspaceView(ctx, co.ID, r.PathValue("workspace"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm := s.buildWorkspaceVM(r, co, wv)
	workspaceFormValues(r, &vm.Form)
	vm.Notice = ""
	f := vm.Form
	if _, err := s.be.UpdateWorkspace(ctx, actorFrom(ctx), co.ID, wv.ID, service.UpdateWorkspaceInput{Name: &f.Name, Path: &f.Path, RepoURL: &f.RepoURL, DefaultBranch: &f.Branch}); err != nil {
		workspaceFormError(ctx, &vm.Form, err)
		s.renderWorkspace(w, r, co, statusFor(err), wv, vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/workspaces/"+wv.Slug+"?ok=w-salvo")
}

func (s *server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	wv, err := s.be.GetWorkspaceView(ctx, co.ID, r.PathValue("workspace"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if err := s.be.DeleteWorkspace(ctx, actorFrom(ctx), co.ID, wv.ID, strings.TrimSpace(r.PostFormValue("unlink")) != ""); err != nil {
		var inUse *service.WorkspaceInUseError
		if errors.As(err, &inUse) {
			vm := s.buildWorkspaceVM(r, co, wv)
			_, title, msg := friendly(ctx, err)
			vm.Error = title + ". " + msg
			s.renderWorkspace(w, r, co, http.StatusConflict, wv, vm)
			return
		}
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/workspaces?ok=w-excluido")
}
