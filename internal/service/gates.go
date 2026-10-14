package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

// RequestGateInput asks a human to decide something.
type RequestGateInput struct {
	Kind    domain.GateKind // default approval
	Title   string
	Body    string // markdown: what is being asked, evidence, impact
	TaskID  *domain.ID
	RunID   *domain.ID
	EventID string // idempotency key (agents/MCP): repeating returns the existing gate

	// requestedStatus is the status a persona with require_review asked for (done or cancelled);
	// set only by requestTaskReview.
	requestedStatus domain.TaskStatus
}

// DecideGateInput is the human answer to a pending gate.
type DecideGateInput struct {
	Decision domain.GateStatus // approved | rejected | changes_requested
	Note     string            // explains the decision to the persona and to the audit trail
}

// GateFilter narrows ListGates.
type GateFilter struct {
	Statuses  []domain.GateStatus
	Kinds     []domain.GateKind
	TaskID    *domain.ID
	PersonaID *domain.ID
	Limit     int // default 100, max 500
	Offset    int
}

// GateView is a gate with display-ready joins.
type GateView struct {
	domain.Gate
	TaskKey     string `json:"task_key,omitempty"`
	TaskTitle   string `json:"task_title,omitempty"`
	PersonaName string `json:"persona_name,omitempty"`
}

func gateView(r store.Gate, taskKey, taskTitle, personaName string) GateView {
	return GateView{Gate: gateFromRow(r), TaskKey: taskKey, TaskTitle: taskTitle, PersonaName: personaName}
}

func loadGateView(ctx context.Context, q *store.Queries, companyID, id domain.ID) (GateView, error) {
	r, err := q.GetGateView(ctx, store.GetGateViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return GateView{}, mapErr(err, "get gate")
	}
	return gateView(r.Gate, r.TaskKey, r.TaskTitle, r.PersonaName), nil
}

const maxGateBody = 100000

