package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- plugins, M18 (ADR-0045) ----------

// PluginCapability concede ou revoga uma capacidade declarada no manifesto (dono da instalação).
func (c *Client) PluginCapability(ctx context.Context, ref, capability string, grant bool) (api.Plugin, error) {
	verb := "revoke"
	if grant {
		verb = "grant"
	}
	return get[api.Plugin](ctx, c, http.MethodPost, "/plugins/"+url.PathEscape(ref)+"/capabilities/"+url.PathEscape(capability)+"/"+verb, nil)
}

// PluginTabs lista as abas dos plugins da empresa.
func (c *Client) PluginTabs(ctx context.Context, company string) ([]api.PluginTab, error) {
	return list[api.PluginTab](ctx, c, cp(company)+"/plugin-tabs", url.Values{"limit": {"500"}})
}

// PluginFlowTriggers devolve os gatilhos de fluxo do plugin e as ligações da empresa.
func (c *Client) PluginFlowTriggers(ctx context.Context, company, ref string) (api.PluginFlowTriggers, error) {
	return get[api.PluginFlowTriggers](ctx, c, http.MethodGet, cp(company)+"/plugins/"+url.PathEscape(ref)+"/flow-triggers", nil)
}

// LinkPluginFlowTrigger liga (ou desliga) o gatilho de um plugin a um fluxo da empresa.
func (c *Client) LinkPluginFlowTrigger(ctx context.Context, company, ref, trigger, flow string, link bool) error {
	method := http.MethodDelete
	if link {
		method = http.MethodPut
	}
	return c.do(ctx, method, cp(company)+"/plugins/"+url.PathEscape(ref)+"/flow-triggers/"+url.PathEscape(trigger)+"/flows/"+url.PathEscape(flow), nil, nil, nil)
}

// PluginSkillSources lista as fontes de skills oferecidas pelos plugins da empresa.
func (c *Client) PluginSkillSources(ctx context.Context, company string) ([]api.PluginSkillSourceItem, error) {
	return list[api.PluginSkillSourceItem](ctx, c, cp(company)+"/plugin-skill-sources", url.Values{"limit": {"500"}})
}
