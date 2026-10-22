package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/store"
)

// Pipelines sobre tasks (ADR-0048).
//
// Um projeto pode ter etapas (project_stages): cada uma mapeia um status de task e, opcionalmente, uma persona. Uma task
// que entra no pipeline recebe o status e a persona da etapa e a persona é acordada. Quando o trabalho da etapa termina
// (a persona chama hanko_stage_complete, ou dá a task como "done") a task avança para a etapa seguinte; a etapa pode
// pedir um gate antes (aprovação humana ou a revisão da task, com a revisão automática da política vigente). Só os
// gates param o pipeline. As decisões de gate viram aprendizados da etapa: voltam no briefing da persona e entram na
// memória. Nenhuma tabela de gate ou de revisão nova: o pipeline reaproveita gates, task_review e a revisão automática.

// StageView é uma etapa com quantas tasks estão nela agora.
type StageView struct {
	domain.ProjectStage
	Tasks int `json:"tasks"`
}

// ProjectStagesView é o pipeline de um projeto.
type ProjectStagesView struct {
	ProjectID   domain.ID   `json:"project_id"`
	ProjectSlug string      `json:"project_slug"`
	Stages      []StageView `json:"stages"`
}

// TaskStageBrief é a etapa em que uma task está, para as listagens.
type TaskStageBrief struct {
	Key      string                `json:"key"`
	Name     string                `json:"name"`
	Position int                   `json:"position"`
	State    domain.TaskStageState `json:"state"`
}

// TaskStageView é a posição de uma task no pipeline, com o histórico e os aprendizados da etapa.
type TaskStageView struct {
	domain.TaskStage
	Events    []domain.StageEvent    `json:"events"`
	Learnings []domain.StageLearning `json:"learnings"`
}

// AdvanceStageInput move a task à mão: To vazio = a etapa seguinte (ou o fim do pipeline na última).
type AdvanceStageInput struct {
	To   string
	Note string
}

// StageContext é o bloco do briefing da persona que trabalha numa etapa.
type StageContext struct {
	ProjectName  string
	Key, Name    string
	Position     int
	Total        int
	NextName     string // vazio na última etapa
	ExitGate     domain.StageExitGate
	Instructions string
	Learnings    string // markdown pronto (domain.LearningsBlock)
}

func projectStageFromRow(s store.ProjectStage, slug, name string) domain.ProjectStage {
	return domain.ProjectStage{ID: s.ID, ProjectID: s.ProjectID, Key: s.Key, Name: s.Name, Position: int(s.Position),
		Status: domain.TaskStatus(s.Status), PersonaID: s.PersonaID, PersonaSlug: slug, PersonaName: name,
		ExitGate: domain.StageExitGate(s.ExitGate), Instructions: s.Instructions}
}

func (t *txc) stageList(companyID, projectID domain.ID) ([]domain.ProjectStage, error) {
	rows, err := t.q.ListProjectStages(t.ctx, store.ListProjectStagesParams{CompanyID: companyID, ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("list project stages: %w", err)
	}
	return stagesOf(rows), nil
}

func stagesOf(rows []store.ListProjectStagesRow) []domain.ProjectStage {
	out := make([]domain.ProjectStage, len(rows))
	for i, r := range rows {
		out[i] = projectStageFromRow(r.ProjectStage, r.PersonaSlug, r.PersonaName)
	}
	return out
}

func (t *txc) stageByID(companyID, id domain.ID) (domain.ProjectStage, error) {
	r, err := t.q.GetProjectStageByID(t.ctx, store.GetProjectStageByIDParams{CompanyID: companyID, ID: id})
	if err != nil {
		return domain.ProjectStage{}, mapErr(err, "project stage")
	}
	return projectStageFromRow(r.ProjectStage, r.PersonaSlug, r.PersonaName), nil
}

// ---------- configuração ----------

// ProjectStages devolve o pipeline do projeto (vazio quando ele não tem etapas). Viewer.
func (s *Service) ProjectStages(ctx context.Context, companyID domain.ID, projectRef string) (ProjectStagesView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ProjectStagesView{}, err
	}
	pr, err := s.GetProject(ctx, companyID, projectRef)
	if err != nil {
		return ProjectStagesView{}, err
	}
	q := s.read()
	rows, err := q.ListProjectStages(ctx, store.ListProjectStagesParams{CompanyID: companyID, ProjectID: pr.ID})
	if err != nil {
		return ProjectStagesView{}, fmt.Errorf("list project stages: %w", err)
	}
	counts, err := q.ProjectStageCounts(ctx, store.ProjectStageCountsParams{CompanyID: companyID, ProjectID: pr.ID})
	if err != nil {
		return ProjectStagesView{}, fmt.Errorf("project stage counts: %w", err)
	}
	by := map[domain.ID]int{}
	for _, c := range counts {
		by[c.StageID] = int(c.Tasks)
	}
	out := ProjectStagesView{ProjectID: pr.ID, ProjectSlug: pr.Slug, Stages: []StageView{}}
	for _, st := range stagesOf(rows) {
		out.Stages = append(out.Stages, StageView{ProjectStage: st, Tasks: by[st.ID]})
	}
	return out, nil
}

