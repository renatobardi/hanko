package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

func loadTaskView(ctx context.Context, q *store.Queries, companyID, id domain.ID) (TaskView, error) {
	r, err := q.GetTaskView(ctx, store.GetTaskViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return TaskView{}, mapErr(err, "get task")
	}
	return taskView(r.Task, r.TaskPrefix, r.PersonaName, r.ProjectName, r.ActiveRunID), nil
}

func validateTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if n := utf8.RuneCountInString(title); n < 1 || n > 500 {
		return "", domain.Invalid("title", "must have 1-500 characters")
	}
	return title, nil
}

// CreateTask allocates the next task number atomically. If assigned to a persona that
// CanWork and status is Workable, enqueues a Wake (trigger assign) in the same tx.
// Same EventID twice → returns the existing task (idempotent).
func (s *Service) CreateTask(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateTaskInput) (TaskView, error) {
	title, err := validateTitle(in.Title)
	if err != nil {
		return TaskView{}, err
	}
	status := in.Status
	if status == "" {
		status = domain.TaskTodo
	}
	if !status.Valid() {
		return TaskView{}, domain.Invalid("status", fmt.Sprintf("unknown status %q", status))
	}
	prio := in.Priority
	if prio == "" {
		prio = domain.PriorityMedium
	}
	if !prio.Valid() {
		return TaskView{}, domain.Invalid("priority", fmt.Sprintf("unknown priority %q", prio))
	}

	var out TaskView
	run := func() error {
		return s.withTx(ctx, func(t *txc) error {
			if in.EventID != "" {
				ev, err := t.q.GetActivityByEventID(ctx, store.GetActivityByEventIDParams{CompanyID: companyID, EventID: &in.EventID})
				if err == nil {
					if ev.Verb != "task.created" {
						return fmt.Errorf("event_id %q: %w", in.EventID, domain.ErrConflict)
					}
					out, err = loadTaskView(ctx, t.q, companyID, ev.SubjectID)
					return err
				}
				if !isNoRows(err) {
					return fmt.Errorf("lookup event: %w", err)
				}
			}

			wsID := in.WorkspaceID
			if wsID != nil {
				if _, err := t.q.GetWorkspaceByID(ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *wsID}); err != nil {
					return domain.Invalid("workspace_id", "workspace not found in company")
				}
			}
			if in.ProjectID != nil {
				pr, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID})
				if err != nil {
					return domain.Invalid("project_id", "project not found in company")
				}
				if wsID == nil {
					wsID = pr.WorkspaceID
				}
			}
			if in.PersonaID != nil {
				p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.PersonaID})
				if err != nil {
					return domain.Invalid("persona_id", "persona not found in company")
				}
				if wsID == nil {
					wsID = p.WorkspaceID
				}
				if err := t.checkDelegation(actor, companyID, in.PersonaID); err != nil {
					return err
				}
			}
			if in.ParentID != nil {
				if _, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *in.ParentID}); err != nil {
					return domain.Invalid("parent_id", "task not found in company")
				}
			}

			num, err := t.q.AllocateTaskNumber(ctx, companyID)
			if err != nil {
				return mapErr(err, "company")
			}
			row, err := t.q.CreateTask(ctx, store.CreateTaskParams{
				ID: domain.NewID(), CompanyID: companyID, Number: num.Number, Title: title, Description: in.Description,
				Status: string(status), Priority: string(prio), PersonaID: in.PersonaID, ProjectID: in.ProjectID,
				WorkspaceID: wsID, ParentID: in.ParentID, CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
			if err != nil {
				return mapErr(err, "create task")
			}
			key := domain.TaskKey(num.TaskPrefix, num.Number)
			if err := t.activity(actor, companyID, "task.created", "task", row.ID, map[string]any{
				"key": key, "title": title, "status": status, "priority": prio, "persona_id": in.PersonaID,
			}, in.EventID); err != nil {
				return err
			}
			if err := t.memoryFact(companyID, wsID, taskFact("task.created", key, title, in.Description, status, prio, ""),
				"task.created:"+row.ID.String()); err != nil {
				return err
			}
			if err := t.wake(companyID, row.ID, in.PersonaID, status, domain.TriggerAssign, true); err != nil {
				return err
			}
			out, err = loadTaskView(ctx, t.q, companyID, row.ID)
			if err != nil {
				return err
			}
			t.publish(companyTopic(companyID), "task.updated", out)
			return nil
		})
	}
	err = run()
	if err != nil && in.EventID != "" && isUniqueOn(err, "activity_event_uq") {
		err = run() // lost the race on event_id: the second pass returns the winner
	}
	if err != nil {
		return TaskView{}, err
	}
	return out, nil
}

