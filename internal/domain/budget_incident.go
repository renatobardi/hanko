package domain

import "time"

// Incidente de orçamento (ADR-0041): abre quando um teto mensal estoura (empresa, projeto ou persona) e fica na Caixa
// até alguém resolver. Resolver é uma decisão humana registrada: subir o teto, manter pausado ou liberar só um run.

// BudgetScope é de quem é o teto estourado.
type BudgetScope string

const (
	BudgetScopeCompany BudgetScope = "company"
	BudgetScopeProject BudgetScope = "project"
	BudgetScopePersona BudgetScope = "persona"
)

// Valid diz se o escopo é conhecido.
func (s BudgetScope) Valid() bool {
	return s == BudgetScopeCompany || s == BudgetScopeProject || s == BudgetScopePersona
}

// IncidentResolution é como o incidente foi resolvido.
type IncidentResolution string

const (
	ResolveRaiseCeiling IncidentResolution = "ceiling_raised" // o teto subiu (ou saiu) e o que estava pausado por ele volta
	ResolveKeepPaused   IncidentResolution = "kept_paused"    // decisão de manter: nada muda, o incidente sai da Caixa
	ResolveReleaseOnce  IncidentResolution = "released_once"  // libera só mais um run, sem mexer no teto
	ResolveAuto         IncidentResolution = "auto"           // o teto subiu por outro caminho, ou o mês virou
)

// UserChoice diz se é uma resolução que uma pessoa escolhe (auto é do sistema).
func (r IncidentResolution) UserChoice() bool {
	return r == ResolveRaiseCeiling || r == ResolveKeepPaused || r == ResolveReleaseOnce
}

// BudgetIncident é um incidente de orçamento.
type BudgetIncident struct {
	ID          ID          `json:"id"`
	CompanyID   ID          `json:"company_id"`
	Scope       BudgetScope `json:"scope"`
	TargetID    ID          `json:"target_id"`
	TargetName  string      `json:"target_name"`
	Month       string      `json:"month"`
	Open        bool        `json:"open"`
	BudgetCents int64       `json:"budget_cents"`
	SpentUSD    float64     `json:"spent_usd"`
	// ReleaseRuns são os runs avulsos liberados e ainda não consumidos.
	ReleaseRuns int                `json:"release_runs"`
	Resolution  IncidentResolution `json:"resolution,omitempty"`
	Note        string             `json:"note,omitempty"`
	ResolvedBy  *Actor             `json:"resolved_by,omitempty"`
	OpenedAt    time.Time          `json:"opened_at"`
	ResolvedAt  *time.Time         `json:"resolved_at,omitempty"`
}
