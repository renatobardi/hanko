package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- skills ----------

type CreateSkillReq struct {
	Name        string `json:"name,omitempty"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body"`
}

type UpdateSkillReq struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Body        *string `json:"body,omitempty"`
}

type enabledReq struct {
	Enabled bool `json:"enabled"`
}

func (c *Client) CreateSkill(ctx context.Context, company string, in CreateSkillReq) (api.Skill, error) {
	return get[api.Skill](ctx, c, http.MethodPost, cp(company)+"/skills", in)
}

func (c *Client) Skills(ctx context.Context, company string) ([]api.Skill, error) {
	return list[api.Skill](ctx, c, cp(company)+"/skills", url.Values{"limit": {"500"}})
}

func (c *Client) Skill(ctx context.Context, company, ref string) (api.Skill, error) {
	return get[api.Skill](ctx, c, http.MethodGet, cp(company)+"/skills/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateSkill(ctx context.Context, company, ref string, in UpdateSkillReq) (api.Skill, error) {
	return get[api.Skill](ctx, c, http.MethodPatch, cp(company)+"/skills/"+url.PathEscape(ref), in)
}

func (c *Client) DeleteSkill(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/skills/"+url.PathEscape(ref), nil, nil, nil)
}

// SkillPersonas lista as personas com o estado (habilitada ou não) da skill.
func (c *Client) SkillPersonas(ctx context.Context, company, skill string) ([]api.SkillPersona, error) {
	return list[api.SkillPersona](ctx, c, cp(company)+"/skills/"+url.PathEscape(skill)+"/personas", nil)
}

// PersonaSkills lista as skills com o estado para a persona.
func (c *Client) PersonaSkills(ctx context.Context, company, persona string) ([]api.PersonaSkill, error) {
	return list[api.PersonaSkill](ctx, c, cp(company)+"/personas/"+url.PathEscape(persona)+"/skills", nil)
}

// SetPersonaSkill liga ou desliga a skill para a persona.
func (c *Client) SetPersonaSkill(ctx context.Context, company, persona, skill string, enabled bool) error {
	return c.do(ctx, http.MethodPut, cp(company)+"/personas/"+url.PathEscape(persona)+"/skills/"+url.PathEscape(skill), nil, enabledReq{Enabled: enabled}, nil)
}

// ---------- integrações ----------

type CreateIntegrationReq struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Slug        string            `json:"slug,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	URL         string            `json:"url,omitempty"`
	Transport   string            `json:"transport,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	AllPersonas bool              `json:"all_personas,omitempty"`
	Personas    []string          `json:"personas,omitempty"`
	Events      []string          `json:"events,omitempty"`
	Secret      string            `json:"secret,omitempty"`
}

type UpdateIntegrationReq struct {
	Name         *string           `json:"name,omitempty"`
	Enabled      *bool             `json:"enabled,omitempty"`
	URL          *string           `json:"url,omitempty"`
	Command      *string           `json:"command,omitempty"`
	Args         *[]string         `json:"args,omitempty"`
	SetHeaders   map[string]string `json:"set_headers,omitempty"`
	DropHeaders  []string          `json:"drop_headers,omitempty"`
	SetEnv       map[string]string `json:"set_env,omitempty"`
	DropEnv      []string          `json:"drop_env,omitempty"`
	AllPersonas  *bool             `json:"all_personas,omitempty"`
	Personas     *[]string         `json:"personas,omitempty"`
	Events       *[]string         `json:"events,omitempty"`
	Secret       *string           `json:"secret,omitempty"`
	RotateSecret bool              `json:"rotate_secret,omitempty"`
}

func (c *Client) CreateIntegration(ctx context.Context, company string, in CreateIntegrationReq) (api.Integration, error) {
	return get[api.Integration](ctx, c, http.MethodPost, cp(company)+"/integrations", in)
}

func (c *Client) Integrations(ctx context.Context, company string) ([]api.Integration, error) {
	return list[api.Integration](ctx, c, cp(company)+"/integrations", url.Values{"limit": {"500"}})
}

func (c *Client) Integration(ctx context.Context, company, ref string) (api.Integration, error) {
	return get[api.Integration](ctx, c, http.MethodGet, cp(company)+"/integrations/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateIntegration(ctx context.Context, company, ref string, in UpdateIntegrationReq) (api.Integration, error) {
	return get[api.Integration](ctx, c, http.MethodPatch, cp(company)+"/integrations/"+url.PathEscape(ref), in)
}

func (c *Client) DeleteIntegration(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/integrations/"+url.PathEscape(ref), nil, nil, nil)
}

func (c *Client) TestIntegration(ctx context.Context, company, ref string) (api.IntegrationTestResult, error) {
	return get[api.IntegrationTestResult](ctx, c, http.MethodPost, cp(company)+"/integrations/"+url.PathEscape(ref)+"/test", nil)
}

func (c *Client) IntegrationDeliveries(ctx context.Context, company, ref string, limit int) ([]api.WebhookDelivery, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.WebhookDelivery](ctx, c, cp(company)+"/integrations/"+url.PathEscape(ref)+"/deliveries", q)
}

func (c *Client) IntegrationPersonas(ctx context.Context, company, ref string) ([]api.IntegrationPersona, error) {
	return list[api.IntegrationPersona](ctx, c, cp(company)+"/integrations/"+url.PathEscape(ref)+"/personas", nil)
}

func (c *Client) PersonaIntegrations(ctx context.Context, company, persona string) ([]api.PersonaIntegration, error) {
	return list[api.PersonaIntegration](ctx, c, cp(company)+"/personas/"+url.PathEscape(persona)+"/integrations", nil)
}

// SetPersonaIntegration liga ou desliga o servidor MCP para a persona.
func (c *Client) SetPersonaIntegration(ctx context.Context, company, persona, integration string, enabled bool) error {
	return c.do(ctx, http.MethodPut, cp(company)+"/personas/"+url.PathEscape(persona)+"/integrations/"+url.PathEscape(integration), nil, enabledReq{Enabled: enabled}, nil)
}

// RekeyIntegrationSecrets reescreve os segredos cifrados com a chave anterior (rotação de HANKO_SECRET_KEY).
func (c *Client) RekeyIntegrationSecrets(ctx context.Context) (api.RekeyResult, error) {
	return get[api.RekeyResult](ctx, c, http.MethodPost, "/integrations/rekey-secrets", nil)
}
