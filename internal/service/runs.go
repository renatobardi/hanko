package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

func runViewFromParts(r store.Run, taskKey, taskTitle, personaName string) RunView {
	return RunView{Run: runFromRow(r), TaskKey: taskKey, TaskTitle: taskTitle, PersonaName: personaName}
}

func loadRunView(ctx context.Context, q *store.Queries, companyID, runID domain.ID) (RunView, error) {
	r, err := q.GetRunView(ctx, store.GetRunViewParams{CompanyID: companyID, ID: runID})
	if err != nil {
		return RunView{}, mapErr(err, "get run")
	}
	return runViewFromParts(r.Run, r.TaskKey, r.TaskTitle, r.PersonaName), nil
}

func (s *Service) ListRuns(ctx context.Context, companyID domain.ID, f RunFilter) ([]RunView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListRunViewsParams{CompanyID: companyID, TaskID: f.TaskID, PersonaID: f.PersonaID,
		Lim: i32(clamp(f.Limit, 50, 500))}
	if len(f.Statuses) > 0 {
		p.Statuses = make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			if !st.Valid() {
				return nil, domain.Invalid("status", fmt.Sprintf("unknown run status %q", st))
			}
			p.Statuses[i] = string(st)
		}
	}
	if n := strings.TrimSpace(f.Name); n != "" {
		if c, ok := domain.CanonicalRunName(n); ok {
			n = c
		}
		p.Name = &n
	}
	rows, err := s.read().ListRunViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	out := make([]RunView, len(rows))
	for i, r := range rows {
		out[i] = runViewFromParts(r.Run, r.TaskKey, r.TaskTitle, r.PersonaName)
	}
	return out, nil
}

func (s *Service) GetRun(ctx context.Context, companyID, runID domain.ID) (RunView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return RunView{}, err
	}
	return loadRunView(ctx, s.read(), companyID, runID)
}

// ListRunEvents returns events with seq > afterSeq, ascending.
func (s *Service) ListRunEvents(ctx context.Context, companyID, runID domain.ID, afterSeq int64, limit int) ([]domain.RunEvent, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListRunEvents(ctx, store.ListRunEventsParams{CompanyID: companyID, RunID: runID,
		AfterSeq: afterSeq, Lim: i32(clamp(limit, 200, 1000))})
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	out := make([]domain.RunEvent, len(rows))
	for i, r := range rows {
		out[i] = runEventFromRow(r)
	}
	return out, nil
}

// ListRunEventsBefore is the backward page of the log: the limit events right before beforeSeq, ascending.
// beforeSeq <= 0 means "from the end" (the last limit events). Together with ListRunEvents (forward from a seq)
// it lets a viewer load the tail first and older history on demand, with seq as the cursor.
func (s *Service) ListRunEventsBefore(ctx context.Context, companyID, runID domain.ID, beforeSeq int64, limit int) ([]domain.RunEvent, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	if beforeSeq <= 0 {
		beforeSeq = math.MaxInt64
	}
	rows, err := s.read().ListRunEventsBefore(ctx, store.ListRunEventsBeforeParams{CompanyID: companyID, RunID: runID,
		BeforeSeq: beforeSeq, Lim: i32(clamp(limit, 200, 1000))})
	if err != nil {
		return nil, fmt.Errorf("list run events before: %w", err)
	}
	out := make([]domain.RunEvent, len(rows))
	for i, r := range rows { // the query is newest-first; the API contract is ascending
		out[len(rows)-1-i] = runEventFromRow(r)
	}
	return out, nil
}

// CancelRun marks a queued run cancelled; for a running run it records the request and
// publishes run.cancel on the broker (the executor cancels its context).
func (s *Service) CancelRun(ctx context.Context, actor domain.Actor, companyID, runID domain.ID) (RunView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return RunView{}, err
	}
	var out RunView
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.lockTaskOfReviewRun(companyID, runID); err != nil {
			return err
		}
		cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		if !domain.RunStatus(cur.Status).Active() {
			return domain.Invalid("run", fmt.Sprintf("run already %s", cur.Status))
		}
		if err := t.cancelRun(actor, companyID, cur, nil); err != nil {
			return err
		}
		out, err = loadRunView(ctx, t.q, companyID, runID)
		return err
	})
	return out, err
}

