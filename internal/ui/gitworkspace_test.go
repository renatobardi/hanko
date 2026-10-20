package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/gitrepo"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Workspace com Git, padrões de projeto e "Nova sessão" na UI (ADR-0054).

func gitFixture(t *testing.T) (*uitest.Fake, *env, service.TaskView) {
	t.Helper()
	f := uitest.Sample()
	f.Git.Operator = true
	task := f.Tasks[0]
	sha := strings.Repeat("b", 40)
	f.Git.Worktrees = map[domain.ID]service.WorktreeView{task.ID: {TaskWorktree: domain.TaskWorktree{TaskID: task.ID, Branch: strings.ToLower(task.Key),
		BaseBranch: "main", State: domain.WorktreeReady, HeadSHA: sha, CreatedAt: time.Now(), UpdatedAt: time.Now()}, TaskKey: task.Key, TaskTitle: task.Title, TaskStatus: string(task.Status)}}
	f.Git.Status = gitrepo.Status{Branch: strings.ToLower(task.Key), Head: sha, Entries: []gitrepo.StatusEntry{{XY: " M", Path: "README.md"}, {XY: "??", Path: "novo.txt"}}}
	f.Git.Patch = gitrepo.Patch{Text: "diff --git a/busca.go b/busca.go\nnew file mode 100644\n--- /dev/null\n+++ b/busca.go\n@@ -0,0 +1 @@\n+package loja\n-velho\n contexto\n", Stat: " busca.go | 1 +"}
	f.Git.Commits = []gitrepo.Commit{{SHA: sha, Author: "Ada", At: time.Now(), Subject: "feat: busca"}}
	f.Git.Dirs = map[string][]gitrepo.Entry{"": {{Name: "src", Dir: true}, {Name: "README.md", Size: 12}}, "src": {{Name: "a.go", Size: 40}}}
	f.Git.Files = map[string]gitrepo.File{"README.md": {Path: "README.md", Size: 12, Content: "# demo <b>x</b>\n"}, "bin.dat": {Path: "bin.dat", Size: 3, Binary: true}}
	return f, newEnv(t, f), task
}

func TestTaskGitPageTabs(t *testing.T) {
	f, e, task := gitFixture(t)
	base := "/c/hanko/tasks/" + task.Key + "/git"

	rec := e.get(t, base)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, strings.ToLower(task.Key), "pronto", "README.md", "novo.txt", "À frente", "Diff", "Log", "Arquivos")

	rec = e.get(t, base+"?aba=diff")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `class="dl add"`, `class="dl del"`, `class="dl hunk"`, `class="dl meta"`, "+package loja", "busca.go | 1 +")

	rec = e.get(t, base+"?aba=log")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "feat: busca", "Ada", strings.Repeat("b", 8))

	rec = e.get(t, base+"?aba=arquivos")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "src", "README.md", "caminho=src")
	rec = e.get(t, base+"?aba=arquivos&caminho=src")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "a.go")
	// arquivo: o conteúdo é escapado
	rec = e.get(t, base+"?aba=arquivos&caminho=README.md")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "# demo &lt;b&gt;x&lt;/b&gt;")
	wantNotContains(t, rec, "<b>x</b>")
	rec = e.get(t, base+"?aba=arquivos&caminho=bin.dat")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Arquivo binário")
	// caminho que sai do repositório vira aviso, não erro de servidor
	rec = e.get(t, base+"?aba=arquivos&caminho=../etc")
	wantStatus(t, rec, http.StatusOK)

	// somente leitura: nenhuma rota de escrita no browser de arquivos
	if rec = e.post(t, base, nil, false); rec.Code < 400 {
		t.Fatalf("POST no browser de arquivos deveria falhar: %d", rec.Code)
	}

	// pasta limpa: o aviso aparece e o botão de limpar some
	w := f.Git.Worktrees[task.ID]
	w.State = domain.WorktreeRemoved
	f.Git.Worktrees[task.ID] = w
	rec = e.get(t, base)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "já foi limpa")
	wantNotContains(t, rec, "Limpar worktree")

	// task sem worktree
	other := f.Tasks[1]
	wantStatus(t, e.get(t, "/c/hanko/tasks/"+other.Key+"/git"), http.StatusNotFound)
}

func TestTaskSidePanelShowsGitAndNewSession(t *testing.T) {
	f, e, task := gitFixture(t)
	page := e.get(t, "/c/hanko/tasks/"+task.Key)
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, `id="task-git"`, "Ver diff, log e arquivos", "/tasks/"+task.Key+"/git", "Recomeçar a sessão", "/tasks/"+task.Key+"/nova-sessao")

	rec := e.post(t, "/c/hanko/tasks/"+task.Key+"/nova-sessao", url.Values{"reason": {"contexto confuso"}}, true)
	wantStatus(t, rec, http.StatusOK)
	if !f.Called("RequestTaskNewSession") || len(f.Git.Resets) != 1 || f.Git.Resets[0].Reason != "contexto confuso" {
		t.Fatalf("pedido não registrado: %+v", f.Git.Resets)
	}
	// sem JS: volta para a task com o aviso
	rec = e.post(t, "/c/hanko/tasks/"+task.Key+"/nova-sessao", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "ok=nova-sessao") {
		t.Fatalf("Location = %q", loc)
	}
}

