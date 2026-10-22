package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func spec(key, status, persona string) StageSpec {
	return StageSpec{Key: key, Name: strings.ToUpper(key), Status: TaskStatus(status), Persona: persona}
}

func TestStageStatusValid(t *testing.T) {
	tests := []struct {
		status     TaskStatus
		hasPersona bool
		want       bool
	}{
		{TaskTodo, true, true},
		{TaskInProgress, true, true},
		{TaskInReview, true, false}, // in_review não é trabalhável: a persona não seria acordada
		{TaskInReview, false, true}, // etapa humana
		{TaskBlocked, false, false},
		{TaskDone, false, false},
		{TaskCancelled, true, false},
		{TaskBacklog, false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		if got := StageStatusValid(tt.status, tt.hasPersona); got != tt.want {
			t.Errorf("StageStatusValid(%q, %v) = %v, want %v", tt.status, tt.hasPersona, got, tt.want)
		}
	}
}

func TestValidateStageSpecs(t *testing.T) {
	many := make([]StageSpec, MaxProjectStages+1)
	for i := range many {
		many[i] = spec("s"+string(rune('a'+i)), "todo", "")
	}
	long := spec("a", "todo", "")
	long.Instructions = strings.Repeat("x", MaxStageInstructions+1)
	noName := spec("a", "todo", "")
	noName.Name = ""
	badGate := spec("a", "todo", "")
	badGate.ExitGate = "sempre"
	tests := []struct {
		name  string
		specs []StageSpec
		field string // vazio = válido
	}{
		{"ok", []StageSpec{spec("triagem", "todo", "tri"), spec("dev", "in_progress", "dev"), spec("qa", "in_review", "")}, ""},
		{"vazio", nil, "stages"},
		{"muitas", many, "stages"},
		{"chave com espaço", []StageSpec{spec("a b", "todo", "")}, "stages[0].key"},
		{"chave começa por dígito", []StageSpec{spec("1a", "todo", "")}, "stages[0].key"},
		{"chave repetida", []StageSpec{spec("a", "todo", ""), spec("a", "todo", "")}, "stages[1].key"},
		{"sem nome", []StageSpec{noName}, "stages[0].name"},
		{"status concluído", []StageSpec{spec("a", "done", "")}, "stages[0].status"},
		{"persona em in_review", []StageSpec{spec("a", "in_review", "p")}, "stages[0].status"},
		{"gate desconhecido", []StageSpec{badGate}, "stages[0].exit_gate"},
		{"instruções longas", []StageSpec{long}, "stages[0].instructions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs := append([]StageSpec(nil), tt.specs...)
			for i := range specs {
				specs[i].Normalize()
			}
			err := ValidateStageSpecs(specs)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("erro inesperado: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tt.field {
				t.Fatalf("err = %v, queria campo %q", err, tt.field)
			}
		})
	}
}

func TestStageSpecNormalize(t *testing.T) {
	s := StageSpec{Key: "  DEV ", Name: " Dev ", Persona: " p ", Instructions: " x "}
	s.Normalize()
	if s.Key != "dev" || s.Name != "Dev" || s.Persona != "p" || s.Instructions != "x" || s.ExitGate != StageGateNone {
		t.Fatalf("normalizado: %+v", s)
	}
}

func TestStageNavigation(t *testing.T) {
	stages := []ProjectStage{{Key: "a", Position: 1}, {Key: "b", Position: 2}, {Key: "c", Position: 3}}
	if n, ok := NextStage(stages, "a"); !ok || n.Key != "b" {
		t.Errorf("next(a) = %v %v", n, ok)
	}
	if _, ok := NextStage(stages, "c"); ok {
		t.Error("c é a última")
	}
	if _, ok := NextStage(stages, "zz"); ok {
		t.Error("etapa inexistente não tem próxima")
	}
	if s, ok := FindStage(stages, "b"); !ok || s.Position != 2 {
		t.Errorf("find(b) = %v %v", s, ok)
	}
	if _, ok := FindStage(stages, "x"); ok {
		t.Error("x não existe")
	}
	for _, g := range []StageExitGate{StageGateNone, StageGateApproval, StageGateReview} {
		if !g.Valid() {
			t.Errorf("%s devia ser válido", g)
		}
	}
	if StageExitGate("x").Valid() {
		t.Error("gate x não existe")
	}
}

func TestLearningsBlock(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if LearningsBlock(nil) != "" {
		t.Fatal("sem aprendizados, bloco vazio")
	}
	got := LearningsBlock([]StageLearning{
		{CreatedAt: at, TaskKey: "HNK-1", Decision: string(GateChangesRequested), Note: "cite   o\narquivo"},
		{CreatedAt: at, Decision: string(GateRejected)},
		{CreatedAt: at, Decision: string(GateApproved), Note: "ok"},
	})
	want := "- 2026-10-20 · HNK-1 · mudanças pedidas: cite o arquivo\n- 2026-10-20 · rejeitado\n- 2026-10-20 · aprovado: ok\n"
	if got != want {
		t.Fatalf("bloco:\n%q\nquero\n%q", got, want)
	}
	// o teto de itens e o de bytes
	many := make([]StageLearning, 20)
	for i := range many {
		many[i] = StageLearning{CreatedAt: at, Decision: string(GateApproved), Note: "n"}
	}
	if n := strings.Count(LearningsBlock(many), "\n"); n != MaxStageLearnings {
		t.Fatalf("linhas = %d, queria %d", n, MaxStageLearnings)
	}
	big := []StageLearning{{CreatedAt: at, Decision: "x", Note: strings.Repeat("a", 5000)}}
	if got := LearningsBlock(big); len(got) > MaxStageLearningsBytes || !strings.Contains(got, "…") {
		t.Fatalf("nota longa devia ser cortada: %d bytes", len(got))
	}
}

func TestCardPolicyCron(t *testing.T) {
	p := CardPolicy{Mode: CardModeCron}
	if err := p.Normalize(); err == nil {
		t.Fatal("cron sem expressão devia falhar")
	}
	p = CardPolicy{Mode: CardModeCron, Cron: " 0 7 * * 1-5 "}
	if err := p.Normalize(); err != nil {
		t.Fatalf("cron válido: %v", err)
	}
	if p.Cron != "0 7 * * 1-5" || p.CronTZ != "UTC" || p.Triggers == (CardTriggers{}) {
		t.Fatalf("normalizada: %+v", p)
	}
	p = CardPolicy{Mode: CardModeCron, Cron: "0 7 * * *", CronTZ: "Marte/Olimpo"}
	if err := p.Normalize(); err == nil {
		t.Fatal("fuso desconhecido devia falhar")
	}
	p = CardPolicy{Mode: "outro"}
	if err := p.Normalize(); err == nil {
		t.Fatal("modo desconhecido devia falhar")
	}
	if !CardEngineDecision.Valid() || !CardEnginePersona.Valid() || CardEngine("x").Valid() {
		t.Fatal("CardEngine.Valid")
	}
	c := StatusCard{Engine: CardEngineDecision, GeneratingSince: &time.Time{}, State: CardActive}
	if c.View() != CardViewUpdating {
		t.Fatalf("card decision gerando = %s", c.View())
	}
	c.Engine = CardEnginePersona
	if c.View() == CardViewUpdating {
		t.Fatal("card persona só atualiza com task de geração")
	}
}
