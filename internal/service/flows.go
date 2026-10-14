package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// CreateFlowInput defines a new Fluxo.
type CreateFlowInput struct {
	Name            string
	Slug            string // derived from Name when empty
	Description     string
	Cron            string // 5 fields: minute hour day-of-month month day-of-week
	Timezone        string // default: the company's
	Enabled         *bool  // default true
	PersonaID       domain.ID
	ProjectID       *domain.ID
	Priority        domain.Priority // default medium
	TaskTitle       string          // template: {{data}}, {{n}}; default: the flow name
	TaskDescription string
}

// UpdateFlowInput changes a Fluxo; nil fields stay as they are.
type UpdateFlowInput struct {
	Name, Description, Cron, Timezone, TaskTitle, TaskDescription *string
	Enabled                                                       *bool
	PersonaID                                                     *domain.ID
	ProjectID                                                     *domain.ID
	ClearProject                                                  bool
	Priority                                                      *domain.Priority
}

// FlowView is a flow with display-ready joins.
type FlowView struct {
	domain.Flow
	PersonaName string `json:"persona_name"`
	ProjectName string `json:"project_name,omitempty"`
}

// FlowFireView is a firing with the task it created.
type FlowFireView struct {
	domain.FlowFire
	TaskKey    string `json:"task_key,omitempty"`
	TaskTitle  string `json:"task_title,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`
}

// FlowFireResult is the outcome of FireFlow. Duplicate means the firing already existed
// (same flow, scheduled_for and origin) and nothing new was created. Skipped means a scheduled
// firing created no task: SkipReason is "disabled" (the flow was turned off) or "open_task"
// (the task of an earlier firing is still open).
type FlowFireResult struct {
	Fire       domain.FlowFire `json:"fire"`
	Task       *TaskView       `json:"task,omitempty"`
	Duplicate  bool            `json:"duplicate,omitempty"`
	Skipped    bool            `json:"skipped,omitempty"`
	SkipReason string          `json:"skip_reason,omitempty"`
}

// ParseCron validates a 5-field cron expression in an IANA time zone and returns its schedule.
//
// Daylight saving time follows the parser's rules (robfig/cron v3, stepping in real hours): a
// wall-clock time that does not exist on the day clocks jump forward (e.g. 02:30) is skipped that
// day, and one that happens twice when clocks go back (e.g. 01:30) fires at both occurrences.
// Fixed-offset zones and zones without DST (America/Sao_Paulo since 2019) are not affected.
func ParseCron(spec, tz string) (cron.Schedule, error) {
	spec = strings.TrimSpace(spec)
	if len(strings.Fields(spec)) != 5 {
		return nil, domain.Invalid("cron", "must have 5 fields (minute hour day-of-month month day-of-week)")
	}
	if tz == "" {
		return nil, domain.Invalid("timezone", "required")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, domain.Invalid("timezone", "unknown IANA time zone")
	}
	sched, err := cron.ParseStandard("CRON_TZ=" + tz + " " + spec)
	if err != nil {
		return nil, domain.Invalid("cron", err.Error())
	}
	return sched, nil
}

// NextCron is the first occurrence of spec strictly after t.
func NextCron(spec, tz string, t time.Time) (time.Time, error) {
	sched, err := ParseCron(spec, tz)
	if err != nil {
		return time.Time{}, err
	}
	n := sched.Next(t)
	if n.IsZero() {
		return time.Time{}, domain.Invalid("cron", "never fires")
	}
	return n.UTC(), nil
}