// SetProjectStages troca o pipeline do projeto pela lista informada (as chaves casam as etapas existentes; a ordem da
// lista é a ordem do pipeline). Admin. Uma etapa que sai da lista não pode ter tasks nela: mova-as antes.
func (s *Service) SetProjectStages(ctx context.Context, actor domain.Actor, companyID domain.ID, projectRef string, specs []domain.StageSpec) (ProjectStagesView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ProjectStagesView{}, err
	}
	for i := range specs {
		specs[i].Normalize()
	}
	if err := domain.ValidateStageSpecs(specs); err != nil {
		return ProjectStagesView{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		projectID, err := t.projectByRef(companyID, projectRef)
		if err != nil {
			return domain.Invalid("project", "project not found in company")
		}
		pr, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID})
		if err != nil {
			return mapErr(err, "project")
		}
		if domain.ProjectStatus(pr.Status) == domain.ProjectArchived {
			return domain.Invalid("project", "project is archived")
		}
		if err := t.q.LockProjectStages(t.ctx, projectID); err != nil {
			return fmt.Errorf("lock project stages: %w", err)
		}
		cur, err := t.stageList(companyID, projectID)
		if err != nil {
			return err
		}
		byKey := map[string]domain.ProjectStage{}
		for _, st := range cur {
			byKey[st.Key] = st
		}
		keep := map[string]bool{}
		for i, sp := range specs {
			var personaID *domain.ID
			if sp.Persona != "" {
				id, err := t.personaByRef(companyID, sp.Persona)
				if err != nil {
					return domain.Invalid(fmt.Sprintf("stages[%d].persona", i), "persona not found in company")
				}
				personaID = &id
			}
			keep[sp.Key] = true
			if ex, ok := byKey[sp.Key]; ok {
				if err := t.q.UpdateProjectStage(t.ctx, store.UpdateProjectStageParams{CompanyID: companyID, ID: ex.ID, Name: sp.Name,
					Position: i32(i + 1), Status: string(sp.Status), PersonaID: personaID, ExitGate: string(sp.ExitGate),
					Instructions: sp.Instructions, Now: t.now}); err != nil {
					return mapErr(err, "update project stage")
				}
				continue
			}
			if _, err := t.q.InsertProjectStage(t.ctx, store.InsertProjectStageParams{ID: domain.NewID(), CompanyID: companyID,
				ProjectID: projectID, Key: sp.Key, Name: sp.Name, Position: i32(i + 1), Status: string(sp.Status), PersonaID: personaID,
				ExitGate: string(sp.ExitGate), Instructions: sp.Instructions, Now: t.now}); err != nil {
				return mapErr(err, "insert project stage")
			}
		}
		for _, st := range cur {
			if keep[st.Key] {
				continue
			}
			if err := t.deleteStage(companyID, st); err != nil {
				return err
			}
		}
		keys := make([]string, len(specs))
		for i, sp := range specs {
			keys[i] = sp.Key
		}
		return t.activity(actor, companyID, "project.stages_set", "project", projectID, map[string]any{"stages": keys}, "")
	})
	if err != nil {
		return ProjectStagesView{}, err
	}
	return s.ProjectStages(ctx, companyID, projectRef)
}

// ClearProjectStages desliga o pipeline do projeto (apaga as etapas). Admin; recusa enquanto houver task numa etapa.
func (s *Service) ClearProjectStages(ctx context.Context, actor domain.Actor, companyID domain.ID, projectRef string) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		projectID, err := t.projectByRef(companyID, projectRef)
		if err != nil {
			return domain.Invalid("project", "project not found in company")
		}
		if err := t.q.LockProjectStages(t.ctx, projectID); err != nil {
			return fmt.Errorf("lock project stages: %w", err)
		}
		cur, err := t.stageList(companyID, projectID)
		if err != nil {
			return err
		}
		for _, st := range cur {
			if err := t.deleteStage(companyID, st); err != nil {
				return err
			}
		}
		return t.activity(actor, companyID, "project.stages_cleared", "project", projectID, map[string]any{"count": len(cur)}, "")
	})
}

func (t *txc) deleteStage(companyID domain.ID, st domain.ProjectStage) error {
	n, err := t.q.CountOpenTasksInStage(t.ctx, st.ID)
	if err != nil {
		return fmt.Errorf("count tasks in stage: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("a etapa %q ainda tem %d task(s): avance ou tire-as do pipeline antes: %w", st.Key, n, domain.ErrConflict)
	}
	if err := t.q.DeleteProjectStage(t.ctx, store.DeleteProjectStageParams{CompanyID: companyID, ID: st.ID}); err != nil {
		return fmt.Errorf("delete project stage: %w", err)
	}
	return nil
}

// ---------- leitura ----------

func (s *Service) taskIDOfRef(ctx context.Context, companyID domain.ID, ref string) (domain.ID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return id, nil
	}
	tv, err := s.GetTask(ctx, companyID, ref)
	if err != nil {
		return domain.ID{}, err
	}
	return tv.ID, nil
}

