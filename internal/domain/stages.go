package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Pipelines sobre tasks (ADR-0048): cada projeto pode ter uma lista ordenada de etapas. A etapa mapeia um status de task
// e, opcionalmente, uma persona; terminar a etapa avança a task para a seguinte, parando só no gate que a etapa pedir.

// StageExitGate é o que acontece quando o trabalho de uma etapa termina.
type StageExitGate string

const (
	// StageGateNone avança sozinho (a revisão automática da política da persona/projeto/task ainda vale).
	StageGateNone StageExitGate = "none"
	// StageGateApproval abre um gate de aprovação humana antes de avançar.
	StageGateApproval StageExitGate = "approval"
	// StageGateReview reaproveita o gate task_review (com a revisão automática da política vigente).
	StageGateReview StageExitGate = "review"
)

// Valid diz se g é um valor conhecido.
func (g StageExitGate) Valid() bool {
	return g == StageGateNone || g == StageGateApproval || g == StageGateReview
}

// TaskStageState é a situação de uma task dentro do pipeline.
type TaskStageState string

const (
	TaskStageWorking    TaskStageState = "working"    // a persona da etapa trabalha
	TaskStageCompleting TaskStageState = "completing" // a etapa terminou; um gate decide o avanço
	TaskStageFinished   TaskStageState = "finished"   // atravessou a última etapa ou saiu do pipeline
)

// StageEventKind é o tipo de um evento do histórico do pipeline.
type StageEventKind string

const (
	StageEvEnrolled      StageEventKind = "enrolled"
	StageEvCompleted     StageEventKind = "completed"
	StageEvGateRequested StageEventKind = "gate_requested"
	StageEvGateDecided   StageEventKind = "gate_decided"
	StageEvAdvanced      StageEventKind = "advanced"
	StageEvReturned      StageEventKind = "returned"
	StageEvFinished      StageEventKind = "finished"
	StageEvLeft          StageEventKind = "left"
)

// Limites do pipeline de um projeto.
const (
	MaxProjectStages       = 12
	MaxStageNameRunes      = 80
	MaxStageInstructions   = 8192
	MaxStageLearnings      = 8       // decisões de gate que voltam no briefing da etapa
	MaxStageLearningsBytes = 4 << 10 // teto do bloco "Aprendizados desta etapa"
	MaxStageNoteRunes      = 4000
	stageKeyPattern        = `^[a-z][a-z0-9_-]{0,39}$`
)

var stageKeyRE = regexp.MustCompile(stageKeyPattern)

// StageStatusValid diz se s é um status que uma etapa pode mapear: o trabalho de uma etapa nunca está bloqueado,
// concluído ou cancelado (isso é do gate e do fim do pipeline). Com persona, só os trabalháveis (todo, in_progress).
func StageStatusValid(s TaskStatus, hasPersona bool) bool {
	switch s {
	case TaskTodo, TaskInProgress:
		return true
	case TaskInReview:
		return !hasPersona
	}
	return false
}

// ProjectStage é uma etapa do pipeline de um projeto.
type ProjectStage struct {
	ID           ID            `json:"id"`
	ProjectID    ID            `json:"project_id"`
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	Position     int           `json:"position"`
	Status       TaskStatus    `json:"status"`
	PersonaID    *ID           `json:"persona_id,omitempty"`
	PersonaSlug  string        `json:"persona_slug,omitempty"`
	PersonaName  string        `json:"persona_name,omitempty"`
	ExitGate     StageExitGate `json:"exit_gate"`
	Instructions string        `json:"instructions,omitempty"`
}

// StageSpec é uma etapa como o admin a descreve: a lista inteira substitui a anterior (as chaves casam as etapas).
// Persona é o slug ou o id da persona; vazio = etapa humana (sem persona).
type StageSpec struct {
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	Status       TaskStatus    `json:"status"`
	Persona      string        `json:"persona,omitempty"`
	ExitGate     StageExitGate `json:"exit_gate,omitempty"`
	Instructions string        `json:"instructions,omitempty"`
}

// Normalize apara e completa os padrões (exit_gate none).
func (s *StageSpec) Normalize() {
	s.Key = strings.ToLower(strings.TrimSpace(s.Key))
	s.Name = strings.TrimSpace(s.Name)
	s.Persona = strings.TrimSpace(s.Persona)
	s.Instructions = strings.TrimSpace(s.Instructions)
	if s.ExitGate == "" {
		s.ExitGate = StageGateNone
	}
}

