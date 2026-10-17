package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// Ciclo de vida da empresa, import com prévia e acesso (ADR-0057).

func (c *Client) ArchiveCompany(ctx context.Context, company string) (api.Company, error) {
	return get[api.Company](ctx, c, http.MethodPost, cp(company)+"/archive", nil)
}

func (c *Client) RestoreCompany(ctx context.Context, company string, resume bool) (api.Company, error) {
	return get[api.Company](ctx, c, http.MethodPost, cp(company)+"/restore", map[string]any{"resume": resume})
}

func (c *Client) DeleteCompany(ctx context.Context, company, confirm string) (api.CompanyDeletion, error) {
	return get[api.CompanyDeletion](ctx, c, http.MethodDelete, cp(company), map[string]string{"confirm": confirm})
}

func (c *Client) CompanyDeletions(ctx context.Context) ([]api.CompanyDeletion, error) {
	return list[api.CompanyDeletion](ctx, c, "/company-deletions", url.Values{"limit": {"200"}})
}

// DownloadCompanyDeletion baixa o export guardado na exclusão (hanko.company.v1).
func (c *Client) DownloadCompanyDeletion(ctx context.Context, id string) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.doWith(ctx, c.longHTTP(), http.MethodGet, "/company-deletions/"+url.PathEscape(id)+"/export", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ImportOpts são as opções de ImportCompanyWith.
type ImportOpts struct {
	Slug       string
	Active     bool
	DryRun     bool
	Into       string   // empresa existente (merge); vazio cria uma nova
	OnConflict string   // skip | rename | replace
	Resolve    []string // "tipo/chave=ação"
}

// ImportCompanyWith importa (ou, com DryRun, só mostra a prévia) um arquivo exportado.
func (c *Client) ImportCompanyWith(ctx context.Context, doc json.RawMessage, o ImportOpts) (api.ImportedCompany, error) {
	q := url.Values{}
	if o.Slug != "" {
		q.Set("slug", o.Slug)
	}
	if o.Active {
		q.Set("paused", "false")
	}
	if o.DryRun {
		q.Set("dry_run", "true")
	}
	if o.Into != "" {
		q.Set("into", o.Into)
	}
	if o.OnConflict != "" {
		q.Set("on_conflict", o.OnConflict)
	}
	for _, r := range o.Resolve {
		q.Add("resolve", r)
	}
	var out api.ImportedCompany
	err := c.doWith(ctx, c.longHTTP(), http.MethodPost, "/companies/import", q, doc, &out)
	return out, err
}

// ---------- acesso ----------

func (c *Client) StartCLIAuth(ctx context.Context, device, scope string) (api.CLIAuthStarted, error) {
	return get[api.CLIAuthStarted](ctx, c, http.MethodPost, "/cli-auth", map[string]string{"device": device, "scope": scope})
}

func (c *Client) PollCLIAuth(ctx context.Context, pollToken string) (api.CLIAuthPolled, error) {
	return get[api.CLIAuthPolled](ctx, c, http.MethodPost, "/cli-auth/poll", map[string]string{"poll_token": pollToken})
}

func (c *Client) Whoami(ctx context.Context) (api.Whoami, error) {
	return get[api.Whoami](ctx, c, http.MethodGet, "/whoami", nil)
}

func (c *Client) Users(ctx context.Context) ([]api.InstanceUser, error) {
	return list[api.InstanceUser](ctx, c, "/users", url.Values{"limit": {"500"}})
}

func (c *Client) JoinLink(ctx context.Context, company string) (api.JoinLink, error) {
	return get[api.JoinLink](ctx, c, http.MethodGet, cp(company)+"/join-link", nil)
}

func (c *Client) CreateJoinLink(ctx context.Context, company string) (api.JoinLink, error) {
	return get[api.JoinLink](ctx, c, http.MethodPost, cp(company)+"/join-link", nil)
}

func (c *Client) RevokeJoinLink(ctx context.Context, company string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/join-link", nil, nil, nil)
}

func (c *Client) JoinRequests(ctx context.Context, company string, pending bool) ([]api.JoinRequest, error) {
	q := url.Values{"limit": {"200"}}
	if pending {
		q.Set("pending", "true")
	}
	return list[api.JoinRequest](ctx, c, cp(company)+"/join-requests", q)
}

func (c *Client) DecideJoinRequest(ctx context.Context, company, id string, approve bool, role string) (api.JoinRequest, error) {
	return get[api.JoinRequest](ctx, c, http.MethodPost, cp(company)+"/join-requests/"+url.PathEscape(id)+"/decision",
		map[string]any{"approve": approve, "role": role})
}