func TestWorkspaceGitTab(t *testing.T) {
	f, e, task := gitFixture(t)
	// ainda sem Git ligado: oferece ligar
	rec := e.get(t, "/c/hanko/workspaces/site?secao=git")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Git do workspace", "Ligar Git e clonar", "${secret:repo-token}", `action="/c/hanko/workspaces/site/git"`)

	// ligar: guarda a referência e já sincroniza
	rec = e.post(t, "/c/hanko/workspaces/site/git", url.Values{"credential_ref": {"${secret:repo-token}"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !f.Called("EnableWorkspaceGit") || !f.Called("SyncWorkspaceMirror") {
		t.Fatalf("chamadas: %v", f.Calls)
	}
	rec = e.get(t, "/c/hanko/workspaces/site?secao=git")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Espelho do repositório", "Sincronizar agora", "Desligar Git", "${secret:repo-token}", "main", strings.ToLower(task.Key), "/tasks/"+task.Key+"/git")
	wantNotContains(t, rec, "ghp_") // só a referência aparece

	// sincronizar e desligar
	wantStatus(t, e.post(t, "/c/hanko/workspaces/site/git/sincronizar", nil, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/workspaces/site/git/desligar", nil, false), http.StatusSeeOther)
	if !f.Called("DisableWorkspaceGit") {
		t.Fatal("DisableWorkspaceGit não foi chamado")
	}

	// operador não ligou o recurso: aviso
	f.Git.Operator = false
	rec = e.get(t, "/c/hanko/workspaces/site?secao=git")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "HANKO_GIT_WORKSPACES")
}

func TestProjectGitAndDefaultsTabs(t *testing.T) {
	f, e, task := gitFixture(t)
	f.Git.Workspaces = map[string]domain.WorkspaceGit{"site": {MirrorState: domain.MirrorReady, BaseBranch: "main"}}
	rec := e.get(t, "/c/hanko/projetos/kubo?secao=git")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Worktrees das tasks do projeto", "/tasks/"+task.Key+"/git")

	// projeto sem workspace
	rec = e.get(t, "/c/hanko/projetos/linkedin?secao=git")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Sem workspace")

	rec = e.get(t, "/c/hanko/projetos/kubo?secao=padroes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Instruções do projeto", "Meta padrão", "Importar as skills do repositório clonado", "Política de revisão do projeto", `action="/c/hanko/projetos/kubo/padroes"`)

	rec = e.post(t, "/c/hanko/projetos/kubo/padroes", url.Values{"instructions": {"Use o guia."}, "import_skills": {"1"}, "skills_path": {".claude/skills"}, "goal": {""}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !f.Called("SetProjectDefaults") {
		t.Fatal("SetProjectDefaults não foi chamado")
	}
	var d domain.ProjectDefaults
	for _, v := range f.Git.Defaults {
		d = v
	}
	if d.Instructions != "Use o guia." || !d.ImportRepoSkills || d.SkillsPath != ".claude/skills" {
		t.Fatalf("padrões: %+v", d)
	}
	rec = e.get(t, "/c/hanko/projetos/kubo?secao=padroes")
	wantContains(t, rec, "Use o guia.", `checked`, ".claude/skills")

	// política de revisão do projeto reaproveita o ADR-0040
	rec = e.post(t, "/c/hanko/projetos/kubo/politica-revisao", url.Values{"require": {"yes"}, "reviewer": {"none"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if !f.Called("SetProjectReviewPolicy") {
		t.Fatal("SetProjectReviewPolicy não foi chamado")
	}
	rec = e.get(t, "/c/hanko/projetos/kubo?secao=padroes")
	wantContains(t, rec, "revisão obrigatória", "direto ao gate humano")
}

func TestGitPagesRoleGates(t *testing.T) {
	f, e, task := gitFixture(t)
	f.MyRoleIs = domain.MemberViewer
	rec := e.get(t, "/c/hanko/tasks/"+task.Key+"/git?aba=diff")
	wantStatus(t, rec, http.StatusOK) // viewer lê
	wantNotContains(t, rec, "Limpar worktree")
	rec = e.get(t, "/c/hanko/tasks/"+task.Key)
	wantNotContains(t, rec, "Recomeçar a sessão")
	rec = e.get(t, "/c/hanko/workspaces/site?secao=git")
	wantNotContains(t, rec, "Ligar Git e clonar")
	rec = e.get(t, "/c/hanko/projetos/kubo?secao=padroes")
	wantNotContains(t, rec, "Salvar padrões")
}

func TestPatchLinesClassification(t *testing.T) {
	got := patchLines("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n c\n")
	kinds := []string{}
	for _, l := range got {
		kinds = append(kinds, l.Kind)
	}
	if strings.Join(kinds, ",") != "meta,meta,meta,hunk,del,add,ctx" {
		t.Fatalf("kinds = %v", kinds)
	}
	if patchLines("") != nil {
		t.Fatal("texto vazio não gera linhas")
	}
	if oneLineTitle("  a\n b   c ") != "a b c" || len([]rune(oneLineTitle(strings.Repeat("x", 90)))) != 60 {
		t.Fatal("oneLineTitle")
	}
	if shortSHA("abc") != "abc" || shortSHA(strings.Repeat("a", 40)) != strings.Repeat("a", 8) {
		t.Fatal("shortSHA")
	}
	if dashText("") != "—" || dashText("x") != "x" {
		t.Fatal("dashText")
	}
}
