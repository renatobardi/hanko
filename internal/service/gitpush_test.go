package service_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Push da branch do worktree (ADR-0091): envio a um remoto local, commit de segurança do que está solto, "em dia" na
// segunda chamada, registro em task_worktrees e evento na atividade.
func TestPushTaskWorktree(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	tk := e.projectTask(t)
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(prep.Dir, "busca.go"), "package loja\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: busca")
	writeFile(t, filepath.Join(prep.Dir, "solto.txt"), "ainda não commitado\n")

	res, err := e.svc.PushTaskWorktree(bg, domain.HumanActor, e.co.ID, tk.ID)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.Branch != prep.Branch || len(res.SHA) != 40 || res.UpToDate || !res.Committed || res.Commits != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	// o remoto tem a branch no mesmo commit do worktree
	if got := gitCmd(t, e.origin, "rev-parse", prep.Branch); got != res.SHA {
		t.Fatalf("remoto em %s, enviado %s", got, res.SHA)
	}
	if got := gitCmd(t, prep.Dir, "rev-parse", "HEAD"); got != res.SHA {
		t.Fatalf("worktree em %s, enviado %s", got, res.SHA)
	}
	if out := gitCmd(t, e.origin, "log", "--format=%s", prep.Branch, "-2"); !strings.Contains(out, "alterações soltas") || !strings.Contains(out, "feat: busca") {
		t.Fatalf("log do remoto: %q", out)
	}
	view, err := e.svc.TaskGit(bg, e.co.ID, tk.ID)
	if err != nil || view.Worktree.PushedSHA != res.SHA || view.Worktree.PushedAt == nil {
		t.Fatalf("registro do envio: %+v %v", view.Worktree, err)
	}
	if !hasVerb(e.verbs(t, tk.ID), "task.worktree_pushed") {
		t.Fatalf("evento: %v", e.verbs(t, tk.ID))
	}

	// nada novo: em dia, sem commit de segurança
	again, err := e.svc.PushTaskWorktree(bg, domain.HumanActor, e.co.ID, tk.ID)
	if err != nil || !again.UpToDate || again.Committed || again.SHA != res.SHA {
		t.Fatalf("segundo envio: %+v %v", again, err)
	}

	// sem worktree é 404
	other := e.task(t, service.CreateTaskInput{Title: "sem worktree"})
	if _, err := e.svc.PushTaskWorktree(bg, domain.HumanActor, e.co.ID, other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("sem worktree: %v", err)
	}
}

// O Hanko nunca força: quando o remoto andou na mesma branch, o envio é recusado com o motivo.
func TestPushTaskWorktreeRefusesDivergedRemote(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	tk := e.projectTask(t)
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tk.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(prep.Dir, "busca.go"), "package loja\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: busca")
	if _, err := e.svc.PushTaskWorktree(bg, domain.HumanActor, e.co.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, e.origin, "checkout", "-q", prep.Branch)
	writeFile(t, filepath.Join(e.origin, "outro.txt"), "do outro lado\n")
	gitCmd(t, e.origin, "add", "-A")
	gitCmd(t, e.origin, "commit", "-q", "-m", "remoto andou")
	gitCmd(t, e.origin, "checkout", "-q", "main")
	writeFile(t, filepath.Join(prep.Dir, "mais.go"), "package loja\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: mais")
	_, err = e.svc.PushTaskWorktree(bg, domain.HumanActor, e.co.ID, tk.ID)
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "recusou") {
		t.Fatalf("push sem força deveria ser recusado: %v", err)
	}
}

// gitCmdErr roda um git que pode falhar (ex.: rev-parse de uma ref ausente) e devolve a saída e o erro.
func gitCmdErr(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// hanko_git_push segue a política de tools como tool do Hanko: sem política pergunta ao gate e não envia; com allow
// envia como a persona; com off recusa.
func TestGitPushForRunFollowsToolPolicy(t *testing.T) {
	e := newGitEnv(t)
	bg := context.Background()
	e.enableAndSync(t)
	pr, err := e.svc.CreateProject(bg, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "Loja", WorkspaceID: &e.ws.ID})
	if err != nil {
		t.Fatal(err)
	}
	ana := e.persona(t, "Ana", nil)
	tv := e.task(t, service.CreateTaskInput{Title: "Adicionar busca", PersonaID: &ana.ID, ProjectID: &pr.ID})
	run, err := e.svc.CheckoutRun(bg, service.Wake{CompanyID: e.co.ID, PersonaID: ana.ID, TaskID: tv.ID, Trigger: domain.TriggerAssign})
	if err != nil {
		t.Fatal(err)
	}
	prep, err := e.svc.PrepareTaskWorktree(bg, e.co.ID, tv.ID, e.ws.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(prep.Dir, "busca.go"), "package loja\n")
	gitCmd(t, prep.Dir, "add", "-A")
	gitCmd(t, prep.Dir, "commit", "-q", "-m", "feat: busca")

	// sem política: ask, nada enviado
	res, err := e.svc.GitPushForRun(bg, e.co.ID, service.GitPushInput{RunID: run.ID})
	if err != nil || res.Decision != service.NativeAsk {
		t.Fatalf("sem política: %+v %v", res, err)
	}
	if out, _ := gitCmdErr(e.origin, "rev-parse", "--verify", prep.Branch); out != "" {
		t.Fatalf("a branch não deveria estar no remoto antes da decisão: %q", out)
	}

	// política allow para a persona: envia, com a persona como autora do evento
	if _, err := e.svc.SetToolPolicy(bg, domain.HumanActor, e.co.ID, service.ToolPolicyInput{Scope: domain.PolicyScopePersona, PersonaID: &ana.ID,
		Target: domain.PolicyTargetNative, ToolName: service.HankoGitPushTool, Mode: domain.PolicyAllow}); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.GitPushForRun(bg, e.co.ID, service.GitPushInput{RunID: run.ID})
	if err != nil || res.Decision != service.NativeAllow || len(res.Push.SHA) != 40 || res.Push.Commits != 1 {
		t.Fatalf("allow: %+v %v", res, err)
	}
	if got := gitCmd(t, e.origin, "rev-parse", prep.Branch); got != res.Push.SHA {
		t.Fatalf("remoto em %s, enviado %s", got, res.Push.SHA)
	}
	if !hasVerb(e.verbs(t, tv.ID), "task.worktree_pushed") {
		t.Fatalf("evento: %v", e.verbs(t, tv.ID))
	}

	// política off: recusa
	if _, err := e.svc.SetToolPolicy(bg, domain.HumanActor, e.co.ID, service.ToolPolicyInput{Scope: domain.PolicyScopePersona, PersonaID: &ana.ID,
		Target: domain.PolicyTargetNative, ToolName: service.HankoGitPushTool, Mode: domain.PolicyOff}); err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.GitPushForRun(bg, e.co.ID, service.GitPushInput{RunID: run.ID})
	if err != nil || res.Decision != service.NativeDeny {
		t.Fatalf("off: %+v %v", res, err)
	}
}
