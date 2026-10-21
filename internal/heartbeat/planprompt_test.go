package heartbeat

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestBuildPromptPlanReview(t *testing.T) {
	rc := sampleContext()

	// sem política de plano: nada de bloco de plano
	_, prompt := BuildPrompt(rc, "", "")
	if strings.Contains(prompt, "plan-review") || strings.Contains(prompt, "Plano aprovado") {
		t.Fatalf("sem política não há bloco de plano:\n%s", prompt)
	}

	// fase de planejamento: só planeja, publica o plano e pede o gate
	rc.Plan = &service.PlanContext{Phase: domain.PlanPhasePlan, Policy: domain.PlanPolicy{Required: true, Source: domain.PlanScopePersona}}
	_, prompt = BuildPrompt(rc, "", "")
	for _, w := range []string{"## Fase de planejamento (plan-review)", "apenas planeja", "hanko_artifact_put", "kind=plan", "hanko_gate_resubmit"} {
		if !strings.Contains(prompt, w) {
			t.Errorf("fase plan sem %q:\n%s", w, prompt)
		}
	}

	// fase de execução: o plano aprovado vai no briefing
	rc.Plan = &service.PlanContext{Phase: domain.PlanPhaseExecute, ApprovedPlan: "1. migrar\n2. publicar", Revision: 3, Policy: domain.PlanPolicy{Required: true}}
	_, prompt = BuildPrompt(rc, "", "")
	for _, w := range []string{"## Plano aprovado (revisão 3)", "1. migrar\n2. publicar"} {
		if !strings.Contains(prompt, w) {
			t.Errorf("fase execute sem %q:\n%s", w, prompt)
		}
	}
	if strings.Contains(prompt, "Fase de planejamento") {
		t.Error("na execução não há bloco de planejamento")
	}

	// o protocolo ensina a pergunta estruturada, o reenvio e a contratação
	system, _ := BuildPrompt(sampleContext(), "", "")
	for _, w := range []string{"kind=question", "hanko_gate_resubmit", "hanko_hire_request"} {
		if !strings.Contains(system, w) {
			t.Errorf("protocolo sem %q", w)
		}
	}
}