// cancelRun cancels a locked, active run inside the transaction: queued → cancelled right
// away; running → records the request and publishes run.cancel after commit.
func (t *txc) cancelRun(actor domain.Actor, companyID domain.ID, cur store.Run, extra map[string]any) error {
	if t.cancelled[cur.ID] {
		return nil
	}
	if t.cancelled == nil {
		t.cancelled = map[domain.ID]bool{}
	}
	t.cancelled[cur.ID] = true
	data := map[string]any{}
	for k, v := range extra {
		data[k] = v
	}
	switch domain.RunStatus(cur.Status) {
	case domain.RunQueued:
		row, err := t.q.CancelQueuedRun(t.ctx, store.CancelQueuedRunParams{CompanyID: companyID, ID: cur.ID, Now: &t.now})
		if err != nil {
			return mapErr(err, "cancel run")
		}
		data["was"] = "queued"
		if err := t.activity(actor, companyID, "run.cancelled", "run", cur.ID, data, ""); err != nil {
			return err
		}
		t.publishRunStatus(runFromRow(row))
		switch domain.RunTrigger(row.Trigger) {
		case domain.TriggerReview:
			if err := t.reviewRunEnded(row, domain.RunCancelled, "", ""); err != nil {
				return err
			}
		case domain.TriggerMemory:
			if err := t.memoryRunEnded(row, domain.RunCancelled, ""); err != nil {
				return err
			}
		}
	case domain.RunRunning:
		data["was"], data["requested"] = "running", true
		if err := t.activity(actor, companyID, "run.cancelled", "run", cur.ID, data, ""); err != nil {
			return err
		}
		t.publish(events.RunTopic(cur.ID.String()), "run.cancel", cur.ID)
	}
	return nil
}

// cancelTaskRun cancels the task's active run, if any (task closed, unassigned or reassigned).
func (t *txc) cancelTaskRun(actor domain.Actor, companyID, taskID domain.ID, reason string) error {
	active, err := t.q.ActiveRunForTask(t.ctx, taskID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("active run for task: %w", err)
	}
	cur, err := t.q.GetRunByIDForUpdate(t.ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: active.ID})
	if err != nil {
		return mapErr(err, "get run")
	}
	if !domain.RunStatus(cur.Status).Active() {
		return nil // finished while we were looking
	}
	return t.cancelRun(actor, companyID, cur, map[string]any{"reason": reason})
}