// RequestGate opens a gate. By a persona it also parks the work: the task goes to blocked
// (approval, hire) or in_review (task_review), and a blocking kind puts the persona in
// awaiting_gate unless it has a run active on another task. Emits gate.requested.
// The same EventID twice returns the existing gate.
//
// A persona may ask for a gate only on a task assigned to itself or on the task of its own
// run (RunID); anything else is ErrNotAllowed, so a persona cannot park someone else's work.
func (s *Service) RequestGate(ctx context.Context, actor domain.Actor, companyID domain.ID, in RequestGateInput) (GateView, error) {
	title := strings.TrimSpace(in.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > 300 {
		return GateView{}, domain.Invalid("title", "must have 1-300 characters")
	}
	if utf8.RuneCountInString(in.Body) > maxGateBody {
		return GateView{}, domain.Invalid("body", "too long")
	}
	if in.Kind == "" {
		in.Kind = domain.GateApproval
	}
	if !in.Kind.Valid() {
		return GateView{}, domain.Invalid("kind", fmt.Sprintf("unknown kind %q", in.Kind))
	}
	in.Title = title

	var out GateView
	run := func() error {
		return s.withTx(ctx, func(t *txc) error {
			if in.EventID != "" {
				ex, err := t.q.GetGateByEventID(ctx, store.GetGateByEventIDParams{CompanyID: companyID, EventID: &in.EventID})
				if err == nil {
					out, err = loadGateView(ctx, t.q, companyID, ex.ID)
					return err
				}
				if !isNoRows(err) {
					return fmt.Errorf("lookup event: %w", err)
				}
			}
			var task *store.Task
			if in.TaskID != nil {
				tk, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *in.TaskID})
				if err != nil {
					return domain.Invalid("task_id", "task not found in company")
				}
				task = &tk
			}
			var run *store.Run
			if in.RunID != nil {
				r, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *in.RunID})
				if err != nil {
					return domain.Invalid("run_id", "run not found in company")
				}
				run = &r
			}
			if actor.Type == domain.ActorPersona && actor.ID != nil {
				if run != nil && run.PersonaID != *actor.ID {
					return fmt.Errorf("request gate: the run belongs to another persona: %w", domain.ErrNotAllowed)
				}
				if task != nil {
					owns := task.PersonaID != nil && *task.PersonaID == *actor.ID
					ownRun := run != nil && run.TaskID == task.ID
					if !owns && !ownRun {
						return fmt.Errorf("request gate: a persona asks for gates only on tasks assigned to it: %w", domain.ErrNotAllowed)
					}
				}
			}
			var personaID *domain.ID
			if actor.Type == domain.ActorPersona && actor.ID != nil {
				if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *actor.ID}); err != nil {
					return domain.Invalid("persona", "persona not found in company")
				}
				personaID = actor.ID
			}
			g, err := t.createGate(actor, companyID, in, personaID)
			if err != nil {
				return err
			}
			if personaID != nil {
				if err := t.parkForGate(actor, companyID, g, task); err != nil {
					return err
				}
			}
			out, err = loadGateView(ctx, t.q, companyID, g.ID)
			if err != nil {
				return err
			}
			if task != nil {
				if tv, err := loadTaskView(ctx, t.q, companyID, task.ID); err == nil {
					t.publish(companyTopic(companyID), "task.updated", tv)
				}
			}
			return nil
		})
	}
	err := run()
	if err != nil && in.EventID != "" && isUniqueOn(err, "gates_event_uq") {
		err = run() // lost the race on event_id: the second pass returns the winner
	}
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// createGate inserts a pending gate and records gate.requested in the current transaction.
func (t *txc) createGate(actor domain.Actor, companyID domain.ID, in RequestGateInput, personaID *domain.ID) (domain.Gate, error) {
	var eid *string
	if in.EventID != "" {
		eid = &in.EventID
	}
	var requested *string
	if in.requestedStatus != "" {
		requested = ptr(string(in.requestedStatus))
	}
	row, err := t.q.CreateGate(t.ctx, store.CreateGateParams{ID: domain.NewID(), CompanyID: companyID, Kind: string(in.Kind),
		Title: in.Title, Body: in.Body, TaskID: in.TaskID, RunID: in.RunID, PersonaID: personaID,
		RequestedByType: string(actor.Type), RequestedByID: actor.ID, EventID: eid,
		RequestedStatus: requested, Now: t.now})
	if err != nil {
		return domain.Gate{}, mapErr(err, "create gate")
	}
	g := gateFromRow(row)
	if err := t.activity(actor, companyID, "gate.requested", "gate", g.ID, map[string]any{
		"kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "run_id": g.RunID, "persona_id": g.PersonaID}, ""); err != nil {
		return domain.Gate{}, err
	}
	t.publish(companyTopic(companyID), "gate.updated", g)
	return g, nil
}

// parkForGate holds the work behind a persona-requested gate: task to blocked/in_review and,
// for blocking kinds, the persona to awaiting_gate. A task that is already closed is left alone.
func (t *txc) parkForGate(actor domain.Actor, companyID domain.ID, g domain.Gate, task *store.Task) error {
	if task != nil {
		from := domain.TaskStatus(task.Status)
		to := domain.TaskBlocked
		if g.Kind == domain.GateTaskReview {
			to = domain.TaskInReview
		}
		if from.Open() && from != to && from.CanTransition(to) {
			if _, err := t.moveTask(actor, companyID, *task, to, map[string]any{"gate_id": g.ID}); err != nil {
				return err
			}
		}
	}
	if g.Kind.Blocking() && g.PersonaID != nil {
		taskID := domain.ID{}
		if g.TaskID != nil {
			taskID = *g.TaskID
		}
		if err := t.q.SetPersonaAwaitingGate(t.ctx, store.SetPersonaAwaitingGateParams{ID: *g.PersonaID, TaskID: taskID, Now: t.now}); err != nil {
			return fmt.Errorf("set persona awaiting_gate: %w", err)
		}
	}
	return nil
}

// moveTask changes a locked task's status inside the transaction and records the same facts
// UpdateTask does: task.status_changed, and for a closing move task.closed, the memory fact
// and the cancellation of the task's active run.
func (t *txc) moveTask(actor domain.Actor, companyID domain.ID, cur store.Task, to domain.TaskStatus, extra map[string]any) (store.Task, error) {
	from := domain.TaskStatus(cur.Status)
	p := store.UpdateTaskParams{CompanyID: companyID, ID: cur.ID, Title: cur.Title, Description: cur.Description,
		Status: string(to), Priority: cur.Priority, PersonaID: cur.PersonaID, ProjectID: cur.ProjectID, ClosedAt: nil, Now: t.now}
	closing := !to.Open()
	if closing {
		c := t.now
		p.ClosedAt = &c
	}
	row, err := t.q.UpdateTask(t.ctx, p)
	if err != nil {
		return store.Task{}, mapErr(err, "update task")
	}
	data := map[string]any{"from": from, "to": to}
	for k, v := range extra {
		data[k] = v
	}
	if err := t.activity(actor, companyID, "task.status_changed", "task", cur.ID, data, ""); err != nil {
		return store.Task{}, err
	}
	if closing {
		if err := t.activity(actor, companyID, "task.closed", "task", cur.ID, map[string]any{"status": to}, ""); err != nil {
			return store.Task{}, err
		}
		if err := t.taskClosedFact(companyID, row); err != nil {
			return store.Task{}, err
		}
		if err := t.expireTaskGates(companyID, cur.ID, "task_closed"); err != nil {
			return store.Task{}, err
		}
		if cur.PersonaID != nil {
			if err := t.cancelTaskRun(actor, companyID, cur.ID, "task_closed"); err != nil {
				return store.Task{}, err
			}
		}
	}
	return row, nil
}

// expireTaskGates marks the gates still pending on a task just closed as expired (gate.expired),
// so no decision is left waiting for a task that no longer exists as work. A persona held by an
// expired blocking gate is released when nothing else holds it.
func (t *txc) expireTaskGates(companyID, taskID domain.ID, reason string) error {
	rows, err := t.q.ExpirePendingGatesForTask(t.ctx, store.ExpirePendingGatesForTaskParams{CompanyID: companyID, TaskID: &taskID, Now: &t.now})
	if err != nil {
		return fmt.Errorf("expire task gates: %w", err)
	}
	for _, r := range rows {
		g := gateFromRow(r)
		if err := t.activity(domain.SystemActor, companyID, "gate.expired", "gate", g.ID, map[string]any{
			"kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "persona_id": g.PersonaID, "reason": reason}, ""); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "gate.updated", g)
		if g.PersonaID == nil {
			continue
		}
		n, err := t.q.CountBlockingGatesForPersona(t.ctx, g.PersonaID)
		if err != nil {
			return fmt.Errorf("count blocking gates: %w", err)
		}
		if n == 0 {
			if err := t.q.ReleasePersonaFromGate(t.ctx, store.ReleasePersonaFromGateParams{ID: *g.PersonaID, Now: t.now}); err != nil {
				return fmt.Errorf("release persona: %w", err)
			}
		}
	}
	return nil
}

// taskClosedFact records the task.closed memory fact for a task just closed in this transaction.
func (t *txc) taskClosedFact(companyID domain.ID, row store.Task) error {
	c, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	key := domain.TaskKey(c.TaskPrefix, row.Number)
	f := taskFact("task.closed", key, row.Title, row.Description, domain.TaskStatus(row.Status), domain.Priority(row.Priority),
		fmt.Sprintf("- Encerrada em: %s\n", t.now.Format("2006-01-02 15:04 UTC")))
	return t.memoryFact(companyID, row.WorkspaceID, f, fmt.Sprintf("task.closed:%s:%d", row.ID, t.now.Unix()))
}

var gateDecisionLabel = map[domain.GateStatus]string{
	domain.GateApproved: "Aprovado", domain.GateRejected: "Rejeitado", domain.GateChangesRequested: "Mudanças pedidas",
}

// DecideGate records a human decision on a pending gate. Only a human may decide (a persona
// gets ErrNotAllowed); an already decided gate is ErrConflict; changes_requested needs a note.
//
// Effects (the work is parked only when a persona asked for the gate):
//   - a system comment on the task states the decision and the note;
//   - the task leaves blocked/in_review: approved → in_progress (task_review: the status asked for, done or cancelled),
//     changes_requested → in_progress, rejected → todo (the persona is woken to pick another path);
//   - the persona leaves awaiting_gate (when no other blocking gate holds it) and the task's current assignee is woken (trigger gate);
//   - activity gate.decided and the memory fact gate.decided at hanko/decisions/<date>-<KEY>-<gate8>.md.
func (s *Service) DecideGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in DecideGateInput) (GateView, error) {
	if actor.Type != domain.ActorHuman {
		return GateView{}, fmt.Errorf("decide gate: only a human decides gates: %w", domain.ErrNotAllowed)
	}
	if !in.Decision.Decision() {
		return GateView{}, domain.Invalid("decision", fmt.Sprintf("want approved, rejected or changes_requested, got %q", in.Decision))
	}
	note := strings.TrimSpace(in.Note)
	if in.Decision == domain.GateChangesRequested && note == "" {
		return GateView{}, domain.Invalid("note", "explain the changes you want")
	}
	if utf8.RuneCountInString(note) > maxGateBody {
		return GateView{}, domain.Invalid("note", "too long")
	}

	var out GateView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGateByIDForUpdate(ctx, store.GetGateByIDForUpdateParams{CompanyID: companyID, ID: gateID})
		if err != nil {
			return mapErr(err, "get gate")
		}
		if domain.GateStatus(cur.Status) != domain.GatePending {
			return fmt.Errorf("gate already %s: %w", cur.Status, domain.ErrConflict)
		}
		row, err := t.q.DecideGate(ctx, store.DecideGateParams{CompanyID: companyID, ID: gateID, Status: string(in.Decision),
			DecidedByType: ptr(string(actor.Type)), DecidedByID: actor.ID, DecisionNote: note, Now: &t.now})
		if err != nil {
			return mapErr(err, "decide gate")
		}
		g := gateFromRow(row)
		if err := t.activity(actor, companyID, "gate.decided", "gate", g.ID, map[string]any{
			"decision": in.Decision, "kind": g.Kind, "title": g.Title, "task_id": g.TaskID, "persona_id": g.PersonaID,
			"note": note}, ""); err != nil {
			return err
		}

		var task *store.Task
		taskKey := "GATE"
		newStatus := domain.TaskStatus("")
		if g.TaskID != nil {
			tk, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: *g.TaskID})
			if err != nil && !isNoRows(err) {
				return fmt.Errorf("get gate task: %w", err)
			}
			if err == nil {
				task = &tk
			}
		}
		if task != nil {
			c, err := t.q.GetCompanyByID(ctx, companyID)
			if err != nil {
				return mapErr(err, "company")
			}
			taskKey = domain.TaskKey(c.TaskPrefix, task.Number)
			newStatus = domain.TaskStatus(task.Status)
			if err := t.decisionComment(companyID, *task, g); err != nil {
				return err
			}
			if g.PersonaID != nil {
				if to, ok := statusAfterDecision(g.Kind, in.Decision, g.RequestedStatus, domain.TaskStatus(task.Status)); ok {
					moved, err := t.moveTask(actor, companyID, *task, to, map[string]any{"gate_id": g.ID, "decision": in.Decision})
					if err != nil {
						return err
					}
					task, newStatus = &moved, to
				}
			}
		}
		if g.PersonaID != nil {
			n, err := t.q.CountBlockingGatesForPersona(ctx, g.PersonaID)
			if err != nil {
				return fmt.Errorf("count blocking gates: %w", err)
			}
			if n == 0 {
				if err := t.q.ReleasePersonaFromGate(ctx, store.ReleasePersonaFromGateParams{ID: *g.PersonaID, Now: t.now}); err != nil {
					return fmt.Errorf("release persona: %w", err)
				}
			}
			if task != nil {
				// whoever owns the task now is woken, which is not necessarily who asked
				if err := t.wake(companyID, task.ID, task.PersonaID, newStatus, domain.TriggerGate, false); err != nil {
					return err
				}
			}
		}
		var wsID *domain.ID
		if task != nil {
			wsID = task.WorkspaceID
		}
		if err := t.memoryFact(companyID, wsID, gateFact(g, taskKey, t.now), "gate.decided:"+g.ID.String()); err != nil {
			return err
		}
		out, err = loadGateView(ctx, t.q, companyID, g.ID)
		if err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "gate.updated", g)
		if task != nil {
			if tv, err := loadTaskView(ctx, t.q, companyID, task.ID); err == nil {
				t.publish(companyTopic(companyID), "task.updated", tv)
			}
		}
		return nil
	})
	if err != nil {
		return GateView{}, err
	}
	return out, nil
}

