package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Fluxos (variáveis, gatilhos, revisões, arquivar) e canais externos (ADR-0046, ADR-0047).

// CreateFlowTriggerReq cria um gatilho de fluxo.
type CreateFlowTriggerReq struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Enabled     *bool             `json:"enabled,omitempty"`
	SecretRef   string            `json:"secret_ref,omitempty"`
	RatePerMin  int               `json:"rate_per_min,omitempty"`
	EventVerb   string            `json:"event_verb,omitempty"`
	EventFilter map[string]string `json:"event_filter,omitempty"`
	Mapping     map[string]string `json:"mapping,omitempty"`
}

// UpdateFlowTriggerReq muda um gatilho.
type UpdateFlowTriggerReq struct {
	Name       *string `json:"name,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
	RatePerMin *int    `json:"rate_per_min,omitempty"`
}

func flowPath(company, ref string) string { return cp(company) + "/flows/" + url.PathEscape(ref) }

// FlowsArchived lista os fluxos arquivados.
func (c *Client) FlowsArchived(ctx context.Context, company string) ([]api.Flow, error) {
	return list[api.Flow](ctx, c, cp(company)+"/flows", url.Values{"limit": {"500"}, "archived": {"true"}})
}

// RunFlowWith roda o fluxo agora com os valores das variáveis.
func (c *Client) RunFlowWith(ctx context.Context, company, ref string, vars map[string]string) (api.FlowRun, error) {
	body := map[string]any{"variables": vars}
	return get[api.FlowRun](ctx, c, http.MethodPost, flowPath(company, ref)+"/run", body)
}

// ArchiveFlow arquiva (ou desarquiva) um fluxo.
func (c *Client) ArchiveFlow(ctx context.Context, company, ref string, archive bool) (api.Flow, error) {
	verb := "/archive"
	if !archive {
		verb = "/unarchive"
	}
	return get[api.Flow](ctx, c, http.MethodPost, flowPath(company, ref)+verb, nil)
}

// FlowTriggers lista os gatilhos de um fluxo.
func (c *Client) FlowTriggers(ctx context.Context, company, ref string) ([]api.FlowTrigger, error) {
	return list[api.FlowTrigger](ctx, c, flowPath(company, ref)+"/triggers", nil)
}

// CreateFlowTrigger cria um gatilho.
func (c *Client) CreateFlowTrigger(ctx context.Context, company, ref string, in CreateFlowTriggerReq) (api.FlowTrigger, error) {
	return get[api.FlowTrigger](ctx, c, http.MethodPost, flowPath(company, ref)+"/triggers", in)
}

// UpdateFlowTrigger muda um gatilho.
func (c *Client) UpdateFlowTrigger(ctx context.Context, company, ref, trigger string, in UpdateFlowTriggerReq) (api.FlowTrigger, error) {
	return get[api.FlowTrigger](ctx, c, http.MethodPatch, flowPath(company, ref)+"/triggers/"+url.PathEscape(trigger), in)
}

// DeleteFlowTrigger remove um gatilho.
func (c *Client) DeleteFlowTrigger(ctx context.Context, company, ref, trigger string) error {
	return c.do(ctx, http.MethodDelete, flowPath(company, ref)+"/triggers/"+url.PathEscape(trigger), nil, nil, nil)
}

// FlowRevisions lista as revisões da definição de um fluxo.
func (c *Client) FlowRevisions(ctx context.Context, company, ref string) ([]api.FlowRevision, error) {
	return list[api.FlowRevision](ctx, c, flowPath(company, ref)+"/revisions", url.Values{"limit": {"100"}})
}

// RestoreFlowRevision restaura uma revisão como edição nova.
func (c *Client) RestoreFlowRevision(ctx context.Context, company, ref string, revision int) (api.Flow, error) {
	return get[api.Flow](ctx, c, http.MethodPost, flowPath(company, ref)+"/revisions/"+strconv.Itoa(revision)+"/restore", nil)
}
