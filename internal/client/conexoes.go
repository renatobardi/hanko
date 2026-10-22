package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- conexões OAuth (ADR-0043) ----------

// CreateConnectionReq cria o rascunho de uma conexão.
type CreateConnectionReq struct {
	Name            string   `json:"name"`
	Slug            string   `json:"slug,omitempty"`
	URL             string   `json:"url"`
	Identity        string   `json:"identity,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	ClientID        string   `json:"client_id,omitempty"`
	ClientSecretRef string   `json:"client_secret_ref,omitempty"`
}

func connPath(company, ref string) string {
	return cp(company) + "/connections/" + url.PathEscape(ref)
}

func (c *Client) Connections(ctx context.Context, company string) ([]api.Connection, error) {
	return list[api.Connection](ctx, c, cp(company)+"/connections", url.Values{"limit": {"500"}})
}

func (c *Client) Connection(ctx context.Context, company, ref string) (api.Connection, error) {
	return get[api.Connection](ctx, c, http.MethodGet, connPath(company, ref), nil)
}

func (c *Client) CreateConnection(ctx context.Context, company string, in CreateConnectionReq) (api.Connection, error) {
	return get[api.Connection](ctx, c, http.MethodPost, cp(company)+"/connections", in)
}

func (c *Client) AuthorizeConnection(ctx context.Context, company, ref string) (api.ConnectionAuthStart, error) {
	return get[api.ConnectionAuthStart](ctx, c, http.MethodPost, connPath(company, ref)+"/authorize", nil)
}

func (c *Client) ProbeConnection(ctx context.Context, company, ref string) (api.Connection, error) {
	return get[api.Connection](ctx, c, http.MethodPost, connPath(company, ref)+"/probe", nil)
}

func (c *Client) RefreshConnection(ctx context.Context, company, ref string) (api.Connection, error) {
	return get[api.Connection](ctx, c, http.MethodPost, connPath(company, ref)+"/refresh", nil)
}

func (c *Client) DisconnectConnection(ctx context.Context, company, ref string) (api.Connection, error) {
	return get[api.Connection](ctx, c, http.MethodPost, connPath(company, ref)+"/disconnect", nil)
}

func (c *Client) DeleteConnection(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, connPath(company, ref), nil, nil, nil)
}
