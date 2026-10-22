package domain

import (
	"net/url"
	"strings"
	"time"
)

// Decision model (ADR-0056): um classificador externo por empresa, chamado pelo próprio Hanko (não por uma persona)
// para decisões pequenas e baratas. Cada chamada vira uma linha do livro de invocações, com ator system.

// DecisionProviderOpenRouter é o único provedor da v1: qualquer endpoint OpenAI-compatível, com o catálogo de
// modelos do OpenRouter.
const DecisionProviderOpenRouter = "openrouter"

// DefaultDecisionBaseURL é a raiz da API do OpenRouter; a base é configurável por empresa (proxy, espelho, testes).
const DefaultDecisionBaseURL = "https://openrouter.ai/api/v1"

// DecisionFeature é uma função do Hanko que usa o decision model. O registro é fixo em código: a empresa só liga ou
// desliga e, se quiser, troca o modelo de cada uma.
type DecisionFeature string

// Features registradas.
const (
	FeatureTriage          DecisionFeature = "triage"
	FeaturePhaseClassifier DecisionFeature = "phase_classifier"
	FeatureNameSummary     DecisionFeature = "name_summary"
	// FeatureFastResponse responde direto a uma mensagem de canal externo (ADR-0046 §D.5); nunca decide nada.
	FeatureFastResponse DecisionFeature = "fast_response"
	// FeatureCardSummary escreve o resumo de um card de status cujo motor é o decision model (ADR-0048).
	FeatureCardSummary DecisionFeature = "card_summary"
)

// DecisionFeatureSpec descreve uma feature registrada.
type DecisionFeatureSpec struct {
	Feature     DecisionFeature `json:"feature"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
}

// DecisionFeatures é o registro, na ordem em que a tela e a CLI os mostram.
var DecisionFeatures = []DecisionFeatureSpec{
	{FeatureTriage, "Triagem", "Sugere a persona e a prioridade para uma task nova, como comentário do sistema."},
	{FeaturePhaseClassifier, "Classificador de fase", "Sugere em qual etapa de um pipeline um texto se encaixa."},
	{FeatureNameSummary, "Nome e resumo", "Gera um nome curto e um resumo para um texto longo."},
	{FeatureFastResponse, "Resposta rápida", "Responde direto a perguntas simples que chegam por um canal externo, sem acordar a persona."},
	{FeatureCardSummary, "Resumo de card de status", "Escreve o resumo dos cards de status que usam o decision model como motor."},
}

// Valid diz se f é uma feature registrada.
func (f DecisionFeature) Valid() bool {
	for _, s := range DecisionFeatures {
		if s.Feature == f {
			return true
		}
	}
	return false
}

// DecisionModel é a configuração do decision model de uma empresa. A chave nunca está aqui: APIKeyRef é a referência
// ${secret:NOME} do segredo da empresa (ADR-0050).
type DecisionModel struct {
	CompanyID          ID        `json:"company_id"`
	Provider           string    `json:"provider"`
	BaseURL            string    `json:"base_url"`
	APIKeyRef          string    `json:"api_key_ref"`
	Model              string    `json:"model"`
	PromptUSDToken     float64   `json:"prompt_usd_token"`
	CompletionUSDToken float64   `json:"completion_usd_token"`
	TimeoutSec         int       `json:"timeout_sec"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// DecisionFeatureState é o estado de uma feature numa empresa.
type DecisionFeatureState struct {
	DecisionFeatureSpec
	Enabled bool `json:"enabled"`
	// Model, quando preenchido, substitui o modelo da empresa nesta feature.
	Model string `json:"model,omitempty"`
}

// DecisionInvocation é uma chamada ao decision model no livro de custos. O ator é sempre system.
type DecisionInvocation struct {
	ID           ID              `json:"id"`
	CompanyID    ID              `json:"company_id"`
	Feature      DecisionFeature `json:"feature"`
	Actor        ActorType       `json:"actor"`
	TaskID       *ID             `json:"task_id,omitempty"`
	Model        string          `json:"model"`
	Status       string          `json:"status"` // ok | error
	Error        string          `json:"error,omitempty"`
	InputTokens  int64           `json:"input_tokens"`
	OutputTokens int64           `json:"output_tokens"`
	CostUSD      float64         `json:"cost_usd"`
	CostSource   string          `json:"cost_source"` // reported | estimated
	LatencyMs    int             `json:"latency_ms"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Limites do decision model.
const (
	MaxDecisionModelLen    = 200
	DefaultDecisionTimeout = 30
	MaxDecisionTimeout     = 300
)

// ValidateDecisionBaseURL aceita só http(s) com host; a proteção contra destinos internos é do service (netguard).
func ValidateDecisionBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || len(raw) > 500 {
		return Invalid("base_url", "must be an http(s) URL of an OpenAI-compatible API (e.g. "+DefaultDecisionBaseURL+")")
	}
	if u.User != nil {
		return Invalid("base_url", "must not carry credentials; use api_key_ref")
	}
	return nil
}
