package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type FlowPath struct {
	CompanyPath
	Flow string `path:"flow" doc:"Id ou slug do fluxo." example:"resumo-diario"`
}

type createFlowIn struct {
	CompanyPath
	Body struct {
		Name            string `json:"name" minLength:"1" maxLength:"120" example:"Resumo diário"`
		Slug            string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" example:"resumo-diario" doc:"Derivado do nome quando omitido."`
		Description     string `json:"description,omitempty"`
		Cron            string `json:"cron" example:"0 9 * * 1-5" doc:"Cron de 5 campos: minuto hora dia-do-mês mês dia-da-semana."`
		Timezone        string `json:"timezone,omitempty" example:"America/Sao_Paulo" doc:"Fuso IANA do cron. Padrão: o da empresa."`
		Enabled         *bool  `json:"enabled,omitempty" doc:"Padrão true."`
		Persona         string `json:"persona" minLength:"1" doc:"Id ou slug da persona que recebe a task." example:"cto"`
		Project         string `json:"project,omitempty" doc:"Id ou slug do projeto das tasks criadas."`
		Priority        string `json:"priority,omitempty" enum:"critical,high,medium,low" doc:"Padrão medium."`
		TaskTitle       string `json:"task_title,omitempty" example:"Resumo de {{data}}" doc:"Modelo do título; {{data}} = data da execução (AAAA-MM-DD), {{n}} = nº do disparo. Padrão: o nome do fluxo."`
		TaskDescription string `json:"task_description,omitempty" doc:"Modelo da descrição (mesmos marcadores)."`
	}
}

type updateFlowIn struct {
	FlowPath
	Body struct {
		Name            *string `json:"name,omitempty" minLength:"1"`
		Description     *string `json:"description,omitempty"`
		Cron            *string `json:"cron,omitempty"`
		Timezone        *string `json:"timezone,omitempty"`
		Enabled         *bool   `json:"enabled,omitempty"`
		Persona         string  `json:"persona,omitempty" doc:"Id ou slug da nova persona."`
		Project         string  `json:"project,omitempty" doc:"Id ou slug do novo projeto."`
		ClearProject    bool    `json:"clear_project,omitempty"`
		Priority        *string `json:"priority,omitempty" enum:"critical,high,medium,low"`
		TaskTitle       *string `json:"task_title,omitempty"`
		TaskDescription *string `json:"task_description,omitempty"`
	}
}

type listFlowFiresIn struct {
	FlowPath
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"30"`
}

func (s *server) registerFlows(api huma.API) {
	huma.Register(api, created(op("create-flow", http.MethodPost, "/companies/{company}/flows", "flows", "Cria um fluxo")),
		func(ctx context.Context, in *createFlowIn) (*ItemOut[Flow], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			p, err := s.personaRef(ctx, cid, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if p == nil {
				return nil, s.fail(ctx, domain.Invalid("persona", "obrigatório"))
			}
			pr, err := s.projectRef(ctx, cid, in.Body.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			f, err := s.svc.CreateFlow(ctx, sc.Actor, cid, service.CreateFlowInput{
				Name: in.Body.Name, Slug: in.Body.Slug, Description: in.Body.Description, Cron: in.Body.Cron,
				Timezone: in.Body.Timezone, Enabled: in.Body.Enabled, PersonaID: *p, ProjectID: pr,
				Priority: domain.Priority(in.Body.Priority), TaskTitle: in.Body.TaskTitle, TaskDescription: in.Body.TaskDescription})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Flow]{Body: flowOut(f)}, nil
		})

	huma.Register(api, op("list-flows", http.MethodGet, "/companies/{company}/flows", "flows", "Lista fluxos"),
		func(ctx context.Context, in *listCompanyIn) (*ListOut[Flow], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			fs, err := s.svc.ListFlows(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(fs, in.Page, flowOut), nil
		})

	huma.Register(api, op("get-flow", http.MethodGet, "/companies/{company}/flows/{flow}", "flows", "Obtém um fluxo"),
		func(ctx context.Context, in *FlowPath) (*ItemOut[Flow], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			f, err := s.svc.GetFlow(ctx, sc.Company.ID, in.Flow)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Flow]{Body: flowOut(f)}, nil
		})

	huma.Register(api, op("update-flow", http.MethodPatch, "/companies/{company}/flows/{flow}", "flows", "Atualiza um fluxo (ativar, desativar, cron...)"),
		func(ctx context.Context, in *updateFlowIn) (*ItemOut[Flow], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetFlow(ctx, sc.Company.ID, in.Flow)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			p, err := s.personaRef(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			pr, err := s.projectRef(ctx, sc.Company.ID, in.Body.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			up := service.UpdateFlowInput{Name: in.Body.Name, Description: in.Body.Description, Cron: in.Body.Cron,
				Timezone: in.Body.Timezone, Enabled: in.Body.Enabled, PersonaID: p, ProjectID: pr,
				ClearProject: in.Body.ClearProject, TaskTitle: in.Body.TaskTitle, TaskDescription: in.Body.TaskDescription}
			if in.Body.Priority != nil {
				pri := domain.Priority(*in.Body.Priority)
				up.Priority = &pri
			}
			f, err := s.svc.UpdateFlow(ctx, sc.Actor, sc.Company.ID, cur.ID, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Flow]{Body: flowOut(f)}, nil
		})

	huma.Register(api, op("run-flow", http.MethodPost, "/companies/{company}/flows/{flow}/run", "flows", "Roda o fluxo agora"),
		func(ctx context.Context, in *FlowPath) (*ItemOut[FlowRun], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetFlow(ctx, sc.Company.ID, in.Flow)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := s.svc.RunFlowNow(ctx, sc.Actor, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[FlowRun]{Body: flowRunOut(r)}, nil
		})

	huma.Register(api, op("list-flow-fires", http.MethodGet, "/companies/{company}/flows/{flow}/fires", "flows", "Histórico de disparos de um fluxo"),
		func(ctx context.Context, in *listFlowFiresIn) (*ListOut[FlowFire], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetFlow(ctx, sc.Company.ID, in.Flow)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			fires, err := s.svc.ListFlowFires(ctx, sc.Company.ID, cur.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(fires, Page{Limit: in.Limit}, flowFireOut), nil
		})
}
