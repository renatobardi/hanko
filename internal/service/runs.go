package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	p := store.ListRunViewsParams{CompanyID: companyID, TaskID: f.TaskID, PersonaID: f.PersonaID,
		Lim: int32(clamp(f.Limit, 50, 500))}
	if len(f.Statuses) > 0 {
		p.Statuses = make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			if !st.Valid() {
				return nil, domain.Invalid("status", fmt.Sprintf("unknown run status %q", st))
			}
			p.Statuses[i] = string(st)
		}
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
	return loadRunView(ctx, s.read(), companyID, runID)
}

// ListRunEvents returns events with seq > afterSeq, ascending.
func (s *Service) ListRunEvents(ctx context.Context, companyID, runID domain.ID, afterSeq int64, limit int) ([]domain.RunEvent, error) {
	rows, err := s.read().ListRunEvents(ctx, store.ListRunEventsParams{CompanyID: companyID, RunID: runID,
		AfterSeq: afterSeq, Lim: int32(clamp(limit, 200, 1000))})
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	out := make([]domain.RunEvent, len(rows))
	for i, r := range rows {
		out[i] = runEventFromRow(r)
	}
	return out, nil
}

// CancelRun marks a queued run cancelled; for a running run it records the request and
// publishes run.cancel on the broker (the executor cancels its context).
func (s *Service) CancelRun(ctx context.Context, actor domain.Actor, companyID, runID domain.ID) (RunView, error) {
	var out RunView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		switch domain.RunStatus(cur.Status) {
		case domain.RunQueued:
			row, err := t.q.CancelQueuedRun(ctx, store.CancelQueuedRunParams{CompanyID: companyID, ID: runID, Now: &t.now})
			if err != nil {
				return mapErr(err, "cancel run")
			}
			if err := t.activity(actor, companyID, "run.cancelled", "run", runID, map[string]any{"was": "queued"}, ""); err != nil {
				return err
			}
			t.publishRunStatus(runFromRow(row))
		case domain.RunRunning:
			if err := t.activity(actor, companyID, "run.cancelled", "run", runID, map[string]any{"was": "running", "requested": true}, ""); err != nil {
				return err
			}
			t.publish(events.RunTopic(runID.String()), "run.cancel", runID)
		default:
			return domain.Invalid("run", fmt.Sprintf("run already %s", cur.Status))
		}
		out, err = loadRunView(ctx, t.q, companyID, runID)
		return err
	})
	return out, err
}