func flowFromRow(r store.Flow) domain.Flow {
	return domain.Flow{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		Cron: r.Cron, Timezone: r.Timezone, Enabled: r.Enabled, PersonaID: r.PersonaID, ProjectID: r.ProjectID,
		Priority: domain.Priority(r.Priority), TaskTitle: r.TaskTitle, TaskDescription: r.TaskDescription,
		LastFiredAt: r.LastFiredAt, NextFireAt: r.NextFireAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func flowFireFromRow(r store.FlowFire) domain.FlowFire {
	return domain.FlowFire{ID: r.ID, FlowID: r.FlowID, CompanyID: r.CompanyID, ScheduledFor: r.ScheduledFor,
		TaskID: r.TaskID, Manual: r.Manual, CreatedAt: r.CreatedAt}
}

func loadFlowView(ctx context.Context, q *store.Queries, companyID, id domain.ID) (FlowView, error) {
	r, err := q.GetFlowView(ctx, store.GetFlowViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return FlowView{}, mapErr(err, "get flow")
	}
	return FlowView{Flow: flowFromRow(r.Flow), PersonaName: r.PersonaName, ProjectName: r.ProjectName}, nil
}

// CreateFlow creates a Fluxo. The cron expression and time zone are validated.
func (s *Service) CreateFlow(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateFlowInput) (FlowView, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return FlowView{}, domain.Invalid("name", "required")
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return FlowView{}, err
	}
	prio := in.Priority
	if prio == "" {
		prio = domain.PriorityMedium
	}
	if !prio.Valid() {
		return FlowView{}, domain.Invalid("priority", fmt.Sprintf("unknown priority %q", prio))
	}
	if in.PersonaID == uuid.Nil {
		return FlowView{}, domain.Invalid("persona_id", "required")
	}
	enabled := in.Enabled == nil || *in.Enabled
	var out FlowView
	err = s.withTx(ctx, func(t *txc) error {
		co, err := t.q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return mapErr(err, "company")
		}
		tz := strings.TrimSpace(in.Timezone)
		if tz == "" {
			tz = co.Timezone
		}
		next, err := NextCron(in.Cron, tz, t.now)
		if err != nil {
			return err
		}
		if err := t.checkFlowRefs(companyID, &in.PersonaID, in.ProjectID); err != nil {
			return err
		}
		row, err := t.q.CreateFlow(ctx, store.CreateFlowParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug,
			Name: name, Description: strings.TrimSpace(in.Description), Cron: strings.Join(strings.Fields(in.Cron), " "),
			Timezone: tz, Enabled: enabled, PersonaID: in.PersonaID, ProjectID: in.ProjectID, Priority: string(prio),
			TaskTitle: strings.TrimSpace(in.TaskTitle), TaskDescription: in.TaskDescription, NextFireAt: &next, Now: t.now})
		if err != nil {
			return mapErr(err, "create flow")
		}
		if err := t.activity(actor, companyID, "flow.created", "flow", row.ID,
			map[string]any{"slug": row.Slug, "name": row.Name, "cron": row.Cron, "timezone": row.Timezone}, ""); err != nil {
			return err
		}
		out, err = loadFlowView(ctx, t.q, companyID, row.ID)
		return err
	})
	return out, err
}

func (t *txc) checkFlowRefs(companyID domain.ID, personaID, projectID *domain.ID) error {
	if personaID != nil {
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *personaID}); err != nil {
			return domain.Invalid("persona_id", "persona not found in company")
		}
	}
	if projectID != nil {
		if _, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *projectID}); err != nil {
			return domain.Invalid("project_id", "project not found in company")
		}
	}
	return nil
}

