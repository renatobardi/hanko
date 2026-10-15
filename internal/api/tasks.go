package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type TaskPath struct {
	CompanyPath
	Task string `path:"task" doc:"Id ou chave da task." example:"HNK-12"`
}

type listTasksIn struct {
	CompanyPath
	Page
	Status  string `query:"status" doc:"Status separados por vírgula: backlog,todo,in_progress,in_review,blocked,done,cancelled." example:"todo,in_progress"`
	Persona string `query:"persona" doc:"Id ou slug da persona atribuída."`
	Project string `query:"project" doc:"Id ou slug do projeto."`
	Q       string `query:"q" doc:"Busca textual em título e descrição."`
}

type createTaskIn struct {
	CompanyPath
	Body struct {
		Title       string `json:"title" minLength:"1" maxLength:"300" example:"Implementar login"`
		Description string `json:"description,omitempty"`
		Status      string `json:"status,omitempty" enum:"backlog,todo,in_progress,in_review,blocked,done,cancelled" doc:"Padrão todo."`
		Priority    string `json:"priority,omitempty" enum:"critical,high,medium,low" doc:"Padrão medium."`
		Persona     string `json:"persona,omitempty" doc:"Id ou slug da persona; atribuir uma task workable acorda a persona."`
		Project     string `json:"project,omitempty" doc:"Id ou slug do projeto."`
		Workspace   string `json:"workspace,omitempty" doc:"Id ou slug do workspace."`
		Model       string `json:"model,omitempty" example:"claude-opus-4-5" doc:"Modelo escolhido à mão; vence o da persona."`
		Parent      string `json:"parent,omitempty" doc:"Id ou chave da task pai."`
		EventID     string `json:"event_id,omitempty" maxLength:"200" doc:"Chave de idempotência: repetir devolve a task existente."`
	}
}

type updateTaskIn struct {
	TaskPath
	Body struct {
		Title        *string `json:"title,omitempty" minLength:"1" maxLength:"300"`
		Description  *string `json:"description,omitempty"`
		Status       *string `json:"status,omitempty" enum:"backlog,todo,in_progress,in_review,blocked,done,cancelled"`
		Priority     *string `json:"priority,omitempty" enum:"critical,high,medium,low"`
		Persona      string  `json:"persona,omitempty" doc:"Id ou slug da persona (re)atribuída."`
		Unassign     bool    `json:"unassign,omitempty" doc:"Remove a atribuição."`
		Project      string  `json:"project,omitempty" doc:"Id ou slug do projeto."`
		ClearProject bool    `json:"clear_project,omitempty" doc:"Tira a task do projeto."`
		Model        *string `json:"model,omitempty" doc:"Modelo escolhido à mão; vazio remove a escolha (volta ao da persona)."`
		EventID      string  `json:"event_id,omitempty" maxLength:"200" doc:"Chave de idempotência."`
	}
}

type addCommentIn struct {
	TaskPath
	Body struct {
		Body    string  `json:"body" minLength:"1" doc:"Texto do comentário (Markdown)."`
		RunID   string  `json:"run_id,omitempty" format:"uuid" doc:"Execução de origem; para run tokens, padrão é a execução do token."`
		EventID string  `json:"event_id,omitempty" maxLength:"200" doc:"Chave de idempotência."`
		Anchor  *Anchor `json:"anchor,omitempty" doc:"Ancora o comentário a um trecho da descrição da task ou do resumo de uma execução; o trecho precisa existir no documento (422 se não)."`
	}
}

type listCommentsIn struct {
	TaskPath
	Page
}

// anchorIn converte a âncora do corpo da requisição para o domínio.
func anchorIn(a *Anchor) (*domain.Anchor, error) {
	if a == nil {
		return nil, nil
	}
	out := &domain.Anchor{Target: domain.AnchorTarget(a.Target), Quote: a.Quote, Start: a.Start, End: a.End, DocHash: a.DocHash}
	if !out.Target.Valid() {
		return nil, domain.Invalid("anchor.target", "deve ser task_description ou run_summary")
	}
	if a.RunID != "" {
		id, err := domain.ParseID(a.RunID)
		if err != nil {
			return nil, domain.Invalid("anchor.run_id", "não é um uuid")
		}
		out.RunID = &id
	}
	return out, nil
}

