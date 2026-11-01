package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- assistente Conectar (ADR-0089) e modelos de acesso ----------

// ConnectBeginReq é o passo 1 do assistente: servidor MCP, por header secreto ou por OAuth.
type ConnectBeginReq struct {
	Name     string            `json:"name"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	OAuth    bool              `json:"oauth,omitempty"`
	Scopes   []string          `json:"scopes,omitempty"`
	ClientID string            `json:"client_id,omitempty"`
}

// ConnectCompleteReq é a conclusão: modelo escolhido e personas que recebem a ferramenta.
type ConnectCompleteReq struct {
	Template    string   `json:"template"`
	Personas    []string `json:"personas"`
	ProfileName string   `json:"profile_name,omitempty"`
	ProfileSlug string   `json:"profile_slug,omitempty"`
}

// ConnectBegin cria a integração (e a conexão OAuth, quando for o caso) e descobre o catálogo.
func (c *Client) ConnectBegin(ctx context.Context, company string, in ConnectBeginReq) (api.ConnectWizard, error) {
	return get[api.ConnectWizard](ctx, c, http.MethodPost, cp(company)+"/integrations/connect", in)
}

// ConnectState devolve o estado do assistente de uma integração.
func (c *Client) ConnectState(ctx context.Context, company, ref string) (api.ConnectWizard, error) {
	return get[api.ConnectWizard](ctx, c, http.MethodGet, cp(company)+"/integrations/"+url.PathEscape(ref)+"/connect", nil)
}

// ConnectDiscover repete a descoberta do catálogo (passo 2).
func (c *Client) ConnectDiscover(ctx context.Context, company, ref string) (api.ConnectWizard, error) {
	return get[api.ConnectWizard](ctx, c, http.MethodPost, cp(company)+"/integrations/"+url.PathEscape(ref)+"/connect/discover", nil)
}

// ConnectDiscard descarta o rascunho: apaga a integração que o assistente criou.
func (c *Client) ConnectDiscard(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/integrations/"+url.PathEscape(ref)+"/connect", nil, nil, nil)
}

// ConnectPreview confere as tools efetivas por persona com o modelo aplicado (passo 5).
func (c *Client) ConnectPreview(ctx context.Context, company, ref, template string, personas []string) (api.ConnectPreview, error) {
	var out api.ConnectPreview
	q := url.Values{"template": {template}, "personas": {strings.Join(personas, ",")}}
	err := c.do(ctx, http.MethodGet, cp(company)+"/integrations/"+url.PathEscape(ref)+"/connect/preview", q, nil, &out)
	return out, err
}

// ConnectComplete grava o perfil, o vínculo e a ligação por persona numa transação.
func (c *Client) ConnectComplete(ctx context.Context, company, ref string, in ConnectCompleteReq) (api.ConnectResult, error) {
	return get[api.ConnectResult](ctx, c, http.MethodPost, cp(company)+"/integrations/"+url.PathEscape(ref)+"/connect/complete", in)
}

// AccessTemplates lista os modelos de acesso da empresa (o sugerido primeiro).
func (c *Client) AccessTemplates(ctx context.Context, company string) ([]api.AccessTemplate, error) {
	return list[api.AccessTemplate](ctx, c, cp(company)+"/access-templates", url.Values{"limit": {"500"}})
}