// CheckoutRun atomically creates a queued run for (task, persona) if the task has no
// active run, the task is workable and assigned to that persona, and the persona CanWork;
// enqueues ExecuteRun in the same tx. Returns domain.ErrActiveRun when occupied.
// Moves task todo → in_progress.
func (s *Service) CheckoutRun(ctx context.Context, w Wake) (domain.Run, error) {
	switch w.Trigger {
	case domain.TriggerAssign, domain.TriggerComment, domain.TriggerManual, domain.TriggerHeartbeat, domain.TriggerGate, domain.TriggerFlow, domain.TriggerMemory:
	default:
		return domain.Run{}, domain.Invalid("trigger", fmt.Sprintf("unknown trigger %q", w.Trigger))
	}
	var out domain.Run
	var blocked error
	err := s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: w.CompanyID, ID: w.TaskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if task.PersonaID == nil || *task.PersonaID != w.PersonaID {
			return domain.Invalid("persona_id", "task is not assigned to this persona")
		}
		from := domain.TaskStatus(task.Status)
		if !from.Workable() {
			return domain.Invalid("status", fmt.Sprintf("task status %q is not workable", from))
		}
		p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: w.CompanyID, ID: w.PersonaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		if !domain.PersonaStatus(p.Status).CanWork() {
			return domain.Invalid("persona", fmt.Sprintf("persona status %q cannot work", p.Status))
		}
		if pending, err := t.q.HasPendingGateForTask(ctx, &task.ID); err != nil {
			return fmt.Errorf("check pending gate: %w", err)
		} else if pending {
			return domain.Invalid("gate", "task has a pending gate; a human must decide it first")
		}
		if _, err := t.q.ActiveRunForTask(ctx, task.ID); err == nil {
			return domain.ErrActiveRun
		} else if !isNoRows(err) {
			return fmt.Errorf("check active run: %w", err)
		}
		// Hard stop: the budget check (spent plus the reservation of every open run plus this
		// one) runs, and its pause/alerts commit, before any run is opened.
		co, err := t.q.GetCompanyByID(ctx, w.CompanyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		project, err := t.projectOf(w.CompanyID, task.ProjectID)
		if err != nil {
			return err
		}
		if berr := t.checkBudgets(co, p, project, true); berr != nil {
			blocked = berr
			var rr *reservationRefused
			// refused only by the persona's or company's reservation: keep the safety net off this persona for a
			// while. A project's budget never silences the persona (its other tasks may run).
			if errors.As(berr, &rr) && rr.scope != "project" {
				if err := t.q.SetPersonaBudgetRefused(ctx, store.SetPersonaBudgetRefusedParams{ID: p.ID, Now: &t.now}); err != nil {
					return fmt.Errorf("mark budget refused: %w", err)
				}
			}
			return nil
		}
		if p.BudgetRefusedAt != nil {
			if err := t.q.ClearPersonaBudgetRefused(ctx, store.ClearPersonaBudgetRefusedParams{PersonaID: &p.ID}); err != nil {
				return fmt.Errorf("clear budget refused: %w", err)
			}
		}
		// The model comes from the task (manual) first, then the persona, else the harness default; the
		// subscription that covers it (if any) is fixed now and its weekly guard holds the line.
		model, modelSource := domain.ResolveModel(task.Model, p.Model)
		sub, err := t.subscriptionFor(p, model)
		if err != nil {
			return err
		}
		var subID *domain.ID
		if sub != nil {
			subID = &sub.ID
			if gerr := t.checkWeeklyGuard(co, *sub); gerr != nil {
				if !errors.Is(gerr, domain.ErrSubscriptionGuard) {
					return gerr
				}
				blocked = gerr
				if err := t.q.SetPersonaBudgetRefused(ctx, store.SetPersonaBudgetRefusedParams{ID: p.ID, Now: &t.now}); err != nil {
					return fmt.Errorf("mark budget refused: %w", err)
				}
				return nil
			}
		}
		ws, wsSource := pickWorkspace(task.WorkspaceID, project, p)
		row, err := t.createRun(w.CompanyID, task.ID, p.ID, p.Agent, model, modelSource, subID, ws, wsSource, w.Trigger)
		if err != nil {
			return err
		}
		out = runFromRow(row)
		if from == domain.TaskTodo {
			if err := t.q.SetTaskStatus(ctx, store.SetTaskStatusParams{ID: task.ID, Status: string(domain.TaskInProgress), Now: t.now}); err != nil {
				return fmt.Errorf("set task in_progress: %w", err)
			}
			if err := t.activity(domain.SystemActor, w.CompanyID, "task.status_changed", "task", task.ID,
				map[string]any{"from": from, "to": domain.TaskInProgress, "run_id": row.ID}, ""); err != nil {
				return err
			}
		}
		if err := t.activity(domain.SystemActor, w.CompanyID, "run.queued", "run", row.ID,
			map[string]any{"task_id": task.ID, "persona_id": p.ID, "trigger": w.Trigger}, ""); err != nil {
			return err
		}
		if s.Jobs != nil {
			if err := s.Jobs.ExecuteRunTx(ctx, t.tx, w.CompanyID, row.ID); err != nil {
				return fmt.Errorf("enqueue execute_run: %w", err)
			}
		}
		t.publishRunStatus(out)
		if tv, err := loadTaskView(ctx, t.q, w.CompanyID, task.ID); err == nil {
			t.publish(companyTopic(w.CompanyID), "task.updated", tv)
		}
		return nil
	})
	if err != nil {
		return domain.Run{}, err
	}
	if blocked != nil {
		return domain.Run{}, blocked
	}
	return out, nil
}

// createRun inserts a queued run with its friendly name. The name comes from the run id; when the
// pair is already taken in the company the next variation of the hash is used (see domain.RunName).
// A race on the same name or on the one-active-run-per-task index is ErrActiveRun, so the wake is retried.
func (t *txc) createRun(companyID, taskID, personaID domain.ID, agent, model string, modelSource domain.ModelSource, subID *domain.ID,
	ws *domain.ID, wsSource domain.WorkspaceSource, trigger domain.RunTrigger) (store.Run, error) {
	id := domain.NewID()
	name, err := t.freeRunName(companyID, id)
	if err != nil {
		return store.Run{}, err
	}
	row, err := t.q.CreateRun(t.ctx, store.CreateRunParams{ID: id, CompanyID: companyID, TaskID: taskID,
		PersonaID: personaID, Trigger: string(trigger), Agent: agent, Model: model, ModelSource: string(modelSource), SubscriptionID: subID, Name: name,
		WorkspaceID: ws, WorkspaceSource: string(wsSource), Now: t.now})
	if err != nil {
		if isUnique(err) {
			return store.Run{}, domain.ErrActiveRun
		}
		return store.Run{}, fmt.Errorf("create run: %w", err)
	}
	return row, nil
}

