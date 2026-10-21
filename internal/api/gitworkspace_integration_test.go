//go:build integration

package api_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func apiGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ana", "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=Ana", "GIT_COMMITTER_EMAIL=a@x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Workspace com Git, padrões de projeto e "Nova sessão" pela API (ADR-0054).
func TestAPIGitWorkspace(t *testing.T) {
	e := newAPIEnv(t)
	root := t.TempDir()
	e.svc.WorkspacesRoot = root
	e.svc.GitWorkspaces = service.GitWorkspacesConfig{Enabled: true, AllowFile: true}
	base := e.base()
	ctx := context.Background()

	origin := t.TempDir()
	apiGit(t, origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# loja\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	apiGit(t, origin, "add", "-A")
	apiGit(t, origin, "commit", "-q", "-m", "inicial")

	var ws api.Workspace
	e.want(201, "POST", base+"/workspaces", `{"name":"Loja","slug":"loja","path":"`+root+`/acme/loja"}`, &ws)
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE workspaces SET repo_url = $1 WHERE id = $2`, "file://"+origin, ws.ID); err != nil {
		t.Fatal(err)
	}

	// sem Git ligado: 404 no detalhe
	if status, _ := e.call("GET", base+"/workspaces/loja/git", "", nil); status != http.StatusNotFound {
		t.Fatalf("antes de ligar: %d", status)
	}
	// credencial: só referência existente
	if status, _ := e.call("PUT", base+"/workspaces/loja/git", `{"credential_ref":"${secret:nao-existe}"}`, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("segredo inexistente: %d", status)
	}
	var g api.WorkspaceGit
	e.want(200, "PUT", base+"/workspaces/loja/git", `{"sync":true}`, &g)
	if g.MirrorState != "ready" || g.BaseBranch != "main" || len(g.HeadSHA) != 40 || !g.Available {
		t.Fatalf("ligar com sync: %+v", g)
	}
	e.want(200, "POST", base+"/workspaces/loja/git/sync", "", &g)
	if g.MirrorState != "ready" {
		t.Fatalf("sync: %+v", g)
	}

	var pr api.Project
	e.want(201, "POST", base+"/projects", `{"name":"Loja online","workspace":"loja"}`, &pr)
	var tk api.Task
	e.want(201, "POST", base+"/tasks", `{"title":"Adicionar busca","project":"`+pr.Slug+`"}`, &tk)

	// sem worktree ainda
	if status, _ := e.call("GET", base+"/tasks/"+tk.Key+"/git", "", nil); status != http.StatusNotFound {
		t.Fatalf("sem worktree: %d", status)
	}
	taskID, _ := domain.ParseID(tk.ID)
	wsID, _ := domain.ParseID(ws.ID)
	prep, err := e.svc.PrepareTaskWorktree(ctx, e.co.ID, taskID, wsID, true)
	if err != nil || prep == nil {
		t.Fatalf("prepare: %v %v", prep, err)
	}
	if err := os.WriteFile(filepath.Join(prep.Dir, "busca.go"), []byte("package loja\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	apiGit(t, prep.Dir, "add", "-A")
	apiGit(t, prep.Dir, "commit", "-q", "-m", "feat: busca")

	var tg api.TaskGit
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git", "", &tg)
	if !tg.Live || tg.Ahead != 1 || tg.Worktree == nil || tg.Worktree.Branch != strings.ToLower(tk.Key) || tg.Worktree.State != "ready" || tg.Status == nil || tg.Status.Dirty {
		t.Fatalf("task git: %+v wt=%+v", tg, tg.Worktree)
	}
	var patch api.GitPatch
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git/diff", "", &patch)
	if !strings.Contains(patch.Text, "+package loja") {
		t.Fatalf("diff: %+v", patch)
	}
	var log api.ListBody[api.GitCommit]
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git/log", "", &log)
	if len(log.Items) != 1 || log.Items[0].Subject != "feat: busca" {
		t.Fatalf("log: %+v", log)
	}
	var tree api.GitTree
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git/tree", "", &tree)
	if len(tree.Entries) < 2 {
		t.Fatalf("tree: %+v", tree)
	}
	var file api.GitFile
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git/file?path=busca.go", "", &file)
	if file.Content != "package loja\n" {
		t.Fatalf("file: %+v", file)
	}
	for _, bad := range []string{"../etc/passwd", ".git/config", "/etc/passwd"} {
		if status, _ := e.call("GET", base+"/tasks/"+tk.Key+"/git/file?path="+bad, "", nil); status < 400 {
			t.Errorf("file %q deveria falhar: %d", bad, status)
		}
	}
	var wsGit api.WorkspaceGit
	e.want(200, "GET", base+"/workspaces/loja/git", "", &wsGit)
	if len(wsGit.Worktrees) != 1 || wsGit.Worktrees[0].TaskKey != tk.Key {
		t.Fatalf("worktrees do workspace: %+v", wsGit.Worktrees)
	}
	var projWts api.ListBody[api.Worktree]
	e.want(200, "GET", base+"/projects/"+pr.Slug+"/git", "", &projWts)
	if len(projWts.Items) != 1 {
		t.Fatalf("worktrees do projeto: %+v", projWts)
	}

	// desligar com worktree em uso: 409; limpar à mão e desligar
	if status, _ := e.call("DELETE", base+"/workspaces/loja/git", "", nil); status != http.StatusConflict {
		t.Fatalf("desligar em uso: %d", status)
	}
	if status, _ := e.call("DELETE", base+"/tasks/"+tk.Key+"/git/worktree", "", nil); status != http.StatusNoContent {
		t.Fatalf("limpar worktree: %d", status)
	}
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git", "", &tg)
	if tg.Live || tg.Worktree.State != "removed" || tg.Worktree.RemovedReason != "manual" {
		t.Fatalf("depois de limpar: %+v wt=%+v", tg, tg.Worktree)
	}
	e.want(200, "GET", base+"/tasks/"+tk.Key+"/git/diff", "", &patch)
	if !strings.Contains(patch.Text, "+package loja") {
		t.Fatalf("diff depois de limpar: %+v", patch)
	}
	if status, _ := e.call("DELETE", base+"/workspaces/loja/git", "", nil); status != http.StatusNoContent {
		t.Fatalf("desligar: %d", status)
	}
}

func TestAPIProjectDefaultsAndNewSession(t *testing.T) {
	e := newAPIEnv(t)
	base := e.base()
	var pr api.Project
	e.want(201, "POST", base+"/projects", `{"name":"Loja online"}`, &pr)
	var d api.ProjectDefaults
	e.want(200, "GET", base+"/projects/"+pr.Slug+"/defaults", "", &d)
	if d.Instructions != "" || d.ImportRepoSkills {
		t.Fatalf("padrões vazios: %+v", d)
	}
	var goal api.Goal
	e.want(201, "POST", base+"/goals", `{"title":"Chegar a 100","metric":"usuários","start_value":0,"target_value":100}`, &goal)
	e.want(200, "PATCH", base+"/projects/"+pr.Slug+"/defaults", `{"instructions":"Use o guia.","goal":"`+goal.Slug+`","import_repo_skills":true,"skills_path":"skills"}`, &d)
	if d.Instructions != "Use o guia." || d.Goal != goal.ID || !d.ImportRepoSkills || d.SkillsPath != "skills" {
		t.Fatalf("padrões: %+v", d)
	}
	var tk api.Task
	e.want(201, "POST", base+"/tasks", `{"title":"Com meta padrão","project":"`+pr.Slug+`"}`, &tk)
	if tk.GoalID != goal.ID {
		t.Fatalf("a task deveria herdar a meta padrão: %+v", tk)
	}
	if status, _ := e.call("PATCH", base+"/projects/"+pr.Slug+"/defaults", `{"skills_path":"../fora"}`, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("caminho inválido: %d", status)
	}
	if status, _ := e.call("PATCH", base+"/projects/nao-existe/defaults", `{}`, nil); status != http.StatusNotFound {
		t.Fatalf("projeto inexistente: %d", status)
	}

	var rst api.TaskSessionReset
	e.want(201, "POST", base+"/tasks/"+tk.Key+"/new-session", `{"reason":"contexto confuso"}`, &rst)
	if rst.Reason != "contexto confuso" || rst.TaskID != tk.ID || rst.ConsumedRunID != "" {
		t.Fatalf("pedido: %+v", rst)
	}
	if status, _ := e.call("POST", base+"/tasks/HNK-9999/new-session", `{}`, nil); status != http.StatusNotFound {
		t.Fatalf("task inexistente: %d", status)
	}
}
