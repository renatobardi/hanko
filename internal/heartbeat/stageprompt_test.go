package heartbeat

import (
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O briefing da persona de uma etapa do pipeline do projeto (ADR-0048).
func TestBuildPromptProjectStage(t *testing.T) {
	rc := sampleContext()
	_, prompt := BuildPrompt(rc, "", "")
	if strings.Contains(prompt, "Etapa do pipeline") {
		t.Fatalf("fora de pipeline não há bloco de etapa:\n%s", prompt)
	}

	rc.Stage = &service.StageContext{ProjectName: "Site", Key: "dev", Name: "Desenvolvimento", Position: 2, Total: 4, NextName: "Review",
		ExitGate: domain.StageGateApproval, Instructions: "Faça mudanças pequenas.",
		Learnings: "- 2026-10-20 · HNK-1 · mudanças pedidas: cite o arquivo alterado\n"}
	_, prompt = BuildPrompt(rc, "", "")
	for _, w := range []string{"## Etapa do pipeline: Desenvolvimento (2 de 4)", "projeto Site", "Depois dela vem **Review**",
		"Faça mudanças pequenas.", "`hanko_stage_complete`", "um humano aprova antes de a task avançar", "Não dê a task como concluída",
		"### Aprendizados desta etapa", "cite o arquivo alterado"} {
		if !strings.Contains(prompt, w) {
			t.Errorf("bloco da etapa sem %q:\n%s", w, prompt)
		}
	}

	// última etapa, sem gate e sem aprendizados
	rc.Stage = &service.StageContext{ProjectName: "Site", Key: "qa", Name: "QA", Position: 4, Total: 4, ExitGate: domain.StageGateNone}
	_, prompt = BuildPrompt(rc, "", "")
	for _, w := range []string{"É a última etapa", "avança sozinha para a próxima etapa"} {
		if !strings.Contains(prompt, w) {
			t.Errorf("última etapa sem %q:\n%s", w, prompt)
		}
	}
	if strings.Contains(prompt, "Aprendizados desta etapa") || strings.Contains(prompt, "O que se espera desta etapa") {
		t.Error("sem aprendizados nem instruções, as seções não aparecem")
	}

	rc.Stage = &service.StageContext{ProjectName: "Site", Key: "rev", Name: "Review", Position: 3, Total: 4, ExitGate: domain.StageGateReview}
	if _, prompt = BuildPrompt(rc, "", ""); !strings.Contains(prompt, "passa por revisão") {
		t.Errorf("gate de revisão sem o aviso:\n%s", prompt)
	}
}
