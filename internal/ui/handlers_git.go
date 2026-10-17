package ui

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Workspace de execução com Git, padrões de projeto e "Nova sessão" na UI (ADR-0054). A barreira de papel é o
// service; a UI só esconde os controles que o papel não tem. O backend de Git é opcional (interface própria): os
// fakes de teste que não o usam continuam valendo.

type gitBackend interface {
	GitWorkspacesEnabled() bool
	GetWorkspaceGit(ctx context.Context, companyID domain.ID, ref string) (service.WorkspaceGitView, error)
	EnableWorkspaceGit(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID, in service.EnableWorkspaceGitInput) (domain.WorkspaceGit, error)
	DisableWorkspaceGit(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID) error
	SyncWorkspaceMirror(ctx context.Context, actor domain.Actor, companyID, workspaceID domain.ID) (domain.WorkspaceGit, error)
	ListProjectWorktrees(ctx context.Context, companyID, projectID domain.ID) ([]service.WorktreeView, error)
	TaskGit(ctx context.Context, companyID, taskID domain.ID) (service.TaskGitView, error)
	TaskGitDiff(ctx context.Context, companyID, taskID domain.ID) (gitrepo.Patch, error)
	TaskGitLog(ctx context.Context, companyID, taskID domain.ID, limit int) ([]gitrepo.Commit, error)
	TaskGitTree(ctx context.Context, companyID, taskID domain.ID, path string) (service.TaskGitTree, error)
	TaskGitFile(ctx context.Context, companyID, taskID domain.ID, path string) (gitrepo.File, error)
	RemoveTaskWorktree(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) error
	RequestTaskNewSession(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, reason string) (domain.TaskSessionReset, error)
	GetProjectDefaults(ctx context.Context, companyID, projectID domain.ID) (domain.ProjectDefaults, error)
	SetProjectDefaults(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in service.SetProjectDefaultsInput) (domain.ProjectDefaults, error)
}

func (s *server) gitBE() (gitBackend, bool) {
	g, ok := s.be.(gitBackend)
	return g, ok
}

func init() {
	for k, v := range map[string]string{
		"git-ligado":    i18n.N("Git ligado: o espelho do repositório está sendo preparado."),
		"git-sync":      i18n.N("Espelho sincronizado."),
		"git-desligado": i18n.N("Git desligado neste workspace."),
		"git-cred":      i18n.N("Credencial salva."),
		"git-limpo":     i18n.N("Worktree limpo; a branch segue no espelho."),
		"nova-sessao":   i18n.N("Pedido registrado: o próximo run começa uma sessão nova."),
		"padroes":       i18n.N("Padrões do projeto salvos."),
		"politica-proj": i18n.N("Política de revisão do projeto salva."),
	} {
		notices[k] = v
		projectNotices[k] = v
	}
}

func worktreeBadge(ctx context.Context, st domain.WorktreeState) views.Badge {
	switch st {
	case domain.WorktreeReady:
		return views.Badge{Label: i18n.T(ctx, "pronto"), Class: "run"}
	case domain.WorktreeProvisioning:
		return views.Badge{Label: i18n.T(ctx, "provisionando"), Class: "outline", Live: true}
	case domain.WorktreeFailed:
		return views.Badge{Label: i18n.T(ctx, "falhou"), Class: "danger"}
	case domain.WorktreeDiverged:
		return views.Badge{Label: i18n.T(ctx, "divergente"), Class: "warn"}
	}
	return views.Badge{Label: i18n.T(ctx, "removido"), Class: "soft"}
}

