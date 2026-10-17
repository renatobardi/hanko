package service_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Workspace de execução com Git (ADR-0054): repositórios locais em diretórios temporários (file://), sem rede.

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Ana", "GIT_AUTHOR_EMAIL=ana@hanko.local", "GIT_COMMITTER_NAME=Ana", "GIT_COMMITTER_EMAIL=ana@hanko.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitEnv é newEnv com o recurso ligado, a raiz dos workspaces numa pasta temporária e um repositório "remoto" local.
type gitEnv struct {
	*env
	root   string
	origin string // pasta do repositório remoto
	ws     domain.Workspace
}

func newGitEnv(t *testing.T) *gitEnv {
	t.Helper()
	e := newEnv(t)
	root := t.TempDir()
	e.svc.WorkspacesRoot = root
	e.svc.GitWorkspaces = service.GitWorkspacesConfig{Enabled: true, AllowFile: true}
	origin := t.TempDir()
	gitCmd(t, origin, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(origin, "README.md"), "# demo\n")
	writeFile(t, filepath.Join(origin, "skills", "revisar", "SKILL.md"), "---\nname: revisar-codigo\ndescription: Revisa o código antes do merge\n---\n\nConfira o diff.\n")
	gitCmd(t, origin, "add", "-A")
	gitCmd(t, origin, "commit", "-q", "-m", "inicial")
	ws, err := e.svc.CreateWorkspace(context.Background(), domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "App", Slug: "app"})
	if err != nil {
		t.Fatal(err)
	}
	// repo_url file:// não passa na validação de criação (só https/ssh); o teste grava direto
	if _, err := e.svc.Pool.Exec(context.Background(), `UPDATE workspaces SET repo_url = $1 WHERE id = $2`, "file://"+origin, ws.ID); err != nil {
		t.Fatal(err)
	}
	ws.RepoURL = "file://" + origin
	return &gitEnv{env: e, root: root, origin: origin, ws: ws}
}