func (s *server) registerTasks(api huma.API) {
	huma.Register(api, created(op("create-task", http.MethodPost, "/companies/{company}/tasks", "tasks", "Cria uma task")),
		func(ctx context.Context, in *createTaskIn) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			b := in.Body
			if b.Status != "" && !domain.TaskStatus(b.Status).Valid() {
				return nil, s.fail(ctx, domain.Invalid("status", "valor desconhecido"))
			}
			if b.Priority != "" && !domain.Priority(b.Priority).Valid() {
				return nil, s.fail(ctx, domain.Invalid("priority", "valor desconhecido"))
			}
			persona, err := s.personaRef(ctx, cid, b.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			project, err := s.projectRef(ctx, cid, b.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ws, err := s.workspaceRef(ctx, cid, b.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			parent, err := s.taskRef(ctx, cid, b.Parent)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if b.Model != "" {
				if err := forbidPersona(ctx); err != nil {
					return nil, err
				}
			}
			t, err := s.svc.CreateTask(ctx, sc.Actor, cid, service.CreateTaskInput{
				Title: b.Title, Description: b.Description, Status: domain.TaskStatus(b.Status),
				Priority: domain.Priority(b.Priority), PersonaID: persona, ProjectID: project, WorkspaceID: ws,
				ParentID: parent, Model: b.Model, EventID: b.EventID})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, op("list-tasks", http.MethodGet, "/companies/{company}/tasks", "tasks", "Lista tasks"),
		func(ctx context.Context, in *listTasksIn) (*ListOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			statuses, err := parseTaskStatuses(in.Status)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			persona, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			project, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ts, err := s.svc.ListTasks(ctx, sc.Company.ID, service.TaskFilter{
				Statuses: statuses, PersonaID: persona, ProjectID: project, Query: in.Q, Limit: in.Limit, Offset: in.Offset})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ts, in.Page, taskOut), nil
		})

	huma.Register(api, op("get-task", http.MethodGet, "/companies/{company}/tasks/{task}", "tasks", "Obtém uma task"),
		func(ctx context.Context, in *TaskPath) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			t, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, op("update-task", http.MethodPatch, "/companies/{company}/tasks/{task}", "tasks", "Atualiza uma task (status, prioridade, atribuição)"),
		func(ctx context.Context, in *updateTaskIn) (*ItemOut[Task], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			if b.Unassign && b.Persona != "" {
				return nil, s.fail(ctx, domain.Invalid("unassign", "não combina com persona"))
			}
			if b.Model != nil {
				if err := forbidPersona(ctx); err != nil {
					return nil, err
				}
			}
			up := service.UpdateTaskInput{Title: b.Title, Description: b.Description, Unassign: b.Unassign, Model: b.Model, EventID: b.EventID,
				ClearProject: b.ClearProject}
			if b.Status != nil {
				st := domain.TaskStatus(*b.Status)
				if !st.Valid() {
					return nil, s.fail(ctx, domain.Invalid("status", "valor desconhecido"))
				}
				up.Status = &st
			}
			if b.Priority != nil {
				pr := domain.Priority(*b.Priority)
				if !pr.Valid() {
					return nil, s.fail(ctx, domain.Invalid("priority", "valor desconhecido"))
				}
				up.Priority = &pr
			}
			if up.PersonaID, err = s.personaRef(ctx, sc.Company.ID, b.Persona); err != nil {
				return nil, s.fail(ctx, err)
			}
			if up.ProjectID, err = s.projectRef(ctx, sc.Company.ID, b.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			t, err := s.svc.UpdateTask(ctx, sc.Actor, sc.Company.ID, cur.ID, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Task]{Body: taskOut(t)}, nil
		})

	huma.Register(api, created(op("add-comment", http.MethodPost, "/companies/{company}/tasks/{task}/comments", "tasks", "Comenta numa task")),
		func(ctx context.Context, in *addCommentIn) (*ItemOut[Comment], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			var runID *domain.ID
			switch {
			case in.Body.RunID != "":
				id, err := domain.ParseID(in.Body.RunID)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("run_id", "não é um uuid"))
				}
				runID = &id
			case sc.Claims != nil:
				if id, err := domain.ParseID(sc.Claims.RunID); err == nil {
					runID = &id
				}
			}
			anchor, err := anchorIn(in.Body.Anchor)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			c, err := s.svc.AddComment(ctx, sc.Actor, sc.Company.ID, cur.ID, service.AddCommentInput{
				Body: in.Body.Body, RunID: runID, EventID: in.Body.EventID, Anchor: anchor})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Comment]{Body: commentOut(c)}, nil
		})

	huma.Register(api, op("list-comments", http.MethodGet, "/companies/{company}/tasks/{task}/comments", "tasks", "Lista a thread de uma task"),
		func(ctx context.Context, in *listCommentsIn) (*ListOut[Comment], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			cs, err := s.svc.ListComments(ctx, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(cs, in.Page, commentOut), nil
		})

	wake := op("wake-task", http.MethodPost, "/companies/{company}/tasks/{task}/wake", "tasks", "Acorda a persona atribuída")
	wake.DefaultStatus = http.StatusAccepted
	huma.Register(api, wake, func(ctx context.Context, in *TaskPath) (*ItemOut[WakeResult], error) {
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cur, err := s.svc.GetTask(ctx, sc.Company.ID, in.Task)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		if err := s.svc.WakeTask(ctx, sc.Actor, sc.Company.ID, cur.ID); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &ItemOut[WakeResult]{Body: WakeResult{Queued: true}}, nil
	})
}
