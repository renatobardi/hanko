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
	ID         string `json:"id" format:"uuid" doc:"Identificador (UUIDv7)."`
	Slug       string `json:"slug" example:"acme" doc:"Identificador legível, único."`
	Name       string `json:"name" example:"Acme Ltda" doc:"Nome de exibição."`
	TaskPrefix string `json:"task_prefix" example:"HNK" doc:"Prefixo das chaves de task (HNK-12)."`
	Timezone   string `json:"timezone" example:"America/Sao_Paulo" doc:"Fuso IANA."`
	// BudgetMonthlyCents é o teto mensal de gasto em centavos de dólar; ausente = sem teto.
	BudgetMonthlyCents *int64    `json:"budget_monthly_cents,omitempty" doc:"Orçamento mensal da empresa em centavos de dólar."`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
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
	PauseReason          string         `json:"pause_reason,omitempty" doc:"Motivo de uma pausa automática (orçamento estourado)."`
	ReviewerPersonaID    string         `json:"reviewer_persona_id,omitempty" format:"uuid" doc:"Persona que revisa, em run headless, as entregas desta antes do gate."`
	ReviewerAgent        string         `json:"reviewer_agent,omitempty" enum:"claude,codex,fake" doc:"Harness alternativo que revisa no papel desta persona."`
	ReviewerModel        string         `json:"reviewer_model,omitempty" doc:"Modelo do harness revisor."`
	SubscriptionID       string         `json:"subscription_id,omitempty" format:"uuid" doc:"Assinatura que cobre as execuções da persona."`
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
	Model       string     `json:"model,omitempty" doc:"Modelo escolhido à mão para esta task; vence o da persona."`
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
	ID                string     `json:"id" format:"uuid"`
	Name              string     `json:"name,omitempty" example:"Veloz_Raposa" doc:"Nome amigável Adjetivo_Substantivo, único na empresa; vazio em execuções anteriores a ele."`
	CompanyID         string     `json:"company_id" format:"uuid"`
	TaskID            string     `json:"task_id,omitempty" format:"uuid" doc:"Task da execução; ausente numa execução de chat."`
	TaskKey           string     `json:"task_key,omitempty" example:"HNK-12"`
	TaskTitle         string     `json:"task_title,omitempty"`
	ConversationID    string     `json:"conversation_id,omitempty" format:"uuid" doc:"Conversa de uma execução de chat (gatilho chat); ausente nas execuções de task."`
	ConversationTitle string     `json:"conversation_title,omitempty"`
	PersonaID         string     `json:"persona_id" format:"uuid"`
	PersonaName       string     `json:"persona_name,omitempty"`
	Status            string     `json:"status" enum:"queued,running,done,interrupted,failed,cancelled,expired"`
	Trigger           string     `json:"trigger" enum:"assign,comment,manual,heartbeat,gate,flow,review,chat"`
	Agent             string     `json:"agent" enum:"claude,codex,fake"`
	Model             string     `json:"model,omitempty"`
	SessionRef        string     `json:"session_ref,omitempty"`
	TraceID           string     `json:"trace_id,omitempty"`
	ExitCode          *int       `json:"exit_code,omitempty"`
	Error             string     `json:"error,omitempty"`
	Summary           string     `json:"summary,omitempty"`
	CostUSD           *float64   `json:"cost_usd,omitempty"`
	CostSource        string     `json:"cost_source,omitempty" enum:"reported,estimated,none" doc:"Origem do custo: informado pelo harness, estimado pela tabela de preços ou desconhecido."`
	ListCostUSD       *float64   `json:"list_cost_usd,omitempty" doc:"Custo de lista: tokens × preço de tabela vigente quando a execução terminou."`
	ModelSource       string     `json:"model_source,omitempty" enum:"task,persona,harness_default" doc:"Quem escolheu o modelo: a task (manual), a persona ou o padrão do harness."`
	SubscriptionID    string     `json:"subscription_id,omitempty" format:"uuid" doc:"Assinatura que cobre a execução."`
	Usage             Usage      `json:"usage"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
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
	// Teto mensal da empresa (centavos de dólar), uso e estado (none, ok, warning, exceeded).
	MonthBudgetCents *int64  `json:"month_budget_cents,omitempty"`
	BudgetPercent    float64 `json:"budget_percent"`
	BudgetState      string  `json:"budget_state" enum:"none,ok,warning,exceeded"`
}

// Budget é o gasto do mês contra o teto.
type Budget struct {
	BudgetMonthlyCents *int64  `json:"budget_monthly_cents,omitempty" doc:"Teto mensal em centavos de dólar; ausente = sem teto."`
	SpentUSD           float64 `json:"spent_usd"`
	Percent            float64 `json:"percent" doc:"Uso do teto em %; 0 sem teto."`
	State              string  `json:"state" enum:"none,ok,warning,exceeded" doc:"warning a partir de 80%, exceeded a partir de 100% (trava dura)."`
}

// CostRow é um grupo do resumo de custos.
type CostRow struct {
	Key          string  `json:"key" doc:"Chave do grupo (data, id da persona, id do projeto, modelo ou harness)."`
	Label        string  `json:"label"`
	CostUSD      float64 `json:"cost_usd"`
	ReportedUSD  float64 `json:"reported_usd" doc:"Custo real: informado pelo harness."`
	EstimatedUSD float64 `json:"estimated_usd" doc:"Parte do custo estimada pela tabela de preços."`
	ListUSD      float64 `json:"list_usd" doc:"Custo de lista: tokens × preço de tabela vigente quando cada execução terminou."`
	PaidUSD      float64 `json:"paid_usd" doc:"Custo pago: rateio da mensalidade das assinaturas (mensalidade ÷ dias do mês, por dia, proporcional ao custo de lista de cada execução do dia)."`
	IdleUSD      float64 `json:"idle_usd" doc:"Mensalidade de um dia sem uso (só no agrupamento por dia)."`
	Unused       bool    `json:"unused" doc:"Dia em que a assinatura não foi usada (a tela mostra \"sem uso\")."`
	Runs         int     `json:"runs"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
}

