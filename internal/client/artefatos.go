package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/renatobardi/hanko/internal/api"
)

// Artefatos e metas (ADR-0028); documentos de artefato com versão MAJOR.MINOR (ADR-0064).

// PutArtifactReq publica uma versão de artefato; Content vai em base64 no JSON.
type PutArtifactReq struct {
	Name    string `json:"name"`
	Mime    string `json:"mime,omitempty"`
	Content []byte `json:"content"`
	Task    string `json:"task,omitempty"`
	RunID   string `json:"run_id,omitempty"`
	Project string `json:"project,omitempty"`
	// Document é o documento a alterar; Change (major|minor) e ChangeReason classificam a mudança.
	Document     string   `json:"document_id,omitempty"`
	Change       string   `json:"change,omitempty"`
	ChangeReason string   `json:"change_reason,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// ArtifactQuery são os filtros de ListArtifacts.
type ArtifactQuery struct {
	Project, Task, Persona, Run, Kind, Q string
	Workspace, Tag, Document             string
	AllVersions                          bool
	Limit, Offset                        int
}

func (a ArtifactQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"project": a.Project, "task": a.Task, "persona": a.Persona, "run": a.Run, "kind": a.Kind, "q": a.Q,
		"workspace": a.Workspace, "tag": a.Tag, "document": a.Document} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if a.AllVersions {
		q.Set("all_versions", "true")
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	if a.Offset > 0 {
		q.Set("offset", strconv.Itoa(a.Offset))
	}
	return q
}

func (c *Client) PutArtifact(ctx context.Context, company string, in PutArtifactReq) (api.Artifact, error) {
	var out api.Artifact
	err := c.doWith(ctx, c.longHTTP(), http.MethodPost, cp(company)+"/artifacts", nil, in, &out)
	return out, err
}

func (c *Client) Artifacts(ctx context.Context, company string, q ArtifactQuery) ([]api.Artifact, error) {
	return list[api.Artifact](ctx, c, cp(company)+"/artifacts", q.values())
}

func (c *Client) Artifact(ctx context.Context, company, id string) (api.Artifact, error) {
	return get[api.Artifact](ctx, c, http.MethodGet, cp(company)+"/artifacts/"+url.PathEscape(id), nil)
}

func (c *Client) ArtifactDiff(ctx context.Context, company, id, against string) (api.ArtifactDiff, error) {
	var out api.ArtifactDiff
	q := url.Values{}
	if against != "" {
		q.Set("against", against)
	}
	err := c.do(ctx, http.MethodGet, cp(company)+"/artifacts/"+url.PathEscape(id)+"/diff", q, nil, &out)
	return out, err
}

// ArtifactDocumentVersion lê uma versão do documento por MAJOR.MINOR ("" ou "latest" = a mais nova), com o histórico.
func (c *Client) ArtifactDocumentVersion(ctx context.Context, company, document, version string) (api.Artifact, error) {
	if version == "" {
		version = "latest"
	}
	return get[api.Artifact](ctx, c, http.MethodGet, cp(company)+"/artifact-documents/"+url.PathEscape(document)+"/versions/"+url.PathEscape(version), nil)
}

// RestoreArtifact publica o conteúdo da versão id como a versão mais nova do documento (major).
func (c *Client) RestoreArtifact(ctx context.Context, company, id string) (api.Artifact, error) {
	var out api.Artifact
	err := c.do(ctx, http.MethodPost, cp(company)+"/artifacts/"+url.PathEscape(id)+"/restore", nil, struct{}{}, &out)
	return out, err
}

// ReclassifyArtifact troca a classificação (major|minor) da versão mais nova do documento.
func (c *Client) ReclassifyArtifact(ctx context.Context, company, id, change string) (api.Artifact, error) {
	var out api.Artifact
	err := c.do(ctx, http.MethodPost, cp(company)+"/artifacts/"+url.PathEscape(id)+"/reclassify", nil, map[string]string{"change": change}, &out)
	return out, err
}

// ArtifactDocumentPatch muda projeto, workspace e tags de um documento; nil não muda, "" esvazia.
type ArtifactDocumentPatch struct {
	Project   *string  `json:"project,omitempty"`
	Workspace *string  `json:"workspace,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

func (c *Client) UpdateArtifactDocument(ctx context.Context, company, document string, p ArtifactDocumentPatch) (api.ArtifactDocument, error) {
	var out api.ArtifactDocument
	err := c.do(ctx, http.MethodPatch, cp(company)+"/artifact-documents/"+url.PathEscape(document), nil, p, &out)
	return out, err
}

func (c *Client) DeleteArtifact(ctx context.Context, company, id string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/artifacts/"+url.PathEscape(id), nil, nil, nil)
}

func (c *Client) ArtifactUsage(ctx context.Context, company string) (api.ArtifactUsage, error) {
	return get[api.ArtifactUsage](ctx, c, http.MethodGet, cp(company)+"/artifact-usage", nil)
}

// ArtifactContent baixa o conteúdo bruto; recusa mais de limit bytes (o servidor já limita o arquivo, isto é só um teto
// defensivo do cliente).
func (c *Client) ArtifactContent(ctx context.Context, company, id string, limit int64) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, cp(company)+"/artifacts/"+url.PathEscape(id)+"/content", nil, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
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
		return nil, fmt.Errorf("ler o conteúdo: %w", err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("o conteúdo passa de %d bytes", limit)
	}
	return b, nil
}

