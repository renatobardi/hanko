package heartbeat

import (
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestBuildPromptProjectInstructionsWorktreeAndSessionReset(t *testing.T) {
	rc := sampleContext()
	rc.Task.ProjectName = "Loja"
	rc.ProjectInstructions = "  Use o guia de estilo.  "
	rc.Worktree = &service.WorktreePrep{Dir: "/x", Branch: "hnk-7"}
	rc.SessionReset = &domain.TaskSessionReset{By: domain.HumanActor, Reason: "contexto confuso", CreatedAt: time.Now()}

	system, prompt := BuildPrompt(rc, "", "")
	// o briefing do projeto vem depois das instruções da persona e antes do protocolo
	iPersona := strings.Index(system, "Escreva código limpo.")
	iProject := strings.Index(system, "## Instruções do projeto Loja")
	iProtocol := strings.Index(system, "## Protocolo Hanko")
	if iPersona < 0 || iPersona >= iProject || iProject >= iProtocol {
		t.Fatalf("ordem do system prompt (persona %d, projeto %d, protocolo %d):\n%s", iPersona, iProject, iProtocol, system)
	}
	if !strings.Contains(system, "Use o guia de estilo.") || !strings.Contains(system, "branch `hnk-7`") || !strings.Contains(system, "não faça push") {
		t.Fatalf("system:\n%s", system)
	}
	if !strings.Contains(prompt, "Sessão nova:") || !strings.Contains(prompt, "Motivo: contexto confuso.") {
		t.Fatalf("prompt:\n%s", prompt)
	}

	// sem nada disso, o prompt não muda
	plain := sampleContext()
	system, prompt = BuildPrompt(plain, "", "")
	for _, absent := range []string{"Instruções do projeto", "worktree Git", "Sessão nova"} {
		if strings.Contains(system, absent) || strings.Contains(prompt, absent) {
			t.Errorf("não deveria conter %q", absent)
		}
	}
	// pedido do sistema
	rc.SessionReset = &domain.TaskSessionReset{By: domain.SystemActor}
	if _, prompt = BuildPrompt(rc, "", ""); !strings.Contains(prompt, "o sistema pediu") {
		t.Fatalf("prompt:\n%s", prompt)
	}
}

func TestBuildEnvWorktreeIdentity(t *testing.T) {
	rc := sampleContext()
	if env := buildEnv("", "http://h", "tok", rc); env["GIT_AUTHOR_NAME"] != "" || env["HANKO_WORKTREE_BRANCH"] != "" {
		t.Fatalf("sem worktree: %v", env)
	}
	rc.Worktree = &service.WorktreePrep{Branch: "hnk-7"}
	env := buildEnv("", "http://h", "tok", rc)
	if env["GIT_AUTHOR_NAME"] != rc.Persona.Name || env["GIT_COMMITTER_NAME"] != rc.Persona.Name ||
		!strings.HasSuffix(env["GIT_AUTHOR_EMAIL"], "@hanko.local") || env["HANKO_WORKTREE_BRANCH"] != "hnk-7" {
		t.Fatalf("env: %v", env)
	}
}