func taskFact(kind, key, title, desc string, status domain.TaskStatus, prio domain.Priority, extra string) memory.Fact {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n- Status: %s\n- Prioridade: %s\n", key, title, status, prio)
	if extra != "" {
		b.WriteString(extra)
	}
	if strings.TrimSpace(desc) != "" {
		fmt.Fprintf(&b, "\n## Descrição\n\n%s\n", desc)
	}
	return memory.Fact{Kind: kind, Path: "hanko/tasks/" + key + ".md", Title: key + " — " + title,
		Body: b.String(), Tags: []string{"hanko", "task"}}
}

func (s *Service) ListTasks(ctx context.Context, companyID domain.ID, f TaskFilter) ([]TaskView, error) {
	p := store.ListTaskViewsParams{CompanyID: companyID, PersonaID: f.PersonaID, ProjectID: f.ProjectID,
		Lim: int32(clamp(f.Limit, 100, 500)), Off: int32(max(f.Offset, 0))}
	if len(f.Statuses) > 0 {
		p.Statuses = make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			if !st.Valid() {
				return nil, domain.Invalid("status", fmt.Sprintf("unknown status %q", st))
			}
			p.Statuses[i] = string(st)
		}
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		q = escapeLike(q)
		p.Q = &q
	}
	rows, err := s.read().ListTaskViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]TaskView, len(rows))
	for i, r := range rows {
		out[i] = taskView(r.Task, r.TaskPrefix, r.PersonaName, r.ProjectName, r.ActiveRunID)
	}
	return out, nil
}

// GetTask resolves ref as UUID or task key ("HNK-12").
func (s *Service) GetTask(ctx context.Context, companyID domain.ID, ref string) (TaskView, error) {
	q := s.read()
	if id, err := uuid.Parse(ref); err == nil {
		return loadTaskView(ctx, q, companyID, id)
	}
	prefix, num, ok := domain.ParseTaskKey(ref)
	if !ok {
		return TaskView{}, notFound("get task " + strconv.Quote(ref))
	}
	r, err := q.GetTaskViewByNumber(ctx, store.GetTaskViewByNumberParams{CompanyID: companyID, Number: num})
	if err != nil {
		return TaskView{}, mapErr(err, "get task")
	}
	if r.TaskPrefix != prefix {
		return TaskView{}, notFound("get task " + strconv.Quote(ref))
	}
	return taskView(r.Task, r.TaskPrefix, r.PersonaName, r.ProjectName, r.ActiveRunID), nil
}