// statusAfterDecision is where a task goes when a gate asked by a persona is decided, given
// where the task stands. Only a task still parked by the gate (blocked/in_review) moves.
func statusAfterDecision(kind domain.GateKind, d domain.GateStatus, requested, cur domain.TaskStatus) (domain.TaskStatus, bool) {
	if cur != domain.TaskBlocked && cur != domain.TaskInReview {
		return "", false
	}
	var to domain.TaskStatus
	switch d {
	case domain.GateApproved:
		to = domain.TaskInProgress
		if kind == domain.GateTaskReview {
			to = domain.TaskDone
			if requested == domain.TaskCancelled {
				to = domain.TaskCancelled
			}
		}
	case domain.GateChangesRequested:
		to = domain.TaskInProgress
	case domain.GateRejected:
		to = domain.TaskTodo
	default:
		return "", false
	}
	return to, cur.CanTransition(to)
}

// decisionComment posts the decision on the task thread as a system comment.
func (t *txc) decisionComment(companyID domain.ID, task store.Task, g domain.Gate) error {
	var b strings.Builder
	fmt.Fprintf(&b, "**Gate decidido: %s** — %s", gateDecisionLabel[g.Status], g.Title)
	if g.DecisionNote != "" {
		fmt.Fprintf(&b, "\n\n%s", g.DecisionNote)
	}
	c, err := t.q.CreateComment(t.ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID, TaskID: task.ID,
		AuthorType: string(domain.ActorSystem), Body: b.String(), EventID: ptr("gate.decided:" + g.ID.String()), Now: t.now})
	if err != nil {
		return mapErr(err, "post gate decision")
	}
	return t.activity(domain.SystemActor, companyID, "comment.added", "task", task.ID,
		map[string]any{"comment_id": c.ID, "gate_id": g.ID}, "")
}

