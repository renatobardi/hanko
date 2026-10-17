package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/api"
)

// Workspace com Git, padrões de projeto e "Nova sessão" pela CLI (ADR-0054).

func TestWorkspaceGitCommands(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	g := api.WorkspaceGit{WorkspaceID: "w1", Available: true, CredentialRef: "${secret:repo-token}", MirrorState: "ready", BaseBranch: "main",
		HeadSHA: strings.Repeat("a", 40), LastFetchAt: &now,
		Worktrees: []api.Worktree{{TaskKey: "HNK-123", Branch: "hnk-123", State: "ready", TaskStatus: "in_progress", HeadSHA: strings.Repeat("b", 40)}}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/workspaces/app/git":       jsonResp(200, g),
		"PUT /api/v1/companies/acme/workspaces/app/git":       jsonResp(200, g),
		"POST /api/v1/companies/acme/workspaces/app/git/sync": jsonResp(200, g),
		"DELETE /api/v1/companies/acme/workspaces/app/git":    jsonResp(204, nil),
		"GET /api/v1/companies/acme/projects/loja/git":        jsonResp(200, api.ListBody[api.Worktree]{Items: g.Worktrees}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}

	code, out, _ := exec(t, Runtime{}, env, "", "workspace", "git", "show", "app")
	if code != 0 {
		t.Fatalf("show: %d %q", code, out)
	}
	for _, frag := range []string{"ready", "main", "${secret:repo-token}", "HNK-123", "hnk-123", strings.Repeat("b", 8)} {
		if !strings.Contains(out, frag) {
			t.Fatalf("show sem %q:\n%s", frag, out)
		}
	}
	if strings.Contains(out, "HANKO_GIT_WORKSPACES") {
		t.Fatalf("o aviso do operador só aparece quando o recurso está desligado:\n%s", out)
	}

	code, out, _ = exec(t, Runtime{}, env, "", "workspace", "git", "enable", "app", "--credential", "${secret:repo-token}", "--sync")
	if code != 0 || !strings.Contains(out, "Git ligado no workspace app.") {
		t.Fatalf("enable: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"credential_ref": "${secret:repo-token}", "sync": true}, body()); diff != "" {
		t.Fatal(diff)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "workspace", "git", "sync", "app"); code != 0 || !strings.Contains(out, "ready") {
		t.Fatalf("sync: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "workspace", "git", "disable", "app"); code != 0 || !strings.Contains(out, "Git desligado no workspace app.") {
		t.Fatalf("disable: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "project", "git", "loja"); code != 0 || !strings.Contains(out, "HNK-123") {
		t.Fatalf("project git: %d %q", code, out)
	}
	_, out, _ = exec(t, Runtime{}, env, "", "workspace", "git", "show", "app", "--json")
	var got api.WorkspaceGit
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.CredentialRef != "${secret:repo-token}" || len(got.Worktrees) != 1 {
		t.Fatalf("json: %v %s", err, out)
	}

	// recurso desligado no servidor: aviso
	off := g
	off.Available = false
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/workspaces/app/git": jsonResp(200, off)})
	_, out, _ = exec(t, Runtime{}, map[string]string{"HANKO_URL": ts2.URL, "HANKO_COMPANY": "acme"}, "", "workspace", "git", "show", "app")
	if !strings.Contains(out, "HANKO_GIT_WORKSPACES") {
		t.Fatalf("aviso do operador ausente:\n%s", out)
	}
}

func TestTaskGitCommands(t *testing.T) {
	sha := strings.Repeat("c", 40)
	view := api.TaskGit{Live: true, Ahead: 2, Behind: 1, HeadSHA: sha, MirrorState: "ready",
		Worktree: &api.Worktree{Branch: "hnk-123", BaseBranch: "main", BaseSHA: strings.Repeat("a", 40), State: "ready"},
		Status:   &api.GitStatus{Branch: "hnk-123", Head: sha, Dirty: true, Entries: []api.GitStatusEntry{{XY: " M", Path: "README.md"}}}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tasks/HNK-123/git":             jsonResp(200, view),
		"GET /api/v1/companies/acme/tasks/HNK-123/git/diff":        jsonResp(200, api.GitPatch{Text: "diff --git a/x b/x\n+oi", Truncated: true}),
		"GET /api/v1/companies/acme/tasks/HNK-123/git/log":         jsonResp(200, api.ListBody[api.GitCommit]{Items: []api.GitCommit{{SHA: sha, Author: "Ana", Subject: "feat: busca"}}}),
		"GET /api/v1/companies/acme/tasks/HNK-123/git/tree":        jsonResp(200, api.GitTree{Entries: []api.GitEntry{{Name: "src", Dir: true}, {Name: "a.go", Size: 40}}}),
		"GET /api/v1/companies/acme/tasks/HNK-123/git/file":        jsonResp(200, api.GitFile{Path: "a.go", Size: 40, Content: "package a\n"}),
		"DELETE /api/v1/companies/acme/tasks/HNK-123/git/worktree": jsonResp(204, nil),
		"POST /api/v1/companies/acme/tasks/HNK-123/new-session":    jsonResp(201, api.TaskSessionReset{ID: "r1", TaskID: "t1", Reason: "confuso"}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	code, out, _ := exec(t, Runtime{}, env, "", "task", "git", "status", "HNK-123")
	if code != 0 {
		t.Fatalf("status: %d %q", code, out)
	}
	for _, frag := range []string{"hnk-123", "2 / 1", "README.md", "main"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("status sem %q:\n%s", frag, out)
		}
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "git", "diff", "HNK-123")
	if code != 0 || !strings.Contains(out, "+oi") || !strings.Contains(out, "512 KiB") {
		t.Fatalf("diff: %d %q", code, out)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "git", "log", "HNK-123", "--limit", "5")
	if code != 0 || !strings.Contains(out, "feat: busca") || !strings.Contains(out, "Ana") {
		t.Fatalf("log: %d %q", code, out)
	}
	if got := f.last().Query; !strings.Contains(got, "limit=5") {
		t.Fatalf("query do log = %q", got)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "git", "ls", "HNK-123", "src")
	if code != 0 || !strings.Contains(out, "src/") || !strings.Contains(out, "a.go") {
		t.Fatalf("ls: %d %q", code, out)
	}
	if got := f.last().Query; !strings.Contains(got, "path=src") {
		t.Fatalf("query do ls = %q", got)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "git", "cat", "HNK-123", "a.go")
	if code != 0 || !strings.Contains(out, "package a") {
		t.Fatalf("cat: %d %q", code, out)
	}
	if code, out, _ = exec(t, Runtime{}, env, "", "task", "git", "clean", "HNK-123"); code != 0 || !strings.Contains(out, "Worktree de HNK-123 limpo.") {
		t.Fatalf("clean: %d %q", code, out)
	}

	code, out, _ = exec(t, Runtime{}, env, "", "task", "new-session", "HNK-123", "--reason", "confuso")
	if code != 0 || !strings.Contains(out, "Nova sessão pedida para HNK-123") {
		t.Fatalf("new-session: %d %q", code, out)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(f.last().Body), &m)
	if diff := cmp.Diff(map[string]any{"reason": "confuso"}, m); diff != "" {
		t.Fatal(diff)
	}
	// pasta já limpa
	gone := view
	gone.Live, gone.Status = false, nil
	ts2, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/tasks/HNK-123/git": jsonResp(200, gone)})
	_, out, _ = exec(t, Runtime{}, map[string]string{"HANKO_URL": ts2.URL, "HANKO_COMPANY": "acme"}, "", "task", "git", "status", "HNK-123")
	if !strings.Contains(out, "já foi limpa") {
		t.Fatalf("status de pasta limpa:\n%s", out)
	}
}

func TestProjectDefaultsCommand(t *testing.T) {
	d := api.ProjectDefaults{ProjectID: "p1", Instructions: "Use o guia.", Goal: "g1", ImportRepoSkills: true, SkillsPath: ".claude/skills"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/projects/loja/defaults":   jsonResp(200, d),
		"PATCH /api/v1/companies/acme/projects/loja/defaults": jsonResp(200, d),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, _ := exec(t, Runtime{}, env, "", "project", "defaults", "loja")
	if code != 0 {
		t.Fatalf("show: %d %q", code, out)
	}
	for _, frag := range []string{"Use o guia.", "g1", "true", ".claude/skills", "review-policy"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("show sem %q:\n%s", frag, out)
		}
	}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "project", "defaults", "loja", "--instructions", "Novo briefing", "--goal", "meta-x", "--import-skills", "--skills-path", "skills"); code != 0 {
		t.Fatalf("patch: %d", code)
	}
	want := map[string]any{"instructions": "Novo briefing", "goal": "meta-x", "import_repo_skills": true, "skills_path": "skills"}
	if diff := cmp.Diff(want, body()); diff != "" {
		t.Fatal(diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "project", "defaults", "loja", "--clear-goal", "--import-skills=false"); code != 0 {
		t.Fatalf("clear: %d", code)
	}
	if diff := cmp.Diff(map[string]any{"clear_goal": true, "import_repo_skills": false}, body()); diff != "" {
		t.Fatal(diff)
	}
	// do arquivo
	file := filepath.Join(t.TempDir(), "briefing.md")
	if err := os.WriteFile(file, []byte("  do arquivo \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "project", "defaults", "loja", "--instructions-file", file); code != 0 {
		t.Fatalf("file: %d", code)
	}
	if diff := cmp.Diff(map[string]any{"instructions": "do arquivo"}, body()); diff != "" {
		t.Fatal(diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "project", "defaults", "loja", "--instructions", "a", "--instructions-file", file); code != 2 {
		t.Fatalf("os dois juntos: %d", code)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "project", "defaults", "loja", "--instructions-file", filepath.Join(t.TempDir(), "nao-existe")); code == 0 {
		t.Fatal("arquivo inexistente deveria falhar")
	}
}