// UpdateTask applies changes; (re)assignment to a workable task enqueues a Wake.
// Closing a task (done/cancelled) sets closed_at and records a memory fact. Closing,
// unassigning or reassigning a task cancels its active run (queued → cancelled in the tx;
// running → run.cancel is published after commit).
func (s *Service) UpdateTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in UpdateTaskInput) (TaskView, error) {
	if in.Title != nil {
		if _, err := validateTitle(*in.Title); err != nil {
			return TaskView{}, err
		}
	}
	if in.Status != nil && !in.Status.Valid() {
		return TaskView{}, domain.Invalid("status", fmt.Sprintf("unknown status %q", *in.Status))
	}
	if in.Priority != nil && !in.Priority.Valid() {
		return TaskView{}, domain.Invalid("priority", fmt.Sprintf("unknown priority %q", *in.Priority))
	}
	if in.Unassign && in.PersonaID != nil {
		return TaskView{}, domain.Invalid("persona_id", "cannot assign and unassign at once")
	}

	var out TaskView
	run := func() error {
		return s.withTx(ctx, func(t *txc) error {
			cur, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			if in.EventID != "" {
				_, err := t.q.GetActivityByEventID(ctx, store.GetActivityByEventIDParams{CompanyID: companyID, EventID: &in.EventID})
				if err == nil {
					out, err = loadTaskView(ctx, t.q, companyID, taskID)
					return err
				}
				if !isNoRows(err) {
					return fmt.Errorf("lookup event: %w", err)
				}
			}

			p := store.UpdateTaskParams{CompanyID: companyID, ID: taskID, Title: cur.Title, Description: cur.Description,
				Status: cur.Status, Priority: cur.Priority, PersonaID: cur.PersonaID, ProjectID: cur.ProjectID,
				ClosedAt: cur.ClosedAt, Now: t.now}
			type act struct {
				verb string
				data map[string]any
			}
			var acts []act
			var fields []string
			if in.Title != nil {
				if v := strings.TrimSpace(*in.Title); v != cur.Title {
					p.Title = v
					fields = append(fields, "title")
				}
			}
			if in.Description != nil && *in.Description != cur.Description {
				p.Description = *in.Description
				fields = append(fields, "description")
			}
			if in.Priority != nil && string(*in.Priority) != cur.Priority {
				p.Priority = string(*in.Priority)
				fields = append(fields, "priority")
			}
			if in.ProjectID != nil && (cur.ProjectID == nil || *cur.ProjectID != *in.ProjectID) {
				if _, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *in.ProjectID}); err != nil {
					return domain.Invalid("project_id", "project not found in company")
				}
				p.ProjectID = in.ProjectID
				fields = append(fields, "project_id")
			}
			if len(fields) > 0 {
				acts = append(acts, act{"task.updated", map[string]any{"fields": fields}})
			}

			personaChanged := false
			switch {
			case in.Unassign && cur.PersonaID != nil:
				p.PersonaID = nil
				personaChanged = true
				acts = append(acts, act{"task.assigned", map[string]any{"from": cur.PersonaID, "to": nil}})
			case in.PersonaID != nil && (cur.PersonaID == nil || *cur.PersonaID != *in.PersonaID):
				if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.PersonaID}); err != nil {
					return domain.Invalid("persona_id", "persona not found in company")
				}
				if err := t.checkDelegation(actor, companyID, in.PersonaID); err != nil {
					return err
				}
				p.PersonaID = in.PersonaID
				personaChanged = true
				acts = append(acts, act{"task.assigned", map[string]any{"from": cur.PersonaID, "to": in.PersonaID}})
			}

			statusChanged, closedNow := false, false
			from := domain.TaskStatus(cur.Status)
			want := in.Status
			reviewGate := false // a persona with require_review closing the task: in_review + task_review gate
			if want != nil && *want == domain.TaskDone && from.Open() {
				if reviewGate, err = t.reviewRequired(actor, companyID); err != nil {
					return err
				}
				if reviewGate {
					inReview := domain.TaskInReview
					want = &inReview
				}
			}
			if want != nil && *want != from {
				if !from.CanTransition(*want) {
					return domain.Invalid("status", fmt.Sprintf("invalid transition %s → %s", from, *want))
				}
				statusChanged = true
				p.Status = string(*want)
				data := map[string]any{"from": from, "to": *want}
				if reviewGate {
					data["requested"] = domain.TaskDone
				}
				acts = append(acts, act{"task.status_changed", data})
				if !want.Open() {
					closedNow = true
					c := t.now
					p.ClosedAt = &c
					acts = append(acts, act{"task.closed", map[string]any{"status": *want}})
				} else {
					p.ClosedAt = nil
				}
			}
			if len(acts) == 0 && reviewGate { // already in_review: make sure a review gate is pending
				if err := t.requestTaskReview(actor, companyID, cur); err != nil {
					return err
				}
				out, err = loadTaskView(ctx, t.q, companyID, taskID)
				return err
			}
			if len(acts) == 0 {
				out, err = loadTaskView(ctx, t.q, companyID, taskID)
				return err
			}

			row, err := t.q.UpdateTask(ctx, p)
			if err != nil {
				return mapErr(err, "update task")
			}
			eid := in.EventID
			for _, a := range acts {
				if err := t.activity(actor, companyID, a.verb, "task", taskID, a.data, eid); err != nil {
					return err
				}
				eid = ""
			}
			newStatus := domain.TaskStatus(row.Status)
			if closedNow {
				if err := t.taskClosedFact(companyID, row); err != nil {
					return err
				}
			}
			if reviewGate {
				if err := t.requestTaskReview(actor, companyID, row); err != nil {
					return err
				}
			}
			if cur.PersonaID != nil && (closedNow || personaChanged) {
				reason := "task_reassigned"
				if closedNow {
					reason = "task_closed"
				}
				if err := t.cancelTaskRun(actor, companyID, taskID, reason); err != nil {
					return err
				}
			}
			if personaChanged || statusChanged {
				if err := t.wake(companyID, taskID, row.PersonaID, newStatus, domain.TriggerAssign, true); err != nil {
					return err
				}
			}
			out, err = loadTaskView(ctx, t.q, companyID, taskID)
			if err != nil {
				return err
			}
			t.publish(companyTopic(companyID), "task.updated", out)
			return nil
		})
	}
	err := run()
	if err != nil && in.EventID != "" && isUniqueOn(err, "activity_event_uq") {
		err = run()
	}
	if err != nil {
		return TaskView{}, err
	}
	return out, nil
}

// WakeTask enqueues a manual Wake for the assignee. No assignee → ErrInvalid.
func (s *Service) WakeTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) error {
	return s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if task.PersonaID == nil {
			return domain.Invalid("persona_id", "task has no assignee")
		}
		p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *task.PersonaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		if !domain.PersonaStatus(p.Status).CanWork() {
			return domain.Invalid("persona", fmt.Sprintf("persona status %q cannot work", p.Status))
		}
		if !domain.TaskStatus(task.Status).Workable() {
			return domain.Invalid("status", fmt.Sprintf("task status %q is not workable", task.Status))
		}
		if s.Jobs == nil {
			return nil
		}
		return s.Jobs.WakeTx(ctx, t.tx, Wake{CompanyID: companyID, PersonaID: p.ID, TaskID: taskID, Trigger: domain.TriggerManual})
	})
}
