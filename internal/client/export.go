package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ExportOpts escolhe o que mais entra no arquivo exportado.
type ExportOpts struct {
	Runs, Logs, Artifacts bool
}

// ExportCompany devolve o arquivo hanko.company.v1 exatamente como o servidor o gerou.
func (c *Client) ExportCompany(ctx context.Context, company string, o ExportOpts) (json.RawMessage, error) {
	q := url.Values{}
	if o.Runs {
		q.Set("with_runs", "true")
	}
	if o.Logs {
		q.Set("with_logs", "true")
	}
	if o.Artifacts {
		q.Set("with_artifacts", "true")
	}
	var out json.RawMessage
	if err := c.doWith(ctx, c.longHTTP(), http.MethodGet, cp(company)+"/export", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ImportCompany cria uma empresa nova a partir do conteúdo de um arquivo exportado. Por padrão ela entra
// pausada (personas pausadas, fluxos desligados); active=true liga o que o arquivo traz ligado.
func (c *Client) ImportCompany(ctx context.Context, doc json.RawMessage, slug string, active bool) (api.ImportedCompany, error) {
	q := url.Values{}
	if slug != "" {
		q.Set("slug", slug)
	}
	if active {
		q.Set("paused", "false")
	}
	var out api.ImportedCompany
	err := c.doWith(ctx, c.longHTTP(), http.MethodPost, "/companies/import", q, doc, &out)
	return out, err
}