// freeRunName picks the first name for id not yet used in the company.
func (t *txc) freeRunName(companyID, id domain.ID) (string, error) {
	cands := make([]string, domain.RunNameAttempts)
	for i := range cands {
		cands[i] = domain.RunName(id, i)
	}
	taken, err := t.q.TakenRunNames(t.ctx, store.TakenRunNamesParams{CompanyID: companyID, Names: cands})
	if err != nil {
		return "", fmt.Errorf("taken run names: %w", err)
	}
	used := make(map[string]bool, len(taken))
	for _, n := range taken {
		used[n] = true
	}
	for _, c := range cands {
		if !used[c] {
			return c, nil
		}
	}
	for n := 0; n < 50; n++ { // espaço da empresa quase esgotado: sufixo numérico
		fb := domain.RunNameFallback(id, n)
		ts, err := t.q.TakenRunNames(t.ctx, store.TakenRunNamesParams{CompanyID: companyID, Names: []string{fb}})
		if err != nil {
			return "", fmt.Errorf("taken run names: %w", err)
		}
		if len(ts) == 0 {
			return fb, nil
		}
	}
	return "", fmt.Errorf("sem nome livre para a run %s", id)
}

// BackfillRunNames gives a friendly name to the runs created before names existed. Idempotent;
// returns how many runs were named. Called once at boot.
func (s *Service) BackfillRunNames(ctx context.Context) (int, error) {
	total := 0
	for {
		rows, err := s.read().ListRunsWithoutName(ctx, 500)
		if err != nil {
			return total, fmt.Errorf("list runs without name: %w", err)
		}
		if len(rows) == 0 {
			return total, nil
		}
		named := 0
		err = s.withTx(ctx, func(t *txc) error {
			named = 0
			for _, r := range rows {
				name, err := t.freeRunName(r.CompanyID, r.ID)
				if err != nil {
					return err
				}
				n, err := t.q.SetRunName(ctx, store.SetRunNameParams{ID: r.ID, Name: name})
				if err != nil {
					return fmt.Errorf("set run name: %w", err)
				}
				named += int(n)
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += named
		if named == 0 {
			return total, nil
		}
	}
}

// LoadRunContext loads what the executor needs (run, company, persona, task, thread, workspace).
func (s *Service) LoadRunContext(ctx context.Context, companyID, runID domain.ID) (RunContext, error) {
	q := s.read()
	run, err := q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
	if err != nil {
		return RunContext{}, mapErr(err, "get run")
	}
	company, err := q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return RunContext{}, mapErr(err, "get company")
	}
	persona, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: run.PersonaID})
	if err != nil {
		return RunContext{}, mapErr(err, "get persona")
	}
	task, err := loadTaskView(ctx, q, companyID, run.TaskID)
	if err != nil {
		return RunContext{}, err
	}
	comments, err := q.ListComments(ctx, store.ListCommentsParams{CompanyID: companyID, TaskID: run.TaskID})
	if err != nil {
		return RunContext{}, fmt.Errorf("list comments: %w", err)
	}
	rc := RunContext{Run: runFromRow(run), Company: companyFromRow(company), Persona: personaFromRow(persona),
		Task: task, Comments: make([]domain.Comment, len(comments))}
	if len(run.PendingResult) > 0 {
		var in FinishRunInput
		if err := json.Unmarshal(run.PendingResult, &in); err != nil {
			return RunContext{}, fmt.Errorf("decode pending result: %w", err)
		}
		rc.PendingResult = &in
	}
	for i, c := range comments {
		rc.Comments[i] = commentFromRow(c)
	}
	if rc.Run.Trigger == domain.TriggerReview {
		rv, err := q.GetReviewByRunID(ctx, &runID)
		if err != nil && !isNoRows(err) {
			return RunContext{}, fmt.Errorf("get review: %w", err)
		}
		if err == nil {
			ap, aerr := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: rv.AuthorPersonaID})
			if aerr != nil {
				return RunContext{}, mapErr(aerr, "author persona")
			}
			rc.Review = &ReviewContext{ID: rv.ID, Artifact: rv.Artifact, ArtifactSHA256: rv.ArtifactSha256,
				RequestedStatus: domain.TaskStatus(rv.RequestedStatus), AuthorName: ap.Name, Independent: rv.ReviewerPersonaID != nil}
		}
	}
	// The workspace was chosen when the run opened (and recorded on it); runs older than that, or whose workspace
	// was deleted since, resolve it again from the task, the project and the persona.
	wsID := run.WorkspaceID
	if wsID == nil {
		var project *store.Project
		if task.ProjectID != nil {
			pr, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *task.ProjectID})
			if err != nil && !isNoRows(err) {
				return RunContext{}, fmt.Errorf("get project: %w", err)
			}
			if err == nil {
				project = &pr
			}
		}
		wsID, _ = pickWorkspace(task.WorkspaceID, project, persona)
	}
	if wsID != nil {
		ws, err := q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *wsID})
		if err == nil {
			w := workspaceFromRow(ws)
			rc.Workspace = &w
		} else if !isNoRows(err) {
			return RunContext{}, fmt.Errorf("get workspace: %w", err)
		}
	}
	return rc, nil
}

