package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Coordenação de tasks (ADR-0040): dependências, pausa herdada e política de revisão por camada.

type addDependencyIn struct {
	TaskPath
	Body struct {
		DependsOn string `json:"depends_on" minLength:"1" doc:"Id ou chave da task de que esta passa a depender."`
	}
}

type removeDependencyIn struct {
	TaskPath
	Dependency string `path:"dependency" doc:"Id ou chave da task de que a dependência é removida."`
}

type holdTaskIn struct {
	TaskPath
	Body struct {
		Reason string `json:"reason" minLength:"1" maxLength:"500" doc:"Por que a task e a subárvore dela param. Aparece no thread e na Caixa."`
	}
}

type reviewOverrideBody struct {
	RequireReview   *bool  `json:"require_review,omitempty" doc:"Ausente = herda; true/false força a revisão do fechamento."`
	ReviewerMode    string `json:"reviewer_mode,omitempty" enum:"inherit,none,custom" doc:"Padrão inherit."`
	ReviewerPersona string `json:"reviewer_persona,omitempty" doc:"Slug ou id da persona revisora (modo custom)."`
	ReviewerAgent   string `json:"reviewer_agent,omitempty" enum:"claude,codex,fake" doc:"Harness revisor (modo custom); exclusivo com reviewer_persona."`
	ReviewerModel   string `json:"reviewer_model,omitempty" doc:"Modelo do revisor."`
}

type setTaskReviewPolicyIn struct {
	TaskPath
	Body reviewOverrideBody
}

type setProjectReviewPolicyIn struct {
	ProjectPath
	Body reviewOverrideBody
}

// reviewOverrideIn converte o corpo da API para o domínio resolvendo a persona revisora.
func (s *server) reviewOverrideIn(ctx context.Context, cid domain.ID, b reviewOverrideBody) (domain.ReviewOverride, error) {
	o := domain.ReviewOverride{RequireReview: b.RequireReview, ReviewerMode: domain.ReviewerMode(b.ReviewerMode),
		ReviewerAgent: b.ReviewerAgent, ReviewerModel: b.ReviewerModel}
	p, err := s.personaRef(ctx, cid, b.ReviewerPersona)
	if err != nil {
		return o, err
	}
	o.ReviewerPersonaID = p
	return o, nil
}

func (s *server) registerCoordination(api huma.API) {
	huma.Register(api, op("get-task-relations", http.MethodGet, "/companies/{company}/tasks/{task}/relations", "tasks",
		"Árvore e dependências de uma task"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[TaskRelations], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			rel, err := s.svc.TaskRelations(ctx, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskRelations]{Body: taskRelationsOut(rel)}, nil
		})

	huma.Register(api, created(op("add-task-dependency", http.MethodPost, "/companies/{company}/tasks/{task}/dependencies", "tasks",
		"Faz a task depender de outra (bloqueante, sem ciclo)")),
		func(ctx context.Context, in *addDependencyIn) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			dep, err := s.svc.GetTask(ctx, sc.Company.ID, in.Body.DependsOn)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("depends_on", "task not found in company"))
			}
			t, err := s.svc.AddTaskDependency(ctx, sc.Actor, sc.Company.ID, cur.ID, dep.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, op("remove-task-dependency", http.MethodDelete, "/companies/{company}/tasks/{task}/dependencies/{dependency}", "tasks",
		"Remove uma dependência; a última dependência aberta removida libera a task e acorda o responsável"),
		func(ctx context.Context, in *removeDependencyIn) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			dep, err := s.svc.GetTask(ctx, sc.Company.ID, in.Dependency)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			t, err := s.svc.RemoveTaskDependency(ctx, sc.Actor, sc.Company.ID, cur.ID, dep.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, op("hold-task", http.MethodPost, "/companies/{company}/tasks/{task}/hold", "tasks",
		"Pausa a task e, por herança, toda a subárvore"),
		func(ctx context.Context, in *holdTaskIn) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			t, err := s.svc.HoldTask(ctx, sc.Actor, sc.Company.ID, cur.ID, in.Body.Reason)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, op("resume-task", http.MethodPost, "/companies/{company}/tasks/{task}/resume", "tasks",
		"Retoma a task pausada e acorda os wakes que foram recusados durante a pausa"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			t, err := s.svc.ResumeTask(ctx, sc.Actor, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, op("list-held-tasks", http.MethodGet, "/companies/{company}/held-tasks", "tasks",
		"Lista as raízes de pausa da empresa"),
		func(ctx context.Context, in *CompanyPath) (*ListOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			hs, err := s.svc.ListHeldTasks(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := make([]Task, len(hs))
			for i, h := range hs {
				out[i] = taskOut(h.Task)
				out[i].Hold = holdOut(&h.Hold)
			}
			return listAll(out, Page{}, func(t Task) Task { return t }), nil
		})

	huma.Register(api, op("get-task-review-policy", http.MethodGet, "/companies/{company}/tasks/{task}/review-policy", "tasks",
		"Política de revisão da task por camada (task, projeto, persona) e o efeito da precedência"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[TaskReviewPolicy], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.TaskReviewPolicy(ctx, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskReviewPolicy]{Body: taskReviewPolicyOut(v)}, nil
		})

	huma.Register(api, op("set-task-review-policy", http.MethodPut, "/companies/{company}/tasks/{task}/review-policy", "tasks",
		"Define a sobreposição de revisão da task (admin); corpo vazio ou inherit remove"),
		func(ctx context.Context, in *setTaskReviewPolicyIn) (*ItemOut[TaskReviewPolicy], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			o, err := s.reviewOverrideIn(ctx, sc.Company.ID, in.Body)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.SetTaskReviewPolicy(ctx, sc.Actor, sc.Company.ID, cur.ID, o)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[TaskReviewPolicy]{Body: taskReviewPolicyOut(v)}, nil
		})

	huma.Register(api, op("get-project-review-policy", http.MethodGet, "/companies/{company}/projects/{project}/review-policy", "projects",
		"Política de revisão do projeto"),
		func(ctx context.Context, in *ProjectPath) (*ItemOut[ReviewOverride], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			o, err := s.svc.ProjectReviewPolicy(ctx, sc.Company.ID, *id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ReviewOverride]{Body: reviewOverrideOut(o)}, nil
		})

	huma.Register(api, op("set-project-review-policy", http.MethodPut, "/companies/{company}/projects/{project}/review-policy", "projects",
		"Define a política de revisão do projeto (admin); vale para as tasks sem sobreposição própria"),
		func(ctx context.Context, in *setProjectReviewPolicyIn) (*ItemOut[ReviewOverride], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			id, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil || id == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			o, err := s.reviewOverrideIn(ctx, sc.Company.ID, in.Body)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			got, err := s.svc.SetProjectReviewPolicy(ctx, sc.Actor, sc.Company.ID, *id, o)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ReviewOverride]{Body: reviewOverrideOut(got)}, nil
		})
}

// orNotFound devolve err, ou "não encontrado" quando a referência veio vazia.
func orNotFound(err error) error {
	if err != nil {
		return err
	}
	return domain.ErrNotFound
}

var _ = service.ReviewPolicyView{}
