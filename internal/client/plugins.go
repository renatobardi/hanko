package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- plugins (ADR-0038) ----------

// Plugins devolve os plugins disponíveis no diretório e os instalados (dono da instalação).
func (c *Client) Plugins(ctx context.Context) (api.PluginsOverview, error) {
	return get[api.PluginsOverview](ctx, c, http.MethodGet, "/plugins", nil)
}

// InstallPlugin instala o plugin do diretório key.
func (c *Client) InstallPlugin(ctx context.Context, key string) (api.Plugin, error) {
	return get[api.Plugin](ctx, c, http.MethodPost, "/plugins/install", map[string]string{"key": key})
}

// PluginAction faz enable, disable, upgrade ou approve-upgrade na instalação.
func (c *Client) PluginAction(ctx context.Context, ref, action string) (api.Plugin, error) {
	return get[api.Plugin](ctx, c, http.MethodPost, "/plugins/"+url.PathEscape(ref)+"/"+action, nil)
}

// UninstallPlugin desinstala o plugin.
func (c *Client) UninstallPlugin(ctx context.Context, ref string) error {
	return c.do(ctx, http.MethodPost, "/plugins/"+url.PathEscape(ref)+"/uninstall", nil, nil, nil)
}

// PluginLogs devolve as últimas linhas do log do plugin.
func (c *Client) PluginLogs(ctx context.Context, ref string, limit int) ([]api.PluginLog, error) {
	return list[api.PluginLog](ctx, c, "/plugins/"+url.PathEscape(ref)+"/logs", url.Values{"limit": {strconv.Itoa(limit)}})
}

// PluginHealth devolve a saúde do plugin.
func (c *Client) PluginHealth(ctx context.Context, ref string) (api.PluginHealth, error) {
	return get[api.PluginHealth](ctx, c, http.MethodGet, "/plugins/"+url.PathEscape(ref)+"/health", nil)
}

// CompanyPlugins lista os plugins instalados com o estado na empresa.
func (c *Client) CompanyPlugins(ctx context.Context, company string) ([]api.CompanyPlugin, error) {
	return list[api.CompanyPlugin](ctx, c, cp(company)+"/plugins", url.Values{"limit": {"500"}})
}

// CompanyPlugin devolve um plugin na empresa.
func (c *Client) CompanyPlugin(ctx context.Context, company, ref string) (api.CompanyPlugin, error) {
	return get[api.CompanyPlugin](ctx, c, http.MethodGet, cp(company)+"/plugins/"+url.PathEscape(ref), nil)
}

// UpdateCompanyPluginReq liga, desliga e configura o plugin na empresa.
type UpdateCompanyPluginReq struct {
	Enabled      *bool          `json:"enabled,omitempty"`
	Config       map[string]any `json:"config,omitempty"`
	ClearSecrets []string       `json:"clear_secrets,omitempty"`
}

// UpdateCompanyPlugin grava o estado e a configuração do plugin na empresa.
func (c *Client) UpdateCompanyPlugin(ctx context.Context, company, ref string, in UpdateCompanyPluginReq) (api.CompanyPlugin, error) {
	return get[api.CompanyPlugin](ctx, c, http.MethodPatch, cp(company)+"/plugins/"+url.PathEscape(ref), in)
}
