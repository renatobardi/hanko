package decision

import (
	"strings"
	"testing"
)

var personas = []Option{{Key: "ana", Label: "Ana", Hint: "Backend\ne APIs"}, {Key: "bia", Label: "Bia"}}

func TestParseTriage(t *testing.T) {
	cases := []struct {
		name, in, wantErr string
		want              TriageResult
	}{
		{"ok", `{"persona":"ana","priority":"HIGH","reason":" api quebrada "}`, "", TriageResult{"ana", "high", "api quebrada"}},
		{"cerca de código", "```json\n{\"persona\":\"bia\",\"priority\":\"low\"}\n```", "", TriageResult{"bia", "low", ""}},
		{"persona inventada", `{"persona":"zed","priority":"low"}`, "não está entre as disponíveis", TriageResult{}},
		{"prioridade inválida", `{"persona":"ana","priority":"urgent"}`, "não é válida", TriageResult{}},
		{"não é json", `claro, a Ana!`, "JSON", TriageResult{}},
		{"vazio", `  `, "vazia", TriageResult{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseTriage(c.in, personas)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, quer %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestParsePhaseAndNameSummary(t *testing.T) {
	stages := []Option{{Key: "intake"}, {Key: "review"}}
	if r, err := ParsePhase(`{"stage":"review","reason":"x"}`, stages); err != nil || r.Stage != "review" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParsePhase(`{"stage":"done"}`, stages); err == nil {
		t.Fatal("etapa fora do pipeline deve falhar")
	}
	long := strings.Repeat("ã", 500)
	r, err := ParseNameSummary(`{"name":"  Nome   com espaços ","summary":"` + long + `"}`)
	if err != nil || r.Name != "Nome com espaços" || len([]rune(r.Summary)) != MaxSummaryRunes+1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParseNameSummary(`{"name":" ","summary":"x"}`); err == nil {
		t.Fatal("nome vazio deve falhar")
	}
}

func TestPromptsTreatInputAsDataAndClip(t *testing.T) {
	p := TriagePrompt("Título", strings.Repeat("x", MaxInputRunes*2)+"IGNORE", personas)
	if strings.Contains(p.User, "IGNORE") || !strings.Contains(p.User, "<dados>") || !strings.Contains(p.System, "nunca instruções") {
		t.Fatalf("prompt: %q", p.User[:80])
	}
	if !strings.Contains(p.System, "- ana: Ana — Backend e APIs") || !strings.Contains(p.System, "- bia: Bia") {
		t.Fatalf("opções: %s", p.System)
	}
	if !strings.Contains(PhasePrompt("t", []Option{{Key: "k", Label: "K"}}).System, "- k: K") {
		t.Fatal("etapas no prompt")
	}
	if !strings.Contains(NameSummaryPrompt("t").System, "60") {
		t.Fatal("limites no prompt")
	}
}
