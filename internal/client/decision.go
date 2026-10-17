package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Decision model (ADR-0056).

// SetDecisionReq configura o decision model.
type SetDecisionReq struct {
	BaseURL    string `json:"base_url,omitempty"`
	APIKeyRef  string `json:"api_key_ref,omitempty"`
	Model      string `json:"model"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

func dp(company string) string { return cp(company) + "/decision" }

// GetDecision devolve a configuração e as features.
func (c *Client) GetDecision(ctx context.Context, company string) (api.Decision, error) {
	return get[api.Decision](ctx, c, http.MethodGet, dp(company), nil)
}

// SetDecision configura o decision model.
func (c *Client) SetDecision(ctx context.Context, company string, req SetDecisionReq) (api.Decision, error) {
	return get[api.Decision](ctx, c, http.MethodPut, dp(company), req)
}

// DeleteDecision remove o decision model.
func (c *Client) DeleteDecision(ctx context.Context, company string) error {
	return c.do(ctx, http.MethodDelete, dp(company), nil, nil, nil)
}

// DecisionCatalog lista o catálogo de modelos do provedor.
func (c *Client) DecisionCatalog(ctx context.Context, company, baseURL, apiKeyRef string) ([]api.DecisionCatalogModel, error) {
	q := url.Values{}
	if baseURL != "" {
		q.Set("base_url", baseURL)
	}
	if apiKeyRef != "" {
		q.Set("api_key_ref", apiKeyRef)
	}
	return list[api.DecisionCatalogModel](ctx, c, dp(company)+"/catalog", q)
}

// SetDecisionFeature liga ou desliga uma feature.
func (c *Client) SetDecisionFeature(ctx context.Context, company, feature string, enabled bool, model string) (api.Decision, error) {
	return get[api.Decision](ctx, c, http.MethodPut, dp(company)+"/features/"+url.PathEscape(feature), map[string]any{"enabled": enabled, "model": model})
}

// DecisionInvocations lista as últimas invocações.
func (c *Client) DecisionInvocations(ctx context.Context, company string, limit int) ([]api.DecisionInvocation, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.DecisionInvocation](ctx, c, dp(company)+"/invocations", q)
}

// TriageTask pede a triagem de uma task.
func (c *Client) TriageTask(ctx context.Context, company, task string) (api.Triage, error) {
	return get[api.Triage](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(task)+"/triage", nil)
}

// ClassifyPhase pede a etapa de um pipeline para um texto.
func (c *Client) ClassifyPhase(ctx context.Context, company, pipeline, text string) (api.PhaseSuggestion, error) {
	return get[api.PhaseSuggestion](ctx, c, http.MethodPost, dp(company)+"/classify-phase", map[string]string{"pipeline": pipeline, "text": text})
}

// NameSummary pede nome e resumo de um texto.
func (c *Client) NameSummary(ctx context.Context, company, text string) (api.NameSummary, error) {
	return get[api.NameSummary](ctx, c, http.MethodPost, dp(company)+"/name-summary", map[string]string{"text": text})
}