func mirrorBadge(ctx context.Context, st domain.MirrorState) views.Badge {
	switch st {
	case domain.MirrorReady:
		return views.Badge{Label: i18n.T(ctx, "pronto"), Class: "run"}
	case domain.MirrorSyncing:
		return views.Badge{Label: i18n.T(ctx, "sincronizando"), Class: "outline", Live: true}
	case domain.MirrorFailed:
		return views.Badge{Label: i18n.T(ctx, "falhou"), Class: "danger"}
	}
	return views.Badge{Label: i18n.T(ctx, "ainda não clonado"), Class: "soft"}
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func (s *server) worktreeRows(ctx context.Context, base string, ws []service.WorktreeView) []views.GitWorktreeRow {
	rows := make([]views.GitWorktreeRow, 0, len(ws))
	for _, w := range ws {
		rows = append(rows, views.GitWorktreeRow{TaskKey: w.TaskKey, TaskHref: base + "/tasks/" + w.TaskKey, TaskTitle: oneLineTitle(w.TaskTitle),
			TaskStatus: taskStatusLabel(ctx, domain.TaskStatus(w.TaskStatus)), Branch: w.Branch, GitHref: base + "/tasks/" + w.TaskKey + "/git",
			State: worktreeBadge(ctx, w.State), Head: shortSHA(w.HeadSHA), Error: w.Error})
	}
	return rows
}

func oneLineTitle(t string) string {
	r := []rune(strings.Join(strings.Fields(t), " "))
	if len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return string(r)
}

// workspaceGitVM monta a aba Git do workspace.
func (s *server) workspaceGitVM(r *http.Request, co domain.Company, wv service.WorkspaceView) views.WorkspaceGitVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	gb, ok := s.gitBE()
	vm := views.WorkspaceGitVM{RepoURL: wv.RepoURL}
	if !ok {
		return vm
	}
	wsBase := base + "/workspaces/" + wv.Slug
	vm.EnableAction, vm.SyncAction, vm.DisableAction = wsBase+"/git", wsBase+"/git/sincronizar", wsBase+"/git/desligar"
	v, err := gb.GetWorkspaceGit(ctx, co.ID, wv.Slug)
	vm.Available = v.Available || gb.GitWorkspacesEnabled()
	if err != nil {
		return vm // sem Git ligado (ou erro de leitura): a aba oferece ligar
	}
	vm.Enabled = true
	loc := loadLoc(co.Timezone)
	vm.Mirror = mirrorBadge(ctx, v.MirrorState)
	vm.Base, vm.Head, vm.LastErr, vm.Credential = dashText(v.BaseBranch), dashText(shortSHA(v.HeadSHA)), v.LastError, v.CredentialRef
	vm.LastTime = "—"
	if v.LastFetchAt != nil {
		vm.LastTime = fmtDateTime(ctx, *v.LastFetchAt, loc)
	}
	vm.Worktrees = s.worktreeRows(ctx, base, v.Worktrees)
	return vm
}

func dashText(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// projectGitVM monta a aba Git do projeto.
func (s *server) projectGitVM(r *http.Request, co domain.Company, p service.ProjectView) views.ProjectGitVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.ProjectGitVM{HasWorkspace: p.WorkspaceSlug != ""}
	gb, ok := s.gitBE()
	if !vm.HasWorkspace || !ok {
		return vm
	}
	vm.WorkspaceHref = base + "/workspaces/" + p.WorkspaceSlug + "?secao=git"
	vm.Available = gb.GitWorkspacesEnabled()
	if _, err := gb.GetWorkspaceGit(ctx, co.ID, p.WorkspaceSlug); err != nil {
		return vm
	}
	vm.GitEnabled = true
	if ws, err := gb.ListProjectWorktrees(ctx, co.ID, p.ID); err == nil {
		vm.Rows = s.worktreeRows(ctx, base, ws)
	}
	return vm
}

// projectDefaultsVM monta a aba Padrões do projeto.
func (s *server) projectDefaultsVM(r *http.Request, co domain.Company, p service.ProjectView) views.ProjectDefaultsVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	pBase := base + "/projetos/" + p.Slug
	vm := views.ProjectDefaultsVM{Action: pBase + "/padroes", ReviewAction: pBase + "/politica-revisao"}
	if gb, ok := s.gitBE(); ok {
		if d, err := gb.GetProjectDefaults(ctx, co.ID, p.ID); err == nil {
			vm.Instructions, vm.ImportSkills, vm.SkillsPath = d.Instructions, d.ImportRepoSkills, d.SkillsPath
			vm.GoalOptions = s.goalOptions(ctx, co, d.GoalID)
		}
		if p.WorkspaceSlug != "" {
			_, err := gb.GetWorkspaceGit(ctx, co.ID, p.WorkspaceSlug)
			vm.HasRepo = err == nil
		}
	}
	if vm.GoalOptions == nil {
		vm.GoalOptions = s.goalOptions(ctx, co, nil)
	}
	o, _ := s.be.ProjectReviewPolicy(ctx, co.ID, p.ID)
	personas, _ := s.be.ListPersonas(ctx, co.ID)
	vm.RequireOptions = requireOptions(ctx, o)
	vm.ReviewerOptions = reviewerOptions(ctx, o, personas)
	vm.ReviewSummary = projectReviewSummary(ctx, o)
	return vm
}