// gateFact is the memory fact for a decided gate: decisions become memory.
func gateFact(g domain.Gate, taskKey string, now time.Time) memory.Fact {
	var b strings.Builder
	fmt.Fprintf(&b, "# Decisão: %s\n\n", g.Title)
	fmt.Fprintf(&b, "- Decisão: %s\n- Tipo: %s\n", gateDecisionLabel[g.Status], g.Kind)
	if g.TaskID != nil {
		fmt.Fprintf(&b, "- Task: %s\n", taskKey)
	}
	fmt.Fprintf(&b, "- Pedido por: %s\n- Decidido por: %s\n- Gate: %s\n", actorLabel(g.RequestedBy), actorLabelPtr(g.DecidedBy), g.ID)
	if g.DecisionNote != "" {
		fmt.Fprintf(&b, "\n## Nota\n\n%s\n", g.DecisionNote)
	}
	if strings.TrimSpace(g.Body) != "" {
		fmt.Fprintf(&b, "\n## Pedido\n\n%s\n", g.Body)
	}
	return memory.Fact{Kind: "gate.decided",
		Path:  fmt.Sprintf("hanko/decisions/%s-%s-%s.md", now.Format("2006-01-02"), taskKey, g.ID.String()[:8]),
		Title: fmt.Sprintf("Decisão %s (%s)", taskKey, strings.ToLower(gateDecisionLabel[g.Status])), Body: b.String(),
		Tags: []string{"hanko", "decision"}}
}