// ---------- metas ----------

type CreateGoalReq struct {
	Title       string   `json:"title"`
	Slug        string   `json:"slug,omitempty"`
	Description string   `json:"description,omitempty"`
	Metric      string   `json:"metric,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	Start       float64  `json:"start_value"`
	Target      float64  `json:"target_value"`
	Current     *float64 `json:"current_value,omitempty"`
	DueDate     string   `json:"due_date,omitempty"`
	Project     string   `json:"project,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Parent      string   `json:"parent,omitempty"`
}

type UpdateGoalReq struct {
	Title        *string  `json:"title,omitempty"`
	Description  *string  `json:"description,omitempty"`
	Metric       *string  `json:"metric,omitempty"`
	Unit         *string  `json:"unit,omitempty"`
	Start        *float64 `json:"start_value,omitempty"`
	Target       *float64 `json:"target_value,omitempty"`
	DueDate      string   `json:"due_date,omitempty"`
	ClearDue     bool     `json:"clear_due,omitempty"`
	Project      string   `json:"project,omitempty"`
	ClearProject bool     `json:"clear_project,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	ClearOwner   bool     `json:"clear_owner,omitempty"`
	Parent       string   `json:"parent,omitempty"`
	ClearParent  bool     `json:"clear_parent,omitempty"`
	Status       *string  `json:"status,omitempty"`
}

func (c *Client) CreateGoal(ctx context.Context, company string, in CreateGoalReq) (api.Goal, error) {
	return get[api.Goal](ctx, c, http.MethodPost, cp(company)+"/goals", in)
}

func (c *Client) Goals(ctx context.Context, company, status, project string) ([]api.Goal, error) {
	q := url.Values{"limit": {"500"}}
	if status != "" {
		q.Set("status", status)
	}
	if project != "" {
		q.Set("project", project)
	}
	return list[api.Goal](ctx, c, cp(company)+"/goals", q)
}

func (c *Client) Goal(ctx context.Context, company, ref string) (api.Goal, error) {
	return get[api.Goal](ctx, c, http.MethodGet, cp(company)+"/goals/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateGoal(ctx context.Context, company, ref string, in UpdateGoalReq) (api.Goal, error) {
	return get[api.Goal](ctx, c, http.MethodPatch, cp(company)+"/goals/"+url.PathEscape(ref), in)
}

func (c *Client) RecordGoalProgress(ctx context.Context, company, ref string, value float64, note string) (api.Goal, error) {
	body := struct {
		Value float64 `json:"value"`
		Note  string  `json:"note,omitempty"`
	}{value, note}
	return get[api.Goal](ctx, c, http.MethodPost, cp(company)+"/goals/"+url.PathEscape(ref)+"/progress", body)
}
