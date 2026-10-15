package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Workspaces e projetos de primeira classe (ADR-0025). Criar, alterar e excluir é de admin (a barreira é o
// service); listar e ler é de qualquer membro. Run tokens (personas) não administram nada aqui.

type WorkspacePath struct {
	CompanyPath
	Workspace string `path:"workspace" doc:"Id ou slug do workspace." example:"api"`
}

type ProjectPath struct {
	CompanyPath
	Project string `path:"project" doc:"Id ou slug do projeto." example:"lancamento"`
}

type updateWorkspaceIn struct {
	WorkspacePath
	Body struct {
		Name    *string `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Path    *string `json:"path,omitempty" doc:"Absoluto, limpo e dentro da raiz de workspaces; vazio limpa."`
		RepoURL *string `json:"repo_url,omitempty" doc:"https://, ssh:// ou formato scp; vazio limpa."`
		Branch  *string `json:"default_branch,omitempty" doc:"Branch padrão (metadado); vazio limpa."`
	}
}

type deleteWorkspaceIn struct {
	WorkspacePath
	Unlink bool `query:"unlink" doc:"Desvincula personas, projetos e tasks que usam o workspace e exclui. Sem isto, um workspace em uso responde 409."`
}

type updateProjectIn struct {
	ProjectPath
	Body struct {
		Name           *string `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Description    *string `json:"description,omitempty"`
		Workspace      string  `json:"workspace,omitempty" doc:"Id ou slug do workspace do projeto."`
		ClearWorkspace bool    `json:"clear_workspace,omitempty" doc:"Remove o workspace do projeto."`
		Status         *string `json:"status,omitempty" enum:"active,archived" doc:"archived: o projeto não recebe tasks novas."`
		Budget         *int64  `json:"budget_monthly_cents,omitempty" minimum:"0" doc:"Orçamento mensal do projeto em centavos de dólar."`
		ClearBudget    bool    `json:"clear_budget,omitempty" doc:"Remove o teto do projeto."`
	}
}

func (s *server) registerProjects(api huma.API) {
	huma.Register(api, op("get-workspace", http.MethodGet, "/companies/{company}/workspaces/{workspace}", "workspaces", "Obtém um workspace e quem o usa"),
		func(ctx context.Context, in *WorkspacePath) (*ItemOut[Workspace], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.GetWorkspaceView(ctx, sc.Company.ID, in.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Workspace]{Body: workspaceViewOut(v)}, nil
		})

	huma.Register(api, op("update-workspace", http.MethodPatch, "/companies/{company}/workspaces/{workspace}", "workspaces", "Atualiza um workspace"),
		func(ctx context.Context, in *updateWorkspaceIn) (*ItemOut[Workspace], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetWorkspace(ctx, sc.Company.ID, in.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			w, err := s.svc.UpdateWorkspace(ctx, sc.Actor, sc.Company.ID, cur.ID, service.UpdateWorkspaceInput{
				Name: b.Name, Path: b.Path, RepoURL: b.RepoURL, DefaultBranch: b.Branch})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Workspace]{Body: workspaceOut(w)}, nil
		})

	delWS := op("delete-workspace", http.MethodDelete, "/companies/{company}/workspaces/{workspace}", "workspaces", "Exclui um workspace (recusa se estiver em uso, a menos que unlink=true)")
	delWS.DefaultStatus = http.StatusNoContent
	huma.Register(api, delWS, func(ctx context.Context, in *deleteWorkspaceIn) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cur, err := s.svc.GetWorkspace(ctx, sc.Company.ID, in.Workspace)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		if err := s.svc.DeleteWorkspace(ctx, sc.Actor, sc.Company.ID, cur.ID, in.Unlink); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, op("get-project", http.MethodGet, "/companies/{company}/projects/{project}", "projects", "Obtém um projeto com progresso, custo do mês e personas"),
		func(ctx context.Context, in *ProjectPath) (*ItemOut[Project], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.GetProjectView(ctx, sc.Company.ID, in.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Project]{Body: projectViewOut(v)}, nil
		})

	huma.Register(api, op("update-project", http.MethodPatch, "/companies/{company}/projects/{project}", "projects", "Atualiza um projeto (nome, workspace, status, orçamento)"),
		func(ctx context.Context, in *updateProjectIn) (*ItemOut[Project], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetProject(ctx, sc.Company.ID, in.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			up := service.UpdateProjectInput{Name: b.Name, Description: b.Description, ClearWorkspace: b.ClearWorkspace,
				BudgetMonthlyCents: b.Budget, ClearBudget: b.ClearBudget}
			if up.WorkspaceID, err = s.workspaceRef(ctx, sc.Company.ID, b.Workspace); err != nil {
				return nil, s.fail(ctx, err)
			}
			if b.Status != nil {
				st := domain.ProjectStatus(*b.Status)
				if !st.Valid() {
					return nil, s.fail(ctx, domain.Invalid("status", "valor desconhecido"))
				}
				up.Status = &st
			}
			if _, err := s.svc.UpdateProject(ctx, sc.Actor, sc.Company.ID, cur.ID, up); err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.GetProjectView(ctx, sc.Company.ID, cur.ID.String())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Project]{Body: projectViewOut(v)}, nil
		})
}
