package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// UpdateMemoryReq muda a configuração da consolidação de memória; campos nil ficam como estão.
type UpdateMemoryReq struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	IntervalHours *int   `json:"interval_hours,omitempty"`
	Curator       string `json:"curator,omitempty"`
	ClearCurator  bool   `json:"clear_curator,omitempty"`
}

func (c *Client) MemoryStatus(ctx context.Context, company string) (api.MemoryStatusOut, error) {
	return get[api.MemoryStatusOut](ctx, c, http.MethodGet, cp(company)+"/memory", nil)
}

func (c *Client) UpdateMemory(ctx context.Context, company string, in UpdateMemoryReq) (api.MemoryStatusOut, error) {
	return get[api.MemoryStatusOut](ctx, c, http.MethodPatch, cp(company)+"/memory", in)
}

// ConsolidateMemory dispara a consolidação agora; workspace vazio = todos.
func (c *Client) ConsolidateMemory(ctx context.Context, company, workspace string) ([]api.MemoryConsolidationOut, error) {
	var out api.ListBody[api.MemoryConsolidationOut]
	err := c.do(ctx, http.MethodPost, cp(company)+"/memory/consolidate", nil, map[string]string{"workspace": workspace}, &out)
	return out.Items, err
}

func (c *Client) MemoryConsolidations(ctx context.Context, company string, limit int) ([]api.MemoryConsolidationOut, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.MemoryConsolidationOut](ctx, c, cp(company)+"/memory/consolidations", q)
}

// MemoryProposals lista as propostas; status é uma lista separada por vírgula (vazio = todas).
func (c *Client) MemoryProposals(ctx context.Context, company, status string, limit int) ([]api.MemoryProposalOut, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.MemoryProposalOut](ctx, c, cp(company)+"/memory/proposals", q)
}

func (c *Client) MemoryProposal(ctx context.Context, company, id string) (api.MemoryProposalOut, error) {
	return get[api.MemoryProposalOut](ctx, c, http.MethodGet, cp(company)+"/memory/proposals/"+url.PathEscape(id), nil)
}

// DecideMemoryProposal aprova ("approve") ou rejeita ("reject") uma proposta; ou recoloca na fila ("retry").
func (c *Client) DecideMemoryProposal(ctx context.Context, company, id, action, note string) (api.MemoryProposalOut, error) {
	var body any
	if action != "retry" {
		body = map[string]string{"note": note}
	}
	return get[api.MemoryProposalOut](ctx, c, http.MethodPost, cp(company)+"/memory/proposals/"+url.PathEscape(id)+"/"+action, body)
}

func (c *Client) MemoryGrants(ctx context.Context, company string) ([]api.MemoryGrantOut, error) {
	return list[api.MemoryGrantOut](ctx, c, cp(company)+"/memory/grants", nil)
}

func (c *Client) GrantMemory(ctx context.Context, company, persona, workspace, access string) (api.MemoryGrantOut, error) {
	return get[api.MemoryGrantOut](ctx, c, http.MethodPut, cp(company)+"/memory/grants",
		map[string]string{"persona": persona, "workspace": workspace, "access": access})
}

func (c *Client) RevokeMemory(ctx context.Context, company, persona, workspace string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/memory/grants/"+url.PathEscape(persona)+"/"+url.PathEscape(workspace), nil, nil, nil)
}
