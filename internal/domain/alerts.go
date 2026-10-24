package domain

import (
	"encoding/json"
	"time"
)

// AlertKind é a condição operacional que um alerta acompanha.
type AlertKind string

const (
	// AlertPersonaSilent: persona com trabalho esperando e sem ser acordada além de N vezes o seu intervalo.
	AlertPersonaSilent AlertKind = "persona_silent"
	// AlertRunStuck: execução em running sem nenhum evento além do limite configurado.
	AlertRunStuck AlertKind = "run_stuck"
	// AlertBudgetNearLimit: orçamento mensal (empresa ou persona) em 80% ou mais.
	AlertBudgetNearLimit AlertKind = "budget_near_limit"
	// AlertPersonaNotReady: o último teste de readiness da persona falhou (binário, login ou modelo); o despacho está
	// bloqueado até um teste passar (ADR-0055).
	AlertPersonaNotReady AlertKind = "persona_not_ready"
	// AlertSecretRotationDue: um campo de segredo da empresa passou do prazo de rotação (do item ou da coleção); um
	// alerta por item, fechado quando o campo é rotacionado (ADR-0068 §7.3).
	AlertSecretRotationDue AlertKind = "secret_rotation_due"
)

// AlertKinds lista todos os tipos, na ordem em que aparecem em /metrics.
var AlertKinds = []AlertKind{AlertPersonaSilent, AlertRunStuck, AlertBudgetNearLimit, AlertPersonaNotReady, AlertSecretRotationDue}

func (k AlertKind) Valid() bool {
	for _, v := range AlertKinds {
		if k == v {
			return true
		}
	}
	return false
}

// AlertSeverity separa o que merece atenção (warning) do que já está parando trabalho (critical).
type AlertSeverity string

const (
	AlertWarning  AlertSeverity = "warning"
	AlertCritical AlertSeverity = "critical"
)

// Alert é uma condição operacional aberta (ou já fechada) numa empresa. Há no máximo um alerta
// aberto por (empresa, tipo, sujeito); ele fecha sozinho quando a condição some.
type Alert struct {
	ID          ID              `json:"id"`
	CompanyID   ID              `json:"company_id"`
	Kind        AlertKind       `json:"kind"`
	SubjectType string          `json:"subject_type"` // persona | run | company
	SubjectID   ID              `json:"subject_id"`
	Severity    AlertSeverity   `json:"severity"`
	Message     string          `json:"message"`
	Data        json.RawMessage `json:"data,omitempty"`
	OpenedAt    time.Time       `json:"opened_at"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	ClosedAt    *time.Time      `json:"closed_at,omitempty"`
}