// TaskStage devolve a posição da task no pipeline do projeto, com o histórico e os aprendizados da etapa. Viewer.
// ErrNotFound quando a task não está em nenhum pipeline.
func (s *Service) TaskStage(ctx context.Context, companyID domain.ID, taskRef string) (TaskStageView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return TaskStageView{}, err
	}
	taskID, err := s.taskIDOfRef(ctx, companyID, taskRef)
	if err != nil {
		return TaskStageView{}, err
	}
	q := s.read()
	ts, err := q.GetTaskStage(ctx, store.GetTaskStageParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return TaskStageView{}, mapErr(err, "task stage")
	}
	rows, err := q.ListProjectStages(ctx, store.ListProjectStagesParams{CompanyID: companyID, ProjectID: ts.ProjectID})
	if err != nil {
		return TaskStageView{}, fmt.Errorf("list project stages: %w", err)
	}
	stages := stagesOf(rows)
	out := TaskStageView{Events: []domain.StageEvent{}, Learnings: []domain.StageLearning{}}
	out.TaskStage = taskStageOf(ts, stages)
	evs, err := q.ListTaskStageEvents(ctx, store.ListTaskStageEventsParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return TaskStageView{}, fmt.Errorf("list stage events: %w", err)
	}
	for _, e := range evs {
		out.Events = append(out.Events, domain.StageEvent{ID: e.ID, TaskID: e.TaskID, Kind: domain.StageEventKind(e.Kind), StageKey: e.StageKey,
			ToStageKey: e.ToStageKey, Actor: actorOf(e.ActorType, e.ActorID), GateID: e.GateID, Decision: e.Decision, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	out.Learnings, err = s.stageLearnings(ctx, q, companyID, ts.ProjectID, out.Stage.Key, domain.MaxStageLearnings)
	return out, err
}

func taskStageOf(ts store.TaskStage, stages []domain.ProjectStage) domain.TaskStage {
	out := domain.TaskStage{TaskID: ts.TaskID, ProjectID: ts.ProjectID, State: domain.TaskStageState(ts.State), Total: len(stages),
		EnteredAt: ts.EnteredAt, UpdatedAt: ts.UpdatedAt}
	for _, st := range stages {
		if st.ID == ts.StageID {
			out.Stage, out.Position = st, st.Position
			if n, ok := domain.NextStage(stages, st.Key); ok {
				out.NextKey = n.Key
			}
		}
	}
	return out
}

// StageLearnings lista os aprendizados (decisões de gate com nota ou sem aprovação) de um projeto, de uma etapa
// ou de todas (stageKey vazio). Viewer.
func (s *Service) StageLearnings(ctx context.Context, companyID domain.ID, projectRef, stageKey string, limit int) ([]domain.StageLearning, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	pr, err := s.GetProject(ctx, companyID, projectRef)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.stageLearnings(ctx, s.read(), companyID, pr.ID, strings.TrimSpace(stageKey), limit)
}

func (s *Service) stageLearnings(ctx context.Context, q *store.Queries, companyID, projectID domain.ID, stageKey string, limit int) ([]domain.StageLearning, error) {
	rows, err := q.ListStageLearnings(ctx, store.ListStageLearningsParams{CompanyID: companyID, ProjectID: projectID, StageKey: stageKey, Lim: i32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list stage learnings: %w", err)
	}
	prefix := ""
	if len(rows) > 0 {
		co, err := q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return nil, mapErr(err, "company")
		}
		prefix = co.TaskPrefix
	}
	out := make([]domain.StageLearning, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.StageLearning{StageKey: r.StageKey, TaskKey: domain.TaskKey(prefix, r.TaskNumber), Decision: r.Decision,
			Note: r.Note, GateID: r.GateID, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// stageContext monta o bloco do briefing de uma persona que trabalha numa task do pipeline; nil sem pipeline, com a
// task concluída ou com a etapa aguardando gate.
func stageContext(ctx context.Context, q *store.Queries, companyID domain.ID, task domain.ID) (*StageContext, error) {
	ts, err := q.GetTaskStage(ctx, store.GetTaskStageParams{CompanyID: companyID, TaskID: task})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("task stage: %w", err)
	}
	if ts.State == string(domain.TaskStageFinished) {
		return nil, nil
	}
	rows, err := q.ListProjectStages(ctx, store.ListProjectStagesParams{CompanyID: companyID, ProjectID: ts.ProjectID})
	if err != nil {
		return nil, fmt.Errorf("list project stages: %w", err)
	}
	stages := stagesOf(rows)
	cur := taskStageOf(ts, stages)
	pr, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: ts.ProjectID})
	if err != nil {
		return nil, mapErr(err, "project")
	}
	sc := &StageContext{ProjectName: pr.Name, Key: cur.Stage.Key, Name: cur.Stage.Name, Position: cur.Position, Total: cur.Total,
		ExitGate: cur.Stage.ExitGate, Instructions: cur.Stage.Instructions}
	if n, ok := domain.NextStage(stages, cur.Stage.Key); ok {
		sc.NextName = n.Name
	}
	lr, err := q.ListStageLearnings(ctx, store.ListStageLearningsParams{CompanyID: companyID, ProjectID: ts.ProjectID,
		StageKey: cur.Stage.Key, Lim: domain.MaxStageLearnings})
	if err != nil {
		return nil, fmt.Errorf("stage learnings: %w", err)
	}
	if len(lr) > 0 {
		co, err := q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return nil, mapErr(err, "company")
		}
		ls := make([]domain.StageLearning, len(lr))
		for i, r := range lr {
			ls[i] = domain.StageLearning{StageKey: r.StageKey, TaskKey: domain.TaskKey(co.TaskPrefix, r.TaskNumber), Decision: r.Decision,
				Note: r.Note, GateID: r.GateID, CreatedAt: r.CreatedAt}
		}
		sc.Learnings = domain.LearningsBlock(ls)
	}
	return sc, nil
}

// enrichTaskStages preenche TaskView.Stage das tasks que estão num pipeline.
func enrichTaskStages(ctx context.Context, q *store.Queries, companyID domain.ID, views []*TaskView) error {
	if len(views) == 0 {
		return nil
	}
	ids := make([]domain.ID, len(views))
	for i, v := range views {
		ids[i] = v.ID
	}
	rows, err := q.ListTaskStagesForTasks(ctx, store.ListTaskStagesForTasksParams{CompanyID: companyID, TaskIds: ids})
	if err != nil {
		return fmt.Errorf("task stages: %w", err)
	}
	by := make(map[domain.ID]store.ListTaskStagesForTasksRow, len(rows))
	for _, r := range rows {
		by[r.TaskID] = r
	}
	for _, v := range views {
		if r, ok := by[v.ID]; ok && r.State != string(domain.TaskStageFinished) {
			v.Stage = &TaskStageBrief{Key: r.StageKey, Name: r.StageName, Position: int(r.StagePosition), State: domain.TaskStageState(r.State)}
		}
	}
	return nil
}

// ---------- entrada no pipeline ----------

// firstStageFor devolve a primeira etapa do pipeline do projeto (nil quando ele não tem).
func (t *txc) firstStageFor(companyID domain.ID, projectID domain.ID) (*domain.ProjectStage, error) {
	stages, err := t.stageList(companyID, projectID)
	if err != nil || len(stages) == 0 {
		return nil, err
	}
	return &stages[0], nil
}

// stageEvent grava uma linha imutável no histórico do pipeline.
func (t *txc) stageEvent(actor domain.Actor, companyID, projectID, taskID domain.ID, kind domain.StageEventKind, stageKey, toKey string,
	gateID *domain.ID, decision, note string) error {
	note = truncateRunes(strings.TrimSpace(note), domain.MaxStageNoteRunes*4)
	if err := t.q.InsertTaskStageEvent(t.ctx, store.InsertTaskStageEventParams{ID: domain.NewID(), CompanyID: companyID, ProjectID: projectID,
		TaskID: taskID, Kind: string(kind), StageKey: stageKey, ToStageKey: toKey, ActorType: string(actor.Type), ActorID: actor.ID,
		GateID: gateID, Decision: decision, Note: note, Now: t.now}); err != nil {
		return mapErr(err, "insert stage event")
	}
	return nil
}

// StartTaskStage põe uma task já existente no pipeline do projeto dela, na etapa informada (padrão: a primeira). Member.
func (s *Service) StartTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, stageKey string) (TaskStageView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskStageView{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if err := t.checkTaskOwnership(actor, companyID, task.PersonaID); err != nil {
			return err
		}
		if !domain.TaskStatus(task.Status).Open() {
			return domain.Invalid("task", "a task está encerrada")
		}
		if task.ProjectID == nil {
			return domain.Invalid("task", "a task não tem projeto: o pipeline é do projeto")
		}
		if sys, err := t.isSystemTask(task.ID); err != nil {
			return err
		} else if sys {
			return domain.Invalid("task", "task de sistema não entra em pipeline")
		}
		if ts, err := t.q.GetTaskStageForUpdate(t.ctx, store.GetTaskStageForUpdateParams{CompanyID: companyID, TaskID: taskID}); err == nil {
			if ts.State != string(domain.TaskStageFinished) {
				return fmt.Errorf("a task já está no pipeline: %w", domain.ErrConflict)
			}
		} else if !isNoRows(err) {
			return fmt.Errorf("task stage: %w", err)
		}
		stages, err := t.stageList(companyID, *task.ProjectID)
		if err != nil {
			return err
		}
		if len(stages) == 0 {
			return domain.Invalid("project", "o projeto não tem etapas")
		}
		st := stages[0]
		if k := strings.TrimSpace(stageKey); k != "" {
			f, ok := domain.FindStage(stages, k)
			if !ok {
				return domain.Invalid("stage", fmt.Sprintf("etapa %q não existe no projeto", k))
			}
			st = f
		}
		_, err = t.stageApply(actor, companyID, task, st, domain.StageEvEnrolled, "", "", domain.TriggerAssign)
		return err
	})
	if err != nil {
		return TaskStageView{}, err
	}
	return s.TaskStage(ctx, companyID, taskID.String())
}

