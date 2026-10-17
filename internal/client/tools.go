package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- apps, gateway de tools e perfis de ferramentas (ADR-0036) ----------

func ip(company, integration string) string {
	return cp(company) + "/integrations/" + url.PathEscape(integration)
}

func toolProfilePath(company, profile string) string {
	return cp(company) + "/tool-profiles/" + url.PathEscape(profile)
}

func gp(company, gateway string) string {
	return cp(company) + "/tool-gateways/" + url.PathEscape(gateway)
}

// IntegrationTools lê o catálogo de tools de uma integração MCP.
func (c *Client) IntegrationTools(ctx context.Context, company, integration string) (api.ToolCatalog, error) {
	return get[api.ToolCatalog](ctx, c, http.MethodGet, ip(company, integration)+"/tools", nil)
}

// RefreshIntegrationTools redescobre o catálogo.
func (c *Client) RefreshIntegrationTools(ctx context.Context, company, integration string) (api.ToolCatalogRefresh, error) {
	return get[api.ToolCatalogRefresh](ctx, c, http.MethodPost, ip(company, integration)+"/tools/refresh", nil)
}

// SetIntegrationToolStatus libera (active) ou desliga (disabled) uma tool.
func (c *Client) SetIntegrationToolStatus(ctx context.Context, company, integration, tool, status string) (api.IntegrationTool, error) {
	return get[api.IntegrationTool](ctx, c, http.MethodPatch, ip(company, integration)+"/tools/"+url.PathEscape(tool),
		map[string]string{"status": status})
}

// ReviewIntegrationTools libera da quarentena as tools listadas (ou todas).
func (c *Client) ReviewIntegrationTools(ctx context.Context, company, integration string, tools []string) (api.ReviewResult, error) {
	return get[api.ReviewResult](ctx, c, http.MethodPost, ip(company, integration)+"/tools/review", map[string]any{"tools": tools})
}

type CreateToolProfileReq struct {
	Name            string `json:"name"`
	Slug            string `json:"slug,omitempty"`
	Description     string `json:"description,omitempty"`
	DefaultAction   string `json:"default_action,omitempty"`
	RateLimitPerMin int    `json:"rate_limit_per_min,omitempty"`
}

type ToolRuleReq struct {
	Target      string `json:"target"`
	Integration string `json:"integration,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Risk        string `json:"risk,omitempty"`
	Effect      string `json:"effect"`
	Mode        string `json:"mode,omitempty"`
}

type ToolBindingReq struct {
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	Persona  string `json:"persona,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

func (c *Client) CreateToolProfile(ctx context.Context, company string, in CreateToolProfileReq) (api.ToolProfile, error) {
	return get[api.ToolProfile](ctx, c, http.MethodPost, cp(company)+"/tool-profiles", in)
}

func (c *Client) ToolProfiles(ctx context.Context, company string) ([]api.ToolProfile, error) {
	return list[api.ToolProfile](ctx, c, cp(company)+"/tool-profiles", url.Values{"limit": {"500"}})
}

func (c *Client) ToolProfile(ctx context.Context, company, profile string) (api.ToolProfile, error) {
	return get[api.ToolProfile](ctx, c, http.MethodGet, toolProfilePath(company, profile), nil)
}

func (c *Client) DeleteToolProfile(ctx context.Context, company, profile string) error {
	return c.do(ctx, http.MethodDelete, toolProfilePath(company, profile), nil, nil, nil)
}

func (c *Client) AddToolProfileRule(ctx context.Context, company, profile string, in ToolRuleReq) (api.ToolRule, error) {
	return get[api.ToolRule](ctx, c, http.MethodPost, toolProfilePath(company, profile)+"/rules", in)
}

func (c *Client) DeleteToolProfileRule(ctx context.Context, company, profile, rule string) error {
	return c.do(ctx, http.MethodDelete, toolProfilePath(company, profile)+"/rules/"+url.PathEscape(rule), nil, nil, nil)
}

func (c *Client) BindToolProfile(ctx context.Context, company, profile string, in ToolBindingReq) (api.ToolBinding, error) {
	return get[api.ToolBinding](ctx, c, http.MethodPost, toolProfilePath(company, profile)+"/bindings", in)
}

func (c *Client) UnbindToolProfile(ctx context.Context, company, profile, binding string) error {
	return c.do(ctx, http.MethodDelete, toolProfilePath(company, profile)+"/bindings/"+url.PathEscape(binding), nil, nil, nil)
}

// EffectiveTools mostra o que a persona enxerga (num projeto, opcional) e por quê.
func (c *Client) EffectiveTools(ctx context.Context, company, persona, project string) (api.EffectiveTools, error) {
	q := url.Values{"persona": {persona}}
	if project != "" {
		q.Set("project", project)
	}
	var out api.EffectiveTools
	err := c.do(ctx, http.MethodGet, cp(company)+"/tool-profiles/effective", q, nil, &out)
	return out, err
}

type CreateToolGatewayReq struct {
	Name    string `json:"name"`
	Slug    string `json:"slug,omitempty"`
	Profile string `json:"profile"`
}

type CreateGatewayTokenReq struct {
	Label         string   `json:"label,omitempty"`
	Actions       []string `json:"actions,omitempty"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
}

func (c *Client) CreateToolGateway(ctx context.Context, company string, in CreateToolGatewayReq) (api.ToolGateway, error) {
	return get[api.ToolGateway](ctx, c, http.MethodPost, cp(company)+"/tool-gateways", in)
}

func (c *Client) ToolGateways(ctx context.Context, company string) ([]api.ToolGateway, error) {
	return list[api.ToolGateway](ctx, c, cp(company)+"/tool-gateways", url.Values{"limit": {"500"}})
}

func (c *Client) ToolGateway(ctx context.Context, company, gateway string) (api.ToolGateway, error) {
	return get[api.ToolGateway](ctx, c, http.MethodGet, gp(company, gateway), nil)
}

func (c *Client) DeleteToolGateway(ctx context.Context, company, gateway string) error {
	return c.do(ctx, http.MethodDelete, gp(company, gateway), nil, nil, nil)
}

// CreateGatewayToken gera um token; o valor (Token) só vem nesta resposta.
func (c *Client) CreateGatewayToken(ctx context.Context, company, gateway string, in CreateGatewayTokenReq) (api.GatewayToken, error) {
	return get[api.GatewayToken](ctx, c, http.MethodPost, gp(company, gateway)+"/tokens", in)
}

func (c *Client) RevokeGatewayToken(ctx context.Context, company, gateway, token string) (api.GatewayToken, error) {
	return get[api.GatewayToken](ctx, c, http.MethodDelete, gp(company, gateway)+"/tokens/"+url.PathEscape(token), nil)
}