// MarkRunStarted sets running, started_at, trace_id, handoff_in; persona → running.
func (s *Service) MarkRunStarted(ctx context.Context, companyID, runID domain.ID, traceID, handoffIn string) (domain.Run, error) {
	var out domain.Run
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.MarkRunStarted(ctx, store.MarkRunStartedParams{CompanyID: companyID, ID: runID,
			TraceID: traceID, HandoffIn: handoffIn, Now: &t.now})
		if isNoRows(err) {
			cur, gerr := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: runID})
			if gerr != nil {
				return mapErr(gerr, "get run")
			}
			return domain.Invalid("run", fmt.Sprintf("run is %s, not queued", cur.Status))
		}
		if err != nil {
			return fmt.Errorf("mark run started: %w", err)
		}
		out = runFromRow(row)
		if err := t.q.SetPersonaRunning(ctx, store.SetPersonaRunningParams{ID: row.PersonaID, Now: t.now}); err != nil {
			return fmt.Errorf("set persona running: %w", err)
		}
		if err := t.activity(domain.PersonaActor(row.PersonaID), companyID, "run.started", "run", runID,
			map[string]any{"task_id": row.TaskID}, ""); err != nil {
			return err
		}
		t.publishRunStatus(out)
		return nil
	})
	if err != nil {
		return domain.Run{}, err
	}
	return out, nil
}

var validEventKinds = map[domain.RunEventKind]bool{
	domain.EventSystem: true, domain.EventAssistant: true, domain.EventThinking: true, domain.EventToolUse: true,
	domain.EventToolResult: true, domain.EventStdout: true, domain.EventStderr: true, domain.EventResult: true,
	domain.EventStatus: true,
}

// AppendRunEvent stores an event with the next seq and publishes it on the broker
// (topic events.RunTopic, type "run.event") after commit.
func (s *Service) AppendRunEvent(ctx context.Context, companyID, runID domain.ID, e adapter.Event) (domain.RunEvent, error) {
	if !validEventKinds[e.Kind] {
		return domain.RunEvent{}, domain.Invalid("kind", fmt.Sprintf("unknown run event kind %q", e.Kind))
	}
	now := s.Now().UTC()
	occurred := e.OccurredAt.UTC()
	if e.OccurredAt.IsZero() {
		occurred = now
	}
	var payload []byte
	if len(e.Payload) > 0 {
		if !json.Valid(e.Payload) {
			return domain.RunEvent{}, domain.Invalid("payload", "not valid JSON")
		}
		payload = e.Payload
	}
	q := s.read()
	var (
		row store.RunEvent
		err error
	)
	// seq comes from runs.next_event_seq (UPDATE ... RETURNING), which serializes concurrent writers.
	row, err = q.AppendRunEvent(ctx, store.AppendRunEventParams{ID: domain.NewID(), Kind: string(e.Kind),
		Text: e.Text, Payload: payload, OccurredAt: occurred, Now: now, CompanyID: companyID, RunID: runID})
	if err != nil {
		return domain.RunEvent{}, mapErr(err, "append run event")
	}
	ev := runEventFromRow(row)
	if s.Broker != nil {
		s.Broker.Publish(events.Message{Topic: events.RunTopic(runID.String()), Type: "run.event", Data: ev})
	}
	return ev, nil
}

// SavePendingResult stores the outcome of a running run before FinishRun is attempted, so a
// finalization that keeps failing can be retried later without executing the harness again.
func (s *Service) SavePendingResult(ctx context.Context, companyID, runID domain.ID, in FinishRunInput) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal pending result: %w", err)
	}
	if err := s.read().SetRunPendingResult(ctx, store.SetRunPendingResultParams{CompanyID: companyID, ID: runID, PendingResult: raw}); err != nil {
		return fmt.Errorf("save pending result: %w", err)
	}
	return nil
}