// stageApply põe a task na etapa st: status e persona mapeados, posição, histórico, comentário de sistema e o wake da
// persona da etapa. A task já está travada (FOR UPDATE).
func (t *txc) stageApply(actor domain.Actor, companyID domain.ID, task store.Task, st domain.ProjectStage, ev domain.StageEventKind,
	fromKey, note string, trigger domain.RunTrigger) (store.Task, error) {
	ctx := t.ctx
	from := domain.TaskStatus(task.Status)
	want := st.Status
	statusChanged := from != want
	personaChanged := !sameID(task.PersonaID, st.PersonaID)
	row := task
	if statusChanged || personaChanged {
		p := store.UpdateTaskParams{CompanyID: companyID, ID: task.ID, Title: task.Title, Description: task.Description,
			Status: string(want), Priority: task.Priority, PersonaID: st.PersonaID, ProjectID: task.ProjectID, Model: task.Model, Now: t.now}
		r, err := t.q.UpdateTask(ctx, p)
		if err != nil {
			return store.Task{}, mapErr(err, "update task")
		}
		row = r
		if actor.Type != domain.ActorPersona {
			if err := t.q.TouchTaskHumanChange(ctx, store.TouchTaskHumanChangeParams{ID: task.ID, Now: &t.now}); err != nil {
				return store.Task{}, fmt.Errorf("touch task: %w", err)
			}
		}
		if statusChanged {
			if err := t.activity(actor, companyID, "task.status_changed", "task", task.ID,
				map[string]any{"from": from, "to": want, "stage": st.Key}, ""); err != nil {
				return store.Task{}, err
			}
		}
		if personaChanged {
			if err := t.activity(actor, companyID, "task.assigned", "task", task.ID,
				map[string]any{"from": task.PersonaID, "to": st.PersonaID, "stage": st.Key}, ""); err != nil {
				return store.Task{}, err
			}
			if task.PersonaID != nil {
				if err := t.cancelStageRun(actor, companyID, task.ID); err != nil {
					return store.Task{}, err
				}
			}
		}
	}
	if _, err := t.q.UpsertTaskStage(ctx, store.UpsertTaskStageParams{TaskID: task.ID, CompanyID: companyID, ProjectID: st.ProjectID,
		StageID: st.ID, State: string(domain.TaskStageWorking), Now: t.now}); err != nil {
		return store.Task{}, mapErr(err, "upsert task stage")
	}
	if err := t.stageEvent(actor, companyID, st.ProjectID, task.ID, ev, fromKey, st.Key, nil, "", note); err != nil {
		return store.Task{}, err
	}
	if err := t.activity(actor, companyID, "task_stage.entered", "task", task.ID,
		map[string]any{"stage": st.Key, "from": fromKey, "kind": ev, "persona_id": st.PersonaID}, ""); err != nil {
		return store.Task{}, err
	}
	countCoord(ctx, "stage", string(ev))
	if err := t.stageComment(companyID, task.ID, st, fromKey, ev, note); err != nil {
		return store.Task{}, err
	}
	if err := t.wake(companyID, task.ID, st.PersonaID, domain.TaskStatus(row.Status), trigger, true); err != nil {
		return store.Task{}, err
	}
	if tv, err := loadTaskView(ctx, t.q, companyID, task.ID); err == nil {
		t.publish(companyTopic(companyID), "task.updated", tv)
	}
	return row, nil
}