func actorLabel(a domain.Actor) string {
	switch a.Type {
	case domain.ActorPersona:
		if a.ID != nil {
			return "persona " + a.ID.String()[:8]
		}
		return "persona"
	case domain.ActorHuman:
		return "humano"
	}
	return string(a.Type)
}

func actorLabelPtr(a *domain.Actor) string {
	if a == nil {
		return "—"
	}
	return actorLabel(*a)
}

// ListGates lists gates, pending ones first (oldest first), then the decided, newest first.
func (s *Service) ListGates(ctx context.Context, companyID domain.ID, f GateFilter) ([]GateView, error) {
	p := store.ListGateViewsParams{CompanyID: companyID, TaskID: f.TaskID, PersonaID: f.PersonaID,
		Lim: int32(clamp(f.Limit, 100, 500)), Off: int32(max(f.Offset, 0))}
	for _, st := range f.Statuses {
		if !st.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("unknown gate status %q", st))
		}
		p.Statuses = append(p.Statuses, string(st))
	}
	for _, k := range f.Kinds {
		if !k.Valid() {
			return nil, domain.Invalid("kind", fmt.Sprintf("unknown gate kind %q", k))
		}
		p.Kinds = append(p.Kinds, string(k))
	}
	rows, err := s.read().ListGateViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list gates: %w", err)
	}
	out := make([]GateView, len(rows))
	for i, r := range rows {
		out[i] = gateView(r.Gate, r.TaskKey, r.TaskTitle, r.PersonaName)
	}
	return out, nil
}

// GetGate returns one gate of the company.
func (s *Service) GetGate(ctx context.Context, companyID, gateID domain.ID) (GateView, error) {
	return loadGateView(ctx, s.read(), companyID, gateID)
}

