package heartbeat

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestContinuationSection(t *testing.T) {
	if got := continuationSection(nil, false); got != "" {
		t.Fatalf("not a continuation: %q", got)
	}
	base := &service.ContinuationContext{OfRunName: "Hábil_Corrente", Reason: domain.ContinueRecovery, Attempt: 1, MaxAttempts: 2,
		PrevStatus: domain.RunInterrupted, Tail: []string{"agente: li o README", "tool: Bash: go test"}}

	got := continuationSection(base, true)
	for _, want := range []string{"Continuação de uma execução anterior", "`Hábil_Corrente`", "continuação 1 de 2", "A sessão do harness foi retomada",
		"não refaça trabalho concluído", "- agente: li o README", "- tool: Bash: go test"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Desfecho incerto") {
		t.Errorf("no uncertain tool, no warning:\n%s", got)
	}

	if got := continuationSection(base, false); !strings.Contains(got, "não pôde ser retomada") {
		t.Errorf("without a session the briefing must say so:\n%s", got)
	}

	uncertain := *base
	uncertain.Reason = domain.ContinueManual
	uncertain.DecisionNote = "conferi o remoto"
	uncertain.Diagnosis = &domain.RunDiagnosis{Uncertain: &domain.UncertainTool{Name: "Bash", Input: "git push origin main"}}
	got = continuationSection(&uncertain, true)
	for _, want := range []string{"O operador decidiu continuar", "Nota do operador: conferi o remoto", "Desfecho incerto", "`Bash`", "git push origin main", "Verifique o estado real antes de repetir"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	retry := &service.ContinuationContext{OfRunName: "X_Y", Reason: domain.ContinueRetry, PrevStatus: domain.RunFailed, PrevError: "boom"}
	got = continuationSection(retry, false)
	if !strings.Contains(got, "refaz o trabalho") || !strings.Contains(got, "(boom)") || strings.Contains(got, "não pôde ser retomada") {
		t.Errorf("retry:\n%s", got)
	}

	steer := &service.ContinuationContext{OfRunName: "X_Y", Reason: domain.ContinueSteer, PrevStatus: domain.RunCancelled}
	if got := continuationSection(steer, true); !strings.Contains(got, "mudar o rumo") {
		t.Errorf("steer:\n%s", got)
	}
}

func TestBuildPromptIncludesContinuation(t *testing.T) {
	rc := service.RunContext{
		Run:           domain.Run{ID: domain.NewID(), Trigger: domain.TriggerHeartbeat},
		Company:       domain.Company{Name: "Acme"},
		Persona:       domain.Persona{Name: "Ana"},
		Continuation:  &service.ContinuationContext{OfRunName: "Hábil_Corrente", Reason: domain.ContinueRecovery, Attempt: 1, MaxAttempts: 2, PrevStatus: domain.RunInterrupted},
		ResumeSession: "sess-1",
	}
	_, prompt := BuildPrompt(rc, "", "")
	if !strings.Contains(prompt, "Continuação de uma execução anterior") || !strings.Contains(prompt, "retomada") {
		t.Fatalf("prompt lacks the briefing:\n%s", prompt)
	}
	rc.Continuation = nil
	if _, prompt := BuildPrompt(rc, "", ""); strings.Contains(prompt, "Continuação de uma execução") {
		t.Fatalf("plain run must not carry the section:\n%s", prompt)
	}
}