// FinishRun records the result. On done with a non-empty summary, posts it as a persona
// comment on the task. Persona → idle (error only after personaMaxFailures failures in a
// row; the last error is kept). A human comment posted while the run was active re-enqueues
// a Wake (trigger comment) in the same tx. Writes activity + a
// memory_outbox fact (run.finished) and enqueues its delivery. Publishes run.status.
func (s *Service) FinishRun(ctx context.Context, companyID, runID domain.ID, in FinishRunInput) (domain.Run, error) {
	if !in.Status.Valid() || !in.Status.Terminal() {
		return domain.Run{}, domain.Invalid("status", fmt.Sprintf("%q is not a terminal run status", in.Status))
	}
	var out domain.Run
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.lockTaskOfReviewRun(companyID, runID); err != nil {
			return err
		}
		cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		if domain.RunStatus(cur.Status).Terminal() {
			out = runFromRow(cur) // already finished: idempotent
			return nil
		}
		res := in.Result
		code := i32(res.ExitCode)
		model := cur.Model
		if res.Model != "" {
			model = res.Model
		}
		cr, err := t.costFor(model, res, t.now)
		if err != nil {
			return err
		}
		cost, source, fallback := cr.cost, cr.source, cr.fallback
		row, err := t.q.FinishRun(ctx, store.FinishRunParams{CompanyID: companyID, ID: runID, Status: string(in.Status),
			ExitCode: &code, Error: res.Error, Summary: res.Summary, CostUsd: cost, CostSource: string(source), ListCostUsd: cr.list,
			InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens,
			CacheReadTokens: res.Usage.CacheReadTokens, CacheCreationTokens: res.Usage.CacheCreationTokens,
			SessionRef: res.SessionRef, Model: model, Now: &t.now})
		if err != nil {
			return mapErr(err, "finish run")
		}
		out = runFromRow(row)
		view, err := loadRunView(ctx, t.q, companyID, runID)
		if err != nil {
			return err
		}
		pactor := domain.PersonaActor(row.PersonaID)

		isReview := domain.RunTrigger(row.Trigger) == domain.TriggerReview
		isMemory := domain.RunTrigger(row.Trigger) == domain.TriggerMemory
		// A review run's report already lives on the gate: it is not posted as a persona comment.
		if in.Status == domain.RunDone && !isReview && strings.TrimSpace(res.Summary) != "" {
			c, err := t.q.CreateComment(ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID,
				TaskID: row.TaskID, AuthorType: string(domain.ActorPersona), AuthorID: &row.PersonaID, RunID: &runID,
				Body: res.Summary, EventID: ptr("run.summary:" + runID.String()), Now: t.now})
			if err != nil {
				return mapErr(err, "post run summary")
			}
			if err := t.activity(pactor, companyID, "comment.added", "task", row.TaskID,
				map[string]any{"comment_id": c.ID, "run_id": runID}, ""); err != nil {
				return err
			}
		}

		if err := t.personaAfterRun(row.PersonaID, in.Status, res.Error); err != nil {
			return err
		}
		if isReview {
			if err := t.reviewRunEnded(row, in.Status, res.Summary, res.Error); err != nil {
				return err
			}
		}
		if isMemory {
			if err := t.memoryRunEnded(row, in.Status, res.Summary); err != nil {
				return err
			}
		}
		if cost != nil {
			co, err := t.q.GetCompanyByID(ctx, companyID)
			if err != nil {
				return mapErr(err, "get company")
			}
			p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: row.PersonaID})
			if err != nil {
				return mapErr(err, "get persona")
			}
			if fallback {
				if err := t.noteUnknownModel(co, model, runID); err != nil {
					return err
				}
			}
			ft, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: row.TaskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			project, err := t.projectOf(companyID, ft.ProjectID)
			if err != nil {
				return err
			}
			if err := t.checkBudgets(co, p, project, false); err != nil {
				return err
			}
		}

		verb := "run.finished"
		if in.Status == domain.RunCancelled {
			verb = "run.cancelled"
		}
		if err := t.activity(pactor, companyID, verb, "run", runID, map[string]any{
			"status": in.Status, "task_id": row.TaskID, "cost_usd": cost, "cost_source": source, "error": res.Error}, ""); err != nil {
			return err
		}

		task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: row.TaskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		// A curadoria de memória não se documenta na própria memória (nem a task, nem a run).
		if !isMemory {
			memWS := row.WorkspaceID
			if memWS == nil {
				memWS = task.WorkspaceID
			}
			if err := t.memoryFact(companyID, memWS, runFact(view, in.Status, res, t.now),
				"run.finished:"+runID.String()); err != nil {
				return err
			}
		}
		if !isReview && !isMemory {
			if err := t.wakeForMissedComment(companyID, task, row); err != nil {
				return err
			}
		}
		t.publishRunStatus(out)
		if tv, err := loadTaskView(ctx, t.q, companyID, row.TaskID); err == nil {
			t.publish(companyTopic(companyID), "task.updated", tv)
		}
		return nil
	})
	if err != nil {
		return domain.Run{}, err
	}
	return out, nil
}

