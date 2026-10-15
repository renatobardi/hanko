package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Metas (ADR-0028). Quem cria, edita e abandona é admin; member registra progresso e liga tasks; viewer lê. A
// persona registra progresso pelo MCP (sem papel: o escopo é a empresa do run token).

const (
	maxGoalTitleRunes  = 200
	maxGoalMetricRunes = 100
	maxGoalUnitRunes   = 20
	maxGoalNoteRunes   = 2000
	maxGoalHistory     = 200
	// maxGoalBriefing é quantas metas entram no briefing da run.
	maxGoalBriefing = 5
)

// CreateGoalInput cria uma meta. Current vazio começa no valor inicial.
type CreateGoalInput struct {
	Title, Slug, Description, Metric, Unit string
	ProjectID                              *domain.ID
	Start, Target                          float64
	Current                                *float64
	DueDate                                *time.Time
	OwnerPersonaID                         *domain.ID
}

// UpdateGoalInput altera uma meta; campo nil fica como está. Mudar o inicial ou o alvo reavalia o progresso.
type UpdateGoalInput struct {
	Title, Description, Metric, Unit *string
	ProjectID                        *domain.ID
	ClearProject                     bool
	Start, Target                    *float64
	DueDate                          *time.Time
	ClearDue                         bool
	OwnerPersonaID                   *domain.ID
	ClearOwner                       bool
	// Status aceita active (reabrir) e abandoned; achieved só acontece ao cruzar o alvo.
	Status *domain.GoalStatus
}

// GoalFilter filtra a listagem de metas.
type GoalFilter struct {
	Status    domain.GoalStatus
	ProjectID *domain.ID
}

