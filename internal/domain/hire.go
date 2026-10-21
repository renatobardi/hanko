package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HireSpec é a ficha de contratação que viaja num gate hire (ADR-0041): o que a persona (ou o humano, quando a empresa
// exige gate) propõe. Aprovar o gate cria a persona com exatamente esta ficha, na mesma transação da decisão.
type HireSpec struct {
	Name                 string          `json:"name"`
	Slug                 string          `json:"slug,omitempty"`
	Title                string          `json:"title,omitempty"`
	Icon                 string          `json:"icon,omitempty"`
	Instructions         string          `json:"instructions,omitempty"`
	Agent                string          `json:"agent"`
	Model                string          `json:"model,omitempty"`
	AdapterConfig        json.RawMessage `json:"adapter_config,omitempty"`
	ReportsTo            *ID             `json:"reports_to,omitempty"`
	WorkspaceID          *ID             `json:"workspace_id,omitempty"`
	HeartbeatIntervalSec int             `json:"heartbeat_interval_sec,omitempty"`
	BudgetMonthlyCents   *int64          `json:"budget_monthly_cents,omitempty"`
	SubscriptionID       *ID             `json:"subscription_id,omitempty"`
	RequireReview        bool            `json:"require_review,omitempty"`
	ReviewerPersona      *ID             `json:"reviewer_persona,omitempty"`
	ReviewerAgent        string          `json:"reviewer_agent,omitempty"`
	ReviewerModel        string          `json:"reviewer_model,omitempty"`
}

// Check valida o que dá para validar sem banco. O env do adapter_config é recusado: uma ficha que viaja no gate fica
// em texto claro na trilha, e segredo nunca entra por aí — o humano o configura depois da contratação (ADR-0050).
func (s HireSpec) Check() error {
	if strings.TrimSpace(s.Name) == "" {
		return Invalid("spec.name", "obrigatório")
	}
	if !ValidAgent(s.Agent) {
		return Invalid("spec.agent", fmt.Sprintf("agente desconhecido %q (um de %v)", s.Agent, KnownAgents))
	}
	if len(s.AdapterConfig) > 0 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(s.AdapterConfig, &m); err != nil {
			return Invalid("spec.adapter_config", "não é um objeto JSON")
		}
		if _, has := m["env"]; has {
			return Invalid("spec.adapter_config", "env não viaja numa proposta de contratação: configure as variáveis (segredos) depois de contratar")
		}
	}
	return nil
}