func ptr[T any](v T) *T { return &v }

// personaMaxFailures is how many failed runs in a row put a persona in error.
const personaMaxFailures = 3

// personaAfterRun returns the persona to idle after a run and records failures. Only
// failed runs count toward error; success resets the counter; cancelled, interrupted and
// expired runs leave it alone.
func (t *txc) personaAfterRun(personaID domain.ID, status domain.RunStatus, errMsg string) error {
	outcome := "neutral"
	switch status {
	case domain.RunDone:
		outcome = "ok"
	case domain.RunFailed:
		outcome = "fail"
		if strings.TrimSpace(errMsg) == "" {
			errMsg = "execução falhou"
		}
	}
	if err := t.q.SetPersonaAfterRun(t.ctx, store.SetPersonaAfterRunParams{ID: personaID, Outcome: outcome,
		MaxFailures: personaMaxFailures, LastError: errMsg, Now: t.now}); err != nil {
		return fmt.Errorf("update persona after run: %w", err)
	}
	return nil
}

// wakeForMissedComment re-enqueues a Wake when a human commented on the task, or decided one
// of its gates, while the run was active (the run's prompt did not include it). A run the user cancelled is not retried.
func (t *txc) wakeForMissedComment(companyID domain.ID, task store.Task, run store.Run) error {
	if run.StartedAt == nil || run.Status == string(domain.RunCancelled) || run.Status == string(domain.RunInterrupted) {
		return nil
	}
	found, err := t.q.HasHumanCommentSince(t.ctx, store.HasHumanCommentSinceParams{CompanyID: companyID, TaskID: task.ID, Since: *run.StartedAt})
	if err != nil {
		return fmt.Errorf("check missed comment: %w", err)
	}
	if found {
		return t.wake(companyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerComment, false)
	}
	// a gate decided while the run was active: the run did not see the decision
	decided, err := t.q.HasGateDecidedSince(t.ctx, store.HasGateDecidedSinceParams{CompanyID: companyID, TaskID: &task.ID, Since: run.StartedAt})
	if err != nil {
		return fmt.Errorf("check missed gate decision: %w", err)
	}
	if !decided {
		return nil
	}
	return t.wake(companyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerGate, false)
}

func runFact(v RunView, status domain.RunStatus, res adapter.Result, now time.Time) memory.Fact {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run %s — %s\n\n", v.TaskKey, v.TaskTitle)
	fmt.Fprintf(&b, "- Task: %s\n- Persona: %s\n- Status: %s\n", v.TaskKey, v.PersonaName, status)
	if v.CostUSD != nil {
		est := ""
		if v.CostSource == domain.CostEstimated {
			est = " (estimado)"
		}
		fmt.Fprintf(&b, "- Custo: US$ %.4f%s\n", *v.CostUSD, est)
	} else {
		b.WriteString("- Custo: n/d\n")
	}
	fmt.Fprintf(&b, "- Run: %s\n", v.ID)
	if res.Error != "" {
		fmt.Fprintf(&b, "- Erro: %s\n", res.Error)
	}
	if strings.TrimSpace(res.Summary) != "" {
		fmt.Fprintf(&b, "\n## Resumo\n\n%s\n", res.Summary)
	}
	return memory.Fact{Kind: "run.finished",
		Path:  fmt.Sprintf("hanko/runs/%s-%s-%s.md", now.Format("2006-01-02"), v.TaskKey, v.ID.String()[:8]),
		Title: fmt.Sprintf("Run %s (%s)", v.TaskKey, status), Body: b.String(), Tags: []string{"hanko", "run"}}
}

