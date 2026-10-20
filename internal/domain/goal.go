package domain

import (
	"math"
	"time"
)

// Metas (ADR-0028): objetivos mensuráveis, opcionalmente ligados a um projeto.

// GoalStatus é o ciclo de vida de uma meta.
type GoalStatus string

const (
	GoalActive    GoalStatus = "active"
	GoalAchieved  GoalStatus = "achieved"
	GoalAbandoned GoalStatus = "abandoned"
)

// Valid diz se s é um status conhecido.
func (s GoalStatus) Valid() bool { return s == GoalActive || s == GoalAchieved || s == GoalAbandoned }

// Goal é uma meta: sai de StartValue, deve chegar a TargetValue (que pode ser menor, numa meta de redução) e está
// em CurrentValue.
type Goal struct {
	ID        ID  `json:"id"`
	CompanyID ID  `json:"company_id"`
	ProjectID *ID `json:"project_id,omitempty"`
	// ParentID é a meta-pai (árvore de metas, ADR-0042); nil = raiz.
	ParentID *ID    `json:"parent_id,omitempty"`
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	// Description em Markdown.
	Description  string     `json:"description,omitempty"`
	Metric       string     `json:"metric,omitempty"`
	Unit         string     `json:"unit,omitempty"`
	StartValue   float64    `json:"start_value"`
	TargetValue  float64    `json:"target_value"`
	CurrentValue float64    `json:"current_value"`
	DueDate      *time.Time `json:"due_date,omitempty"`
	Status       GoalStatus `json:"status"`
	// OwnerPersonaID é a persona dona da meta; nil = dona humana (o time).
	OwnerPersonaID *ID        `json:"owner_persona_id,omitempty"`
	CreatedBy      Actor      `json:"created_by"`
	AchievedAt     *time.Time `json:"achieved_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// GoalProgress é (atual − inicial) / (alvo − inicial) limitado a [0, 1]. Valores fora de ordem (abaixo do
// inicial) dão 0; passar do alvo dá 1. Inicial igual ao alvo não existe (o service recusa).
func GoalProgress(start, target, current float64) float64 {
	d := target - start
	if d == 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return 0
	}
	p := (current - start) / d
	switch {
	case math.IsNaN(p) || p < 0:
		return 0
	case p > 1:
		return 1
	}
	return p
}

// Progress é o progresso da meta, de 0 a 1.
func (g Goal) Progress() float64 { return GoalProgress(g.StartValue, g.TargetValue, g.CurrentValue) }

// Reaches diz se o valor v alcança o alvo (cruza-o, na direção da meta).
func (g Goal) Reaches(v float64) bool {
	if g.TargetValue >= g.StartValue {
		return v >= g.TargetValue
	}
	return v <= g.TargetValue
}

// GoalUpdate é um registro de progresso.
type GoalUpdate struct {
	ID        ID        `json:"id"`
	GoalID    ID        `json:"goal_id"`
	Value     float64   `json:"value"`
	Note      string    `json:"note,omitempty"`
	Author    Actor     `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}