// GoalView é a meta com projeto, dona, progresso e as tasks ligadas por status.
type GoalView struct {
	domain.Goal
	ProjectSlug string `json:"project_slug,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	OwnerSlug   string `json:"owner_slug,omitempty"`
	OwnerName   string `json:"owner_name,omitempty"`
	// ProgressPercent é o progresso de 0 a 100.
	ProgressPercent float64                   `json:"progress_percent"`
	Tasks           map[domain.TaskStatus]int `json:"tasks"`
	TasksTotal      int                       `json:"tasks_total"`
	// Updates só vem no detalhe: do mais antigo para o mais novo (o gráfico), os últimos 200.
	Updates []domain.GoalUpdate `json:"updates,omitempty"`
}

// GoalBrief é a meta resumida que entra no briefing da run.
type GoalBrief struct {
	Slug, Title, Metric, Unit string
	Start, Target, Current    float64
	Progress                  float64 // 0 a 100
	DueDate                   *time.Time
	// Linked diz que a task da run está ligada a esta meta.
	Linked bool
}

func goalFromRow(r store.Goal) domain.Goal {
	return domain.Goal{ID: r.ID, CompanyID: r.CompanyID, ProjectID: r.ProjectID, Slug: r.Slug, Title: r.Title,
		Description: r.Description, Metric: r.Metric, Unit: r.Unit, StartValue: r.StartValue, TargetValue: r.TargetValue,
		CurrentValue: r.CurrentValue, DueDate: r.DueDate, Status: domain.GoalStatus(r.Status), OwnerPersonaID: r.OwnerPersonaID,
		CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), AchievedAt: r.AchievedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func goalUpdateFromRow(r store.GoalUpdate) domain.GoalUpdate {
	return domain.GoalUpdate{ID: r.ID, GoalID: r.GoalID, Value: r.Value, Note: r.Note, Author: actorOf(r.AuthorType, r.AuthorID), CreatedAt: r.CreatedAt}
}

func finite(field string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return domain.Invalid(field, "must be a finite number")
	}
	return nil
}

// dateOnly corta o instante para o dia (UTC): prazo é data, sem hora.
func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func checkGoalText(title, metric, unit string) error {
	if n := utf8.RuneCountInString(title); n < 1 || n > maxGoalTitleRunes {
		return domain.Invalid("title", fmt.Sprintf("must have 1-%d characters", maxGoalTitleRunes))
	}
	if utf8.RuneCountInString(metric) > maxGoalMetricRunes {
		return domain.Invalid("metric", fmt.Sprintf("too long (max %d)", maxGoalMetricRunes))
	}
	if utf8.RuneCountInString(unit) > maxGoalUnitRunes {
		return domain.Invalid("unit", fmt.Sprintf("too long (max %d)", maxGoalUnitRunes))
	}
	return nil
}

// goalProject confere que o projeto e a dona existem na empresa.
func (t *txc) checkGoalRefs(companyID domain.ID, project, owner *domain.ID) error {
	if project != nil {
		if _, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *project}); err != nil {
			return domain.Invalid("project_id", "project not found in company")
		}
	}
	if owner != nil {
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *owner}); err != nil {
			return domain.Invalid("owner", "persona not found in company")
		}
	}
	return nil
}

// checkLinkableGoal confere que a meta existe na empresa e está ativa (task nova só serve a meta em andamento).
func (t *txc) checkLinkableGoal(companyID, goalID domain.ID) error {
	g, err := t.q.GetGoalByID(t.ctx, store.GetGoalByIDParams{CompanyID: companyID, ID: goalID})
	if err != nil {
		return domain.Invalid("goal_id", "goal not found in company")
	}
	if domain.GoalStatus(g.Status) != domain.GoalActive {
		return domain.Invalid("goal_id", "goal is "+g.Status+" and takes no new tasks")
	}
	return nil
}

// CreateGoal cria uma meta (admin).
func (s *Service) CreateGoal(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateGoalInput) (domain.Goal, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Goal{}, err
	}
	title, metric, unit := strings.TrimSpace(in.Title), strings.TrimSpace(in.Metric), strings.TrimSpace(in.Unit)
	if err := checkGoalText(title, metric, unit); err != nil {
		return domain.Goal{}, err
	}
	slug, err := resolveSlug(in.Slug, title)
	if err != nil {
		return domain.Goal{}, err
	}
	cur := in.Start
	if in.Current != nil {
		cur = *in.Current
	}
	for field, v := range map[string]float64{"start_value": in.Start, "target_value": in.Target, "current_value": cur} {
		if err := finite(field, v); err != nil {
			return domain.Goal{}, err
		}
	}
	if in.Start == in.Target {
		return domain.Goal{}, domain.Invalid("target_value", "must differ from the start value")
	}
	probe := domain.Goal{StartValue: in.Start, TargetValue: in.Target}
	if probe.Reaches(cur) {
		return domain.Goal{}, domain.Invalid("current_value", "already reaches the target")
	}
	var due *time.Time
	if in.DueDate != nil {
		d := dateOnly(*in.DueDate)
		due = &d
	}
	var out domain.Goal
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if err := t.checkGoalRefs(companyID, in.ProjectID, in.OwnerPersonaID); err != nil {
			return err
		}
		row, err := t.q.CreateGoal(ctx, store.CreateGoalParams{ID: domain.NewID(), CompanyID: companyID, ProjectID: in.ProjectID,
			Slug: slug, Title: title, Description: strings.TrimSpace(in.Description), Metric: metric, Unit: unit,
			StartValue: in.Start, TargetValue: in.Target, CurrentValue: cur, DueDate: due, Status: string(domain.GoalActive),
			OwnerPersonaID: in.OwnerPersonaID, CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create goal")
		}
		out = goalFromRow(row)
		return t.activity(actor, companyID, "goal.created", "goal", out.ID, map[string]any{
			"slug": out.Slug, "title": out.Title, "start_value": out.StartValue, "target_value": out.TargetValue, "project_id": out.ProjectID}, "")
	})
	return out, err
}

// UpdateGoal altera uma meta (admin): texto, projeto, dona, prazo, inicial/alvo e status (reabrir ou abandonar).
func (s *Service) UpdateGoal(ctx context.Context, actor domain.Actor, companyID, goalID domain.ID, in UpdateGoalInput) (domain.Goal, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Goal{}, err
	}
	if in.Status != nil && *in.Status != domain.GoalActive && *in.Status != domain.GoalAbandoned {
		return domain.Goal{}, domain.Invalid("status", "use active or abandoned (achieved happens when progress crosses the target)")
	}
	if in.ProjectID != nil && in.ClearProject {
		return domain.Goal{}, domain.Invalid("project_id", "cannot set and clear the project at once")
	}
	if in.OwnerPersonaID != nil && in.ClearOwner {
		return domain.Goal{}, domain.Invalid("owner", "cannot set and clear the owner at once")
	}
	if in.DueDate != nil && in.ClearDue {
		return domain.Goal{}, domain.Invalid("due_date", "cannot set and clear the due date at once")
	}
	for field, v := range map[string]*float64{"start_value": in.Start, "target_value": in.Target} {
		if v != nil {
			if err := finite(field, *v); err != nil {
				return domain.Goal{}, err
			}
		}
	}
	var out domain.Goal
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGoalByIDForUpdate(ctx, store.GetGoalByIDForUpdateParams{CompanyID: companyID, ID: goalID})
		if err != nil {
			return mapErr(err, "get goal")
		}
		p := store.UpdateGoalParams{CompanyID: companyID, ID: goalID, ProjectID: cur.ProjectID, Title: cur.Title,
			Description: cur.Description, Metric: cur.Metric, Unit: cur.Unit, StartValue: cur.StartValue, TargetValue: cur.TargetValue,
			CurrentValue: cur.CurrentValue, DueDate: cur.DueDate, Status: cur.Status, OwnerPersonaID: cur.OwnerPersonaID,
			AchievedAt: cur.AchievedAt, Now: t.now}
		changed := map[string]any{}
		if in.Title != nil {
			if v := strings.TrimSpace(*in.Title); v != cur.Title {
				p.Title, changed["title"] = v, v
			}
		}
		if in.Description != nil {
			if v := strings.TrimSpace(*in.Description); v != cur.Description {
				p.Description, changed["description"] = v, true
			}
		}
		if in.Metric != nil {
			if v := strings.TrimSpace(*in.Metric); v != cur.Metric {
				p.Metric, changed["metric"] = v, v
			}
		}
		if in.Unit != nil {
			if v := strings.TrimSpace(*in.Unit); v != cur.Unit {
				p.Unit, changed["unit"] = v, v
			}
		}
		if err := checkGoalText(p.Title, p.Metric, p.Unit); err != nil {
			return err
		}
		switch {
		case in.ClearProject && cur.ProjectID != nil:
			p.ProjectID, changed["project_id"] = nil, nil
		case in.ProjectID != nil && !sameID(cur.ProjectID, in.ProjectID):
			p.ProjectID, changed["project_id"] = in.ProjectID, in.ProjectID
		}
		switch {
		case in.ClearOwner && cur.OwnerPersonaID != nil:
			p.OwnerPersonaID, changed["owner"] = nil, nil
		case in.OwnerPersonaID != nil && !sameID(cur.OwnerPersonaID, in.OwnerPersonaID):
			p.OwnerPersonaID, changed["owner"] = in.OwnerPersonaID, in.OwnerPersonaID
		}
		if err := t.checkGoalRefs(companyID, in.ProjectID, in.OwnerPersonaID); err != nil {
			return err
		}
		switch {
		case in.ClearDue && cur.DueDate != nil:
			p.DueDate, changed["due_date"] = nil, nil
		case in.DueDate != nil:
			d := dateOnly(*in.DueDate)
			if cur.DueDate == nil || !cur.DueDate.Equal(d) {
				p.DueDate, changed["due_date"] = &d, d.Format("2006-01-02")
			}
		}
		if in.Start != nil && *in.Start != cur.StartValue {
			p.StartValue, changed["start_value"] = *in.Start, *in.Start
		}
		if in.Target != nil && *in.Target != cur.TargetValue {
			p.TargetValue, changed["target_value"] = *in.Target, *in.Target
		}
		if p.StartValue == p.TargetValue {
			return domain.Invalid("target_value", "must differ from the start value")
		}
		g := goalFromRow(store.Goal{StartValue: p.StartValue, TargetValue: p.TargetValue})
		achievedNow := false
		if in.Status != nil && string(*in.Status) != cur.Status {
			if *in.Status == domain.GoalActive && g.Reaches(p.CurrentValue) {
				return domain.Invalid("status", "the current value already reaches the target; change the target first")
			}
			p.Status, changed["status"] = string(*in.Status), string(*in.Status)
			if *in.Status == domain.GoalActive {
				p.AchievedAt = nil
			}
		}
		if p.Status == string(domain.GoalActive) && g.Reaches(p.CurrentValue) { // o alvo mudou e o valor atual já o cobre
			p.Status, p.AchievedAt, achievedNow = string(domain.GoalAchieved), &t.now, true
			changed["status"] = string(domain.GoalAchieved)
		}
		if len(changed) == 0 {
			out = goalFromRow(cur)
			return nil
		}
		row, err := t.q.UpdateGoal(ctx, p)
		if err != nil {
			return mapErr(err, "update goal")
		}
		out = goalFromRow(row)
		if err := t.activity(actor, companyID, "goal.updated", "goal", out.ID, changed, ""); err != nil {
			return err
		}
		if achievedNow {
			return t.activity(actor, companyID, "goal.achieved", "goal", out.ID, map[string]any{"slug": out.Slug, "title": out.Title, "value": out.CurrentValue}, "")
		}
		return nil
	})
	return out, err
}

// RecordGoalProgress registra o valor atual da meta (member, ou persona pelo run token). Só metas ativas aceitam
// progresso. Ao cruzar o alvo a meta vira atingida (activity goal.achieved; aparece na Caixa de entrada).
func (s *Service) RecordGoalProgress(ctx context.Context, actor domain.Actor, companyID, goalID domain.ID, value float64, note string) (GoalView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return GoalView{}, err
	}
	if err := finite("value", value); err != nil {
		return GoalView{}, err
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxGoalNoteRunes {
		return GoalView{}, domain.Invalid("note", fmt.Sprintf("too long (max %d)", maxGoalNoteRunes))
	}
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetGoalByIDForUpdate(ctx, store.GetGoalByIDForUpdateParams{CompanyID: companyID, ID: goalID})
		if err != nil {
			return mapErr(err, "get goal")
		}
		if domain.GoalStatus(cur.Status) != domain.GoalActive {
			return domain.Invalid("goal", fmt.Sprintf("goal is %s and takes no progress", cur.Status))
		}
		g := goalFromRow(cur)
		if _, err := t.q.CreateGoalUpdate(ctx, store.CreateGoalUpdateParams{ID: domain.NewID(), CompanyID: companyID, GoalID: goalID,
			Value: value, Note: note, AuthorType: string(actor.Type), AuthorID: actor.ID, Now: t.now}); err != nil {
			return fmt.Errorf("create goal update: %w", err)
		}
		status, achievedAt := cur.Status, cur.AchievedAt
		reached := g.Reaches(value)
		if reached {
			status, achievedAt = string(domain.GoalAchieved), &t.now
		}
		if _, err := t.q.UpdateGoal(ctx, store.UpdateGoalParams{CompanyID: companyID, ID: goalID, ProjectID: cur.ProjectID,
			Title: cur.Title, Description: cur.Description, Metric: cur.Metric, Unit: cur.Unit, StartValue: cur.StartValue,
			TargetValue: cur.TargetValue, CurrentValue: value, DueDate: cur.DueDate, Status: status,
			OwnerPersonaID: cur.OwnerPersonaID, AchievedAt: achievedAt, Now: t.now}); err != nil {
			return mapErr(err, "update goal")
		}
		data := map[string]any{"from": cur.CurrentValue, "value": value, "progress_percent": domain.GoalProgress(cur.StartValue, cur.TargetValue, value) * 100}
		if note != "" {
			data["note"] = note
		}
		if err := t.activity(actor, companyID, "goal.progress", "goal", goalID, data, ""); err != nil {
			return err
		}
		if reached {
			return t.activity(actor, companyID, "goal.achieved", "goal", goalID,
				map[string]any{"slug": cur.Slug, "title": cur.Title, "value": value, "target_value": cur.TargetValue}, "")
		}
		return nil
	})
	if err != nil {
		return GoalView{}, err
	}
	return s.GetGoalView(ctx, companyID, goalID.String())
}

// GetGoal resolve ref como UUID ou slug.
func (s *Service) GetGoal(ctx context.Context, companyID domain.ID, ref string) (domain.Goal, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Goal{}, err
	}
	q := s.read()
	var row store.Goal
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		row, err = q.GetGoalByID(ctx, store.GetGoalByIDParams{CompanyID: companyID, ID: id})
	} else {
		row, err = q.GetGoalBySlug(ctx, store.GetGoalBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return domain.Goal{}, mapErr(err, "get goal")
	}
	return goalFromRow(row), nil
}

func newGoalView(g domain.Goal, counts map[domain.TaskStatus]int) GoalView {
	v := GoalView{Goal: g, ProgressPercent: g.Progress() * 100, Tasks: map[domain.TaskStatus]int{}}
	for _, st := range domain.TaskStatuses {
		v.Tasks[st] = counts[st]
		v.TasksTotal += counts[st]
	}
	return v
}

// ListGoalViews lista as metas com projeto, dona, progresso e tasks ligadas (ativas primeiro, depois atingidas e
// abandonadas; dentro de cada grupo, por prazo).
func (s *Service) ListGoalViews(ctx context.Context, companyID domain.ID, f GoalFilter) ([]GoalView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	p := store.ListGoalsParams{CompanyID: companyID, ProjectID: f.ProjectID}
	if f.Status != "" {
		if !f.Status.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("unknown status %q", f.Status))
		}
		st := string(f.Status)
		p.Status = &st
	}
	rows, err := q.ListGoals(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	names, err := s.goalNames(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	counts, err := q.GoalTaskCountsAll(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("goal task counts: %w", err)
	}
	byGoal := map[domain.ID]map[domain.TaskStatus]int{}
	for _, c := range counts {
		if c.GoalID == nil {
			continue
		}
		m := byGoal[*c.GoalID]
		if m == nil {
			m = map[domain.TaskStatus]int{}
			byGoal[*c.GoalID] = m
		}
		m[domain.TaskStatus(c.Status)] = int(c.N)
	}
	out := make([]GoalView, len(rows))
	for i, r := range rows {
		out[i] = names.apply(newGoalView(goalFromRow(r), byGoal[r.ID]))
	}
	return out, nil
}

type goalLookup struct {
	projects map[domain.ID]store.Project
	personas map[domain.ID]store.Persona
}

func (s *Service) goalNames(ctx context.Context, q *store.Queries, companyID domain.ID) (goalLookup, error) {
	projects, err := q.ListProjects(ctx, companyID)
	if err != nil {
		return goalLookup{}, fmt.Errorf("list projects: %w", err)
	}
	personas, err := q.ListPersonas(ctx, companyID)
	if err != nil {
		return goalLookup{}, fmt.Errorf("list personas: %w", err)
	}
	l := goalLookup{projects: make(map[domain.ID]store.Project, len(projects)), personas: make(map[domain.ID]store.Persona, len(personas))}
	for _, p := range projects {
		l.projects[p.ID] = p
	}
	for _, p := range personas {
		l.personas[p.ID] = p
	}
	return l, nil
}

func (l goalLookup) apply(v GoalView) GoalView {
	if v.ProjectID != nil {
		if p, ok := l.projects[*v.ProjectID]; ok {
			v.ProjectSlug, v.ProjectName = p.Slug, p.Name
		}
	}
	if v.OwnerPersonaID != nil {
		if p, ok := l.personas[*v.OwnerPersonaID]; ok {
			v.OwnerSlug, v.OwnerName = p.Slug, p.Name
		}
	}
	return v
}

// GetGoalView é o detalhe de uma meta: com o histórico de progresso (do mais antigo ao mais novo) e as tasks
// ligadas por status.
func (s *Service) GetGoalView(ctx context.Context, companyID domain.ID, ref string) (GoalView, error) {
	g, err := s.GetGoal(ctx, companyID, ref)
	if err != nil {
		return GoalView{}, err
	}
	q := s.read()
	counts, err := q.GoalTaskCounts(ctx, store.GoalTaskCountsParams{CompanyID: companyID, GoalID: &g.ID})
	if err != nil {
		return GoalView{}, fmt.Errorf("goal task counts: %w", err)
	}
	m := map[domain.TaskStatus]int{}
	for _, c := range counts {
		m[domain.TaskStatus(c.Status)] = int(c.N)
	}
	names, err := s.goalNames(ctx, q, companyID)
	if err != nil {
		return GoalView{}, err
	}
	v := names.apply(newGoalView(g, m))
	ups, err := q.ListGoalUpdates(ctx, store.ListGoalUpdatesParams{CompanyID: companyID, GoalID: g.ID, Lim: maxGoalHistory})
	if err != nil {
		return GoalView{}, fmt.Errorf("list goal updates: %w", err)
	}
	for i := len(ups) - 1; i >= 0; i-- { // a consulta devolve do mais novo; a tela quer em ordem do tempo
		v.Updates = append(v.Updates, goalUpdateFromRow(ups[i]))
	}
	return v, nil
}

// RecentlyAchievedGoals são as metas atingidas desde since (a Caixa de entrada mostra as dos últimos dias).
func (s *Service) recentlyAchievedGoals(ctx context.Context, q *store.Queries, companyID domain.ID, since time.Time) ([]GoalView, error) {
	rows, err := q.ListRecentlyAchievedGoals(ctx, store.ListRecentlyAchievedGoalsParams{CompanyID: companyID, Since: &since})
	if err != nil {
		return nil, fmt.Errorf("list achieved goals: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	names, err := s.goalNames(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	out := make([]GoalView, len(rows))
	for i, r := range rows {
		out[i] = names.apply(newGoalView(goalFromRow(r), nil))
	}
	return out, nil
}

// goalBriefs são as metas ativas do projeto da task (e a da própria task), em formato curto para o briefing.
func goalBriefs(ctx context.Context, q *store.Queries, companyID domain.ID, task TaskView) ([]GoalBrief, error) {
	if task.ProjectID == nil && task.GoalID == nil {
		return nil, nil
	}
	rows, err := q.ListGoalsForBriefing(ctx, store.ListGoalsForBriefingParams{CompanyID: companyID, ProjectID: task.ProjectID, GoalID: task.GoalID})
	if err != nil {
		return nil, fmt.Errorf("list goals for briefing: %w", err)
	}
	var out []GoalBrief
	for _, r := range rows {
		if len(out) == maxGoalBriefing {
			break
		}
		g := goalFromRow(r)
		out = append(out, GoalBrief{Slug: g.Slug, Title: g.Title, Metric: g.Metric, Unit: g.Unit, Start: g.StartValue, Target: g.TargetValue,
			Current: g.CurrentValue, Progress: g.Progress() * 100, DueDate: g.DueDate, Linked: task.GoalID != nil && *task.GoalID == g.ID})
	}
	return out, nil
}
