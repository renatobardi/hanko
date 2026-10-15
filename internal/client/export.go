package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// ExportCompany devolve o arquivo hanko.company.v1 exatamente como o servidor o gerou.
func (c *Client) ExportCompany(ctx context.Context, company string, withRuns, withLogs bool) (json.RawMessage, error) {
	q := url.Values{}
	if withRuns {
		q.Set("with_runs", "true")
	}
	if withLogs {
		q.Set("with_logs", "true")
	}
	var out json.RawMessage
	if err := c.do(ctx, http.MethodGet, cp(company)+"/export", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ImportCompany cria uma empresa nova a partir do conteúdo de um arquivo exportado.
func (c *Client) ImportCompany(ctx context.Context, doc json.RawMessage, slug string, paused bool) (api.ImportedCompany, error) {
	q := url.Values{}
	if slug != "" {
		q.Set("slug", slug)
	}
	if paused {
		q.Set("paused", "true")
	}
	var out api.ImportedCompany
	err := c.do(ctx, http.MethodPost, "/companies/import", q, doc, &out)
	return out, err
}