// UpdateFlow changes a Fluxo; enabling it or changing its schedule recomputes the next
// occurrence from now (no catch-up of what was missed while it was off).
func (s *Service) UpdateFlow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID, in UpdateFlowInput) (FlowView, error) {
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return FlowView{}, domain.Invalid("name", "must not be empty")
	}
	if in.Priority != nil && !in.Priority.Valid() {
		return FlowView{}, domain.Invalid("priority", fmt.Sprintf("unknown priority %q", *in.Priority))
	}
	if in.ClearProject && in.ProjectID != nil {
		return FlowView{}, domain.Invalid("project_id", "cannot set and clear at once")
	}
	var out FlowView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetFlowByIDForUpdate(ctx, flowID)
		if err != nil || cur.CompanyID != companyID {
			return notFound("get flow")
		}
		p := store.UpdateFlowParams{CompanyID: companyID, ID: flowID, Name: cur.Name, Description: cur.Description,
			Cron: cur.Cron, Timezone: cur.Timezone, Enabled: cur.Enabled, PersonaID: cur.PersonaID, ProjectID: cur.ProjectID,
			Priority: cur.Priority, TaskTitle: cur.TaskTitle, TaskDescription: cur.TaskDescription,
			NextFireAt: cur.NextFireAt, Now: t.now}
		var changed []string
		set := func(field string, dst *string, v *string) {
			if v != nil && *v != *dst {
				*dst = *v
				changed = append(changed, field)
			}
		}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			set("name", &p.Name, &n)
		}
		set("description", &p.Description, in.Description)
		set("task_title", &p.TaskTitle, in.TaskTitle)
		set("task_description", &p.TaskDescription, in.TaskDescription)
		reschedule := false
		if in.Cron != nil {
			c := strings.Join(strings.Fields(*in.Cron), " ")
			if c != p.Cron {
				p.Cron = c
				changed = append(changed, "cron")
				reschedule = true
			}
		}
		if in.Timezone != nil && strings.TrimSpace(*in.Timezone) != p.Timezone {
			p.Timezone = strings.TrimSpace(*in.Timezone)
			changed = append(changed, "timezone")
			reschedule = true
		}
		if in.Enabled != nil && *in.Enabled != p.Enabled {
			p.Enabled = *in.Enabled
			if p.Enabled {
				changed = append(changed, "enabled")
				reschedule = true
			} else {
				changed = append(changed, "disabled")
			}
		}
		if in.Priority != nil && string(*in.Priority) != p.Priority {
			p.Priority = string(*in.Priority)
			changed = append(changed, "priority")
		}
		if in.PersonaID != nil && *in.PersonaID != p.PersonaID {
			if err := t.checkFlowRefs(companyID, in.PersonaID, nil); err != nil {
				return err
			}
			p.PersonaID = *in.PersonaID
			changed = append(changed, "persona_id")
		}
		if in.ClearProject && p.ProjectID != nil {
			p.ProjectID = nil
			changed = append(changed, "project_id")
		} else if in.ProjectID != nil && (p.ProjectID == nil || *p.ProjectID != *in.ProjectID) {
			if err := t.checkFlowRefs(companyID, nil, in.ProjectID); err != nil {
				return err
			}
			p.ProjectID = in.ProjectID
			changed = append(changed, "project_id")
		}
		// Always validate the resulting schedule (also when only the time zone changed).
		next, err := NextCron(p.Cron, p.Timezone, t.now)
		if err != nil {
			return err
		}
		if reschedule || p.NextFireAt == nil {
			p.NextFireAt = &next
		}
		if len(changed) == 0 {
			out, err = loadFlowView(ctx, t.q, companyID, flowID)
			return err
		}
		if _, err := t.q.UpdateFlow(ctx, p); err != nil {
			return mapErr(err, "update flow")
		}
		if err := t.activity(actor, companyID, "flow.updated", "flow", flowID, map[string]any{"fields": changed}, ""); err != nil {
			return err
		}
		out, err = loadFlowView(ctx, t.q, companyID, flowID)
		return err
	})
	return out, err
}

