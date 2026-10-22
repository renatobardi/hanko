package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pipelines sobre tasks (ADR-0048): as etapas de um projeto, a posição de cada task e os aprendizados das etapas.
// Configurar é de admin; ler é de qualquer membro; entrar, concluir, avançar e sair são de member (uma persona só
// conclui a etapa da própria task).

// stagesBackend é o subconjunto do service para pipelines de tasks; fica fora de backend para que os fakes dos testes
// não precisem implementá-lo.
type stagesBackend interface {
	ProjectStages(ctx context.Context, companyID domain.ID, projectRef string) (service.ProjectStagesView, error)
	SetProjectStages(ctx context.Context, actor domain.Actor, companyID domain.ID, projectRef string, specs []domain.StageSpec) (service.ProjectStagesView, error)
	ClearProjectStages(ctx context.Context, actor domain.Actor, companyID domain.ID, projectRef string) error
	StageLearnings(ctx context.Context, companyID domain.ID, projectRef, stageKey string, limit int) ([]domain.StageLearning, error)
	TaskStage(ctx context.Context, companyID domain.ID, taskRef string) (service.TaskStageView, error)
	StartTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, stageKey string) (service.TaskStageView, error)
	CompleteTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) (service.TaskStageView, error)
	AdvanceTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in service.AdvanceStageInput) (service.TaskStageView, error)
	LeaveTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) error
	DashboardStatusCards(ctx context.Context, companyID domain.ID) ([]service.DashboardCard, error)
}

func (s *server) stagesAPI() (stagesBackend, error) {
	b, ok := s.svc.(stagesBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "pipelines de tasks indisponíveis neste servidor")
	}
	return b, nil
}

// ProjectStageSpec é uma etapa como o admin a descreve.
type ProjectStageSpec struct {
	Key          string `json:"key" pattern:"^[a-z][a-z0-9_-]{0,39}$" example:"dev" doc:"Chave estável da etapa (minúsculas, dígitos, _ e -)."`
	Name         string `json:"name" minLength:"1" maxLength:"80" example:"Desenvolvimento"`
	Status       string `json:"status" enum:"todo,in_progress,in_review" doc:"Status que a task recebe ao entrar na etapa. Com persona, só todo ou in_progress."`
	Persona      string `json:"persona,omitempty" doc:"Slug ou id da persona da etapa; vazio = etapa humana."`
	ExitGate     string `json:"exit_gate,omitempty" enum:"none,approval,review" doc:"O que para o avanço ao fim da etapa: nada (padrão), uma aprovação humana ou a revisão da task (com a revisão automática da política vigente)."`
	Instructions string `json:"instructions,omitempty" maxLength:"8192" doc:"O que se espera da etapa; entra no briefing da persona."`
}

// ProjectStage é uma etapa do pipeline de um projeto.
type ProjectStage struct {
	ID           string `json:"id" format:"uuid"`
	Key          string `json:"key"`
	Name         string `json:"name"`
	Position     int    `json:"position"`
	Status       string `json:"status" enum:"todo,in_progress,in_review"`
	PersonaID    string `json:"persona_id,omitempty" format:"uuid"`
	PersonaSlug  string `json:"persona_slug,omitempty"`
	PersonaName  string `json:"persona_name,omitempty"`
	ExitGate     string `json:"exit_gate" enum:"none,approval,review"`
	Instructions string `json:"instructions,omitempty"`
	Tasks        int    `json:"tasks" doc:"Tasks que estão nesta etapa agora."`
}

// ProjectStages é o pipeline de um projeto.
type ProjectStages struct {
	ProjectID   string         `json:"project_id" format:"uuid"`
	ProjectSlug string         `json:"project_slug"`
	Stages      []ProjectStage `json:"stages"`
}

