package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ---------- pacotes de time hanko.team.v1 (ADR-0044) ----------

// TeamInstallReq escolhe o pacote (do catálogo ou enviado) e como tratar os conflitos.
type TeamInstallReq struct {
	Catalog       string            `json:"catalog,omitempty"`
	Package       map[string]any    `json:"package,omitempty"`
	OnConflict    string            `json:"on_conflict,omitempty"`
	Resolve       map[string]string `json:"resolve,omitempty"`
	Subscriptions map[string]string `json:"subscriptions,omitempty"`
}

func (c *Client) TeamCatalog(ctx context.Context) ([]api.TeamSummary, error) {
	return list[api.TeamSummary](ctx, c, "/team-catalog", url.Values{"limit": {"500"}})
}

// TeamCatalogPackage devolve um pacote do catálogo inteiro, como objeto livre.
func (c *Client) TeamCatalogPackage(ctx context.Context, slug string) (map[string]any, error) {
	return get[map[string]any](ctx, c, http.MethodGet, "/team-catalog/"+url.PathEscape(slug), nil)
}

// ExportTeam exporta as personas escolhidas (vazio = todas) como pacote hanko.team.v1.
func (c *Client) ExportTeam(ctx context.Context, company string, personas []string, name, slug, description string) (map[string]any, error) {
	q := url.Values{}
	if len(personas) > 0 {
		v := ""
		for i, p := range personas {
			if i > 0 {
				v += ","
			}
			v += p
		}
		q.Set("personas", v)
	}
	for k, v := range map[string]string{"name": name, "slug": slug, "description": description} {
		if v != "" {
			q.Set(k, v)
		}
	}
	var out map[string]any
	err := c.do(ctx, http.MethodGet, cp(company)+"/team-export", q, nil, &out)
	return out, err
}

func (c *Client) PreviewTeam(ctx context.Context, company string, in TeamInstallReq) (api.TeamInstalled, error) {
	return get[api.TeamInstalled](ctx, c, http.MethodPost, cp(company)+"/teams/preview", in)
}

func (c *Client) InstallTeam(ctx context.Context, company string, in TeamInstallReq) (api.TeamInstalled, error) {
	return get[api.TeamInstalled](ctx, c, http.MethodPost, cp(company)+"/teams/install", in)
}

func (c *Client) TeamInstalls(ctx context.Context, company string) ([]api.TeamInstallRecord, error) {
	return list[api.TeamInstallRecord](ctx, c, cp(company)+"/team-installs", url.Values{"limit": {"200"}})
}