// InterruptOrphanRuns marks runs left queued/running by a dead process as interrupted.
// Called at boot (single-instance v0.1). Returns how many were changed.
func (s *Service) InterruptOrphanRuns(ctx context.Context) (int, error) {
	n := 0
	err := s.withTx(ctx, func(t *txc) error {
		rows, err := t.q.InterruptActiveRuns(ctx, &t.now)
		if err != nil {
			return fmt.Errorf("interrupt runs: %w", err)
		}
		for _, r := range rows {
			if err := t.personaAfterRun(r.PersonaID, domain.RunInterrupted, ""); err != nil {
				return err
			}
			switch domain.RunTrigger(r.Trigger) {
			case domain.TriggerReview:
				if err := t.reviewRunEnded(r, domain.RunInterrupted, "", r.Error); err != nil {
					return err
				}
			case domain.TriggerMemory:
				if err := t.memoryRunEnded(r, domain.RunInterrupted, ""); err != nil {
					return err
				}
			}
			if err := t.activity(domain.SystemActor, r.CompanyID, "run.finished", "run", r.ID,
				map[string]any{"status": domain.RunInterrupted, "task_id": r.TaskID, "reason": "orphan"}, ""); err != nil {
				return err
			}
			t.publishRunStatus(runFromRow(r))
			// The work was cut short: wake the assignee so the task is not left waiting for the next scan.
			task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: r.CompanyID, ID: r.TaskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			if err := t.wake(r.CompanyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), domain.TriggerHeartbeat, true); err != nil {
				return err
			}
		}
		n = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// budgetRefusedQuiet is how long a persona whose checkout was refused only by the budget reservation
// stays out of the heartbeat's wake candidates (an explicit human wake still tries).
const budgetRefusedQuiet = 10 * time.Minute

// wakeStaleAfter is how long a task may sit changed and unattended before the heartbeat wakes its
// assignee regardless of heartbeat_interval_sec (safety net for a wake the queue lost).
const wakeStaleAfter = time.Minute

// WakeCandidates lists assigned workable tasks with no active run and no pending gate whose
// persona CanWork and either has heartbeat_interval_sec > 0 elapsed since its last run on the
// task, or (safety net, whatever the interval) that got an unseen change more than a minute ago
// (status or assignment by a non-persona actor, or a human comment) which no run covers (none ran
// or is running at that instant or later): the wake that should have followed it was lost. A
// persona's own writes and title/description/priority edits never count, so no run loop. Personas
// whose last checkout was refused by the budget reservation sit out for budgetRefusedQuiet.
func (s *Service) WakeCandidates(ctx context.Context, limit int) ([]WakeCandidate, error) {
	now := s.Now().UTC()
	rows, err := s.read().ListWakeCandidates(ctx, store.ListWakeCandidatesParams{Now: now, StaleBefore: now.Add(-wakeStaleAfter),
		BudgetCutoff: now.Add(-budgetRefusedQuiet), Lim: i32(clamp(limit, 100, 1000))})
	if err != nil {
		return nil, fmt.Errorf("wake candidates: %w", err)
	}
	out := make([]WakeCandidate, 0, len(rows))
	for _, r := range rows {
		if r.PersonaID == nil {
			continue
		}
		out = append(out, WakeCandidate{CompanyID: r.CompanyID, PersonaID: *r.PersonaID, TaskID: r.TaskID})
	}
	return out, nil
}

// BumpWakeRequestTx records, inside tx, that a wake for (persona, task) was requested. The
// heartbeat calls it for every wake it enqueues; the counter lets a worker notice a request that
// the queue coalesced into the job it was already running (see WakeSeq).
func BumpWakeRequestTx(ctx context.Context, tx pgx.Tx, w Wake) error {
	if err := store.New(tx).BumpWakeRequest(ctx, store.BumpWakeRequestParams{CompanyID: w.CompanyID, PersonaID: w.PersonaID, TaskID: w.TaskID}); err != nil {
		return fmt.Errorf("bump wake request: %w", err)
	}
	return nil
}

// WakeSeq is how many wakes were requested for (persona, task) so far. A worker reads it before
// checking the task out and, if it changed by the time the checkout opened no run, retries:
// the request that moved it was coalesced into the running job and would otherwise be lost.
func (s *Service) WakeSeq(ctx context.Context, personaID, taskID domain.ID) (int64, error) {
	n, err := s.read().GetWakeSeq(ctx, store.GetWakeSeqParams{PersonaID: personaID, TaskID: taskID})
	if err != nil {
		return 0, fmt.Errorf("wake seq: %w", err)
	}
	return n, nil
}
