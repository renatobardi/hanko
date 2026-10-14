// Package service holds Hanko's use cases. One transaction per use case; every
// domain write emits an activity_event in the same transaction (docs/STACK.md §6).
// Handlers (api, cli, mcp, ui) and the heartbeat call only this package.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
)

// ErrNotImplemented marks contract stubs not yet filled.
var ErrNotImplemented = errors.New("not implemented")

// Wake asks the heartbeat to consider running a persona on a task.
type Wake struct {
	CompanyID domain.ID         `json:"company_id"`
	PersonaID domain.ID         `json:"persona_id"`
	TaskID    domain.ID         `json:"task_id"`
	Trigger   domain.RunTrigger `json:"trigger"`
}

// Jobs enqueues background work inside the caller's transaction (river InsertTx).
// Implemented by internal/heartbeat. Must coalesce duplicate wakes.
type Jobs interface {
	WakeTx(ctx context.Context, tx pgx.Tx, w Wake) error
	ExecuteRunTx(ctx context.Context, tx pgx.Tx, companyID, runID domain.ID) error
	DeliverMemoryTx(ctx context.Context, tx pgx.Tx, outboxID domain.ID) error
}

// Clock is injectable for tests.
type Clock func() time.Time

// Service is the use-case layer.
type Service struct {
	Pool   *pgxpool.Pool
	Jobs   Jobs
	Broker *events.Broker
	Now    Clock
	// WorkspacesRoot restringe o caminho de novos workspaces a esta raiz (HANKO_WORKSPACES_ROOT).
	// Vazio = sem restrição de raiz (só caminho absoluto e limpo); o app sempre define.
	WorkspacesRoot string
	// RunReserveUSD é o piso da reserva de orçamento por run aberta (HANKO_RUN_RESERVE_USD);
	// 0 usa DefaultRunReserveUSD.
	RunReserveUSD float64
}

func New(pool *pgxpool.Pool, jobs Jobs, broker *events.Broker) *Service {
	return &Service{Pool: pool, Jobs: jobs, Broker: broker, Now: func() time.Time { return time.Now().UTC() }}
}

// ---------- inputs ----------

type CreateCompanyInput struct {
	Name       string
	Slug       string // derived from Name when empty
	TaskPrefix string // default "HNK"
	Timezone   string // default America/Sao_Paulo
}

type CreateWorkspaceInput struct {
	Name, Slug, Path, RepoURL string
}

type CreateProjectInput struct {
	Name, Slug, Description string
	WorkspaceID             *domain.ID
}

type HirePersonaInput struct {
	Name                 string
	Slug                 string
	Title                string
	Instructions         string
	Agent                string // claude | codex | fake
	Model                string
	AdapterConfig        json.RawMessage
	ReportsTo            *domain.ID
	WorkspaceID          *domain.ID
	HeartbeatIntervalSec int
	BudgetMonthlyCents   *int64
	// RequireReview sends this persona's attempts to close a task through a task_review gate.
	RequireReview bool
}

type UpdatePersonaInput struct {
	Name, Title, Instructions, Model *string
	Status                           *domain.PersonaStatus
	HeartbeatIntervalSec             *int
	WorkspaceID                      *domain.ID
	ClearWorkspace                   bool
	// ReportsTo moves the persona under another one; ClearReportsTo makes it top-level.
	// A move that would close a loop in the chart is rejected (ErrInvalid).
	ReportsTo      *domain.ID
	ClearReportsTo bool
	RequireReview  *bool
	// BudgetMonthlyCents sets the persona's monthly budget (USD cents); ClearBudget removes it.
	BudgetMonthlyCents *int64
	ClearBudget        bool
}

type CreateTaskInput struct {
	Title       string
	Description string
	Status      domain.TaskStatus // default todo
	Priority    domain.Priority   // default medium
	PersonaID   *domain.ID
	ProjectID   *domain.ID
	WorkspaceID *domain.ID // default: project's workspace, else persona's workspace
	ParentID    *domain.ID
	EventID     string // idempotency key (agents/MCP)

	trigger domain.RunTrigger // wake trigger; default assign (flows use flow)
}

type UpdateTaskInput struct {
	Title       *string
	Description *string
	Status      *domain.TaskStatus
	Priority    *domain.Priority
	PersonaID   *domain.ID
	Unassign    bool
	ProjectID   *domain.ID
	EventID     string
}

type TaskFilter struct {
	Statuses  []domain.TaskStatus
	PersonaID *domain.ID
	ProjectID *domain.ID
	Query     string
	Limit     int // default 100, max 500
	Offset    int
}

type AddCommentInput struct {
	Body    string
	RunID   *domain.ID
	EventID string
}

type RunFilter struct {
	TaskID    *domain.ID
	PersonaID *domain.ID
	Statuses  []domain.RunStatus
	Limit     int // default 50, max 500
}

type ActivityFilter struct {
	SubjectType string
	SubjectID   *domain.ID
	Before      *time.Time
	Limit       int // default 50
}

type FinishRunInput struct {
	Status domain.RunStatus // done | failed | interrupted | cancelled | expired
	Result adapter.Result
}

// TaskView is a task with display-ready joins.
type TaskView struct {
	domain.Task
	PersonaName string     `json:"persona_name,omitempty"`
	ProjectName string     `json:"project_name,omitempty"`
	ActiveRunID *domain.ID `json:"active_run_id,omitempty"`
}

// RunView is a run with display-ready joins.
type RunView struct {
	domain.Run
	TaskKey     string `json:"task_key"`
	TaskTitle   string `json:"task_title"`
	PersonaName string `json:"persona_name"`
}

// DashboardStats feeds the Painel.
type DashboardStats struct {
	ActivePersonas  int     `json:"active_personas"`
	TasksInProgress int     `json:"tasks_in_progress"`
	OpenTasks       int     `json:"open_tasks"`
	MonthCostUSD    float64 `json:"month_cost_usd"`
	// Company monthly ceiling (USD cents), its use and state (none|ok|warning|exceeded).
	MonthBudgetCents *int64  `json:"month_budget_cents,omitempty"`
	BudgetPercent    float64 `json:"budget_percent"`
	BudgetState      string  `json:"budget_state"`
	PendingGates     int     `json:"pending_gates"`
	RunningRuns      int     `json:"running_runs"`
}

// RunContext is everything the executor needs to build the agent prompt.
type RunContext struct {
	Run       domain.Run
	Company   domain.Company
	Persona   domain.Persona
	Task      TaskView
	Comments  []domain.Comment
	Workspace *domain.Workspace
	// PendingResult is set when the harness already ran and only finalization is missing.
	PendingResult *FinishRunInput
}

// WakeCandidate is an (assigned, workable, idle) task found by the periodic heartbeat.
type WakeCandidate struct {
	CompanyID domain.ID
	PersonaID domain.ID
	TaskID    domain.ID
}

// MemoryItem is a pending memory_outbox row.
type MemoryItem struct {
	ID          domain.ID
	CompanyID   domain.ID
	CompanySlug string
	Workspace   *domain.Workspace
	Kind        string
	Payload     json.RawMessage
	EventID     string
	Attempts    int
	Status      string // pending | sent | failed
	NextAttempt *time.Time
}

// PersonaHealth is the reliability state of a persona: its last failed run and how many
// failures in a row it has (the persona goes to error at personaMaxFailures).
type PersonaHealth struct {
	LastError           string
	LastErrorAt         *time.Time
	ConsecutiveFailures int
}
