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

// defaultTimeout é o prazo total das chamadas curtas (var para os testes o encurtarem).
var defaultTimeout = 30 * time.Second

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

// longHTTP é o cliente das transferências longas (export/import, streams): uma cópia sem Timeout total, que
// cortaria arquivos grandes em 30 s. O cancelamento fica com o ctx da chamada (o do cobra).
func (c *Client) longHTTP() *http.Client {
	hc := *c.http()
	hc.Timeout = 0
	return &hc
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
	return c.doWith(ctx, c.http(), method, path, q, in, out)
}

func (c *Client) doWith(ctx context.Context, hc *http.Client, method, path string, q url.Values, in, out any) error {
	req, err := c.newRequest(ctx, method, path, q, in)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
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

func list[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	var out api.ListBody[T]
	if err := c.do(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func get[T any](ctx context.Context, c *Client, method, path string, in any) (T, error) {
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
	Branch  string `json:"default_branch,omitempty"`
}

type UpdateWorkspaceReq struct {
	Name    *string `json:"name,omitempty"`
	Path    *string `json:"path,omitempty"`
	RepoURL *string `json:"repo_url,omitempty"`
	Branch  *string `json:"default_branch,omitempty"`
}

type CreateProjectReq struct {
	Name               string `json:"name"`
	Slug               string `json:"slug,omitempty"`
	Description        string `json:"description,omitempty"`
	Workspace          string `json:"workspace,omitempty"`
	BudgetMonthlyCents *int64 `json:"budget_monthly_cents,omitempty"`
}

type UpdateProjectReq struct {
	Name               *string `json:"name,omitempty"`
	Description        *string `json:"description,omitempty"`
	Workspace          string  `json:"workspace,omitempty"`
	ClearWorkspace     bool    `json:"clear_workspace,omitempty"`
	Status             *string `json:"status,omitempty"`
	BudgetMonthlyCents *int64  `json:"budget_monthly_cents,omitempty"`
	ClearBudget        bool    `json:"clear_budget,omitempty"`
}

type HirePersonaReq struct {
	Name                 string         `json:"name"`
	Slug                 string         `json:"slug,omitempty"`
	Title                string         `json:"title,omitempty"`
	Icon                 string         `json:"icon,omitempty"`
	Instructions         string         `json:"instructions,omitempty"`
	Agent                string         `json:"agent"`
	Model                string         `json:"model,omitempty"`
	AdapterConfig        map[string]any `json:"adapter_config,omitempty"`
	ReportsTo            string         `json:"reports_to,omitempty"`
	Workspace            string         `json:"workspace,omitempty"`
	HeartbeatIntervalSec int            `json:"heartbeat_interval_sec,omitempty"`
	BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty"`
	RequireReview        bool           `json:"require_review,omitempty"`
	Reviewer             string         `json:"reviewer,omitempty"`
	ReviewerAgent        string         `json:"reviewer_agent,omitempty"`
	ReviewerModel        string         `json:"reviewer_model,omitempty"`
	Subscription         string         `json:"subscription,omitempty"`
}

type CreateTaskReq struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Priority    string `json:"priority,omitempty"`
	Persona     string `json:"persona,omitempty"`
	Project     string `json:"project,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Goal        string `json:"goal,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Model       string `json:"model,omitempty"`
	EventID     string `json:"event_id,omitempty"`
}

type UpdateTaskReq struct {
	Title        *string `json:"title,omitempty"`
	Description  *string `json:"description,omitempty"`
	Status       *string `json:"status,omitempty"`
	Priority     *string `json:"priority,omitempty"`
	Persona      string  `json:"persona,omitempty"`
	Unassign     bool    `json:"unassign,omitempty"`
	Project      string  `json:"project,omitempty"`
	ClearProject bool    `json:"clear_project,omitempty"`
	Goal         string  `json:"goal,omitempty"`
	ClearGoal    bool    `json:"clear_goal,omitempty"`
	Model        *string `json:"model,omitempty"`
}

type AddCommentReq struct {
	Body    string      `json:"body"`
	RunID   string      `json:"run_id,omitempty"`
	EventID string      `json:"event_id,omitempty"`
	Anchor  *api.Anchor `json:"anchor,omitempty"`
}

// TaskQuery são os filtros de ListTasks.
type TaskQuery struct {
	Status, Persona, Project, Q, Goal string
	Limit, Offset                     int
}

func (t TaskQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"status": t.Status, "persona": t.Persona, "project": t.Project, "q": t.Q, "goal": t.Goal} {
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
	Task, Persona, Status, Name string
	Limit, Offset               int
}

func (r RunQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"task": r.Task, "persona": r.Persona, "status": r.Status, "name": r.Name} {
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

// UpdateCompanyReq muda o orçamento da empresa.
type UpdateCompanyReq struct {
	BudgetMonthlyCents *int64 `json:"budget_monthly_cents,omitempty"`
	ClearBudget        bool   `json:"clear_budget,omitempty"`
}

// UpdatePersonaReq muda o orçamento, o organograma e a revisão obrigatória de uma persona.
type UpdatePersonaReq struct {
	ReportsTo          string  `json:"reports_to,omitempty"`
	ClearReportsTo     bool    `json:"clear_reports_to,omitempty"`
	RequireReview      *bool   `json:"require_review,omitempty"`
	BudgetMonthlyCents *int64  `json:"budget_monthly_cents,omitempty"`
	ClearBudget        bool    `json:"clear_budget,omitempty"`
	Reviewer           string  `json:"reviewer,omitempty"`
	ReviewerAgent      *string `json:"reviewer_agent,omitempty"`
	ReviewerModel      *string `json:"reviewer_model,omitempty"`
	ClearReviewer      bool    `json:"clear_reviewer,omitempty"`
	Subscription       string  `json:"subscription,omitempty"`
	ClearSubscription  bool    `json:"clear_subscription,omitempty"`
}

// CostQuery são os parâmetros de Costs.
type CostQuery struct {
	From, To, GroupBy, Project string
}

func (c CostQuery) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"from": c.From, "to": c.To, "group_by": c.GroupBy, "project": c.Project} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return q
}

type CreateFlowReq struct {
	Name            string `json:"name"`
	Slug            string `json:"slug,omitempty"`
	Description     string `json:"description,omitempty"`
	Cron            string `json:"cron"`
	Timezone        string `json:"timezone,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
	Persona         string `json:"persona"`
	Project         string `json:"project,omitempty"`
	Priority        string `json:"priority,omitempty"`
	TaskTitle       string `json:"task_title,omitempty"`
	TaskDescription string `json:"task_description,omitempty"`
}

type UpdateFlowReq struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Cron    *string `json:"cron,omitempty"`
}

// ---------- operações ----------

func (c *Client) CreateCompany(ctx context.Context, in CreateCompanyReq) (api.Company, error) {
	return get[api.Company](ctx, c, http.MethodPost, "/companies", in)
}

func (c *Client) Companies(ctx context.Context) ([]api.Company, error) {
	return list[api.Company](ctx, c, "/companies", url.Values{"limit": {"500"}})
}

func (c *Client) CreateWorkspace(ctx context.Context, company string, in CreateWorkspaceReq) (api.Workspace, error) {
	return get[api.Workspace](ctx, c, http.MethodPost, cp(company)+"/workspaces", in)
}

func (c *Client) Workspaces(ctx context.Context, company string) ([]api.Workspace, error) {
	return list[api.Workspace](ctx, c, cp(company)+"/workspaces", url.Values{"limit": {"500"}})
}

func (c *Client) Workspace(ctx context.Context, company, ref string) (api.Workspace, error) {
	return get[api.Workspace](ctx, c, http.MethodGet, cp(company)+"/workspaces/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateWorkspace(ctx context.Context, company, ref string, in UpdateWorkspaceReq) (api.Workspace, error) {
	return get[api.Workspace](ctx, c, http.MethodPatch, cp(company)+"/workspaces/"+url.PathEscape(ref), in)
}

// DeleteWorkspace exclui o workspace; com unlink, desvincula antes quem o usa.
func (c *Client) DeleteWorkspace(ctx context.Context, company, ref string, unlink bool) error {
	q := url.Values{}
	if unlink {
		q.Set("unlink", "true")
	}
	return c.do(ctx, http.MethodDelete, cp(company)+"/workspaces/"+url.PathEscape(ref), q, nil, nil)
}

func (c *Client) Project(ctx context.Context, company, ref string) (api.Project, error) {
	return get[api.Project](ctx, c, http.MethodGet, cp(company)+"/projects/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateProject(ctx context.Context, company, ref string, in UpdateProjectReq) (api.Project, error) {
	return get[api.Project](ctx, c, http.MethodPatch, cp(company)+"/projects/"+url.PathEscape(ref), in)
}

func (c *Client) CreateProject(ctx context.Context, company string, in CreateProjectReq) (api.Project, error) {
	return get[api.Project](ctx, c, http.MethodPost, cp(company)+"/projects", in)
}

func (c *Client) Projects(ctx context.Context, company string) ([]api.Project, error) {
	return list[api.Project](ctx, c, cp(company)+"/projects", url.Values{"limit": {"500"}})
}

func (c *Client) HirePersona(ctx context.Context, company string, in HirePersonaReq) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodPost, cp(company)+"/personas", in)
}

func (c *Client) Personas(ctx context.Context, company string) ([]api.Persona, error) {
	return list[api.Persona](ctx, c, cp(company)+"/personas", url.Values{"limit": {"500"}})
}

func (c *Client) Persona(ctx context.Context, company, ref string) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodGet, cp(company)+"/personas/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdatePersona(ctx context.Context, company, ref string, in UpdatePersonaReq) (api.Persona, error) {
	return get[api.Persona](ctx, c, http.MethodPatch, cp(company)+"/personas/"+url.PathEscape(ref), in)
}

func (c *Client) UpdateCompany(ctx context.Context, company string, in UpdateCompanyReq) (api.Company, error) {
	return get[api.Company](ctx, c, http.MethodPatch, cp(company), in)
}

func (c *Client) Costs(ctx context.Context, company string, q CostQuery) (api.CostSummary, error) {
	var out api.CostSummary
	err := c.do(ctx, http.MethodGet, cp(company)+"/costs", q.values(), nil, &out)
	return out, err
}

func (c *Client) CreateFlow(ctx context.Context, company string, in CreateFlowReq) (api.Flow, error) {
	return get[api.Flow](ctx, c, http.MethodPost, cp(company)+"/flows", in)
}

func (c *Client) Flows(ctx context.Context, company string) ([]api.Flow, error) {
	return list[api.Flow](ctx, c, cp(company)+"/flows", url.Values{"limit": {"500"}})
}

func (c *Client) Flow(ctx context.Context, company, ref string) (api.Flow, error) {
	return get[api.Flow](ctx, c, http.MethodGet, cp(company)+"/flows/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateFlow(ctx context.Context, company, ref string, in UpdateFlowReq) (api.Flow, error) {
	return get[api.Flow](ctx, c, http.MethodPatch, cp(company)+"/flows/"+url.PathEscape(ref), in)
}

func (c *Client) RunFlow(ctx context.Context, company, ref string) (api.FlowRun, error) {
	return get[api.FlowRun](ctx, c, http.MethodPost, cp(company)+"/flows/"+url.PathEscape(ref)+"/run", nil)
}

func (c *Client) FlowFires(ctx context.Context, company, ref string) ([]api.FlowFire, error) {
	return list[api.FlowFire](ctx, c, cp(company)+"/flows/"+url.PathEscape(ref)+"/fires", url.Values{"limit": {"30"}})
}

func (c *Client) CreateTask(ctx context.Context, company string, in CreateTaskReq) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodPost, cp(company)+"/tasks", in)
}

func (c *Client) Tasks(ctx context.Context, company string, q TaskQuery) ([]api.Task, error) {
	return list[api.Task](ctx, c, cp(company)+"/tasks", q.values())
}

func (c *Client) Task(ctx context.Context, company, ref string) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodGet, cp(company)+"/tasks/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateTask(ctx context.Context, company, ref string, in UpdateTaskReq) (api.Task, error) {
	return get[api.Task](ctx, c, http.MethodPatch, cp(company)+"/tasks/"+url.PathEscape(ref), in)
}

func (c *Client) AddComment(ctx context.Context, company, ref string, in AddCommentReq) (api.Comment, error) {
	return get[api.Comment](ctx, c, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/comments", in)
}

func (c *Client) Comments(ctx context.Context, company, ref string) ([]api.Comment, error) {
	return list[api.Comment](ctx, c, cp(company)+"/tasks/"+url.PathEscape(ref)+"/comments", url.Values{"limit": {"500"}})
}

func (c *Client) WakeTask(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodPost, cp(company)+"/tasks/"+url.PathEscape(ref)+"/wake", nil, nil, nil)
}

func (c *Client) Runs(ctx context.Context, company string, q RunQuery) ([]api.Run, error) {
	return list[api.Run](ctx, c, cp(company)+"/runs", q.values())
}

func (c *Client) Run(ctx context.Context, company, id string) (api.Run, error) {
	return get[api.Run](ctx, c, http.MethodGet, cp(company)+"/runs/"+url.PathEscape(id), nil)
}

func (c *Client) CancelRun(ctx context.Context, company, id string) (api.Run, error) {
	return get[api.Run](ctx, c, http.MethodPost, cp(company)+"/runs/"+url.PathEscape(id)+"/cancel", nil)
}

// RunEvents devolve eventos com seq > after, em páginas de até 1000; chame de novo
// com o último seq para continuar.
func (c *Client) RunEvents(ctx context.Context, company, id string, after int64, limit int) ([]api.RunEvent, error) {
	q := url.Values{"after": {strconv.FormatInt(after, 10)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return list[api.RunEvent](ctx, c, cp(company)+"/runs/"+url.PathEscape(id)+"/events", q)
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
	resp, err := c.longHTTP().Do(req) // stream longo: sem Timeout total
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

// ---------- assinaturas ----------

type CreateSubscriptionReq struct {
	Name           string   `json:"name"`
	Agent          string   `json:"agent"`
	MonthlyUSD     float64  `json:"monthly_usd"`
	ModelPrefixes  []string `json:"model_prefixes,omitempty"`
	WeeklyLimitUSD *float64 `json:"weekly_limit_usd,omitempty"`
	WeeklyGuardPct int      `json:"weekly_guard_pct,omitempty"`
	Inactive       bool     `json:"inactive,omitempty"`
}

type UpdateSubscriptionReq struct {
	Name             *string   `json:"name,omitempty"`
	Agent            *string   `json:"agent,omitempty"`
	MonthlyUSD       *float64  `json:"monthly_usd,omitempty"`
	ModelPrefixes    *[]string `json:"model_prefixes,omitempty"`
	WeeklyLimitUSD   *float64  `json:"weekly_limit_usd,omitempty"`
	ClearWeeklyLimit bool      `json:"clear_weekly_limit,omitempty"`
	WeeklyGuardPct   *int      `json:"weekly_guard_pct,omitempty"`
	Active           *bool     `json:"active,omitempty"`
}

func (c *Client) CreateSubscription(ctx context.Context, company string, in CreateSubscriptionReq) (api.Subscription, error) {
	return get[api.Subscription](ctx, c, http.MethodPost, cp(company)+"/subscriptions", in)
}

func (c *Client) Subscriptions(ctx context.Context, company string) ([]api.Subscription, error) {
	return list[api.Subscription](ctx, c, cp(company)+"/subscriptions", url.Values{"limit": {"500"}})
}

func (c *Client) Subscription(ctx context.Context, company, ref string) (api.Subscription, error) {
	return get[api.Subscription](ctx, c, http.MethodGet, cp(company)+"/subscriptions/"+url.PathEscape(ref), nil)
}

func (c *Client) UpdateSubscription(ctx context.Context, company, ref string, in UpdateSubscriptionReq) (api.Subscription, error) {
	return get[api.Subscription](ctx, c, http.MethodPatch, cp(company)+"/subscriptions/"+url.PathEscape(ref), in)
}

func (c *Client) DeleteSubscription(ctx context.Context, company, ref string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/subscriptions/"+url.PathEscape(ref), nil, nil, nil)
}

// ---------- membros e convites ----------

func (c *Client) Members(ctx context.Context, company string) ([]api.Member, error) {
	return list[api.Member](ctx, c, cp(company)+"/members", url.Values{"limit": {"500"}})
}

func (c *Client) Invites(ctx context.Context, company string) ([]api.Invite, error) {
	return list[api.Invite](ctx, c, cp(company)+"/invites", url.Values{"limit": {"500"}})
}

// InviteMember cria o convite; a resposta traz o token e o caminho do link uma única vez.
func (c *Client) InviteMember(ctx context.Context, company, email, role string) (api.Invite, error) {
	return get[api.Invite](ctx, c, http.MethodPost, cp(company)+"/invites", map[string]string{"email": email, "role": role})
}

func (c *Client) RevokeInvite(ctx context.Context, company, inviteID string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/invites/"+url.PathEscape(inviteID), nil, nil, nil)
}

func (c *Client) SetMemberRole(ctx context.Context, company, member, role string) (api.Member, error) {
	return get[api.Member](ctx, c, http.MethodPatch, cp(company)+"/members/"+url.PathEscape(member), map[string]string{"role": role})
}

func (c *Client) RemoveMember(ctx context.Context, company, member string) error {
	return c.do(ctx, http.MethodDelete, cp(company)+"/members/"+url.PathEscape(member), nil, nil, nil)
}
