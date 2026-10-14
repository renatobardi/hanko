package service

// Method set of *Service (the contract other packages compile against).
// Track A replaces each ErrNotImplemented body with the real implementation
// in topic files (companies.go, personas.go, tasks.go, runs.go, ...), deleting it here.

import (
	"context"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// ---- companies / workspaces / projects ----

// CreateCompany creates a company. Slug unique → ErrConflict.
func (s *Service) CreateCompany(ctx context.Context, actor domain.Actor, in CreateCompanyInput) (domain.Company, error) {
	return domain.Company{}, ErrNotImplemented
}

// GetCompany resolves ref as UUID or slug.
func (s *Service) GetCompany(ctx context.Context, ref string) (domain.Company, error) {
	return domain.Company{}, ErrNotImplemented
}

func (s *Service) ListCompanies(ctx context.Context) ([]domain.Company, error) {
	return nil, ErrNotImplemented
}

func (s *Service) CreateWorkspace(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateWorkspaceInput) (domain.Workspace, error) {
	return domain.Workspace{}, ErrNotImplemented
}

func (s *Service) ListWorkspaces(ctx context.Context, companyID domain.ID) ([]domain.Workspace, error) {
	return nil, ErrNotImplemented
}

// GetWorkspace resolves ref as UUID or slug within the company.
func (s *Service) GetWorkspace(ctx context.Context, companyID domain.ID, ref string) (domain.Workspace, error) {
	return domain.Workspace{}, ErrNotImplemented
}

func (s *Service) CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateProjectInput) (domain.Project, error) {
	return domain.Project{}, ErrNotImplemented
}

func (s *Service) ListProjects(ctx context.Context, companyID domain.ID) ([]domain.Project, error) {
	return nil, ErrNotImplemented
}

func (s *Service) GetProject(ctx context.Context, companyID domain.ID, ref string) (domain.Project, error) {
	return domain.Project{}, ErrNotImplemented
}

// ---- personas ----

func (s *Service) HirePersona(ctx context.Context, actor domain.Actor, companyID domain.ID, in HirePersonaInput) (domain.Persona, error) {
	return domain.Persona{}, ErrNotImplemented
}

func (s *Service) ListPersonas(ctx context.Context, companyID domain.ID) ([]domain.Persona, error) {
	return nil, ErrNotImplemented
}

// GetPersona resolves ref as UUID or slug within the company.
func (s *Service) GetPersona(ctx context.Context, companyID domain.ID, ref string) (domain.Persona, error) {
	return domain.Persona{}, ErrNotImplemented
}

func (s *Service) UpdatePersona(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in UpdatePersonaInput) (domain.Persona, error) {
	return domain.Persona{}, ErrNotImplemented
}

// ---- tasks ----

// CreateTask allocates the next task number atomically. If assigned to a persona that
// CanWork and status is Workable, enqueues a Wake (trigger assign) in the same tx.
// Same EventID twice → returns the existing task (idempotent).
func (s *Service) CreateTask(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateTaskInput) (TaskView, error) {
	return TaskView{}, ErrNotImplemented
}

func (s *Service) ListTasks(ctx context.Context, companyID domain.ID, f TaskFilter) ([]TaskView, error) {
	return nil, ErrNotImplemented
}

// GetTask resolves ref as UUID or task key ("HNK-12").
func (s *Service) GetTask(ctx context.Context, companyID domain.ID, ref string) (TaskView, error) {
	return TaskView{}, ErrNotImplemented
}

// UpdateTask applies changes; (re)assignment to a workable task enqueues a Wake.
// Closing a task (done/cancelled) sets closed_at and records a memory fact.
func (s *Service) UpdateTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in UpdateTaskInput) (TaskView, error) {
	return TaskView{}, ErrNotImplemented
}

// AddComment appends to the thread. A human comment on an assigned open task enqueues
// a Wake (trigger comment). Same EventID twice → returns the existing comment.
func (s *Service) AddComment(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in AddCommentInput) (domain.Comment, error) {
	return domain.Comment{}, ErrNotImplemented
}

func (s *Service) ListComments(ctx context.Context, companyID, taskID domain.ID) ([]domain.Comment, error) {
	return nil, ErrNotImplemented
}

// WakeTask enqueues a manual Wake for the assignee. No assignee → ErrInvalid.
func (s *Service) WakeTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) error {
	return ErrNotImplemented
}

// ---- runs ----