// PersonaAlert is a persona in error with what is known about the failure.
type PersonaAlert struct {
	Persona domain.Persona
	PersonaHealth
}

// Inbox is what waits for the human: gates to decide, blocked tasks, runs that failed and
// personas in error.
type Inbox struct {
	Gates         []GateView
	BlockedTasks  []TaskView // blocked tasks that have no pending gate (the gate already covers those)
	FailedRuns    []RunView  // failed/expired runs of open tasks not superseded by a newer run
	ErrorPersonas []PersonaAlert
}

// Count is the number of items waiting.
func (i Inbox) Count() int {
	return len(i.Gates) + len(i.BlockedTasks) + len(i.FailedRuns) + len(i.ErrorPersonas)
}

const inboxFailedWindow = 7 * 24 * time.Hour

// Inbox assembles the Caixa de entrada.
func (s *Service) Inbox(ctx context.Context, companyID domain.ID) (Inbox, error) {
	q := s.read()
	var in Inbox
	gates, err := s.ListGates(ctx, companyID, GateFilter{Statuses: []domain.GateStatus{domain.GatePending}, Limit: 200})
	if err != nil {
		return in, err
	}
	in.Gates = gates
	withGate := map[domain.ID]bool{}
	for _, g := range gates {
		if g.TaskID != nil {
			withGate[*g.TaskID] = true
		}
	}
	blocked, err := s.ListTasks(ctx, companyID, TaskFilter{Statuses: []domain.TaskStatus{domain.TaskBlocked}, Limit: 200})
	if err != nil {
		return in, err
	}
	for _, tv := range blocked {
		if !withGate[tv.ID] {
			in.BlockedTasks = append(in.BlockedTasks, tv)
		}
	}
	failed, err := q.ListFailedRuns(ctx, store.ListFailedRunsParams{CompanyID: companyID, Since: s.Now().UTC().Add(-inboxFailedWindow), Lim: 50})
	if err != nil {
		return in, fmt.Errorf("list failed runs: %w", err)
	}
	for _, r := range failed {
		in.FailedRuns = append(in.FailedRuns, runViewFromParts(r.Run, r.TaskKey, r.TaskTitle, r.PersonaName))
	}
	errs, err := q.ListErrorPersonas(ctx, companyID)
	if err != nil {
		return in, fmt.Errorf("list error personas: %w", err)
	}
	for _, p := range errs {
		a := PersonaAlert{Persona: personaFromRow(p), PersonaHealth: PersonaHealth{LastErrorAt: p.LastErrorAt, ConsecutiveFailures: int(p.ConsecutiveFailures)}}
		if p.LastError != nil {
			a.LastError = *p.LastError
		}
		in.ErrorPersonas = append(in.ErrorPersonas, a)
	}
	return in, nil
}

// requestTaskReview opens the task_review gate of a persona with require_review that tried to
// close the task as requested (done or cancelled); the caller already moved it to in_review.
// Approving the gate applies the requested status. A review already pending is kept.
func (t *txc) requestTaskReview(actor domain.Actor, companyID domain.ID, task store.Task, requested domain.TaskStatus) error {
	pending, err := t.q.HasPendingGateOfKind(t.ctx, store.HasPendingGateOfKindParams{TaskID: &task.ID, Kind: string(domain.GateTaskReview)})
	if err != nil {
		return fmt.Errorf("check pending review: %w", err)
	}
	if pending {
		return nil
	}
	c, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	key := domain.TaskKey(c.TaskPrefix, task.Number)
	body := "A persona deu a task como pronta e esta persona exige revisão antes de concluir. Aprove para concluir, peça mudanças ou rejeite."
	if requested == domain.TaskCancelled {
		body = "A persona quer cancelar a task e esta persona exige revisão antes de encerrar. Aprove para cancelar, peça mudanças ou rejeite."
	}
	in := RequestGateInput{Kind: domain.GateTaskReview, TaskID: &task.ID, requestedStatus: requested,
		Title: fmt.Sprintf("Revisar %s — %s", key, task.Title), Body: body}
	if run, err := t.q.ActiveRunForTask(t.ctx, task.ID); err == nil && actor.ID != nil && run.PersonaID == *actor.ID {
		in.RunID = &run.ID
	}
	_, err = t.createGate(actor, companyID, in, actor.ID)
	return err
}