func (s *Service) ListFlows(ctx context.Context, companyID domain.ID) ([]FlowView, error) {
	rows, err := s.read().ListFlowViews(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	out := make([]FlowView, len(rows))
	for i, r := range rows {
		out[i] = FlowView{Flow: flowFromRow(r.Flow), PersonaName: r.PersonaName, ProjectName: r.ProjectName}
	}
	return out, nil
}

// GetFlow resolves ref as UUID or slug within the company.
func (s *Service) GetFlow(ctx context.Context, companyID domain.ID, ref string) (FlowView, error) {
	q := s.read()
	if id, err := uuid.Parse(ref); err == nil {
		return loadFlowView(ctx, q, companyID, id)
	}
	row, err := q.GetFlowBySlug(ctx, store.GetFlowBySlugParams{CompanyID: companyID, Slug: ref})
	if err != nil {
		return FlowView{}, mapErr(err, "get flow")
	}
	return loadFlowView(ctx, q, companyID, row.ID)
}

// ListFlowFires returns the latest firings of a flow with the tasks they created.
func (s *Service) ListFlowFires(ctx context.Context, companyID, flowID domain.ID, limit int) ([]FlowFireView, error) {
	rows, err := s.read().ListFlowFires(ctx, store.ListFlowFiresParams{FlowID: flowID, CompanyID: companyID, Lim: int32(clamp(limit, 30, 200))})
	if err != nil {
		return nil, fmt.Errorf("list flow fires: %w", err)
	}
	out := make([]FlowFireView, len(rows))
	for i, r := range rows {
		v := FlowFireView{FlowFire: domain.FlowFire{ID: r.ID, FlowID: r.FlowID, CompanyID: r.CompanyID,
			ScheduledFor: r.ScheduledFor, TaskID: r.TaskID, Manual: r.Manual, CreatedAt: r.CreatedAt}, TaskKey: r.TaskKey}
		if r.TaskTitle != nil {
			v.TaskTitle = *r.TaskTitle
		}
		if r.TaskStatus != nil {
			v.TaskStatus = *r.TaskStatus
		}
		out[i] = v
	}
	return out, nil
}

// DueFlows lists enabled flows whose next occurrence is at or before now.
func (s *Service) DueFlows(ctx context.Context, now time.Time) ([]domain.Flow, error) {
	rows, err := s.read().DueFlows(ctx, store.DueFlowsParams{Now: ptr(now.UTC()), Lim: 100})
	if err != nil {
		return nil, fmt.Errorf("due flows: %w", err)
	}
	out := make([]domain.Flow, len(rows))
	for i, r := range rows {
		out[i] = flowFromRow(r)
	}
	return out, nil
}

// FireFlow fires the scheduled occurrence scheduledFor of a flow as the system: it creates
// the task assigned to the flow's persona and wakes it (trigger flow). Idempotent per
// (flow, scheduledFor): a repeat returns the existing firing with Duplicate set.
//
// Scheduled firings follow three rules:
//   - catch-up: when the process was down and several occurrences came due, only the most
//     recent one fires (with its own scheduled_for) and flow.missed records how many were skipped;
//   - overlap: if a task created by an earlier firing is still open, no new task is created and
//     flow.skipped is recorded;
//   - the schedule always advances (next_fire_at) past max(now, occurrence), whatever happened.
func (s *Service) FireFlow(ctx context.Context, flowID domain.ID, scheduledFor time.Time) (FlowFireResult, error) {
	return s.fireFlow(ctx, domain.SystemActor, nil, flowID, scheduledFor, true)
}

// RunFlowNow is the "Rodar agora" button: a manual firing with scheduled_for = now truncated to
// the minute (two clicks in the same minute create one task). It never collides with the cron
// occurrence of the same minute, does not move the schedule and ignores an open earlier task.
func (s *Service) RunFlowNow(ctx context.Context, actor domain.Actor, companyID, flowID domain.ID) (FlowFireResult, error) {
	return s.fireFlow(ctx, actor, &companyID, flowID, s.Now().UTC().Truncate(time.Minute), false)
}

// maxMissedScan bounds the catch-up scan (a per-minute flow down for ~70 days); past it the
// occurrence found is the latest scanned.
const maxMissedScan = 100000

// latestDue walks the schedule from first (a due occurrence) to the most recent occurrence at or
// before now, and says how many occurrences came before it.
func latestDue(spec, tz string, first, now time.Time) (latest time.Time, missed int, err error) {
	sched, err := ParseCron(spec, tz)
	if err != nil {
		return time.Time{}, 0, err
	}
	latest = first
	for missed < maxMissedScan {
		nx := sched.Next(latest)
		if nx.IsZero() || nx.After(now) {
			break
		}
		latest = nx.UTC()
		missed++
	}
	return latest, missed, nil
}

func (s *Service) fireFlow(ctx context.Context, actor domain.Actor, companyID *domain.ID, flowID domain.ID, scheduledFor time.Time, scheduled bool) (FlowFireResult, error) {
	scheduledFor = scheduledFor.UTC().Truncate(time.Microsecond)
	var out FlowFireResult
	err := s.withTx(ctx, func(t *txc) error {
		f, err := t.q.GetFlowByIDForUpdate(ctx, flowID)
		if err != nil || (companyID != nil && f.CompanyID != *companyID) {
			return notFound("get flow")
		}
		if scheduled && !f.Enabled {
			out.Skipped, out.SkipReason = true, "disabled"
			return nil
		}
		// advance moves the schedule past this occurrence; manual firings leave it alone.
		advance := func(lastFired *time.Time, occurrence time.Time) error {
			next := f.NextFireAt
			if scheduled {
				after := t.now
				if occurrence.After(after) {
					after = occurrence
				}
				nx, err := NextCron(f.Cron, f.Timezone, after)
				if err != nil {
					return err
				}
				next = &nx
			}
			if err := t.q.MarkFlowFired(ctx, store.MarkFlowFiredParams{ID: f.ID, LastFiredAt: lastFired, NextFireAt: next, Now: t.now}); err != nil {
				return fmt.Errorf("mark flow fired: %w", err)
			}
			return nil
		}

		if scheduled { // catch-up after downtime: fire only the most recent due occurrence
			latest, missed, err := latestDue(f.Cron, f.Timezone, scheduledFor, t.now)
			if err != nil {
				return err
			}
			if missed > 0 {
				if err := t.activity(domain.SystemActor, f.CompanyID, "flow.missed", "flow", f.ID, map[string]any{
					"missed": missed, "from": scheduledFor, "fired_for": latest}, ""); err != nil {
					return err
				}
			}
			scheduledFor = latest.Truncate(time.Microsecond)
		}

		fire, err := t.q.InsertFlowFire(ctx, store.InsertFlowFireParams{ID: domain.NewID(), FlowID: f.ID, CompanyID: f.CompanyID,
			ScheduledFor: scheduledFor, Manual: !scheduled, Now: t.now})
		if isNoRows(err) {
			old, gerr := t.q.GetFlowFire(ctx, store.GetFlowFireParams{FlowID: f.ID, ScheduledFor: scheduledFor, Manual: !scheduled})
			if gerr != nil {
				return mapErr(gerr, "get flow fire")
			}
			out.Fire, out.Duplicate = flowFireFromRow(old), true
			if old.TaskID != nil {
				if tv, terr := loadTaskView(ctx, t.q, f.CompanyID, *old.TaskID); terr == nil {
					out.Task = &tv
				}
			}
			if scheduled { // the occurrence was handled but the schedule was left behind: move on
				return advance(f.LastFiredAt, scheduledFor)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("insert flow fire: %w", err)
		}

		if scheduled { // overlap: do not pile a new task on top of one still open
			open, err := t.q.FlowHasOpenTask(ctx, f.ID)
			if err != nil && !isNoRows(err) {
				return fmt.Errorf("check open flow task: %w", err)
			}
			if err == nil {
				key := ""
				if c, cerr := t.q.GetCompanyByID(ctx, f.CompanyID); cerr == nil {
					key = domain.TaskKey(c.TaskPrefix, open.Number)
				}
				if err := t.activity(domain.SystemActor, f.CompanyID, "flow.skipped", "flow", f.ID, map[string]any{
					"reason": "open_task", "task_id": open.ID, "task_key": key, "scheduled_for": scheduledFor}, ""); err != nil {
					return err
				}
				if err := advance(f.LastFiredAt, scheduledFor); err != nil {
					return err
				}
				out.Fire, out.Skipped, out.SkipReason = flowFireFromRow(fire), true, "open_task"
				return nil
			}
		}

		n, err := t.q.CountFlowFires(ctx, f.ID)
		if err != nil {
			return fmt.Errorf("count flow fires: %w", err)
		}
		loc := loadLocation(f.Timezone)
		title := renderFlowTemplate(f.TaskTitle, scheduledFor, loc, n)
		if strings.TrimSpace(title) == "" {
			title = f.Name
		}
		title = truncateRunes(strings.TrimSpace(title), 500)
		persona := f.PersonaID
		task, err := t.createTask(actor, f.CompanyID, CreateTaskInput{Title: title,
			Description: renderFlowTemplate(f.TaskDescription, scheduledFor, loc, n),
			Priority:    domain.Priority(f.Priority), PersonaID: &persona, ProjectID: f.ProjectID,
			trigger: domain.TriggerFlow})
		if err != nil {
			return fmt.Errorf("fire flow: %w", err)
		}
		if err := t.q.SetFlowFireTask(ctx, store.SetFlowFireTaskParams{ID: fire.ID, TaskID: &task.ID}); err != nil {
			return fmt.Errorf("set fire task: %w", err)
		}
		fire.TaskID = &task.ID
		if err := advance(&t.now, scheduledFor); err != nil {
			return err
		}
		if err := t.activity(actor, f.CompanyID, "flow.fired", "flow", f.ID, map[string]any{
			"task_id": task.ID, "task_key": task.Key, "scheduled_for": scheduledFor, "manual": !scheduled}, ""); err != nil {
			return err
		}
		t.publish(companyTopic(f.CompanyID), "flow.fired", map[string]any{"flow_id": f.ID, "task_id": task.ID})
		out.Fire, out.Task = flowFireFromRow(fire), &task
		return nil
	})
	if err != nil {
		return FlowFireResult{}, err
	}
	return out, nil
}

// renderFlowTemplate replaces {{data}} (ISO date of the occurrence in the flow's time zone)
// and {{n}} (firing sequence number).
func renderFlowTemplate(tpl string, at time.Time, loc *time.Location, n int64) string {
	r := strings.NewReplacer("{{data}}", at.In(loc).Format("2006-01-02"), "{{n}}", strconv.FormatInt(n, 10))
	return r.Replace(tpl)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