// cancelStageRun cancela a run ativa da persona que deixa a task, menos quando é a própria persona que conclui a etapa:
// a run dela termina sozinha (resumo publicado) e o wake da persona seguinte espera por ela (a task só tem uma run ativa).
func (t *txc) cancelStageRun(actor domain.Actor, companyID, taskID domain.ID) error {
	if actor.Type == domain.ActorPersona && actor.ID != nil {
		if run, err := t.q.ActiveRunForTask(t.ctx, taskID); err == nil && run.PersonaID == *actor.ID {
			return nil
		}
	}
	return t.cancelTaskRun(actor, companyID, taskID, "stage_changed")
}

// stageComment deixa na thread da task o que o pipeline fez (comentário de sistema, sem acordar ninguém).
func (t *txc) stageComment(companyID, taskID domain.ID, st domain.ProjectStage, fromKey string, ev domain.StageEventKind, note string) error {
	var b strings.Builder
	switch ev {
	case domain.StageEvEnrolled:
		fmt.Fprintf(&b, "**Pipeline**: a task entrou na etapa **%s**.", st.Name)
	case domain.StageEvReturned:
		fmt.Fprintf(&b, "**Pipeline**: a task voltou para a etapa **%s**.", st.Name)
	default:
		fmt.Fprintf(&b, "**Pipeline**: etapa **%s** concluída; a task avançou para **%s**.", fromKey, st.Name)
	}
	if st.PersonaName != "" {
		fmt.Fprintf(&b, " Responsável: %s.", st.PersonaName)
	}
	if n := strings.TrimSpace(note); n != "" {
		fmt.Fprintf(&b, "\n\n%s", truncateRunes(n, domain.MaxStageNoteRunes))
	}
	if _, err := t.q.CreateComment(t.ctx, store.CreateCommentParams{ID: domain.NewID(), CompanyID: companyID, TaskID: taskID,
		AuthorType: string(domain.ActorSystem), Body: b.String(), Now: t.now}); err != nil {
		return mapErr(err, "stage comment")
	}
	return nil
}

// ---------- conclusão da etapa ----------

// CompleteTaskStage diz que o trabalho da etapa terminou. Sem gate na etapa a task avança para a seguinte (ou termina,
// na última); com gate, o avanço espera a decisão. Persona (a dona da task) ou member. Repetir enquanto o gate está
// pendente não faz nada.
func (s *Service) CompleteTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) (TaskStageView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskStageView{}, err
	}
	if utf8.RuneCountInString(note) > domain.MaxStageNoteRunes {
		return TaskStageView{}, domain.Invalid("note", "too long")
	}
	ctx, span := coordSpan(ctx, "service.stage_complete", attribute.String("task_id", taskID.String()))
	defer span()
	err := s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if err := t.checkTaskOwnership(actor, companyID, task.PersonaID); err != nil {
			return err
		}
		return t.stageComplete(actor, companyID, task, note)
	})
	if err != nil {
		return TaskStageView{}, err
	}
	return s.TaskStage(ctx, companyID, taskID.String())
}

