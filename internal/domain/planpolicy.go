package domain

import "fmt"

// Plan-review (ADR-0041): a política "esta task precisa de plano aprovado antes de executar" vale por task, projeto ou
// persona, com a mesma precedência da política de revisão (task > projeto > persona). nil = herda da camada seguinte.

// PlanScope é a camada de uma política de plano.
type PlanScope string

const (
	PlanScopeTask    PlanScope = "task"
	PlanScopeProject PlanScope = "project"
	PlanScopePersona PlanScope = "persona"
)

// Valid diz se o escopo é conhecido.
func (s PlanScope) Valid() bool {
	return s == PlanScopeTask || s == PlanScopeProject || s == PlanScopePersona
}

// Check valida o escopo com uma mensagem de campo.
func (s PlanScope) Check() error {
	if !s.Valid() {
		return Invalid("scope", fmt.Sprintf("escopo desconhecido %q (task, project ou persona)", s))
	}
	return nil
}

// PlanPhase é a fase de uma task sob plan-review.
type PlanPhase string

const (
	PlanPhaseNone    PlanPhase = ""        // a política não vale: execução normal
	PlanPhasePlan    PlanPhase = "plan"    // sem plano aprovado: o run só planeja (sem tools de escrita)
	PlanPhaseExecute PlanPhase = "execute" // plano aprovado: o run executa com o plano no briefing
)

// PlanPolicy é a política efetiva e a camada que decidiu.
type PlanPolicy struct {
	Required bool      `json:"required"`
	Source   PlanScope `json:"source,omitempty"` // vazio = nenhuma camada definiu (não exige)
}

// ResolvePlanPolicy aplica task > projeto > persona.
func ResolvePlanPolicy(task, project, persona *bool) PlanPolicy {
	switch {
	case task != nil:
		return PlanPolicy{Required: *task, Source: PlanScopeTask}
	case project != nil:
		return PlanPolicy{Required: *project, Source: PlanScopeProject}
	case persona != nil:
		return PlanPolicy{Required: *persona, Source: PlanScopePersona}
	}
	return PlanPolicy{}
}

// PhaseFor diz a fase de uma task dada a política e se já existe um plano aprovado.
func (p PlanPolicy) PhaseFor(approved bool) PlanPhase {
	switch {
	case !p.Required:
		return PlanPhaseNone
	case approved:
		return PlanPhaseExecute
	}
	return PlanPhasePlan
}