func (e *gitEnv) enableAndSync(t *testing.T) domain.WorkspaceGit {
	t.Helper()
	bg := context.Background()
	if _, err := e.svc.EnableWorkspaceGit(bg, domain.HumanActor, e.co.ID, e.ws.ID, service.EnableWorkspaceGitInput{}); err != nil {
		t.Fatal(err)
	}
	wg, err := e.svc.SyncWorkspaceMirror(bg, domain.HumanActor, e.co.ID, e.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wg.MirrorState != domain.MirrorReady || wg.BaseBranch != "main" || len(wg.HeadSHA) != 40 {
		t.Fatalf("espelho: %+v", wg)
	}
	return wg
}

// project cria um projeto no workspace e uma task nele; devolve a task.
func (e *gitEnv) projectTask(t *testing.T) service.TaskView {
	t.Helper()
	pr, err := e.svc.CreateProject(context.Background(), domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Loja", WorkspaceID: &e.ws.ID})
	if err != nil {
		t.Fatal(err)
	}
	p := e.persona(t, "Ana", nil)
	return e.task(t, service.CreateTaskInput{Title: "Adicionar busca", PersonaID: &p.ID, ProjectID: &pr.ID})
}

func (e *gitEnv) verbs(t *testing.T, subject domain.ID) []string {
	t.Helper()
	rows, err := e.svc.Pool.Query(context.Background(), `SELECT verb FROM activity_events WHERE company_id = $1 AND subject_id = $2 ORDER BY occurred_at, received_at`, e.co.ID, subject)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func hasVerb(vs []string, want string) bool {
	for _, v := range vs {
		if v == want {
			return true
		}
	}
	return false
}

func TestWorktreeLifecycleDemo(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	tk := e.projectTask(t)

	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil || prep == nil {
		t.Fatalf("prepare: %v %v", prep, err)
	}
	if want := strings.ToLower(tk.Key); prep.Branch != want {
		t.Fatalf("branch = %q, quer %q", prep.Branch, want)
	}
	if !strings.HasPrefix(prep.Dir, filepath.Join(e.root, e.co.Slug, ".hanko", "worktrees", "app")) {
		t.Fatalf("o worktree deve ficar sob a pasta da empresa: %s", prep.Dir)
	}
	if got := gitCmd(t, prep.Dir, "rev-parse", "--abbrev-ref", "HEAD"); got != prep.Branch {
		t.Fatalf("HEAD = %q", got)
	}
	// segunda chamada reaproveita
	again, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil || again.Dir != prep.Dir {
		t.Fatalf("segunda preparação: %+v %v", again, err)
	}

	// a persona commita (o adapter fake faz isto no teste do heartbeat; aqui, direto no worktree)
	writeFile(t, filepath.Join(prep.Dir, "busca.go"), "package loja\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: busca")
	writeFile(t, filepath.Join(prep.Dir, "README.md"), "# demo\nbusca\n")

	view, err := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Live || view.Ahead != 1 || view.Behind != 0 || view.Merged || view.Status == nil || !view.Status.Dirty() ||
		view.Worktree.State != domain.WorktreeReady || view.Worktree.TaskKey != tk.Key {
		t.Fatalf("view: %+v status=%+v wt=%+v", view, view.Status, view.Worktree)
	}
	patch, err := e.svc.TaskGitDiff(bg, e.co.ID, tk.ID)
	if err != nil || !strings.Contains(patch.Text, "+package loja") || !strings.Contains(patch.Text, "+busca") {
		t.Fatalf("diff: %+v %v", patch, err)
	}
	log, err := e.svc.TaskGitLog(bg, e.co.ID, tk.ID, 10)
	if err != nil || len(log) != 1 || log[0].Subject != "feat: busca" {
		t.Fatalf("log: %+v %v", log, err)
	}
	tree, err := e.svc.TaskGitTree(bg, e.co.ID, tk.ID, "")
	if err != nil || len(tree.Entries) < 3 {
		t.Fatalf("tree: %+v %v", tree, err)
	}
	file, err := e.svc.TaskGitFile(bg, e.co.ID, tk.ID, "busca.go")
	if err != nil || file.Content != "package loja\n" {
		t.Fatalf("file: %+v %v", file, err)
	}
	for _, bad := range []string{"../etc/passwd", ".git/config", "/etc/passwd"} {
		if _, err := e.svc.TaskGitFile(bg, e.co.ID, tk.ID, bad); err == nil {
			t.Errorf("TaskGitFile(%q) deveria falhar", bad)
		}
	}

	// task aberta: nada a limpar
	res, err := e.svc.ReapWorktrees(bg, 50)
	if err != nil || res.Removed != 0 || res.Kept != 1 {
		t.Fatalf("reap com task aberta: %+v %v", res, err)
	}

	// concluir a task: o worktree some, com o trabalho solto preservado num commit
	done := domain.TaskDone
	if _, err := e.svc.UpdateTask(bg, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.ReapWorktrees(bg, 50)
	if err != nil || res.Removed != 1 || res.Failed != 0 {
		t.Fatalf("reap: %+v %v", res, err)
	}
	if _, err := os.Stat(prep.Dir); !os.IsNotExist(err) {
		t.Fatalf("a pasta deveria ter sumido: %v", err)
	}
	after, err := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Live || after.Worktree.State != domain.WorktreeRemoved || after.Worktree.RemovedWhy != domain.RemovedDone || after.Ahead != 2 {
		t.Fatalf("depois: %+v wt=%+v", after, after.Worktree)
	}
	// a aba Git continua mostrando o diff e o log (agora a partir da branch no espelho), inclusive o commit de segurança
	patch, err = e.svc.TaskGitDiff(bg, e.co.ID, tk.ID)
	if err != nil || !strings.Contains(patch.Text, "+package loja") || !strings.Contains(patch.Text, "+busca") {
		t.Fatalf("diff pós-limpeza: %+v %v", patch, err)
	}
	log, err = e.svc.TaskGitLog(bg, e.co.ID, tk.ID, 10)
	if err != nil || len(log) != 2 || !strings.Contains(log[0].Subject, "alterações não commitadas") {
		t.Fatalf("log pós-limpeza: %+v %v", log, err)
	}
	if f, err := e.svc.TaskGitFile(bg, e.co.ID, tk.ID, "busca.go"); err != nil || f.Content != "package loja\n" {
		t.Fatalf("arquivo pós-limpeza: %+v %v", f, err)
	}
	vs := e.verbs(t, tk.ID)
	for _, want := range []string{"task.worktree_provisioning", "task.worktree_ready", "task.worktree_removed"} {
		if !hasVerb(vs, want) {
			t.Errorf("faltou a atividade %s em %v", want, vs)
		}
	}
	// idempotente
	if res, err := e.svc.ReapWorktrees(bg, 50); err != nil || res.Removed != 0 {
		t.Fatalf("segundo reap: %+v %v", res, err)
	}
}

// A pasta nunca some debaixo de uma run em andamento, mesmo com a task já concluída.
func TestWorktreeKeptWhileARunIsActive(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	tk := e.projectTask(t)
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	run, err := e.svc.CheckoutRun(bg, service.Wake{CompanyID: e.co.ID, PersonaID: *tk.PersonaID, TaskID: tk.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	done := domain.TaskDone
	if _, err := e.svc.UpdateTask(bg, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Status: &done}); err != nil {
		t.Fatal(err)
	}
	// fechar a task pode cancelar a run na fila; o caso real é a run já rodando, então força o estado
	if _, err := e.svc.Pool.Exec(bg, `UPDATE runs SET status = 'running', finished_at = NULL WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.ReapWorktrees(bg, 50)
	if err != nil || res.Removed != 0 {
		t.Fatalf("com run ativa nada sai: %+v %v", res, err)
	}
	if _, err := os.Stat(prep.Dir); err != nil {
		t.Fatalf("a pasta deveria continuar: %v", err)
	}
	if _, err := e.svc.FinishRun(bg, e.co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{Summary: "ok"}}); err != nil {
		t.Fatal(err)
	}
	if res, err = e.svc.ReapWorktrees(bg, 50); err != nil || res.Removed != 1 {
		t.Fatalf("depois da run: %+v %v", res, err)
	}
}

func TestWorktreeRemovedWhenBranchMerged(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	tk := e.projectTask(t)
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	// sem commits próprios: não há o que integrar
	if res, _ := e.svc.ReapWorktrees(bg, 50); res.Removed != 0 {
		t.Fatalf("worktree vazio não deve ser limpo: %+v", res)
	}
	writeFile(t, filepath.Join(prep.Dir, "x.txt"), "x\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: x")
	// ainda não integrada
	if res, _ := e.svc.ReapWorktrees(bg, 50); res.Removed != 0 {
		t.Fatalf("branch não integrada: %+v", res)
	}
	// o remoto integra a branch (fast-forward) e o espelho é atualizado
	gitCmd(t, e.origin, "fetch", "-q", filepath.Join(e.root, e.co.Slug, ".hanko", "mirrors", "app.git"), prep.Branch+":"+prep.Branch)
	gitCmd(t, e.origin, "merge", "-q", "--ff-only", prep.Branch)
	if _, err := e.svc.SyncWorkspaceMirror(bg, domain.HumanActor, e.co.ID, e.ws.ID); err != nil {
		t.Fatal(err)
	}
	view, _ := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if !view.Merged || view.Behind != 0 {
		t.Fatalf("deveria constar como integrada: %+v", view)
	}
	res, err := e.svc.ReapWorktrees(bg, 50)
	if err != nil || res.Removed != 1 {
		t.Fatalf("reap: %+v %v", res, err)
	}
	if _, err := os.Stat(prep.Dir); !os.IsNotExist(err) {
		t.Fatal("a pasta deveria ter sumido")
	}
	after, _ := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if after.Worktree.RemovedWhy != domain.RemovedMerged {
		t.Fatalf("motivo = %q", after.Worktree.RemovedWhy)
	}
	mirror := filepath.Join(e.root, e.co.Slug, ".hanko", "mirrors", "app.git")
	if out := gitCmd(t, mirror, "branch", "--list", prep.Branch); out != "" {
		t.Fatalf("a branch integrada deveria ter sido apagada do espelho: %q", out)
	}
	// a task continua aberta e é retomada: novo worktree, branch nova a partir da base atual
	again, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil || again == nil {
		t.Fatalf("reprovisionar: %v %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(again.Dir, "x.txt")); err != nil {
		t.Fatalf("o novo worktree parte da base com o merge: %v", err)
	}
}

func TestWorktreeDivergedAndRecovered(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	tk := e.projectTask(t)
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(prep.Dir, "a.txt"), "a\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: a")

	// alguém troca de branch no worktree: divergente, mas a run segue com aviso
	gitCmd(t, prep.Dir, "checkout", "-q", "-b", "outra")
	got, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil || got == nil || !strings.Contains(got.Warning, "divergente") || got.Dir != prep.Dir {
		t.Fatalf("divergente: %+v %v", got, err)
	}
	view, _ := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if view.Worktree.State != domain.WorktreeDiverged {
		t.Fatalf("estado = %s", view.Worktree.State)
	}
	// volta para a branch: normaliza
	gitCmd(t, prep.Dir, "checkout", "-q", prep.Branch)
	if got, err = e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true); err != nil || got.Warning != "" {
		t.Fatalf("normalizado: %+v %v", got, err)
	}
	if view, _ = e.svc.TaskGit(bg, e.co.ID, tk.ID); view.Worktree.State != domain.WorktreeReady {
		t.Fatalf("estado = %s", view.Worktree.State)
	}

	// a pasta é apagada na mão: o worktree é recriado reaproveitando a branch (e o commit)
	if err := os.RemoveAll(prep.Dir); err != nil {
		t.Fatal(err)
	}
	got, err = e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil || got == nil {
		t.Fatalf("recriar: %v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "a.txt")); err != nil {
		t.Fatalf("o commit anterior deveria estar no worktree recriado: %v", err)
	}
	vs := e.verbs(t, tk.ID)
	if !hasVerb(vs, "task.worktree_diverged") {
		t.Errorf("faltou task.worktree_diverged em %v", vs)
	}
	// revisor: só lê o que existe, não cria
	other := e.task(t, service.CreateTaskInput{Title: "outra"})
	if p, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, other.ID, e.ws.ID, false); err != nil || p != nil {
		t.Fatalf("create=false sem worktree: %v %v", p, err)
	}
}

func TestWorktreeProvisionFailureAndRetry(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	// remoto que não existe: o espelho nunca fica pronto e a run falha cedo, com o motivo
	if _, err := e.svc.Pool.Exec(bg, `UPDATE workspaces SET repo_url = $1 WHERE id = $2`, "file:///nao/existe/mesmo", e.ws.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EnableWorkspaceGit(bg, domain.HumanActor, e.co.ID, e.ws.ID, service.EnableWorkspaceGitInput{}); err != nil {
		t.Fatal(err)
	}
	wg, err := e.svc.SyncWorkspaceMirror(bg, domain.HumanActor, e.co.ID, e.ws.ID)
	if err != nil {
		t.Fatalf("falha de rede não é erro da chamada: %v", err)
	}
	if wg.MirrorState != domain.MirrorFailed || wg.LastError == "" {
		t.Fatalf("espelho: %+v", wg)
	}
	tk := e.projectTask(t)
	if _, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true); err == nil || !strings.Contains(err.Error(), "espelho") {
		t.Fatalf("esperava erro de espelho: %v", err)
	}
	// corrige o endereço: a próxima tentativa funciona
	if _, err := e.svc.Pool.Exec(bg, `UPDATE workspaces SET repo_url = $1 WHERE id = $2`, "file://"+e.origin, e.ws.ID); err != nil {
		t.Fatal(err)
	}
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil || prep == nil {
		t.Fatalf("retry: %v %v", prep, err)
	}
	// worktree que falha no git (caminho já ocupado por um arquivo) fica `failed` e a próxima tentativa o refaz
	if err := os.RemoveAll(prep.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(bg, `UPDATE task_worktrees SET state = 'removed' WHERE task_id = $1`, tk.ID); err != nil {
		t.Fatal(err)
	}
	writeFile(t, prep.Dir, "arquivo no lugar da pasta")
	if _, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true); err == nil {
		t.Fatal("caminho ocupado deveria falhar")
	}
	v, _ := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if v.Worktree.State != domain.WorktreeFailed || v.Worktree.Error == "" {
		t.Fatalf("estado = %+v", v.Worktree)
	}
	if prep, err = e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true); err != nil || prep == nil {
		t.Fatalf("depois de falhar: %v %v", prep, err)
	}
	if !hasVerb(e.verbs(t, tk.ID), "task.worktree_failed") {
		t.Error("faltou task.worktree_failed")
	}
}

func TestWorkspaceGitGuards(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	// recurso desligado
	off := e.svc.GitWorkspaces
	e.svc.GitWorkspaces = service.GitWorkspacesConfig{}
	if _, err := e.svc.EnableWorkspaceGit(bg, domain.HumanActor, e.co.ID, e.ws.ID, service.EnableWorkspaceGitInput{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("desligado: %v", err)
	}
	if p, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, domain.NewID(), e.ws.ID, true); p != nil || err != nil {
		t.Fatalf("desligado não prepara nada: %v %v", p, err)
	}
	e.svc.GitWorkspaces = off
	// sem repositório
	bare, err := e.svc.CreateWorkspace(bg, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Sem repo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EnableWorkspaceGit(bg, domain.HumanActor, e.co.ID, bare.ID, service.EnableWorkspaceGitInput{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem repo_url: %v", err)
	}
	// credencial: só uma referência, que existe
	for _, bad := range []string{"token-solto", "${secret:nao-existe}", "x ${secret:a} y", "${secret:user/pessoal}"} {
		if _, err := e.svc.EnableWorkspaceGit(bg, domain.HumanActor, e.co.ID, e.ws.ID, service.EnableWorkspaceGitInput{CredentialRef: bad}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("credencial %q deveria ser recusada: %v", bad, err)
		}
	}
	// papéis
	e.svc.ExternalIdentity = true
	viewer := mkUser(t, e.env, "viewer@x.com", domain.RoleMember)
	join(t, e.env, viewer, domain.MemberViewer)
	member := mkUser(t, e.env, "member@x.com", domain.RoleMember)
	join(t, e.env, member, domain.MemberMember)
	if _, err := e.svc.EnableWorkspaceGit(as(member), member.Actor(), e.co.ID, e.ws.ID, service.EnableWorkspaceGitInput{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro não liga o Git: %v", err)
	}
	e.enableAndSync(t)
	if _, err := e.svc.SyncWorkspaceMirror(as(viewer), viewer.Actor(), e.co.ID, e.ws.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("viewer não sincroniza: %v", err)
	}
	if _, err := e.svc.SyncWorkspaceMirror(as(member), member.Actor(), e.co.ID, e.ws.ID); err != nil {
		t.Fatalf("membro sincroniza: %v", err)
	}
	if _, err := e.svc.GetWorkspaceGit(as(viewer), e.co.ID, "app"); err != nil {
		t.Fatalf("viewer lê: %v", err)
	}
	// desligar com worktree em uso é conflito
	tk := e.projectTask(t)
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DisableWorkspaceGit(bg, domain.HumanActor, e.co.ID, e.ws.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("desligar em uso: %v", err)
	}
	// excluir o workspace (com desvincular) limpa o disco
	if err := e.svc.DeleteWorkspace(bg, domain.HumanActor, e.co.ID, e.ws.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prep.Dir); !os.IsNotExist(err) {
		t.Fatal("o worktree deveria sair do disco com o workspace")
	}
	if _, err := os.Stat(filepath.Join(e.root, e.co.Slug, ".hanko", "mirrors", "app.git")); !os.IsNotExist(err) {
		t.Fatal("o espelho deveria sair do disco com o workspace")
	}
}

func TestWorkspacePathRejectsReservedFolder(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	_, err := e.svc.CreateWorkspace(bg, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Mirrors", Path: filepath.Join(e.root, e.co.Slug, ".hanko", "mirrors")})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("pasta reservada: %v", err)
	}
	if _, err := e.svc.CreateWorkspace(bg, domain.HumanActor, e.co.ID, service.CreateWorkspaceInput{Name: "Ok", Path: filepath.Join(e.root, e.co.Slug, "ok")}); err != nil {
		t.Fatalf("pasta normal: %v", err)
	}
}

func TestMirrorCredentialNeverLeaks(t *testing.T) {
	e := newGitEnv(t)
	sec := newSecEnv(t)
	sec.svc.WorkspacesRoot, sec.svc.GitWorkspaces = e.svc.WorkspacesRoot, e.svc.GitWorkspaces
	bg := context.Background()
	const token = "ghp_SENTINELA_nunca_em_log_123456"
	sec.create(t, "repo-token", token)
	ws, err := sec.svc.CreateWorkspace(bg, domain.HumanActor, sec.co.ID, service.CreateWorkspaceInput{Name: "Privado", Slug: "privado"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sec.svc.Pool.Exec(bg, `UPDATE workspaces SET repo_url = $1 WHERE id = $2`, "file://"+e.origin, ws.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sec.svc.EnableWorkspaceGit(bg, domain.HumanActor, sec.co.ID, ws.ID, service.EnableWorkspaceGitInput{CredentialRef: "${secret:repo-token}"}); err != nil {
		t.Fatal(err)
	}
	// um token num endereço file:// não faz sentido: a busca falha, e o valor não aparece em lugar nenhum
	wg, err := sec.svc.SyncWorkspaceMirror(bg, domain.HumanActor, sec.co.ID, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wg.MirrorState != domain.MirrorFailed {
		t.Fatalf("estado = %s", wg.MirrorState)
	}
	if wg.CredentialRef != "${secret:repo-token}" {
		t.Fatalf("a referência (não o valor) fica guardada: %q", wg.CredentialRef)
	}
	rows, err := sec.svc.Pool.Query(bg, `SELECT data::text FROM activity_events WHERE company_id = $1`, sec.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(d, token) {
			t.Fatalf("o token vazou na atividade: %s", d)
		}
	}
	if strings.Contains(wg.LastError, token) || strings.Contains(sec.logs.String(), token) {
		t.Fatal("o token vazou no erro ou no log")
	}
	// segredo apagado depois: a busca falha citando o nome, nunca o valor
	view, err := sec.svc.GetWorkspaceGit(bg, sec.co.ID, "privado")
	if err != nil || view.CredentialRef != "${secret:repo-token}" {
		t.Fatalf("view: %+v %v", view, err)
	}
}