// stageComplete é o núcleo da conclusão da etapa (task travada). Devolve ErrInvalid quando a task não está num
// pipeline ativo.
func (t *txc) stageComplete(actor domain.Actor, companyID domain.ID, task store.Task, note string) error {
	ts, err := t.q.GetTaskStageForUpdate(t.ctx, store.GetTaskStageForUpdateParams{CompanyID: companyID, TaskID: task.ID})
	if isNoRows(err) {
		return domain.Invalid("task", "a task não está em um pipeline")
	}
	if err != nil {
		return fmt.Errorf("task stage: %w", err)
	}
	switch domain.TaskStageState(ts.State) {
	case domain.TaskStageFinished:
		return domain.Invalid("task", "a task já saiu do pipeline")
	case domain.TaskStageCompleting:
		return nil // já espera o gate
	}
	st, err := t.stageByID(companyID, ts.StageID)
	if err != nil {
		return err
	}
	if err := t.stageEvent(actor, companyID, ts.ProjectID, task.ID, domain.StageEvCompleted, st.Key, "", nil, "", note); err != nil {
		return err
	}
	gate := st.ExitGate
	if gate == domain.StageGateNone && actor.Type == domain.ActorPersona {
		// a política de revisão da persona/projeto/task (ADR-0040) continua valendo dentro do pipeline
		req, err := t.reviewRequiredFor(actor, companyID, task)
		if err != nil {
			return err
		}
		if req {
			gate = domain.StageGateReview
		}
	}
	switch gate {
	case domain.StageGateReview:
		reviewer := actor
		if reviewer.Type != domain.ActorPersona {
			if task.PersonaID == nil {
				gate = domain.StageGateApproval // etapa humana: não há autora para a revisão, a aprovação faz o papel
			} else {
				reviewer = domain.PersonaActor(*task.PersonaID)
			}
		}
		if gate == domain.StageGateReview {
			return t.stageHold(actor, reviewer, companyID, task, st, note, true)
		}
		fallthrough
	case domain.StageGateApproval:
		return t.stageHold(actor, domain.SystemActor, companyID, task, st, note, false)
	}
	_, err = t.stageAdvance(actor, companyID, task, ts, st, "", note, false)
	return err
}