// CostSummary agrega o custo de um período por uma dimensão.
type CostSummary struct {
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to" doc:"Exclusivo."`
	Timezone      string             `json:"timezone"`
	GroupBy       string             `json:"group_by" enum:"day,persona,project,model,harness"`
	TotalUSD      float64            `json:"total_usd"`
	ReportedUSD   float64            `json:"reported_usd"`
	EstimatedUSD  float64            `json:"estimated_usd"`
	ListUSD       float64            `json:"list_usd"`
	PaidUSD       float64            `json:"paid_usd" doc:"Mensalidades rateadas nas execuções, até hoje."`
	IdleUSD       float64            `json:"idle_usd" doc:"Mensalidades de dias sem uso. paid_usd + idle_usd fecha com a mensalidade num mês completo."`
	Subscriptions []SubscriptionCost `json:"subscriptions" doc:"Custo pago por assinatura no período."`
	Runs          int                `json:"runs"`
	InputTokens   int64              `json:"input_tokens"`
	OutputTokens  int64              `json:"output_tokens"`
	CacheTokens   int64              `json:"cache_tokens"`
	Rows          []CostRow          `json:"rows"`
	Budget        Budget             `json:"budget" doc:"Orçamento da empresa no mês corrente."`
}

// Flow é uma definição recorrente que cria tasks por cron.
type Flow struct {
	ID              string     `json:"id" format:"uuid"`
	CompanyID       string     `json:"company_id" format:"uuid"`
	Slug            string     `json:"slug" example:"resumo-diario"`
	Name            string     `json:"name" example:"Resumo diário"`
	Description     string     `json:"description,omitempty"`
	Cron            string     `json:"cron" example:"0 9 * * 1-5"`
	Timezone        string     `json:"timezone" example:"America/Sao_Paulo"`
	Enabled         bool       `json:"enabled"`
	PersonaID       string     `json:"persona_id" format:"uuid"`
	PersonaName     string     `json:"persona_name"`
	ProjectID       string     `json:"project_id,omitempty" format:"uuid"`
	ProjectName     string     `json:"project_name,omitempty"`
	Priority        string     `json:"priority" enum:"critical,high,medium,low"`
	TaskTitle       string     `json:"task_title" doc:"Modelo do título: {{data}}, {{n}}."`
	TaskDescription string     `json:"task_description"`
	LastFiredAt     *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt      *time.Time `json:"next_fire_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// FlowFire é um disparo de um fluxo.
type FlowFire struct {
	ID           string    `json:"id" format:"uuid"`
	FlowID       string    `json:"flow_id" format:"uuid"`
	ScheduledFor time.Time `json:"scheduled_for"`
	TaskID       string    `json:"task_id,omitempty" format:"uuid"`
	TaskKey      string    `json:"task_key,omitempty" example:"HNK-12"`
	TaskTitle    string    `json:"task_title,omitempty"`
	TaskStatus   string    `json:"task_status,omitempty"`
	Manual       bool      `json:"manual,omitempty" doc:"true no disparo de \"Rodar agora\" (não é uma ocorrência do cron)."`
	CreatedAt    time.Time `json:"created_at"`
}

// FlowRun é o resultado de "rodar agora".
type FlowRun struct {
	Fire      FlowFire `json:"fire"`
	Task      *Task    `json:"task,omitempty"`
	Duplicate bool     `json:"duplicate,omitempty" doc:"true quando já havia um disparo neste minuto: nada novo foi criado."`
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
		Timezone: c.Timezone, BudgetMonthlyCents: c.BudgetMonthlyCents, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
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
		BudgetMonthlyCents: p.BudgetMonthlyCents, PauseReason: p.PauseReason, SubscriptionID: idPtr(p.SubscriptionID), RequireReview: p.RequireReview, ReviewerPersonaID: idPtr(p.ReviewerPersonaID), ReviewerAgent: p.ReviewerAgent, ReviewerModel: p.ReviewerModel, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func taskOut(t service.TaskView) Task {
	return Task{ID: idStr(t.ID), CompanyID: idStr(t.CompanyID), Number: t.Number, Key: t.Key, Title: t.Title,
		Description: t.Description, Status: string(t.Status), Priority: string(t.Priority),
		PersonaID: idPtr(t.PersonaID), PersonaName: t.PersonaName, ProjectID: idPtr(t.ProjectID), ProjectName: t.ProjectName,
		WorkspaceID: idPtr(t.WorkspaceID), ParentID: idPtr(t.ParentID), Model: t.Model, ActiveRunID: idPtr(t.ActiveRunID),
		CreatedBy: actorRef(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, ClosedAt: t.ClosedAt}
}

func commentOut(c domain.Comment) Comment {
	return Comment{ID: idStr(c.ID), CompanyID: idStr(c.CompanyID), TaskID: idStr(c.TaskID), Author: actorRef(c.Author),
		RunID: idPtr(c.RunID), Body: c.Body, EventID: c.EventID, CreatedAt: c.CreatedAt}
}

func runOut(r service.RunView) Run {
	taskID := ""
	if r.TaskID != (domain.ID{}) {
		taskID = idStr(r.TaskID)
	}
	return Run{ID: idStr(r.ID), Name: r.Name, CompanyID: idStr(r.CompanyID), TaskID: taskID, TaskKey: r.TaskKey,
		ConversationID: idPtr(r.ConversationID), ConversationTitle: r.ConversationTitle, TaskTitle: r.TaskTitle, PersonaID: idStr(r.PersonaID), PersonaName: r.PersonaName, Status: string(r.Status),
		CostSource: string(r.CostSource), ListCostUSD: r.ListCostUSD, ModelSource: string(r.ModelSource), SubscriptionID: idPtr(r.SubscriptionID),
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

func dashboardOut(d service.DashboardStats) Dashboard {
	return Dashboard{ActivePersonas: d.ActivePersonas, TasksInProgress: d.TasksInProgress, OpenTasks: d.OpenTasks,
		MonthCostUSD: d.MonthCostUSD, PendingGates: d.PendingGates, RunningRuns: d.RunningRuns,
		MonthBudgetCents: d.MonthBudgetCents, BudgetPercent: d.BudgetPercent, BudgetState: d.BudgetState}
}

func budgetOut(b service.BudgetStatus) Budget {
	return Budget{BudgetMonthlyCents: b.BudgetCents, SpentUSD: b.SpentUSD, Percent: b.Percent, State: b.State}
}

func costSummaryOut(c service.CostSummary, b service.BudgetStatus) CostSummary {
	rows := make([]CostRow, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = CostRow(r)
	}
	subs := make([]SubscriptionCost, len(c.Subscriptions))
	for i, sc := range c.Subscriptions {
		subs[i] = SubscriptionCost{ID: idStr(sc.ID), Name: sc.Name, Agent: sc.Agent, MonthlyUSD: sc.MonthlyUSD, PaidUSD: sc.PaidUSD,
			IdleUSD: sc.IdleUSD, ListUSD: sc.ListUSD, Runs: sc.Runs}
	}
	return CostSummary{From: c.From, To: c.To, Timezone: c.Timezone, GroupBy: c.GroupBy, TotalUSD: c.TotalUSD,
		ReportedUSD: c.ReportedUSD, EstimatedUSD: c.EstimatedUSD, ListUSD: c.ListUSD, PaidUSD: c.PaidUSD, IdleUSD: c.IdleUSD,
		Subscriptions: subs, Runs: c.Runs, InputTokens: c.InputTokens,
		OutputTokens: c.OutputTokens, CacheTokens: c.CacheTokens, Rows: rows, Budget: budgetOut(b)}
}

func flowOut(f service.FlowView) Flow {
	return Flow{ID: idStr(f.ID), CompanyID: idStr(f.CompanyID), Slug: f.Slug, Name: f.Name, Description: f.Description,
		Cron: f.Cron, Timezone: f.Timezone, Enabled: f.Enabled, PersonaID: idStr(f.PersonaID), PersonaName: f.PersonaName,
		ProjectID: idPtr(f.ProjectID), ProjectName: f.ProjectName, Priority: string(f.Priority), TaskTitle: f.TaskTitle,
		TaskDescription: f.TaskDescription, LastFiredAt: f.LastFiredAt, NextFireAt: f.NextFireAt,
		CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt}
}

func flowFireOut(f service.FlowFireView) FlowFire {
	return FlowFire{ID: idStr(f.ID), FlowID: idStr(f.FlowID), ScheduledFor: f.ScheduledFor, TaskID: idPtr(f.TaskID),
		TaskKey: f.TaskKey, TaskTitle: f.TaskTitle, TaskStatus: f.TaskStatus, Manual: f.Manual, CreatedAt: f.CreatedAt}
}

func flowRunOut(r service.FlowFireResult) FlowRun {
	out := FlowRun{Fire: FlowFire{ID: idStr(r.Fire.ID), FlowID: idStr(r.Fire.FlowID), ScheduledFor: r.Fire.ScheduledFor,
		TaskID: idPtr(r.Fire.TaskID), Manual: r.Fire.Manual, CreatedAt: r.Fire.CreatedAt}, Duplicate: r.Duplicate}
	if r.Task != nil {
		t := taskOut(*r.Task)
		out.Task = &t
		out.Fire.TaskKey, out.Fire.TaskTitle, out.Fire.TaskStatus = t.Key, t.Title, t.Status
	}
	return out
}

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

// Subscription é o plano de um harness: mensalidade, modelos cobertos e trava semanal.
type Subscription struct {
	ID             string    `json:"id" format:"uuid"`
	CompanyID      string    `json:"company_id" format:"uuid"`
	Name           string    `json:"name" example:"Claude Max"`
	Agent          string    `json:"agent" enum:"claude,codex,fake"`
	MonthlyUSD     float64   `json:"monthly_usd"`
	ModelPrefixes  []string  `json:"model_prefixes" doc:"Prefixos de modelo cobertos; vazio = todos os do harness."`
	WeeklyLimitUSD *float64  `json:"weekly_limit_usd,omitempty" doc:"Teto semanal de uso estimado em dólares."`
	WeeklyGuardPct int       `json:"weekly_guard_pct" doc:"Percentual do teto semanal que trava novas execuções."`
	Active         bool      `json:"active"`
	WeekStart      time.Time `json:"week_start" doc:"Início (segunda-feira, fuso da empresa) da semana corrente."`
	WeekEnd        time.Time `json:"week_end" doc:"Exclusivo."`
	WeekUsageUSD   float64   `json:"week_usage_usd" doc:"Uso estimado da semana corrente (custo de lista das execuções cobertas)."`
	WeekPercent    float64   `json:"week_percent" doc:"Uso da semana em % do teto semanal; 0 sem teto."`
	Blocked        bool      `json:"blocked" doc:"A trava semanal disparou: novas execuções cobertas ficam bloqueadas até a semana virar."`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SubscriptionCost é o custo de uma assinatura num período.
type SubscriptionCost struct {
	ID         string  `json:"id" format:"uuid"`
	Name       string  `json:"name"`
	Agent      string  `json:"agent"`
	MonthlyUSD float64 `json:"monthly_usd"`
	PaidUSD    float64 `json:"paid_usd" doc:"Parte da mensalidade rateada nas execuções."`
	IdleUSD    float64 `json:"idle_usd" doc:"Parte da mensalidade de dias sem uso."`
	ListUSD    float64 `json:"list_usd" doc:"Custo de lista das execuções cobertas."`
	Runs       int     `json:"runs"`
}

func subscriptionOut(s service.SubscriptionStatus) Subscription {
	prefixes := s.ModelPrefixes
	if prefixes == nil {
		prefixes = []string{}
	}
	return Subscription{ID: idStr(s.ID), CompanyID: idStr(s.CompanyID), Name: s.Name, Agent: s.Agent, MonthlyUSD: s.MonthlyUSD,
		ModelPrefixes: prefixes, WeeklyLimitUSD: s.WeeklyLimitUSD, WeeklyGuardPct: s.WeeklyGuardPct, Active: s.Active,
		WeekStart: s.WeekStart, WeekEnd: s.WeekEnd, WeekUsageUSD: s.WeekUsageUSD, WeekPercent: s.WeekPercent, Blocked: s.Blocked,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