// ValidateStageSpecs confere a lista inteira (já normalizada): de 1 a 12 etapas, chaves únicas, status e gates válidos.
func ValidateStageSpecs(specs []StageSpec) error {
	if len(specs) == 0 {
		return Invalid("stages", "informe ao menos uma etapa (para desligar o pipeline, remova todas pela rota de limpeza)")
	}
	if len(specs) > MaxProjectStages {
		return Invalid("stages", fmt.Sprintf("no máximo %d etapas por projeto", MaxProjectStages))
	}
	seen := map[string]bool{}
	for i, s := range specs {
		f := func(field string) string { return fmt.Sprintf("stages[%d].%s", i, field) }
		if !stageKeyRE.MatchString(s.Key) {
			return Invalid(f("key"), "use letras minúsculas, dígitos, _ ou -, começando por letra (até 40 caracteres)")
		}
		if seen[s.Key] {
			return Invalid(f("key"), fmt.Sprintf("chave %q repetida", s.Key))
		}
		seen[s.Key] = true
		if n := utf8.RuneCountInString(s.Name); n < 1 || n > MaxStageNameRunes {
			return Invalid(f("name"), fmt.Sprintf("de 1 a %d caracteres", MaxStageNameRunes))
		}
		if !StageStatusValid(s.Status, s.Persona != "") {
			if s.Status == TaskInReview {
				return Invalid(f("status"), "uma etapa com persona precisa de status trabalhável (todo ou in_progress); in_review é de etapa humana")
			}
			return Invalid(f("status"), "use todo, in_progress ou in_review")
		}
		if !s.ExitGate.Valid() {
			return Invalid(f("exit_gate"), "use none, approval ou review")
		}
		if utf8.RuneCountInString(s.Instructions) > MaxStageInstructions {
			return Invalid(f("instructions"), fmt.Sprintf("no máximo %d caracteres", MaxStageInstructions))
		}
	}
	return nil
}

// NextStage devolve a etapa seguinte à de chave key (a lista está em ordem). ok falso = key é a última (ou não existe).
func NextStage(stages []ProjectStage, key string) (ProjectStage, bool) {
	for i, s := range stages {
		if s.Key == key {
			if i+1 < len(stages) {
				return stages[i+1], true
			}
			return ProjectStage{}, false
		}
	}
	return ProjectStage{}, false
}

// FindStage acha a etapa de chave key.
func FindStage(stages []ProjectStage, key string) (ProjectStage, bool) {
	for _, s := range stages {
		if s.Key == key {
			return s, true
		}
	}
	return ProjectStage{}, false
}

// TaskStage é a posição de uma task no pipeline do projeto.
type TaskStage struct {
	TaskID    ID             `json:"task_id"`
	ProjectID ID             `json:"project_id"`
	Stage     ProjectStage   `json:"stage"`
	State     TaskStageState `json:"state"`
	Position  int            `json:"position"` // 1-based
	Total     int            `json:"total"`
	NextKey   string         `json:"next_stage,omitempty"`
	EnteredAt time.Time      `json:"entered_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// StageEvent é uma linha do histórico do pipeline (imutável).
type StageEvent struct {
	ID         ID             `json:"id"`
	TaskID     ID             `json:"task_id"`
	Kind       StageEventKind `json:"kind"`
	StageKey   string         `json:"stage_key,omitempty"`
	ToStageKey string         `json:"to_stage_key,omitempty"`
	Actor      Actor          `json:"actor"`
	GateID     *ID            `json:"gate_id,omitempty"`
	Decision   string         `json:"decision,omitempty"`
	Note       string         `json:"note,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// StageLearning é uma decisão de gate numa etapa: o que o humano pediu ou recusou e por quê.
type StageLearning struct {
	StageKey  string    `json:"stage_key"`
	TaskKey   string    `json:"task_key,omitempty"`
	Decision  string    `json:"decision"`
	Note      string    `json:"note,omitempty"`
	GateID    *ID       `json:"gate_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// LearningsBlock monta o bloco de markdown "Aprendizados desta etapa" (até MaxStageLearningsBytes), do mais novo ao
// mais antigo. Vazio quando não há nenhum.
func LearningsBlock(ls []StageLearning) string {
	if len(ls) == 0 {
		return ""
	}
	var b strings.Builder
	for i, l := range ls {
		if i >= MaxStageLearnings {
			break
		}
		line := "- " + l.CreatedAt.UTC().Format("2006-01-02") + " · "
		if l.TaskKey != "" {
			line += l.TaskKey + " · "
		}
		line += LearningDecisionLabel(l.Decision)
		if n := strings.Join(strings.Fields(l.Note), " "); n != "" {
			if utf8.RuneCountInString(n) > 500 {
				n = string([]rune(n)[:500]) + "…"
			}
			line += ": " + n
		}
		line += "\n"
		if b.Len()+len(line) > MaxStageLearningsBytes {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// LearningDecisionLabel é a frase curta da decisão de um gate.
func LearningDecisionLabel(decision string) string {
	switch decision {
	case string(GateApproved):
		return "aprovado"
	case string(GateChangesRequested):
		return "mudanças pedidas"
	case string(GateRejected):
		return "rejeitado"
	}
	return decision
}