// CheckoutRun atomically creates a queued run for (task, persona) if the task has no
// active run, the task is workable and assigned to that persona, and the persona CanWork;
// enqueues ExecuteRun in the same tx. Returns domain.ErrActiveRun when occupied.
// Moves task todo → in_progress.
func (s *Service) CheckoutRun(ctx context.Context, w Wake) (domain.Run, error) {
	switch w.Trigger {
	case domain.TriggerAssign, domain.TriggerComment, domain.TriggerManual, domain.TriggerHeartbeat:
	default:
		return domain.Run{}, domain.Invalid("trigger", fmt.Sprintf("unknown trigger %q", w.Trigger))
	}
	var out domain.Run
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
		if _, err := t.q.ActiveRunForTask(ctx, task.ID); err == nil {
			return domain.ErrActiveRun
		} else if !isNoRows(err) {
			return fmt.Errorf("check active run: %w", err)
		}
		row, err := t.q.CreateRun(ctx, store.CreateRunParams{ID: domain.NewID(), CompanyID: w.CompanyID, TaskID: task.ID,
			PersonaID: p.ID, Trigger: string(w.Trigger), Agent: p.Agent, Model: p.Model, Now: t.now})
		if err != nil {
			if isUnique(err) {
				return domain.ErrActiveRun
			}
			return fmt.Errorf("create run: %w", err)
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
	return out, nil
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
	for i, c := range comments {
		rc.Comments[i] = commentFromRow(c)
	}
	wsID := task.WorkspaceID
	if wsID == nil {
		wsID = persona.WorkspaceID
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
	for attempt := 0; attempt < 5; attempt++ { // concurrent writers may collide on (run_id, seq)
		row, err = q.AppendRunEvent(ctx, store.AppendRunEventParams{ID: domain.NewID(), Kind: string(e.Kind),
			Text: e.Text, Payload: payload, OccurredAt: occurred, Now: now, CompanyID: companyID, RunID: runID})
		if err == nil || !isUnique(err) {
			break
		}
	}
	if err != nil {
		return domain.RunEvent{}, mapErr(err, "append run event")
	}
	ev := runEventFromRow(row)
	if s.Broker != nil {
		s.Broker.Publish(events.Message{Topic: events.RunTopic(runID.String()), Type: "run.event", Data: ev})
	}
	return ev, nil
}

// FinishRun records the result. On done with a non-empty summary, posts it as a persona
// comment on the task. Persona → idle (or error on failure). Writes activity + a
// memory_outbox fact (run.finished) and enqueues its delivery. Publishes run.status.
func (s *Service) FinishRun(ctx context.Context, companyID, runID domain.ID, in FinishRunInput) (domain.Run, error) {
	if !in.Status.Valid() || !in.Status.Terminal() {
		return domain.Run{}, domain.Invalid("status", fmt.Sprintf("%q is not a terminal run status", in.Status))
	}
	var out domain.Run
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: runID})
		if err != nil {
			return mapErr(err, "get run")
		}
		if domain.RunStatus(cur.Status).Terminal() {
			out = runFromRow(cur) // already finished: idempotent
			return nil
		}
		res := in.Result
		code := int32(res.ExitCode)
		model := cur.Model
		if res.Model != "" {
			model = res.Model
		}
		row, err := t.q.FinishRun(ctx, store.FinishRunParams{CompanyID: companyID, ID: runID, Status: string(in.Status),
			ExitCode: &code, Error: res.Error, Summary: res.Summary, CostUsd: res.CostUSD,
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

		if in.Status == domain.RunDone && strings.TrimSpace(res.Summary) != "" {
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

		pstatus := domain.PersonaIdle
		if in.Status == domain.RunFailed {
			pstatus = domain.PersonaError
		}
		if err := t.q.SetPersonaAfterRun(ctx, store.SetPersonaAfterRunParams{ID: row.PersonaID, Status: string(pstatus), Now: t.now}); err != nil {
			return fmt.Errorf("update persona after run: %w", err)
		}

		verb := "run.finished"
		if in.Status == domain.RunCancelled {
			verb = "run.cancelled"
		}
		if err := t.activity(pactor, companyID, verb, "run", runID, map[string]any{
			"status": in.Status, "task_id": row.TaskID, "cost_usd": res.CostUSD, "error": res.Error}, ""); err != nil {
			return err
		}

		task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: row.TaskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if err := t.memoryFact(companyID, task.WorkspaceID, runFact(view, in.Status, res, t.now),
			"run.finished:"+runID.String()); err != nil {
			return err
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

func runFact(v RunView, status domain.RunStatus, res adapter.Result, now time.Time) memory.Fact {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run %s — %s\n\n", v.TaskKey, v.TaskTitle)
	fmt.Fprintf(&b, "- Task: %s\n- Persona: %s\n- Status: %s\n", v.TaskKey, v.PersonaName, status)
	if res.CostUSD != nil {
		fmt.Fprintf(&b, "- Custo: US$ %.4f\n", *res.CostUSD)
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
			if err := t.q.SetPersonaAfterRun(ctx, store.SetPersonaAfterRunParams{ID: r.PersonaID, Status: string(domain.PersonaIdle), Now: t.now}); err != nil {
				return fmt.Errorf("update persona after interrupt: %w", err)
			}
			if err := t.activity(domain.SystemActor, r.CompanyID, "run.finished", "run", r.ID,
				map[string]any{"status": domain.RunInterrupted, "task_id": r.TaskID, "reason": "orphan"}, ""); err != nil {
				return err
			}
			t.publishRunStatus(runFromRow(r))
		}
		n = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// WakeCandidates lists assigned workable tasks with no active run whose persona CanWork
// and has heartbeat_interval_sec > 0 elapsed since its last run on that task.
func (s *Service) WakeCandidates(ctx context.Context, limit int) ([]WakeCandidate, error) {
	rows, err := s.read().ListWakeCandidates(ctx, store.ListWakeCandidatesParams{Now: ptr(s.Now().UTC()), Lim: int32(clamp(limit, 100, 1000))})
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