// stageHold para a task no gate da etapa: ela vai para in_review e o gate (task_review com a revisão automática da
// política, ou aprovação) é aberto; a decisão avança ou devolve (stageGateDecided).
func (t *txc) stageHold(actor, requester domain.Actor, companyID domain.ID, task store.Task, st domain.ProjectStage, note string, review bool) error {
	ctx := t.ctx
	row := task
	if from := domain.TaskStatus(task.Status); from != domain.TaskInReview && from.CanTransition(domain.TaskInReview) {
		r, err := t.moveTask(actor, companyID, task, domain.TaskInReview, map[string]any{"stage": st.Key})
		if err != nil {
			return err
		}
		row = r
	}
	if err := t.q.SetTaskStageState(ctx, store.SetTaskStageStateParams{TaskID: task.ID, State: string(domain.TaskStageCompleting), Now: t.now}); err != nil {
		return fmt.Errorf("set task stage state: %w", err)
	}
	countCoord(ctx, "stage", string(domain.StageEvGateRequested))
	if review {
		if err := t.requestTaskReview(requester, companyID, row, domain.TaskDone); err != nil {
			return err
		}
		return t.stageEvent(actor, companyID, st.ProjectID, task.ID, domain.StageEvGateRequested, st.Key, "", nil, "review", note)
	}
	co, err := t.q.GetCompanyByID(ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	key := domain.TaskKey(co.TaskPrefix, task.Number)
	body := fmt.Sprintf("A etapa **%s** do pipeline terminou. Aprove para a task avançar; peça mudanças ou rejeite para a etapa voltar à persona.", st.Name)
	if n := strings.TrimSpace(note); n != "" {
		body += "\n\n## Entrega\n\n" + n
	}
	g, err := t.createGate(domain.SystemActor, companyID, RequestGateInput{Kind: domain.GateApproval, TaskID: &task.ID,
		Title: fmt.Sprintf("Aprovar etapa %s — %s — %s", st.Name, key, task.Title), Body: body}, nil)
	if err != nil {
		return err
	}
	return t.stageEvent(actor, companyID, st.ProjectID, task.ID, domain.StageEvGateRequested, st.Key, "", &g.ID, "approval", note)
}

// stageAdvance leva a task para a etapa seguinte; na última, encerra a task como concluída e o pipeline como terminado.
// to != "" escolhe a etapa de destino (humano). A task já está travada.
func (t *txc) stageAdvance(actor domain.Actor, companyID domain.ID, task store.Task, ts store.TaskStage, cur domain.ProjectStage,
	to, note string, manual bool) (store.Task, error) {
	stages, err := t.stageList(companyID, cur.ProjectID)
	if err != nil {
		return store.Task{}, err
	}
	var dest domain.ProjectStage
	var ok bool
	if to != "" {
		if dest, ok = domain.FindStage(stages, to); !ok {
			return store.Task{}, domain.Invalid("to", fmt.Sprintf("etapa %q não existe no projeto", to))
		}
	} else {
		dest, ok = domain.NextStage(stages, cur.Key)
	}
	if !ok { // atravessou a última etapa
		if err := t.q.SetTaskStageState(t.ctx, store.SetTaskStageStateParams{TaskID: task.ID, State: string(domain.TaskStageFinished), Now: t.now}); err != nil {
			return store.Task{}, fmt.Errorf("set task stage state: %w", err)
		}
		if err := t.stageEvent(actor, companyID, cur.ProjectID, task.ID, domain.StageEvFinished, cur.Key, "", nil, decisionOf(manual), note); err != nil {
			return store.Task{}, err
		}
		if err := t.activity(actor, companyID, "task_stage.finished", "task", task.ID, map[string]any{"stage": cur.Key}, ""); err != nil {
			return store.Task{}, err
		}
		countCoord(t.ctx, "stage", string(domain.StageEvFinished))
		row := task
		if domain.TaskStatus(task.Status).Open() {
			if row, err = t.moveTask(actor, companyID, task, domain.TaskDone, map[string]any{"stage": cur.Key}); err != nil {
				return store.Task{}, err
			}
		}
		return row, nil
	}
	ev := domain.StageEvAdvanced
	if dest.Position <= cur.Position {
		ev = domain.StageEvReturned
	}
	row, err := t.stageApply(actor, companyID, task, dest, ev, cur.Key, note, domain.TriggerAssign)
	_ = ts
	return row, err
}

func decisionOf(manual bool) string {
	if manual {
		return "manual"
	}
	return ""
}

// AdvanceTaskStage move a task à mão: à etapa seguinte (ou ao fim do pipeline) ou à etapa To, para a frente ou para
// trás. Só uma pessoa (member): é a saída de emergência que passa por cima dos gates; o gate pendente do pipeline é
// expirado.
func (s *Service) AdvanceTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in AdvanceStageInput) (TaskStageView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskStageView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return TaskStageView{}, fmt.Errorf("advance stage: uma persona conclui a etapa (hanko_stage_complete); só uma pessoa move a task à mão: %w", domain.ErrNotAllowed)
	}
	if utf8.RuneCountInString(in.Note) > domain.MaxStageNoteRunes {
		return TaskStageView{}, domain.Invalid("note", "too long")
	}
	err := s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		ts, err := t.q.GetTaskStageForUpdate(t.ctx, store.GetTaskStageForUpdateParams{CompanyID: companyID, TaskID: taskID})
		if isNoRows(err) {
			return domain.Invalid("task", "a task não está em um pipeline")
		}
		if err != nil {
			return fmt.Errorf("task stage: %w", err)
		}
		if ts.State == string(domain.TaskStageFinished) {
			return domain.Invalid("task", "a task já saiu do pipeline")
		}
		cur, err := t.stageByID(companyID, ts.StageID)
		if err != nil {
			return err
		}
		if ts.State == string(domain.TaskStageCompleting) {
			if err := t.expireTaskGates(companyID, taskID, "stage_advanced_manually"); err != nil {
				return err
			}
		}
		_, err = t.stageAdvance(actor, companyID, task, ts, cur, strings.TrimSpace(in.To), in.Note, true)
		return err
	})
	if err != nil {
		return TaskStageView{}, err
	}
	return s.TaskStage(ctx, companyID, taskID.String())
}

// LeaveTaskStage tira a task do pipeline (ela segue como task comum, com o status e a persona de agora). Member.
func (s *Service) LeaveTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) error {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if err := t.checkTaskOwnership(actor, companyID, task.PersonaID); err != nil {
			return err
		}
		ts, err := t.q.GetTaskStageForUpdate(t.ctx, store.GetTaskStageForUpdateParams{CompanyID: companyID, TaskID: taskID})
		if isNoRows(err) || err == nil && ts.State == string(domain.TaskStageFinished) {
			return domain.Invalid("task", "a task não está em um pipeline")
		}
		if err != nil {
			return fmt.Errorf("task stage: %w", err)
		}
		cur, err := t.stageByID(companyID, ts.StageID)
		if err != nil {
			return err
		}
		if ts.State == string(domain.TaskStageCompleting) {
			if err := t.expireTaskGates(companyID, taskID, "stage_left"); err != nil {
				return err
			}
		}
		return t.stageLeave(actor, companyID, task, cur, note)
	})
}

func (t *txc) stageLeave(actor domain.Actor, companyID domain.ID, task store.Task, cur domain.ProjectStage, note string) error {
	if err := t.q.SetTaskStageState(t.ctx, store.SetTaskStageStateParams{TaskID: task.ID, State: string(domain.TaskStageFinished), Now: t.now}); err != nil {
		return fmt.Errorf("set task stage state: %w", err)
	}
	if err := t.stageEvent(actor, companyID, cur.ProjectID, task.ID, domain.StageEvLeft, cur.Key, "", nil, "", note); err != nil {
		return err
	}
	return t.activity(actor, companyID, "task_stage.left", "task", task.ID, map[string]any{"stage": cur.Key}, "")
}

