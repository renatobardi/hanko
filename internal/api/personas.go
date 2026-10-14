package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type PersonaPath struct {
	CompanyPath
	Persona string `path:"persona" doc:"Id ou slug da persona." example:"cto"`
}

type hirePersonaIn struct {
	CompanyPath
	Body struct {
		Name                 string         `json:"name" minLength:"1" maxLength:"120" example:"Marina"`
		Slug                 string         `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" example:"cto" doc:"Derivado do nome quando omitido."`
		Title                string         `json:"title,omitempty" example:"CTO"`
		Instructions         string         `json:"instructions,omitempty" doc:"Instruções permanentes da persona."`
		Agent                string         `json:"agent" enum:"claude,codex,fake" example:"claude" doc:"Harness que executa a persona."`
		Model                string         `json:"model,omitempty" example:"claude-sonnet-4-5"`
		AdapterConfig        map[string]any `json:"adapter_config,omitempty" doc:"Configuração específica do adapter (ex.: permission_mode, extra_args)."`
		ReportsTo            string         `json:"reports_to,omitempty" doc:"Id ou slug da persona superior."`
		Workspace            string         `json:"workspace,omitempty" doc:"Id ou slug do workspace."`
		HeartbeatIntervalSec int            `json:"heartbeat_interval_sec,omitempty" minimum:"0" example:"300"`
		BudgetMonthlyCents   *int64         `json:"budget_monthly_cents,omitempty" minimum:"0"`
	}
}

type listPersonasIn struct {
	CompanyPath
	Page
}

type updatePersonaIn struct {
	PersonaPath
	Body struct {
		Name                 *string `json:"name,omitempty" minLength:"1"`
		Title                *string `json:"title,omitempty"`
		Instructions         *string `json:"instructions,omitempty"`
		Model                *string `json:"model,omitempty"`
		Status               *string `json:"status,omitempty" enum:"active,idle,running,paused,error,awaiting_gate,off"`
		HeartbeatIntervalSec *int    `json:"heartbeat_interval_sec,omitempty" minimum:"0"`
		Workspace            string  `json:"workspace,omitempty" doc:"Id ou slug do novo workspace."`
		ClearWorkspace       bool    `json:"clear_workspace,omitempty" doc:"Remove o workspace da persona."`
	}
}

func (s *server) registerPersonas(api huma.API) {
	huma.Register(api, created(op("hire-persona", http.MethodPost, "/companies/{company}/personas", "personas", "Contrata uma persona")),
		func(ctx context.Context, in *hirePersonaIn) (*ItemOut[Persona], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			reports, err := s.personaRef(ctx, cid, in.Body.ReportsTo)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ws, err := s.workspaceRef(ctx, cid, in.Body.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			var cfg json.RawMessage
			if len(in.Body.AdapterConfig) > 0 {
				if cfg, err = json.Marshal(in.Body.AdapterConfig); err != nil {
					return nil, s.fail(ctx, domain.Invalid("adapter_config", "JSON inválido"))
				}
			}
			p, err := s.svc.HirePersona(ctx, sc.Actor, cid, service.HirePersonaInput{
				Name: in.Body.Name, Slug: in.Body.Slug, Title: in.Body.Title, Instructions: in.Body.Instructions,
				Agent: in.Body.Agent, Model: in.Body.Model, AdapterConfig: cfg, ReportsTo: reports, WorkspaceID: ws,
				HeartbeatIntervalSec: in.Body.HeartbeatIntervalSec, BudgetMonthlyCents: in.Body.BudgetMonthlyCents})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Persona]{Body: personaOut(p)}, nil
		})

	huma.Register(api, op("list-personas", http.MethodGet, "/companies/{company}/personas", "personas", "Lista personas"),
		func(ctx context.Context, in *listPersonasIn) (*ListOut[Persona], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ps, err := s.svc.ListPersonas(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ps, in.Page, personaOut), nil
		})

	huma.Register(api, op("get-persona", http.MethodGet, "/companies/{company}/personas/{persona}", "personas", "Obtém uma persona"),
		func(ctx context.Context, in *PersonaPath) (*ItemOut[Persona], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Persona]{Body: personaOut(p)}, nil
		})

	huma.Register(api, op("update-persona", http.MethodPatch, "/companies/{company}/personas/{persona}", "personas", "Atualiza uma persona"),
		func(ctx context.Context, in *updatePersonaIn) (*ItemOut[Persona], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ws, err := s.workspaceRef(ctx, sc.Company.ID, in.Body.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			up := service.UpdatePersonaInput{
				Name: in.Body.Name, Title: in.Body.Title, Instructions: in.Body.Instructions, Model: in.Body.Model,
				HeartbeatIntervalSec: in.Body.HeartbeatIntervalSec, WorkspaceID: ws, ClearWorkspace: in.Body.ClearWorkspace}
			if in.Body.Status != nil {
				st := domain.PersonaStatus(*in.Body.Status)
				up.Status = &st
			}
			p, err := s.svc.UpdatePersona(ctx, sc.Actor, sc.Company.ID, cur.ID, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Persona]{Body: personaOut(p)}, nil
		})
}
