package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/renatobardi/hanko/internal/api"
)

// Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034). Os corpos de escrita vão como
// mapas: a CLI monta só os campos informados.

// BulkApproveResult é a resposta da aprovação em lote.
type BulkApproveResult struct {
	Results []api.BulkResult `json:"results"`
	Failed  int              `json:"failed"`
}

func pp(company, ref string) string { return cp(company) + "/pipelines/" + url.PathEscape(ref) }
func cc(company, ref string) string { return cp(company) + "/cases/" + url.PathEscape(ref) }

func (c *Client) Pipelines(ctx context.Context, company string, archived bool) ([]api.Pipeline, error) {
	q := url.Values{"limit": {"500"}}
	if archived {
		q.Set("archived", "true")
	}
	return list[api.Pipeline](ctx, c, cp(company)+"/pipelines", q)
}

func (c *Client) Pipeline(ctx context.Context, company, ref string) (api.Pipeline, error) {
	return get[api.Pipeline](ctx, c, http.MethodGet, pp(company, ref), nil)
}

func (c *Client) CreatePipeline(ctx context.Context, company string, body any) (api.Pipeline, error) {
	return get[api.Pipeline](ctx, c, http.MethodPost, cp(company)+"/pipelines", body)
}

func (c *Client) UpdatePipeline(ctx context.Context, company, ref string, body any) (api.Pipeline, error) {
	return get[api.Pipeline](ctx, c, http.MethodPatch, pp(company, ref), body)
}

func (c *Client) AddStage(ctx context.Context, company, ref string, body any) (api.Pipeline, error) {
	return get[api.Pipeline](ctx, c, http.MethodPost, pp(company, ref)+"/stages", body)
}

func (c *Client) DeleteStage(ctx context.Context, company, ref, stage string) (api.Pipeline, error) {
	return get[api.Pipeline](ctx, c, http.MethodDelete, pp(company, ref)+"/stages/"+url.PathEscape(stage), nil)
}

func (c *Client) ReorderStages(ctx context.Context, company, ref string, keys []string) (api.Pipeline, error) {
	return get[api.Pipeline](ctx, c, http.MethodPut, pp(company, ref)+"/stages/order", map[string]any{"stages": keys})
}

func (c *Client) SetTransitions(ctx context.Context, company, ref string, ts []api.PipelineTransition, enforce bool) (api.Pipeline, error) {
	if ts == nil {
		ts = []api.PipelineTransition{}
	}
	return get[api.Pipeline](ctx, c, http.MethodPut, pp(company, ref)+"/transitions", map[string]any{"transitions": ts, "enforce": enforce})
}

func (c *Client) Cases(ctx context.Context, company string, q url.Values) ([]api.Case, error) {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("limit") == "" {
		q.Set("limit", "500")
	}
	return list[api.Case](ctx, c, cp(company)+"/cases", q)
}

func (c *Client) Case(ctx context.Context, company, ref string) (api.Case, error) {
	return get[api.Case](ctx, c, http.MethodGet, cc(company, ref), nil)
}

func (c *Client) CreateCase(ctx context.Context, company string, body any) (api.Case, error) {
	return get[api.Case](ctx, c, http.MethodPost, cp(company)+"/cases", body)
}

func (c *Client) UpdateCase(ctx context.Context, company, ref string, body any) (api.Case, error) {
	return get[api.Case](ctx, c, http.MethodPatch, cc(company, ref), body)
}

func (c *Client) TransitionCase(ctx context.Context, company, ref string, body any) (api.Case, error) {
	return get[api.Case](ctx, c, http.MethodPost, cc(company, ref)+"/transition", body)
}

func (c *Client) ReviewCase(ctx context.Context, company, ref string, body any) (api.Case, error) {
	return get[api.Case](ctx, c, http.MethodPost, cc(company, ref)+"/review", body)
}

func (c *Client) ReviewQueue(ctx context.Context, company string) ([]api.ReviewQueueItem, error) {
	return list[api.ReviewQueueItem](ctx, c, cp(company)+"/review-queue", url.Values{"limit": {"500"}})
}

func (c *Client) BulkApprove(ctx context.Context, company string, items []map[string]any) (BulkApproveResult, error) {
	var out BulkApproveResult
	err := c.do(ctx, http.MethodPost, cp(company)+"/review-queue/bulk", nil, map[string]any{"items": items}, &out)
	return out, err
}

func (c *Client) Learnings(ctx context.Context, company string, q url.Values) ([]api.Learning, error) {
	return list[api.Learning](ctx, c, cp(company)+"/learnings", q)
}

func (c *Client) DecisionExamples(ctx context.Context, company string) ([]api.DecisionExample, error) {
	return list[api.DecisionExample](ctx, c, cp(company)+"/decision-examples", url.Values{"limit": {"500"}})
}

// ExportDecisionExamples baixa o JSONL dos exemplos (até limit bytes).
func (c *Client) ExportDecisionExamples(ctx context.Context, company string, limit int64) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, cp(company)+"/decision-examples/export.jsonl", nil, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/x-ndjson")
	resp, err := c.longHTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("falha ao falar com %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, parseError(resp)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ler o JSONL: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("a exportação passa de %d bytes", limit)
	}
	return b, nil
}
