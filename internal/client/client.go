// Package client é o cliente HTTP tipado da API REST do Hanko, usado pela CLI.
// Os tipos de resposta vêm de internal/api, então cliente e servidor não divergem.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/api"
)

// DefaultURL é o endereço padrão do servidor local.
const DefaultURL = "http://127.0.0.1:7420"

// Client fala com um servidor Hanko.
type Client struct {
	BaseURL string       // ex.: http://127.0.0.1:7420
	Token   string       // run token opcional (Authorization: Bearer)
	HTTP    *http.Client // nil = cliente padrão com timeout
}

// New cria um cliente. baseURL vazio usa DefaultURL.
func New(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Error é uma resposta de erro da API (RFC 9457).
type Error struct {
	Status int
	Title  string
	Detail string
	Fields []string // "campo: motivo"
}

func (e *Error) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = e.Title
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if len(e.Fields) > 0 && !strings.Contains(msg, e.Fields[0]) {
		msg += " (" + strings.Join(e.Fields, "; ") + ")"
	}
	return fmt.Sprintf("%s [HTTP %d]", msg, e.Status)
}

func parseError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var p struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}
	e := &Error{Status: resp.StatusCode}
	if json.Unmarshal(b, &p) == nil {
		e.Title, e.Detail = p.Title, p.Detail
		for _, d := range p.Errors {
			f := d.Message
			if d.Location != "" {
				f = d.Location + ": " + d.Message
			}
			e.Fields = append(e.Fields, f)
		}
	}
	if e.Title == "" && e.Detail == "" {
		e.Detail = strings.TrimSpace(string(b))
	}
	return e
}

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("codificar requisição: %w", err)
		}
		body = bytes.NewReader(b)
	}
	u := c.BaseURL + api.Prefix + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("montar requisição: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	req, err := c.newRequest(ctx, method, path, q, in)
	if err != nil {
		return err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("falha ao falar com %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return parseError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("resposta inválida do servidor: %w", err)
	}
	return nil
}

func list[T any](c *Client, ctx context.Context, path string, q url.Values) ([]T, error) {
	var out api.ListBody[T]
	if err := c.do(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func get[T any](c *Client, ctx context.Context, method, path string, in any) (T, error) {
	var out T
	err := c.do(ctx, method, path, nil, in, &out)
	return out, err
}

func cp(company string) string { return "/companies/" + url.PathEscape(company) }

// ---------- requisições ----------

type CreateCompanyReq struct {
	Name       string `json:"name"`
	Slug       string `json:"slug,omitempty"`
	TaskPrefix string `json:"task_prefix,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

type CreateWorkspaceReq struct {
	Name    string `json:"name"`
	Slug    string `json:"slug,omitempty"`
	Path    string `json:"path"`
	RepoURL string `json:"repo_url,omitempty"`
}

type CreateProjectReq struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
}

type HirePersonaReq struct {
	Name                 string         `json:"name"`
	Slug                 string         `json:"slug,omitempty"`
	Title                string         `json:"title,omitempty"`
	Instructions         string         `json:"instructions,omitempty"`
	Agent                string         `json:"agent"`
	Model                string         `json:"model,omitempty"`
	AdapterConfig        map[string]any `json:"adapter_config,omitempty"`
	ReportsTo            string         `json:"reports_to,omitempty"`
	Workspace            string         `json:"workspace,omitempty"`
	HeartbeatIntervalSec int            `json:"heartbeat_interval_sec,omitempty"`
	BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty"`
}

type CreateTaskReq struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Priority    string `json:"priority,omitempty"`
	Persona     string `json:"persona,omitempty"`
	Project     string `json:"project,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Parent      string `json:"parent,omitempty"`
	EventID     string `json:"event_id,omitempty"`
}

type UpdateTaskReq struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	Persona     string  `json:"persona,omitempty"`
	Unassign    bool    `json:"unassign,omitempty"`
	Project     string  `json:"project,omitempty"`
}

type AddCommentReq struct {
	Body    string `json:"body"`
	RunID   string `json:"run_id,omitempty"`
	EventID string `json:"event_id,omitempty"`
}

// TaskQuery são os filtros de ListTasks.
type TaskQuery struct {
	Status, Persona, Project, Q string
	Limit, Offset               int
}

func (t TaskQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"status": t.Status, "persona": t.Persona, "project": t.Project, "q": t.Q} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if t.Limit > 0 {
		q.Set("limit", strconv.Itoa(t.Limit))
	}
	if t.Offset > 0 {
		q.Set("offset", strconv.Itoa(t.Offset))
	}
	return q
}

// RunQuery são os filtros de ListRuns.
type RunQuery struct {
	Task, Persona, Status string
	Limit, Offset         int
}

func (r RunQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"task": r.Task, "persona": r.Persona, "status": r.Status} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if r.Limit > 0 {
		q.Set("limit", strconv.Itoa(r.Limit))
	}
	if r.Offset > 0 {
		q.Set("offset", strconv.Itoa(r.Offset))
	}
	return q
}

// ---------- operações ----------

func (c *Client) CreateCompany(ctx context.Context, in CreateCompanyReq) (api.Company, error) {
	return get[api.Company](c, ctx, http.MethodPost, "/companies", in)
}

func (c *Client) Companies(ctx context.Context) ([]api.Company, error) {
	return list[api.Company](c, ctx, "/companies", url.Values{"limit": {"500"}})
}

func (c *Client) CreateWorkspace(ctx context.Context, company string, in CreateWorkspaceReq) (api.Workspace, error) {
	return get[api.Workspace](c, ctx, http.MethodPost, cp(company)+"/workspaces", in)
}

func (c *Client) Workspaces(ctx context.Context, company string) ([]api.Workspace, error) {
	return list[api.Workspace](c, ctx, cp(company)+"/workspaces", url.Values{"limit": {"500"}})
}

func (c *Client) CreateProject(ctx context.Context, company string, in CreateProjectReq) (api.Project, error) {
	return get[api.Project](c, ctx, http.MethodPost, cp(company)+"/projects", in)
}

func (c *Client) Projects(ctx context.Context, company string) ([]api.Project, error) {
	return list[api.Project](c, ctx, cp(company)+"/projects", url.Values{"limit": {"500"}})
}

func (c *Client) HirePersona(ctx context.Context, company string, in HirePersonaReq) (api.Persona, error) {
	return get[api.Persona](c, ctx, http.MethodPost, cp(company)+"/personas", in)
}

func (c *Client) Personas(ctx context.Context, company string) ([]api.Persona, error) {
	return list[api.Persona](c, ctx, cp(company)+"/personas", url.Values{"limit": {"500"}})
}

func (c *Client) Persona(ctx context.Context, company, ref string) (api.Persona, error) {
	return get[api.Persona](c, ctx, http.MethodGet, cp(company)+"/personas/"+url.PathEscape(ref), nil)
}

func (c *Client) CreateTask(ctx context.Context, company string, in CreateTaskReq) (api.Task, error) {
	return get[api.Task](c, ctx, http.MethodPost, cp(company)+"/tasks", in)
}

func (c *Client) Tasks(ctx context.Context, company string, q TaskQuery) ([]api.Task, error) {
	return list[api.Task](c, ctx, cp(company)+"/tasks", q.values())
}

func (c *Client) Task(ctx context.Context, company, ref string) (api.Task, error) {
	return get[api.Task](c, ctx, http.MethodGet, cp(company)+"/tasks/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateTask(ctx context.Context, company, ref string, in UpdateTaskReq) (api.Task, error) {
	return get[api.Task](c, ctx, http.MethodPatch, cp(company)+"/tasks/"+url.PathEscape(ref), in)
}

func (c *Client) AddComment(ctx context.Context, company, ref string, in AddCommentReq) (api.Comment, error) {
	return get[api.Comment](c, ctx, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/comments", in)
}

func (c *Client) Comments(ctx context.Context, company, ref string) ([]api.Comment, error) {
	return list[api.Comment](c, ctx, cp(company)+"/tasks/"+url.PathEscape(ref)+"/comments", url.Values{"limit": {"500"}})
}

func (c *Client) WakeTask(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/wake", nil, nil, nil)
}

func (c *Client) Runs(ctx context.Context, company string, q RunQuery) ([]api.Run, error) {
	return list[api.Run](c, ctx, cp(company)+"/runs", q.values())
}

func (c *Client) Run(ctx context.Context, company, id string) (api.Run, error) {
	return get[api.Run](c, ctx, http.MethodGet, cp(company)+"/runs/"+url.PathEscape(id), nil)
}

func (c *Client) CancelRun(ctx context.Context, company, id string) (api.Run, error) {
	return get[api.Run](c, ctx, http.MethodPost, cp(company)+"/runs/"+url.PathEscape(id)+"/cancel", nil)
}

// RunEvents devolve eventos com seq > after, em páginas de até 1000; chame de novo
// com o último seq para continuar.
func (c *Client) RunEvents(ctx context.Context, company, id string, after int64, limit int) ([]api.RunEvent, error) {
	q := url.Values{"after": {strconv.FormatInt(after, 10)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.RunEvent](c, ctx, cp(company)+"/runs/"+url.PathEscape(id)+"/events", q)
}

// AllRunEvents lê o log inteiro a partir de after.
func (c *Client) AllRunEvents(ctx context.Context, company, id string, after int64) ([]api.RunEvent, error) {
	var all []api.RunEvent
	for {
		page, err := c.RunEvents(ctx, company, id, after, 1000)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 1000 {
			return all, nil
		}
		after = page[len(page)-1].Seq
	}
}

// ---------- SSE ----------

// Frame é um evento SSE recebido.
type Frame struct {
	ID    string
	Event string
	Data  string
}

// ErrStreamEnded indica que o servidor encerrou o stream sem run.status terminal.
var ErrStreamEnded = errors.New("stream encerrado antes do fim da execução")

// Stream abre o SSE de uma execução e chama fn para cada frame (run.event e
// run.status). Retorna nil quando chega um run.status; ErrStreamEnded se a conexão
// cair antes disso.
func (c *Client) Stream(ctx context.Context, company, id string, after int64, fn func(Frame) error) error {
	req, err := c.newRequest(ctx, http.MethodGet, cp(company)+"/runs/"+url.PathEscape(id)+"/stream", url.Values{"after": {strconv.FormatInt(after, 10)}}, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	hc := *c.http()
	hc.Timeout = 0 // stream longo
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("falha ao falar com %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return parseError(resp)
	}
	return readFrames(resp.Body, fn)
}

func readFrames(r io.Reader, fn func(Frame) error) error {
	br := newLineReader(r)
	var cur Frame
	var data []string
	for {
		line, err := br.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ErrStreamEnded
			}
			return err
		}
		switch {
		case line == "":
			if cur.Event != "" || len(data) > 0 {
				cur.Data = strings.Join(data, "\n")
				if cur.Event == "" {
					cur.Event = "message"
				}
				if err := fn(cur); err != nil {
					return err
				}
				if cur.Event == "run.status" {
					return nil
				}
			}
			cur, data = Frame{}, nil
		case strings.HasPrefix(line, ":"): // comentário (ping)
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "id":
				cur.ID = value
			case "event":
				cur.Event = value
			case "data":
				data = append(data, value)
			}
		}
	}
}
