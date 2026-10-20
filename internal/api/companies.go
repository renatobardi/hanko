package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type CompanyPath struct {
	Company string `path:"company" doc:"Id ou slug da empresa." example:"acme"`
}

type createCompanyIn struct {
	Body struct {
		Name       string `json:"name" minLength:"1" maxLength:"120" example:"Acme Ltda" doc:"Nome de exibição."`
		Slug       string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" example:"acme" doc:"Derivado do nome quando omitido."`
		TaskPrefix string `json:"task_prefix,omitempty" pattern:"^[A-Z][A-Z0-9]{0,9}$" example:"HNK" doc:"Padrão HNK."`
		Timezone   string `json:"timezone,omitempty" example:"America/Sao_Paulo" doc:"Padrão America/Sao_Paulo."`
	}
}

type createWorkspaceIn struct {
	CompanyPath
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"120" example:"API"`
		Slug    string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" example:"api"`
		Path    string `json:"path" minLength:"1" example:"/srv/work/api" doc:"Diretório de trabalho das personas."`
		RepoURL string `json:"repo_url,omitempty" example:"https://example.com/acme/api.git" doc:"https://, ssh:// ou formato scp (metadado)."`
		Branch  string `json:"default_branch,omitempty" example:"main" doc:"Branch padrão (metadado)."`
	}
}

type createProjectIn struct {
	CompanyPath
	Body struct {
		Name        string `json:"name" minLength:"1" maxLength:"120" example:"Lançamento"`
		Slug        string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" example:"lancamento"`
		Description string `json:"description,omitempty"`
		Workspace   string `json:"workspace,omitempty" doc:"Id ou slug do workspace padrão do projeto." example:"api"`
		Budget      *int64 `json:"budget_monthly_cents,omitempty" minimum:"0" doc:"Orçamento mensal do projeto em centavos de dólar."`
	}
}

type listCompaniesIn struct {
	Page
	Archived string `query:"archived" enum:"true,false" doc:"true: só as arquivadas; false: oculta as arquivadas; omitido: todas."`
}

func filterCompanies(cs []domain.Company, archived bool) []domain.Company {
	out := make([]domain.Company, 0, len(cs))
	for _, c := range cs {
		if c.Archived() == archived {
			out = append(out, c)
		}
	}
	return out
}

type listCompanyIn struct {
	CompanyPath
	Page
}

func (s *server) registerCompanies(api huma.API) {
	huma.Register(api, created(op("create-company", http.MethodPost, "/companies", "companies", "Cria uma empresa")),
		func(ctx context.Context, in *createCompanyIn) (*ItemOut[Company], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			c, err := s.svc.CreateCompany(ctx, principalFrom(ctx).Actor, service.CreateCompanyInput{
				Name: in.Body.Name, Slug: in.Body.Slug, TaskPrefix: in.Body.TaskPrefix, Timezone: in.Body.Timezone})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Company]{Body: companyOut(c)}, nil
		})

	huma.Register(api, op("list-companies", http.MethodGet, "/companies", "companies", "Lista empresas"),
		func(ctx context.Context, in *listCompaniesIn) (*ListOut[Company], error) {
			if cl := principalFrom(ctx).Claims; cl != nil { // run token enxerga só a própria empresa
				c, err := s.svc.GetCompany(ctx, cl.CompanyID)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				return listOf([]domain.Company{c}, in.Page, companyOut), nil
			}
			cs, err := s.svc.ListCompanies(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			switch in.Archived { // "" = todas
			case "true":
				cs = filterCompanies(cs, true)
			case "false":
				cs = filterCompanies(cs, false)
			}
			return listOf(cs, in.Page, companyOut), nil
		})

	huma.Register(api, op("get-company", http.MethodGet, "/companies/{company}", "companies", "Obtém uma empresa"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[Company], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			return &ItemOut[Company]{Body: companyOut(sc.Company)}, nil
		})

	huma.Register(api, created(op("create-workspace", http.MethodPost, "/companies/{company}/workspaces", "workspaces", "Cria um workspace")),
		func(ctx context.Context, in *createWorkspaceIn) (*ItemOut[Workspace], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			w, err := s.svc.CreateWorkspace(ctx, sc.Actor, sc.Company.ID, service.CreateWorkspaceInput{
				Name: in.Body.Name, Slug: in.Body.Slug, Path: in.Body.Path, RepoURL: in.Body.RepoURL, DefaultBranch: in.Body.Branch})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Workspace]{Body: workspaceOut(w)}, nil
		})

	huma.Register(api, op("list-workspaces", http.MethodGet, "/companies/{company}/workspaces", "workspaces", "Lista workspaces"),
		func(ctx context.Context, in *listCompanyIn) (*ListOut[Workspace], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ws, err := s.svc.ListWorkspaceViews(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ws, in.Page, func(v service.WorkspaceView) Workspace {
				w := workspaceOut(v.Workspace)
				w.Usage = &WorkspaceUsage{Personas: v.PersonaCount, Projects: v.ProjectCount, OpenTasks: v.OpenTasks}
				return w
			}), nil
		})

	huma.Register(api, created(op("create-project", http.MethodPost, "/companies/{company}/projects", "projects", "Cria um projeto")),
		func(ctx context.Context, in *createProjectIn) (*ItemOut[Project], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			wsID, err := s.workspaceRef(ctx, sc.Company.ID, in.Body.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			p, err := s.svc.CreateProject(ctx, sc.Actor, sc.Company.ID, service.CreateProjectInput{
				Name: in.Body.Name, Slug: in.Body.Slug, Description: in.Body.Description, WorkspaceID: wsID, BudgetMonthlyCents: in.Body.Budget})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Project]{Body: projectOut(p)}, nil
		})

	huma.Register(api, op("list-projects", http.MethodGet, "/companies/{company}/projects", "projects", "Lista projetos"),
		func(ctx context.Context, in *listCompanyIn) (*ListOut[Project], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ps, err := s.svc.ListProjectViews(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ps, in.Page, projectViewOut), nil
		})
}
