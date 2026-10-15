package domain

import (
	"slices"
	"time"
)

// IntegrationKind é o tipo de uma integração (ADR-0029).
type IntegrationKind string

const (
	// IntegrationMCP é um servidor MCP da empresa, injetado na configuração MCP das runs.
	IntegrationMCP IntegrationKind = "mcp"
	// IntegrationWebhook é um webhook de saída.
	IntegrationWebhook IntegrationKind = "webhook"
)

func (k IntegrationKind) Valid() bool { return k == IntegrationMCP || k == IntegrationWebhook }

// Transportes de um servidor MCP.
const (
	TransportHTTP  = "http"
	TransportStdio = "stdio"
)

// Eventos que um webhook pode assinar.
const (
	EventTaskCreated   = "task.created"
	EventTaskCompleted = "task.completed"
	EventGateOpened    = "gate.opened"
	EventGateDecided   = "gate.decided"
	EventRunFailed     = "run.failed"
	EventAlertOpened   = "alert.opened"
	// EventPing só existe na entrega de teste; não se assina.
	EventPing = "ping"
)

// WebhookEvents lista os eventos assináveis, na ordem de exibição.
var WebhookEvents = []string{EventTaskCreated, EventTaskCompleted, EventGateOpened, EventGateDecided, EventRunFailed, EventAlertOpened}

// ValidWebhookEvent informa se e é um evento assinável.
func ValidWebhookEvent(e string) bool { return slices.Contains(WebhookEvents, e) }

// IntegrationConfig é a parte da configuração que NÃO é segredo. Os valores dos headers e do ambiente ficam
// cifrados; aqui só os nomes, para a UI mostrar o que está definido.
type IntegrationConfig struct {
	// Transport (MCP): http ou stdio.
	Transport string `json:"transport,omitempty"`
	// URL: endereço do servidor MCP (http) ou destino do webhook (https).
	URL string `json:"url,omitempty"`
	// Command e Args (MCP stdio).
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// HeaderNames e EnvNames: nomes dos segredos guardados (headers do MCP http, variáveis do MCP stdio).
	HeaderNames []string `json:"header_names,omitempty"`
	EnvNames    []string `json:"env_names,omitempty"`
}

// Integration é um serviço externo conectado à empresa. Nunca carrega o valor de um segredo.
type Integration struct {
	ID          ID                `json:"id"`
	CompanyID   ID                `json:"company_id"`
	Kind        IntegrationKind   `json:"kind"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	AllPersonas bool              `json:"all_personas"`
	Config      IntegrationConfig `json:"config"`
	Events      []string          `json:"events"`
	// SecretSet: há segredo guardado (headers/variáveis do MCP, segredo do webhook).
	SecretSet bool `json:"secret_set"`
	// SecretHint: prefixo inofensivo do segredo gerado pelo servidor (webhook); vazio se o segredo foi informado.
	SecretHint string    `json:"secret_hint,omitempty"`
	CreatedBy  Actor     `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IntegrationPersona é o estado de uma persona para um servidor MCP.
type IntegrationPersona struct {
	PersonaID ID     `json:"persona_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
}

// Estados de uma entrega de webhook.
const (
	DeliveryPending  = "pending"
	DeliveryRetrying = "retrying"
	DeliverySuccess  = "success"
	DeliveryFailed   = "failed"
)

// WebhookDelivery é uma entrega (fato × webhook) com o resultado da última tentativa.
type WebhookDelivery struct {
	ID            ID         `json:"id"`
	IntegrationID ID         `json:"integration_id"`
	Event         string     `json:"event"`
	Test          bool       `json:"test"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	HTTPStatus    *int       `json:"http_status,omitempty"`
	LatencyMs     *int       `json:"latency_ms,omitempty"`
	Error         string     `json:"error,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
}
