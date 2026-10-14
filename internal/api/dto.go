package api

import (
	"encoding/json"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Tipos de entrada e saída da API. Códigos e campos em inglês (ADR-0016);
// documentação (tags doc) em pt-BR.

// Page são os parâmetros de paginação das listagens.
type Page struct {
	Limit  int `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"Máximo de itens retornados."`
	Offset int `query:"offset" minimum:"0" default:"0" doc:"Quantos itens pular."`
}

// ListBody envelopa listagens paginadas.
type ListBody[T any] struct {
	Items  []T `json:"items" doc:"Itens da página."`
	Limit  int `json:"limit" doc:"Limite aplicado."`
	Offset int `json:"offset" doc:"Deslocamento aplicado."`
}

// ListOut é a resposta de uma listagem.
type ListOut[T any] struct {
	Body ListBody[T]
}

// ItemOut é a resposta de um recurso único.
type ItemOut[T any] struct {
	Body T
}

// ActorRef identifica quem causou um fato.
type ActorRef struct {
	Type string `json:"type" enum:"human,persona,system" doc:"Tipo do autor."`
	ID   string `json:"id,omitempty" format:"uuid" doc:"Id da persona, quando o autor é persona."`
}

// Company é a unidade de isolamento multi-org.
type Company struct {
	ID         string    `json:"id" format:"uuid" doc:"Identificador (UUIDv7)."`
	Slug       string    `json:"slug" example:"acme" doc:"Identificador legível, único."`
	Name       string    `json:"name" example:"Acme Ltda" doc:"Nome de exibição."`
	TaskPrefix string    `json:"task_prefix" example:"HNK" doc:"Prefixo das chaves de task (HNK-12)."`
	Timezone   string    `json:"timezone" example:"America/Sao_Paulo" doc:"Fuso IANA."`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Workspace é um diretório/repositório onde personas trabalham.
type Workspace struct {
	ID        string    `json:"id" format:"uuid"`
	CompanyID string    `json:"company_id" format:"uuid"`
	Slug      string    `json:"slug" example:"api"`
	Name      string    `json:"name" example:"API"`
	Path      string    `json:"path" example:"/srv/work/api" doc:"Diretório de trabalho."`
	RepoURL   string    `json:"repo_url,omitempty" example:"https://example.com/acme/api.git"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Project agrupa tasks.
type Project struct {
	ID          string    `json:"id" format:"uuid"`
	CompanyID   string    `json:"company_id" format:"uuid"`
	Slug        string    `json:"slug" example:"lancamento"`
	Name        string    `json:"name" example:"Lançamento"`
	Description string    `json:"description,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty" format:"uuid"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Persona é um papel contratado pela empresa.
type Persona struct {
	ID                   string         `json:"id" format:"uuid"`
	CompanyID            string         `json:"company_id" format:"uuid"`
	Slug                 string         `json:"slug" example:"cto"`
	Name                 string         `json:"name" example:"Marina"`
	Title                string         `json:"title" example:"CTO"`
	Instructions         string         `json:"instructions" doc:"Instruções permanentes da persona."`
	Agent                string         `json:"agent" enum:"claude,codex,fake" doc:"Harness que executa a persona."`
	Model                string         `json:"model,omitempty" example:"claude-sonnet-4-5"`
	AdapterConfig        map[string]any `json:"adapter_config,omitempty" doc:"Configuração específica do adapter."`
	Status               string         `json:"status" enum:"active,idle,running,paused,error,awaiting_gate,off"`
	ReportsTo            string         `json:"reports_to,omitempty" format:"uuid" doc:"Persona à qual esta reporta."`
	WorkspaceID          string         `json:"workspace_id,omitempty" format:"uuid"`
	HeartbeatIntervalSec int            `json:"heartbeat_interval_sec" doc:"Intervalo do heartbeat em segundos; 0 desliga."`
	BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty" doc:"Orçamento mensal em centavos de dólar."`
	RequireReview        bool           `json:"require_review" doc:"Quando verdadeiro, concluir uma task passa por um gate de revisão (task_review)."`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

// Task é uma unidade de trabalho atribuída a uma persona.
type Task struct {
	ID          string     `json:"id" format:"uuid"`
	CompanyID   string     `json:"company_id" format:"uuid"`
	Number      int64      `json:"number" example:"12"`
	Key         string     `json:"key" example:"HNK-12" doc:"Chave legível <prefixo>-<número>."`
	Title       string     `json:"title" example:"Implementar login"`
	Description string     `json:"description"`
	Status      string     `json:"status" enum:"backlog,todo,in_progress,in_review,blocked,done,cancelled"`
	Priority    string     `json:"priority" enum:"critical,high,medium,low"`
	PersonaID   string     `json:"persona_id,omitempty" format:"uuid"`
	PersonaName string     `json:"persona_name,omitempty"`
	ProjectID   string     `json:"project_id,omitempty" format:"uuid"`
	ProjectName string     `json:"project_name,omitempty"`
	WorkspaceID string     `json:"workspace_id,omitempty" format:"uuid"`
	ParentID    string     `json:"parent_id,omitempty" format:"uuid"`
	ActiveRunID string     `json:"active_run_id,omitempty" format:"uuid" doc:"Execução em andamento, se houver."`
	CreatedBy   ActorRef   `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

// Comment é uma mensagem na thread de uma task.
type Comment struct {
	ID        string    `json:"id" format:"uuid"`
	CompanyID string    `json:"company_id" format:"uuid"`
	TaskID    string    `json:"task_id" format:"uuid"`
	Author    ActorRef  `json:"author"`
	RunID     string    `json:"run_id,omitempty" format:"uuid"`
	Body      string    `json:"body"`
	EventID   string    `json:"event_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Usage são contagens de tokens reportadas pelo agente.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
}

// Run é uma execução de uma persona sobre uma task.
type Run struct {
	ID          string     `json:"id" format:"uuid"`
	CompanyID   string     `json:"company_id" format:"uuid"`
	TaskID      string     `json:"task_id" format:"uuid"`
	TaskKey     string     `json:"task_key,omitempty" example:"HNK-12"`
	TaskTitle   string     `json:"task_title,omitempty"`
	PersonaID   string     `json:"persona_id" format:"uuid"`
	PersonaName string     `json:"persona_name,omitempty"`
	Status      string     `json:"status" enum:"queued,running,done,interrupted,failed,cancelled,expired"`
	Trigger     string     `json:"trigger" enum:"assign,comment,manual,heartbeat,gate"`
	Agent       string     `json:"agent" enum:"claude,codex,fake"`
	Model       string     `json:"model,omitempty"`
	SessionRef  string     `json:"session_ref,omitempty"`
	TraceID     string     `json:"trace_id,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`
	Error       string     `json:"error,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	CostUSD     *float64   `json:"cost_usd,omitempty"`
	Usage       Usage      `json:"usage"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// RunEvent é uma linha do log de uma execução.
type RunEvent struct {
	ID         string          `json:"id" format:"uuid"`
	RunID      string          `json:"run_id" format:"uuid"`
	Seq        int64           `json:"seq" doc:"Ordem dentro da execução."`
	Kind       string          `json:"kind" enum:"system,assistant,thinking,tool_use,tool_result,stdout,stderr,result,status"`
	Text       string          `json:"text"`
	Payload    json.RawMessage `json:"payload,omitempty" doc:"Dados estruturados do evento." type:"object"`
	OccurredAt time.Time       `json:"occurred_at"`
	ReceivedAt time.Time       `json:"received_at"`
}

// Activity é um fato de domínio.
type Activity struct {
	ID          string          `json:"id" format:"uuid"`
	CompanyID   string          `json:"company_id" format:"uuid"`
	Actor       ActorRef        `json:"actor"`
	Verb        string          `json:"verb" example:"task.created"`
	SubjectType string          `json:"subject_type" example:"task"`
	SubjectID   string          `json:"subject_id" format:"uuid"`
	Data        json.RawMessage `json:"data,omitempty" type:"object"`
	EventID     string          `json:"event_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ReceivedAt  time.Time       `json:"received_at"`
}

// Dashboard são os números do Painel.
type Dashboard struct {
	ActivePersonas  int     `json:"active_personas"`
	TasksInProgress int     `json:"tasks_in_progress"`
	OpenTasks       int     `json:"open_tasks"`
	MonthCostUSD    float64 `json:"month_cost_usd"`
	PendingGates    int     `json:"pending_gates"`
	RunningRuns     int     `json:"running_runs"`
}

// WakeResult confirma o enfileiramento de um wake.
type WakeResult struct {
	Queued bool `json:"queued" doc:"Sempre true quando aceito; execuções simultâneas são coalescidas."`
}

// ---------- conversões ----------

func idStr(id domain.ID) string { return id.String() }

func idPtr(id *domain.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func actorRef(a domain.Actor) ActorRef {
	return ActorRef{Type: string(a.Type), ID: idPtr(a.ID)}
}

func companyOut(c domain.Company) Company {
	return Company{ID: idStr(c.ID), Slug: c.Slug, Name: c.Name, TaskPrefix: c.TaskPrefix,
		Timezone: c.Timezone, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func workspaceOut(w domain.Workspace) Workspace {
	return Workspace{ID: idStr(w.ID), CompanyID: idStr(w.CompanyID), Slug: w.Slug, Name: w.Name,
		Path: w.Path, RepoURL: w.RepoURL, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
}

func projectOut(p domain.Project) Project {
	return Project{ID: idStr(p.ID), CompanyID: idStr(p.CompanyID), Slug: p.Slug, Name: p.Name,
		Description: p.Description, WorkspaceID: idPtr(p.WorkspaceID), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func personaOut(p domain.Persona) Persona {
	var cfg map[string]any
	if len(p.AdapterConfig) > 0 {
		_ = json.Unmarshal(p.AdapterConfig, &cfg)
	}
	return Persona{ID: idStr(p.ID), CompanyID: idStr(p.CompanyID), Slug: p.Slug, Name: p.Name, Title: p.Title,
		Instructions: p.Instructions, Agent: p.Agent, Model: p.Model, AdapterConfig: cfg, Status: string(p.Status),
		ReportsTo: idPtr(p.ReportsTo), WorkspaceID: idPtr(p.WorkspaceID), HeartbeatIntervalSec: p.HeartbeatIntervalSec,
		BudgetMonthlyCents: p.BudgetMonthlyCents, RequireReview: p.RequireReview, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func taskOut(t service.TaskView) Task {
	return Task{ID: idStr(t.ID), CompanyID: idStr(t.CompanyID), Number: t.Number, Key: t.Key, Title: t.Title,
		Description: t.Description, Status: string(t.Status), Priority: string(t.Priority),
		PersonaID: idPtr(t.PersonaID), PersonaName: t.PersonaName, ProjectID: idPtr(t.ProjectID), ProjectName: t.ProjectName,
		WorkspaceID: idPtr(t.WorkspaceID), ParentID: idPtr(t.ParentID), ActiveRunID: idPtr(t.ActiveRunID),
		CreatedBy: actorRef(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, ClosedAt: t.ClosedAt}
}

func commentOut(c domain.Comment) Comment {
	return Comment{ID: idStr(c.ID), CompanyID: idStr(c.CompanyID), TaskID: idStr(c.TaskID), Author: actorRef(c.Author),
		RunID: idPtr(c.RunID), Body: c.Body, EventID: c.EventID, CreatedAt: c.CreatedAt}
}

func runOut(r service.RunView) Run {
	return Run{ID: idStr(r.ID), CompanyID: idStr(r.CompanyID), TaskID: idStr(r.TaskID), TaskKey: r.TaskKey,
		TaskTitle: r.TaskTitle, PersonaID: idStr(r.PersonaID), PersonaName: r.PersonaName, Status: string(r.Status),
		Trigger: string(r.Trigger), Agent: r.Agent, Model: r.Model, SessionRef: r.SessionRef, TraceID: r.TraceID,
		ExitCode: r.ExitCode, Error: r.Error, Summary: r.Summary, CostUSD: r.CostUSD, Usage: Usage(r.Usage),
		CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}

func runEventOut(e domain.RunEvent) RunEvent {
	return RunEvent{ID: idStr(e.ID), RunID: idStr(e.RunID), Seq: e.Seq, Kind: string(e.Kind), Text: e.Text,
		Payload: e.Payload, OccurredAt: e.OccurredAt, ReceivedAt: e.ReceivedAt}
}

func activityOut(a domain.ActivityEvent) Activity {
	return Activity{ID: idStr(a.ID), CompanyID: idStr(a.CompanyID), Actor: actorRef(a.Actor), Verb: a.Verb,
		SubjectType: a.SubjectType, SubjectID: idStr(a.SubjectID), Data: a.Data, EventID: a.EventID,
		OccurredAt: a.OccurredAt, ReceivedAt: a.ReceivedAt}
}

func dashboardOut(d service.DashboardStats) Dashboard { return Dashboard(d) }

// listOf converte, pagina e envelopa. A paginação por fatiamento vale para
// listagens que o service devolve completas.
func listOf[S, T any](items []S, p Page, conv func(S) T) *ListOut[T] {
	start := min(p.Offset, len(items))
	end := min(start+p.Limit, len(items))
	out := make([]T, 0, end-start)
	for _, it := range items[start:end] {
		out = append(out, conv(it))
	}
	return &ListOut[T]{Body: ListBody[T]{Items: out, Limit: p.Limit, Offset: p.Offset}}
}

// listAll envelopa itens já paginados na origem.
func listAll[S, T any](items []S, p Page, conv func(S) T) *ListOut[T] {
	out := make([]T, 0, len(items))
	for _, it := range items {
		out = append(out, conv(it))
	}
	return &ListOut[T]{Body: ListBody[T]{Items: out, Limit: p.Limit, Offset: p.Offset}}
}