func projectReviewSummary(ctx context.Context, o domain.ReviewOverride) string {
	req := i18n.T(ctx, "herda da persona")
	if o.RequireReview != nil {
		req = i18n.T(ctx, "dispensada")
		if *o.RequireReview {
			req = i18n.T(ctx, "obrigatória")
		}
	}
	rev := i18n.T(ctx, "herda da persona")
	switch o.ReviewerMode {
	case domain.ReviewerNone:
		rev = i18n.T(ctx, "nenhum: direto ao gate humano")
	case domain.ReviewerCustom:
		rev = i18n.T(ctx, "definido pelo projeto")
	}
	return i18n.Tf(ctx, "Política de revisão do projeto: revisão %s; revisor %s. Uma sobreposição na task vale mais.", req, rev)
}

func (s *server) goalOptions(ctx context.Context, co domain.Company, sel *domain.ID) []views.Option {
	out := []views.Option{{Value: "", Label: i18n.T(ctx, "Nenhuma"), Selected: sel == nil}}
	goals, err := s.be.ListGoalViews(ctx, co.ID, service.GoalFilter{})
	if err != nil {
		return out
	}
	for _, g := range goals {
		cur := sel != nil && *sel == g.ID
		if g.Status != domain.GoalActive && !cur {
			continue
		}
		out = append(out, views.Option{Value: g.ID.String(), Label: g.Title, Selected: cur})
	}
	return out
}

// ---------- ações do workspace ----------

func (s *server) gitWorkspaceOf(w http.ResponseWriter, r *http.Request, co domain.Company) (gitBackend, service.WorkspaceView, bool) {
	gb, ok := s.gitBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return nil, service.WorkspaceView{}, false
	}
	wv, err := s.be.GetWorkspaceView(r.Context(), co.ID, r.PathValue("workspace"))
	if err != nil {
		s.fail(w, r, &co, err)
		return nil, service.WorkspaceView{}, false
	}
	return gb, wv, true
}