// stageTaskClosed tira do pipeline a task que foi encerrada por fora dele (uma pessoa deu como concluída ou cancelou).
func (t *txc) stageTaskClosed(actor domain.Actor, companyID domain.ID, task store.Task) error {
	ts, err := t.q.GetTaskStageForUpdate(t.ctx, store.GetTaskStageForUpdateParams{CompanyID: companyID, TaskID: task.ID})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("task stage: %w", err)
	}
	if ts.State == string(domain.TaskStageFinished) {
		return nil
	}
	cur, err := t.stageByID(companyID, ts.StageID)
	if err != nil {
		return err
	}
	return t.stageLeave(actor, companyID, task, cur, "task encerrada fora do pipeline")
}

// stageIntercepts diz se um status pedido por uma persona deve virar "etapa concluída": a persona de uma task em
// pipeline que dá a task como concluída não a fecha, conclui a etapa.
func (t *txc) stageIntercepts(actor domain.Actor, companyID domain.ID, taskID domain.ID, want *domain.TaskStatus) (bool, error) {
	if want == nil || *want != domain.TaskDone || actor.Type != domain.ActorPersona {
		return false, nil
	}
	ts, err := t.q.GetTaskStage(t.ctx, store.GetTaskStageParams{CompanyID: companyID, TaskID: taskID})
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("task stage: %w", err)
	}
	return ts.State != string(domain.TaskStageFinished), nil
}

// ---------- decisão de gate ----------

// stageManagedGate diz se o gate g decide o avanço de uma etapa: aprovação ou revisão de uma task cuja etapa está
// aguardando gate. Devolve a posição da task.
func (t *txc) stageManagedGate(companyID domain.ID, g domain.Gate) (store.TaskStage, bool, error) {
	if g.TaskID == nil || (g.Kind != domain.GateApproval && g.Kind != domain.GateTaskReview) {
		return store.TaskStage{}, false, nil
	}
	ts, err := t.q.GetTaskStageForUpdate(t.ctx, store.GetTaskStageForUpdateParams{CompanyID: companyID, TaskID: *g.TaskID})
	if isNoRows(err) {
		return store.TaskStage{}, false, nil
	}
	if err != nil {
		return store.TaskStage{}, false, fmt.Errorf("task stage: %w", err)
	}
	return ts, ts.State == string(domain.TaskStageCompleting), nil
}

// stageGateDecided aplica a decisão do gate da etapa: grava o aprendizado (histórico e memória) e avança a task
// (aprovado) ou devolve a etapa à persona (mudanças pedidas ou rejeição). A persona que estava presa ao gate já foi
// liberada por quem chama.
func (t *txc) stageGateDecided(actor domain.Actor, companyID domain.ID, g domain.Gate, ts store.TaskStage, decision domain.GateStatus) error {
	task, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: ts.TaskID})
	if err != nil {
		return mapErr(err, "get task")
	}
	cur, err := t.stageByID(companyID, ts.StageID)
	if err != nil {
		return err
	}
	if err := t.stageEvent(actor, companyID, ts.ProjectID, task.ID, domain.StageEvGateDecided, cur.Key, "", &g.ID, string(decision), g.DecisionNote); err != nil {
		return err
	}
	if err := t.stageLearningFact(companyID, task, cur, g, decision); err != nil {
		return err
	}
	if decision == domain.GateApproved {
		_, err = t.stageAdvance(actor, companyID, task, ts, cur, "", g.DecisionNote, false)
		return err
	}
	// mudanças pedidas ou rejeição: a etapa volta à persona, com o status mapeado, e ela é acordada com a nota
	row, err := t.stageApply(actor, companyID, task, cur, domain.StageEvReturned, cur.Key, g.DecisionNote, domain.TriggerGate)
	_ = row
	return err
}

// stageLearningFact grava a decisão de gate da etapa na memória do workspace (o aprendizado vive além da task).
func (t *txc) stageLearningFact(companyID domain.ID, task store.Task, cur domain.ProjectStage, g domain.Gate, decision domain.GateStatus) error {
	pr, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: cur.ProjectID})
	if err != nil {
		return mapErr(err, "project")
	}
	co, err := t.q.GetCompanyByID(t.ctx, companyID)
	if err != nil {
		return mapErr(err, "company")
	}
	key := domain.TaskKey(co.TaskPrefix, task.Number)
	ws := task.WorkspaceID
	if ws == nil {
		ws = pr.WorkspaceID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Aprendizado da etapa %s (%s)\n\n", cur.Name, pr.Name)
	fmt.Fprintf(&b, "- Projeto: %s\n- Etapa: %s\n- Task: %s — %s\n- Decisão: %s\n- Gate: %s\n", pr.Name, cur.Key, key, task.Title,
		domain.LearningDecisionLabel(string(decision)), g.ID)
	if n := strings.TrimSpace(g.DecisionNote); n != "" {
		fmt.Fprintf(&b, "\n## O que o humano disse\n\n%s\n", n)
	}
	f := memory.Fact{Kind: "pipeline.learning",
		Path:  fmt.Sprintf("hanko/pipelines/%s/%s/%s-%s-%s.md", pr.Slug, cur.Key, t.now.Format("2006-01-02"), key, g.ID.String()[:8]),
		Title: fmt.Sprintf("Etapa %s: %s (%s)", cur.Name, strings.ToLower(domain.LearningDecisionLabel(string(decision))), key),
		Body:  b.String(), Tags: []string{"hanko", "pipeline", "learning"}}
	return t.memoryFact(companyID, ws, f, fmt.Sprintf("pipeline.learning:%s:%d", g.ID, g.Revision))
}
