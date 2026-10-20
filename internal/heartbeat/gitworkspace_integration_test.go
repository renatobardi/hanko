//go:build integration

package heartbeat

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Fulano", "GIT_AUTHOR_EMAIL=f@x", "GIT_COMMITTER_NAME=Fulano", "GIT_COMMITTER_EMAIL=f@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Demo do M10: a task cria a branch hnk-123, a persona commita (adapter fake), a aba Git mostra o diff e o worktree
// some ao concluir. Repositório remoto local (file://), sem rede.
func TestTaskWorktreeDemo(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	broker := events.NewBroker()
	hb, err := New(pool, Options{Workers: 2, PeriodicInterval: time.Hour, RunTimeout: time.Minute, WorkspacesRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool, hb, broker)
	svc.WorkspacesRoot = root
	svc.GitWorkspaces = service.GitWorkspacesConfig{Enabled: true, AllowFile: true}
	spy := &spyAdapter{Adapter: fake.New(fake.Options{Delay: -1})}
	hb.Attach(svc, adapter.Registry{"fake": spy}, memory.Noop{}, broker, authForTest())
	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme", TaskPrefix: "HNK"})
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hb.Stop(sctx)
	})

	// repositório remoto com um commit
	origin := t.TempDir()
	gitIn(t, origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# loja\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "-A")
	gitIn(t, origin, "commit", "-q", "-m", "inicial")

	ws, err := svc.CreateWorkspace(ctx, domain.HumanActor, co.ID, service.CreateWorkspaceInput{Name: "Loja", Slug: "loja"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspaces SET repo_url = $1 WHERE id = $2`, "file://"+origin, ws.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnableWorkspaceGit(ctx, domain.HumanActor, co.ID, ws.ID, service.EnableWorkspaceGitInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SyncWorkspaceMirror(ctx, domain.HumanActor, co.ID, ws.ID); err != nil {
		t.Fatal(err)
	}
	pr, err := svc.CreateProject(ctx, domain.HumanActor, co.ID, service.CreateProjectInput{Name: "Loja online", WorkspaceID: &ws.ID})
	if err != nil {
		t.Fatal(err)
	}
	brief := "Siga o guia de estilo da loja."
	if _, err := svc.SetProjectDefaults(ctx, domain.HumanActor, co.ID, pr.ID, service.SetProjectDefaultsInput{Instructions: &brief}); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"touch":"busca.txt","commit":"feat: busca"}`)
	persona, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Instructions: "Você é a Ana.", Agent: "fake", AdapterConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}

	// a task nasce numerada HNK-1; para o demo da branch hnk-123, avança o contador
	if _, err := pool.Exec(ctx, `UPDATE companies SET next_task_number = 123 WHERE id = $1`, co.ID); err != nil {
		t.Logf("contador de tasks não ajustado (a branch será outra): %v", err)
	}
	task, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "Adicionar busca", PersonaID: &persona.ID, ProjectID: &pr.ID, Status: domain.TaskTodo})
	if err != nil {
		t.Fatal(err)
	}
	branch := strings.ToLower(task.Key)

	var run service.RunView
	waitFor(t, 60*time.Second, "run terminar", func() bool {
		runs, err := svc.ListRuns(ctx, co.ID, service.RunFilter{TaskID: &task.ID})
		if err != nil || len(runs) == 0 || !runs[0].Status.Terminal() {
			return false
		}
		run = runs[0]
		return true
	})
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}

	// o adapter rodou DENTRO do worktree, na branch da task, com o nome da persona e o briefing do projeto
	specs := spy.all()
	if len(specs) != 1 {
		t.Fatalf("specs = %d", len(specs))
	}
	sp := specs[0]
	wantDir := filepath.Join(root, "acme", ".hanko", "worktrees", "loja", branch)
	if got, _ := filepath.EvalSymlinks(sp.Dir); got != mustEval(t, wantDir) {
		t.Fatalf("Spec.Dir = %q, quer %q", sp.Dir, wantDir)
	}
	if sp.Env["GIT_AUTHOR_NAME"] != "Ana" || sp.Env["HANKO_WORKTREE_BRANCH"] != branch {
		t.Fatalf("env do adapter: %v", sp.Env)
	}
	if !strings.Contains(sp.System, "Siga o guia de estilo da loja.") || strings.Index(sp.System, "Você é a Ana.") > strings.Index(sp.System, "Siga o guia de estilo da loja.") {
		t.Fatalf("o briefing do projeto vem depois das instruções da persona:\n%s", sp.System)
	}
	if !strings.Contains(sp.System, "`"+branch+"`") {
		t.Fatalf("o system prompt deveria dizer a branch:\n%s", sp.System)
	}
	if got := gitIn(t, wantDir, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Fatalf("branch do worktree = %q", got)
	}

	// a aba Git: status, log e diff mostram o commit da persona
	view, err := svc.TaskGit(ctx, co.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Live || view.Ahead != 1 || view.Status == nil || view.Status.Dirty() || view.Worktree.State != domain.WorktreeReady {
		t.Fatalf("view: %+v wt=%+v", view, view.Worktree)
	}
	log, err := svc.TaskGitLog(ctx, co.ID, task.ID, 10)
	if err != nil || len(log) != 1 || log[0].Subject != "feat: busca" || log[0].Author != "Ana" {
		t.Fatalf("log: %+v %v", log, err)
	}
	patch, err := svc.TaskGitDiff(ctx, co.ID, task.ID)
	if err != nil || !strings.Contains(patch.Text, "busca.txt") || !strings.Contains(patch.Text, "+fake") {
		t.Fatalf("diff: %+v %v", patch, err)
	}

	// concluir a task: o tick de limpeza tira o worktree
	done := domain.TaskDone
	if _, err := svc.UpdateTask(ctx, domain.HumanActor, co.ID, task.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if err := hb.worktreeTick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wantDir); !os.IsNotExist(err) {
		t.Fatalf("o worktree deveria ter sumido: %v", err)
	}
	after, err := svc.TaskGit(ctx, co.ID, task.ID)
	if err != nil || after.Live || after.Worktree.State != domain.WorktreeRemoved {
		t.Fatalf("depois: %+v %v", after, err)
	}
	// o diff continua navegável a partir da branch guardada no espelho
	if p2, err := svc.TaskGitDiff(ctx, co.ID, task.ID); err != nil || !strings.Contains(p2.Text, "+fake") {
		t.Fatalf("diff pós-limpeza: %+v %v", p2, err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	// o diretório do worktree pode já não existir quando comparamos só o texto; resolve o pai que existe
	d := p
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(r, rest)
		}
		rest = filepath.Join(filepath.Base(d), rest)
		d = filepath.Dir(d)
		if d == "/" || d == "." {
			return p
		}
	}
}

// Um workspace sem Git continua rodando no diretório de sempre (ou no temporário): nada muda para quem não liga.
func TestRunWithoutGitWorkspaceKeepsOldBehavior(t *testing.T) {
	ctx := context.Background()
	svc, spy, co := newSecretStack(t)
	p, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	run := runTask(t, svc, co, p)
	if run.Status != domain.RunDone {
		t.Fatalf("status=%s error=%q", run.Status, run.Error)
	}
	specs := spy.all()
	if len(specs) != 1 || strings.Contains(specs[0].System, "worktree Git") || specs[0].Env["HANKO_WORKTREE_BRANCH"] != "" {
		t.Fatalf("sem Git, sem worktree: %+v", specs)
	}
}