// StageLearning é uma decisão de gate numa etapa.
type StageLearning struct {
	Stage     string    `json:"stage"`
	TaskKey   string    `json:"task_key,omitempty"`
	Decision  string    `json:"decision" enum:"approved,changes_requested,rejected"`
	Note      string    `json:"note,omitempty"`
	GateID    string    `json:"gate_id,omitempty" format:"uuid"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskStageEvent é uma linha do histórico do pipeline da task.
type TaskStageEvent struct {
	Kind      string    `json:"kind" enum:"enrolled,completed,gate_requested,gate_decided,advanced,returned,finished,left"`
	Stage     string    `json:"stage,omitempty"`
	ToStage   string    `json:"to_stage,omitempty"`
	Actor     ActorRef  `json:"actor"`
	GateID    string    `json:"gate_id,omitempty" format:"uuid"`
	Decision  string    `json:"decision,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskStage é a posição de uma task no pipeline do projeto.
type TaskStage struct {
	TaskID    string           `json:"task_id" format:"uuid"`
	ProjectID string           `json:"project_id" format:"uuid"`
	Stage     ProjectStage     `json:"stage"`
	State     string           `json:"state" enum:"working,completing,finished" doc:"working = a persona da etapa trabalha; completing = a etapa terminou e um gate decide o avanço; finished = atravessou a última etapa ou saiu do pipeline."`
	Position  int              `json:"position"`
	Total     int              `json:"total"`
	NextStage string           `json:"next_stage,omitempty"`
	EnteredAt time.Time        `json:"entered_at"`
	Events    []TaskStageEvent `json:"events"`
	Learnings []StageLearning  `json:"learnings" doc:"Aprendizados da etapa atual: decisões de gate com nota."`
}

// DashboardStatusCard é um card de status no Painel.
type DashboardStatusCard struct {
	ID           string     `json:"id" format:"uuid"`
	Title        string     `json:"title"`
	Summary      string     `json:"summary" doc:"O resumo atual em markdown."`
	Revision     int        `json:"revision"`
	Engine       string     `json:"engine" enum:"persona,decision"`
	State        string     `json:"state"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	NextAt       *time.Time `json:"next_at,omitempty"`
	ArtifactID   string     `json:"artifact_id,omitempty" format:"uuid" doc:"Versão mais nova do artefato versionado do card."`
	ArtifactName string     `json:"artifact_name,omitempty"`
}

type setProjectStagesIn struct {
	ProjectPath
	Body struct {
		Stages []ProjectStageSpec `json:"stages" minItems:"1" maxItems:"12" doc:"A lista inteira, em ordem: substitui o pipeline. As chaves casam as etapas existentes; uma etapa que sai da lista não pode ter tasks."`
	}
}

type stageLearningsIn struct {
	ProjectPath
	Stage string `query:"stage" doc:"Chave da etapa; vazio = todas."`
	Limit int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type startStageIn struct {
	TaskPath
	Body struct {
		Stage string `json:"stage,omitempty" doc:"Chave da etapa de entrada; padrão a primeira."`
	}
}

type completeStageIn struct {
	TaskPath
	Body struct {
		Note string `json:"note,omitempty" maxLength:"16000" doc:"O que foi entregue na etapa."`
	}
}

type advanceStageIn struct {
	TaskPath
	Body struct {
		To   string `json:"to,omitempty" doc:"Etapa de destino (para a frente ou para trás); vazio = a seguinte, ou o fim do pipeline na última."`
		Note string `json:"note,omitempty" maxLength:"16000"`
	}
}

type leaveStageIn struct {
	TaskPath
	Body struct {
		Note string `json:"note,omitempty" maxLength:"16000"`
	}
}

func projectStageOut(s domain.ProjectStage, tasks int) ProjectStage {
	return ProjectStage{ID: idStr(s.ID), Key: s.Key, Name: s.Name, Position: s.Position, Status: string(s.Status), PersonaID: idPtr(s.PersonaID),
		PersonaSlug: s.PersonaSlug, PersonaName: s.PersonaName, ExitGate: string(s.ExitGate), Instructions: s.Instructions, Tasks: tasks}
}

func projectStagesOut(v service.ProjectStagesView) ProjectStages {
	out := ProjectStages{ProjectID: idStr(v.ProjectID), ProjectSlug: v.ProjectSlug, Stages: []ProjectStage{}}
	for _, s := range v.Stages {
		out.Stages = append(out.Stages, projectStageOut(s.ProjectStage, s.Tasks))
	}
	return out
}

func stageLearningOut(l domain.StageLearning) StageLearning {
	return StageLearning{Stage: l.StageKey, TaskKey: l.TaskKey, Decision: l.Decision, Note: l.Note, GateID: idPtr(l.GateID), CreatedAt: l.CreatedAt}
}

func taskStageOut(v service.TaskStageView) TaskStage {
	out := TaskStage{TaskID: idStr(v.TaskID), ProjectID: idStr(v.ProjectID), Stage: projectStageOut(v.Stage, 0), State: string(v.State),
		Position: v.Position, Total: v.Total, NextStage: v.NextKey, EnteredAt: v.EnteredAt, Events: []TaskStageEvent{}, Learnings: []StageLearning{}}
	for _, e := range v.Events {
		out.Events = append(out.Events, TaskStageEvent{Kind: string(e.Kind), Stage: e.StageKey, ToStage: e.ToStageKey, Actor: actorRef(e.Actor),
			GateID: idPtr(e.GateID), Decision: e.Decision, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	for _, l := range v.Learnings {
		out.Learnings = append(out.Learnings, stageLearningOut(l))
	}
	return out
}

func (s *server) registerStages(api huma.API) {
	const tag = "stages"
	proj := "/companies/{company}/projects/{project}"
	task := "/companies/{company}/tasks/{task}"

	huma.Register(api, op("get-project-stages", http.MethodGet, proj+"/stages", tag, "Pipeline do projeto: as etapas em ordem"),
		func(ctx context.Context, in *ProjectPath) (*ItemOut[ProjectStages], error) {
			b, err := s.stagesAPI()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := b.ProjectStages(ctx, sc.Company.ID, in.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ProjectStages]{Body: projectStagesOut(v)}, nil
		})

	huma.Register(api, op("set-project-stages", http.MethodPut, proj+"/stages", tag, "Define o pipeline do projeto, substituindo a lista de etapas (admin)"),
		func(ctx context.Context, in *setProjectStagesIn) (*ItemOut[ProjectStages], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.stagesAPI()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			specs := make([]domain.StageSpec, len(in.Body.Stages))
			for i, st := range in.Body.Stages {
				specs[i] = domain.StageSpec{Key: st.Key, Name: st.Name, Status: domain.TaskStatus(st.Status), Persona: st.Persona,
					ExitGate: domain.StageExitGate(st.ExitGate), Instructions: st.Instructions}
			}
			v, err := b.SetProjectStages(ctx, sc.Actor, sc.Company.ID, in.Project, specs)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ProjectStages]{Body: projectStagesOut(v)}, nil
		})

	huma.Register(api, noContent(op("clear-project-stages", http.MethodDelete, proj+"/stages", tag, "Desliga o pipeline do projeto (admin); recusa enquanto houver task numa etapa")),
		func(ctx context.Context, in *ProjectPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, err := s.stagesAPI()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := b.ClearProjectStages(ctx, sc.Actor, sc.Company.ID, in.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("list-stage-learnings", http.MethodGet, proj+"/stage-learnings", tag,
		"Aprendizados das etapas: as decisões de gate com nota (ou sem aprovação), da mais nova para a mais antiga"),
		func(ctx context.Context, in *stageLearningsIn) (*ListOut[StageLearning], error) {
			b, err := s.stagesAPI()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ls, err := b.StageLearnings(ctx, sc.Company.ID, in.Project, in.Stage, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ls, Page{}, stageLearningOut), nil
		})

	huma.Register(api, op("get-task-stage", http.MethodGet, task+"/stage", tag, "Posição da task no pipeline do projeto, com o histórico e os aprendizados da etapa"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[TaskStage], error) {
			b, err := s.stagesAPI()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := b.TaskStage(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskStage]{Body: taskStageOut(v)}, nil
		})

	huma.Register(api, op("start-task-stage", http.MethodPost, task+"/stage/start", tag, "Põe a task no pipeline do projeto dela (member)"),
		func(ctx context.Context, in *startStageIn) (*ItemOut[TaskStage], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.stageCall(ctx, in.TaskPath)
			if err != nil {
				return nil, err
			}
			v, err := b.StartTaskStage(ctx, sc.Actor, sc.Company.ID, id, in.Body.Stage)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskStage]{Body: taskStageOut(v)}, nil
		})

	huma.Register(api, op("complete-task-stage", http.MethodPost, task+"/stage/complete", tag,
		"Conclui a etapa: a task avança para a seguinte, ou espera o gate da etapa (member; a persona dona da task)"),
		func(ctx context.Context, in *completeStageIn) (*ItemOut[TaskStage], error) {
			b, sc, id, err := s.stageCall(ctx, in.TaskPath)
			if err != nil {
				return nil, err
			}
			v, err := b.CompleteTaskStage(ctx, sc.Actor, sc.Company.ID, id, in.Body.Note)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskStage]{Body: taskStageOut(v)}, nil
		})

	huma.Register(api, op("advance-task-stage", http.MethodPost, task+"/stage/advance", tag,
		"Move a task à mão para a etapa seguinte ou para a etapa indicada, por cima dos gates (member humano)"),
		func(ctx context.Context, in *advanceStageIn) (*ItemOut[TaskStage], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.stageCall(ctx, in.TaskPath)
			if err != nil {
				return nil, err
			}
			v, err := b.AdvanceTaskStage(ctx, sc.Actor, sc.Company.ID, id, service.AdvanceStageInput{To: in.Body.To, Note: in.Body.Note})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskStage]{Body: taskStageOut(v)}, nil
		})

	huma.Register(api, noContent(op("leave-task-stage", http.MethodPost, task+"/stage/leave", tag, "Tira a task do pipeline (member)")),
		func(ctx context.Context, in *leaveStageIn) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			b, sc, id, err := s.stageCall(ctx, in.TaskPath)
			if err != nil {
				return nil, err
			}
			if err := b.LeaveTaskStage(ctx, sc.Actor, sc.Company.ID, id, in.Body.Note); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("get-dashboard-status-cards", http.MethodGet, "/companies/{company}/dashboard/status-cards", "status-cards",
		"Cards de status marcados para o Painel, com o resumo e o artefato versionado"),
		func(ctx context.Context, in *CompanyPath) (*ListOut[DashboardStatusCard], error) {
			b, err := s.stagesAPI()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cs, err := b.DashboardStatusCards(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(cs, Page{}, func(c service.DashboardCard) DashboardStatusCard {
				return DashboardStatusCard{ID: idStr(c.ID), Title: c.Title, Summary: c.Summary, Revision: c.Revision, Engine: c.Engine, State: c.State,
					UpdatedAt: c.UpdatedAt, NextAt: c.NextAt, ArtifactID: idPtr(c.ArtifactID), ArtifactName: c.ArtifactName}
			}), nil
		})
}

// stageCall resolve a empresa e a task do caminho (id ou chave).
func (s *server) stageCall(ctx context.Context, in TaskPath) (stagesBackend, scope, domain.ID, error) {
	b, err := s.stagesAPI()
	if err != nil {
		return nil, scope{}, domain.ID{}, err
	}
	sc, err := s.scope(ctx, in.Company)
	if err != nil {
		return nil, scope{}, domain.ID{}, err
	}
	t, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
	if err != nil {
		return nil, scope{}, domain.ID{}, s.fail(ctx, err)
	}
	return b, sc, t.ID, nil
}