func (s *Service) ListRuns(ctx context.Context, companyID domain.ID, f RunFilter) ([]RunView, error) {
	return nil, ErrNotImplemented
}

func (s *Service) GetRun(ctx context.Context, companyID, runID domain.ID) (RunView, error) {
	return RunView{}, ErrNotImplemented
}

// ListRunEvents returns events with seq > afterSeq, ascending.
func (s *Service) ListRunEvents(ctx context.Context, companyID, runID domain.ID, afterSeq int64, limit int) ([]domain.RunEvent, error) {
	return nil, ErrNotImplemented
}

// CancelRun marks a queued run cancelled; for a running run it records the request and
// publishes run.cancel on the broker (the executor cancels its context).
func (s *Service) CancelRun(ctx context.Context, actor domain.Actor, companyID, runID domain.ID) (RunView, error) {
	return RunView{}, ErrNotImplemented
}

// CheckoutRun atomically creates a queued run for (task, persona) if the task has no
// active run, the task is workable and assigned to that persona, and the persona CanWork;
// enqueues ExecuteRun in the same tx. Returns domain.ErrActiveRun when occupied.
// Moves task todo → in_progress.
func (s *Service) CheckoutRun(ctx context.Context, w Wake) (domain.Run, error) {
	return domain.Run{}, ErrNotImplemented
}

// LoadRunContext loads what the executor needs (run, company, persona, task, thread, workspace).
func (s *Service) LoadRunContext(ctx context.Context, companyID, runID domain.ID) (RunContext, error) {
	return RunContext{}, ErrNotImplemented
}

// MarkRunStarted sets running, started_at, trace_id, handoff_in; persona → running.
func (s *Service) MarkRunStarted(ctx context.Context, companyID, runID domain.ID, traceID, handoffIn string) (domain.Run, error) {
	return domain.Run{}, ErrNotImplemented
}

// AppendRunEvent stores an event with the next seq and publishes it on the broker
// (topic events.RunTopic, type "run.event") after commit.
func (s *Service) AppendRunEvent(ctx context.Context, companyID, runID domain.ID, e adapter.Event) (domain.RunEvent, error) {
	return domain.RunEvent{}, ErrNotImplemented
}

// FinishRun records the result. On done with a non-empty summary, posts it as a persona
// comment on the task. Persona → idle (or error on failure). Writes activity + a
// memory_outbox fact (run.finished) and enqueues its delivery. Publishes run.status.
func (s *Service) FinishRun(ctx context.Context, companyID, runID domain.ID, in FinishRunInput) (domain.Run, error) {
	return domain.Run{}, ErrNotImplemented
}

// InterruptOrphanRuns marks runs left queued/running by a dead process as interrupted.
// Called at boot (single-instance v0.1). Returns how many were changed.
func (s *Service) InterruptOrphanRuns(ctx context.Context) (int, error) {
	return 0, ErrNotImplemented
}

// WakeCandidates lists assigned workable tasks with no active run whose persona CanWork
// and has heartbeat_interval_sec > 0 elapsed since its last run on that task.
func (s *Service) WakeCandidates(ctx context.Context, limit int) ([]WakeCandidate, error) {
	return nil, ErrNotImplemented
}

// ---- activity / dashboard ----

func (s *Service) ListActivity(ctx context.Context, companyID domain.ID, f ActivityFilter) ([]domain.ActivityEvent, error) {
	return nil, ErrNotImplemented
}

func (s *Service) Dashboard(ctx context.Context, companyID domain.ID) (DashboardStats, error) {
	return DashboardStats{}, ErrNotImplemented
}

// ---- memory outbox ----

// GetMemoryItem loads one outbox row (any status) with company slug and workspace.
func (s *Service) GetMemoryItem(ctx context.Context, id domain.ID) (MemoryItem, error) {
	return MemoryItem{}, ErrNotImplemented
}

// MarkMemorySent / MarkMemoryFailed update delivery state (attempts++ on failure).
func (s *Service) MarkMemorySent(ctx context.Context, id domain.ID) error { return ErrNotImplemented }
func (s *Service) MarkMemoryFailed(ctx context.Context, id domain.ID, errMsg string) error {
	return ErrNotImplemented
}

// PendingMemory lists pending outbox ids older than minAge (re-enqueue sweep).
func (s *Service) PendingMemory(ctx context.Context, limit int) ([]domain.ID, error) {
	return nil, ErrNotImplemented
}