func (s *server) handleEnableGit(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, wv, ok := s.gitWorkspaceOf(w, r, co)
	if !ok {
		return
	}
	ctx := r.Context()
	_, had := gb.GetWorkspaceGit(ctx, co.ID, wv.Slug)
	if _, err := gb.EnableWorkspaceGit(ctx, actorFrom(ctx), co.ID, wv.ID, service.EnableWorkspaceGitInput{CredentialRef: trimForm(r, "credential_ref")}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	notice := "git-cred"
	if had != nil { // primeira vez: já clona
		notice = "git-ligado"
		if _, err := gb.SyncWorkspaceMirror(ctx, actorFrom(ctx), co.ID, wv.ID); err != nil {
			s.fail(w, r, &co, err)
			return
		}
	}
	redirect(w, r, "/c/"+co.Slug+"/workspaces/"+wv.Slug+"?secao=git&ok="+notice)
}

func (s *server) handleSyncGit(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, wv, ok := s.gitWorkspaceOf(w, r, co)
	if !ok {
		return
	}
	if _, err := gb.SyncWorkspaceMirror(r.Context(), actorFrom(r.Context()), co.ID, wv.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/workspaces/"+wv.Slug+"?secao=git&ok=git-sync")
}

func (s *server) handleDisableGit(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, wv, ok := s.gitWorkspaceOf(w, r, co)
	if !ok {
		return
	}
	if err := gb.DisableWorkspaceGit(r.Context(), actorFrom(r.Context()), co.ID, wv.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/workspaces/"+wv.Slug+"?secao=git&ok=git-desligado")
}

// ---------- padrões do projeto ----------

func (s *server) handleProjectDefaults(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, ok := s.gitBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	p, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	instr := strings.TrimSpace(r.PostFormValue("instructions"))
	skillsPath := trimForm(r, "skills_path")
	importSkills := r.PostFormValue("import_skills") != ""
	in := service.SetProjectDefaultsInput{Instructions: &instr, ImportRepoSkills: &importSkills, SkillsPath: &skillsPath}
	switch g := trimForm(r, "goal"); g {
	case "":
		in.ClearGoal = true
	default:
		id, perr := domain.ParseID(g)
		if perr != nil {
			s.fail(w, r, &co, domain.Invalid("goal", i18n.T(ctx, "id inválido")))
			return
		}
		in.GoalID = &id
	}
	if _, err := gb.SetProjectDefaults(ctx, actorFrom(ctx), co.ID, p.ID, in); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/projetos/"+p.Slug+"?secao=padroes&ok=padroes")
}

func (s *server) handleProjectReviewPolicy(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetProjectView(ctx, co.ID, r.PathValue("project"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	o := domain.ReviewOverride{ReviewerMode: domain.ReviewerInherit}
	switch trimForm(r, "require") {
	case "yes":
		v := true
		o.RequireReview = &v
	case "no":
		v := false
		o.RequireReview = &v
	}
	switch rv := trimForm(r, "reviewer"); rv {
	case "", "inherit":
	case "none":
		o.ReviewerMode = domain.ReviewerNone
	default:
		id, perr := domain.ParseID(rv)
		if perr != nil {
			s.fail(w, r, &co, domain.Invalid("reviewer", i18n.T(ctx, "id inválido")))
			return
		}
		o.ReviewerMode, o.ReviewerPersonaID = domain.ReviewerCustom, &id
	}
	if _, err := s.be.SetProjectReviewPolicy(ctx, actorFrom(ctx), co.ID, p.ID, o); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/projetos/"+p.Slug+"?secao=padroes&ok=politica-proj")
}

// ---------- task ----------

// gitCoord preenche, no painel lateral da task, o worktree (se houver) e a ação "Nova sessão".
func (s *server) gitCoord(ctx context.Context, co domain.Company, t service.TaskView, c *views.TaskCoordVM) {
	gb, ok := s.gitBE()
	if !ok {
		return
	}
	base := "/c/" + co.Slug
	if t.Status.Open() {
		c.NewSessionHref = base + "/tasks/" + t.Key + "/nova-sessao"
	}
	if v, err := gb.TaskGit(ctx, co.ID, t.ID); err == nil && v.Worktree != nil {
		c.GitHref = base + "/tasks/" + t.Key + "/git"
		c.GitBranch = v.Worktree.Branch
		c.GitBadge = worktreeBadge(ctx, v.Worktree.State)
	}
}

func (s *server) handleNewSession(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, ok := s.gitBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	if _, err := gb.RequestTaskNewSession(r.Context(), actorFrom(r.Context()), co.ID, t.ID, trimForm(r, "reason")); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.respondTaskSide(w, r, co, t.ID, "nova-sessao")
}

var taskGitTabs = []struct{ key, label string }{
	{"status", i18n.N("Status")}, {"diff", i18n.N("Diff")}, {"log", i18n.N("Log")}, {"arquivos", i18n.N("Arquivos")},
}

func patchLines(text string) []views.GitDiffLine {
	if text == "" {
		return nil
	}
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]views.GitDiffLine, 0, len(raw))
	for _, l := range raw {
		kind := "ctx"
		switch {
		case strings.HasPrefix(l, "diff --git"), strings.HasPrefix(l, "index "), strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "),
			strings.HasPrefix(l, "new file"), strings.HasPrefix(l, "deleted file"), strings.HasPrefix(l, "similarity"), strings.HasPrefix(l, "Binary files"):
			kind = "meta"
		case strings.HasPrefix(l, "@@"):
			kind = "hunk"
		case strings.HasPrefix(l, "+"):
			kind = "add"
		case strings.HasPrefix(l, "-"):
			kind = "del"
		}
		out = append(out, views.GitDiffLine{Kind: kind, Text: l})
	}
	return out
}

func (s *server) handleTaskGit(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, ok := s.gitBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	base := "/c/" + co.Slug
	taskBase := base + "/tasks/" + t.Key
	v, err := gb.TaskGit(ctx, co.ID, t.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	loc := loadLoc(co.Timezone)
	tab := "status"
	for _, tt := range taskGitTabs {
		if tt.key == r.URL.Query().Get("aba") {
			tab = tt.key
		}
	}
	vm := views.TaskGitVM{Key: t.Key, Title: t.Title, TaskHref: taskBase, Tab: tab, Live: v.Live, Ahead: v.Ahead, Behind: v.Behind, Merged: v.Merged,
		Notice: i18n.T(ctx, notices[r.URL.Query().Get("ok")]), MirrorOK: v.MirrorState == domain.MirrorReady || v.MirrorState == domain.MirrorSyncing || v.MirrorError == "",
		MirrorEr: v.MirrorError, CleanAction: taskBase + "/git/limpar"}
	if wt := v.Worktree; wt != nil {
		vm.Branch, vm.Base, vm.Head, vm.State, vm.WtError = wt.Branch, dashText(wt.BaseBranch), shortSHA(v.HeadSHA), worktreeBadge(ctx, wt.State), wt.Error
	}
	for _, tt := range taskGitTabs {
		vm.Tabs = append(vm.Tabs, views.TabLink{Key: tt.key, Label: i18n.T(ctx, tt.label), Href: taskBase + "/git?aba=" + tt.key, On: tt.key == tab})
	}
	fail := func(err error) {
		vm.Error = i18n.T(ctx, "Não foi possível ler o repositório agora.")
		var ve *domain.ValidationError
		if errors.As(err, &ve) {
			vm.Error = ve.Reason
		}
	}
	switch tab {
	case "diff":
		p, err := gb.TaskGitDiff(ctx, co.ID, t.ID)
		if err != nil {
			fail(err)
			break
		}
		vm.Diff, vm.DiffStat, vm.DiffTruncated = patchLines(p.Text), strings.TrimSpace(p.Stat), p.Truncated
	case "log":
		cs, err := gb.TaskGitLog(ctx, co.ID, t.ID, 200)
		if err != nil {
			fail(err)
			break
		}
		for _, c := range cs {
			vm.Log = append(vm.Log, views.GitCommitRow{SHA: shortSHA(c.SHA), Author: c.Author, When: fmtDateTime(ctx, c.At.In(time.UTC), loc), Subject: c.Subject})
		}
	case "arquivos":
		s.fillGitFiles(ctx, gb, co, t, taskBase, r.URL.Query().Get("caminho"), &vm, fail)
	default:
		if v.Status != nil {
			for _, e := range v.Status.Entries {
				vm.Files = append(vm.Files, views.GitFileRow{XY: e.XY, Path: e.Path})
			}
			vm.Clean = len(v.Status.Entries) == 0
		} else {
			vm.Clean = true
		}
	}
	sh := s.shell(r, co, "tasks", t.Key+" · Git", views.Crumb{Label: i18n.T(ctx, "Tasks"), Href: base + "/tasks"}, views.Crumb{Label: t.Key, Href: taskBase}, views.Crumb{Label: "Git"})
	s.render(w, r, http.StatusOK, views.TaskGitPage(sh, vm))
}

// fillGitFiles abre uma pasta ou um arquivo no browser somente leitura. caminho é relativo à raiz do repositório;
// um arquivo é tentado quando o caminho não é uma pasta.
func (s *server) fillGitFiles(ctx context.Context, gb gitBackend, co domain.Company, t service.TaskView, taskBase, rel string, vm *views.TaskGitVM, fail func(error)) {
	clean, err := domain.CleanRepoPath(rel)
	if err != nil {
		fail(err)
		return
	}
	href := func(p string) string {
		if p == "" {
			return taskBase + "/git?aba=arquivos"
		}
		return taskBase + "/git?aba=arquivos&caminho=" + url.QueryEscape(p)
	}
	vm.Crumbs = []views.GitPathCrumb{{Name: i18n.T(ctx, "raiz"), Href: href("")}}
	acc := ""
	if clean != "" {
		for _, seg := range strings.Split(clean, "/") {
			acc = path.Join(acc, seg)
			vm.Crumbs = append(vm.Crumbs, views.GitPathCrumb{Name: seg, Href: href(acc)})
		}
	}
	tree, terr := gb.TaskGitTree(ctx, co.ID, t.ID, clean)
	if terr == nil {
		for _, e := range tree.Entries {
			size := ""
			if !e.Dir {
				size = fmtBytes(ctx, e.Size)
			}
			vm.Entries = append(vm.Entries, views.GitEntryRow{Name: e.Name, Dir: e.Dir, Size: size, Href: href(path.Join(clean, e.Name))})
		}
		return
	}
	if clean == "" {
		fail(terr)
		return
	}
	f, ferr := gb.TaskGitFile(ctx, co.ID, t.ID, clean)
	if ferr != nil {
		fail(ferr)
		return
	}
	vm.File = &views.GitFileView{Path: f.Path, Size: fmtBytes(ctx, f.Size), Content: f.Content, Binary: f.Binary, Truncated: f.Truncated}
}

func (s *server) handleTaskGitClean(w http.ResponseWriter, r *http.Request, co domain.Company) {
	gb, ok := s.gitBE()
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	t, ok := s.taskOf(w, r, co)
	if !ok {
		return
	}
	if err := gb.RemoveTaskWorktree(r.Context(), actorFrom(r.Context()), co.ID, t.ID); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/tasks/"+t.Key+"/git?ok=git-limpo")
}
