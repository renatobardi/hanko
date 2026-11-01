package export

import "time"

// Configuração externa do arquivo (ADR-0088 item 1): agentes e hosts externos, emparelhamentos de repositório, a chave externa
// e o histórico de mensalidade das assinaturas e os limites de alerta da empresa. Os campos são opcionais (o formato continua
// v1) e por referência, sem id. Conversas, sessões, rodadas, aprovações, chamadas ao modelo, alertas, preços e telemetria
// não viajam, e nenhuma chave `ingest` ou `read` também (ADR-0088 itens 2 e 3).

// ExternalAgent é um agente externo da empresa, com os apelidos que já chegaram.
type ExternalAgent struct {
	Name     string     `json:"name"`
	Aliases  []string   `json:"aliases,omitempty" doc:"Textos de agente que chegaram e apontam para este agente."`
	Archived *time.Time `json:"archived_at,omitempty"`
}

// ExternalHost é um host externo. Vai sem chave: o host importado precisa de uma chave `ingest` nova (ADR-0088 item 3).
type ExternalHost struct {
	Name     string     `json:"name"`
	Aliases  []string   `json:"aliases,omitempty"`
	AlwaysOn bool       `json:"always_on,omitempty" doc:"Liga o alerta host_no_data."`
	Archived *time.Time `json:"archived_at,omitempty"`
}

// RepoPairing liga um repositório (URL ou nome, como chegou) a um workspace da empresa.
type RepoPairing struct {
	Repo      string `json:"repo"`
	Kind      string `json:"kind,omitempty" enum:"url,name" doc:"Ausente = name."`
	Workspace string `json:"workspace" doc:"Slug do workspace."`
}

// MonthlyValue é uma linha do histórico de mensalidade de uma assinatura (ADR-0082 item 6).
type MonthlyValue struct {
	MonthlyUSD float64   `json:"monthly_usd"`
	StartsOn   string    `json:"starts_on" format:"date" doc:"Primeiro dia (UTC) em que o valor vale."`
	RecordedAt time.Time `json:"recorded_at" doc:"Quando a linha foi registrada; desempata o mesmo início."`
	Origin     string    `json:"origin,omitempty" enum:"created,manual,migrated"`
}

// AlertSetting é um limite ou prazo de alerta que a empresa mudou (ADR-0083 itens 3 e 6). Só entra o que difere do padrão.
type AlertSetting struct {
	Key   string  `json:"key" example:"limit.queue_max_ratio"`
	Value float64 `json:"value"`
}
