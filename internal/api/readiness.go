package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
)

// Readiness da persona e catálogo de modelos (ADR-0055). Leitura de qualquer membro; "Testar agora" é de admin.

// readinessBackend é o subconjunto do service para readiness e modelos; opcional pelo mesmo motivo de skillsBackend.
type readinessBackend interface {
	GetPersonaReadiness(ctx context.Context, companyID, personaID domain.ID) (*domain.Readiness, error)
	ListPersonaReadiness(ctx context.Context, companyID domain.ID) (map[domain.ID]domain.Readiness, error)
	TestPersonaReadiness(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID) (domain.Readiness, error)
	ModelCatalog(ctx context.Context, companyID domain.ID, agent string) ([]domain.CatalogModel, error)
}

func (s *server) readiness() (readinessBackend, error) {
	b, ok := s.svc.(readinessBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "readiness indisponível neste servidor")
	}
	return b, nil
}

// ReadinessStep é um passo do teste de readiness.
type ReadinessStep struct {
	Name   string `json:"name" enum:"environment,binary,auth,model" doc:"environment: o ambiente responde; binary: o binário do harness existe; auth: há login ou chave de API; model: o modelo é válido e tem preço."`
	Status string `json:"status" enum:"ok,warn,fail,skip" doc:"fail bloqueia o despacho; warn e skip não."`
	Detail string `json:"detail,omitempty"`
}

// PersonaReadiness é o último teste de readiness de uma persona.
type PersonaReadiness struct {
	PersonaID         string          `json:"persona_id" format:"uuid"`
	Status            string          `json:"status" enum:"ready,not_ready,untested" doc:"untested: a persona nunca foi testada (não bloqueia o despacho)."`
	Agent             string          `json:"agent,omitempty"`
	Model             string          `json:"model,omitempty"`
	EnvironmentID     string          `json:"environment_id,omitempty" format:"uuid"`
	EnvironmentSource string          `json:"environment_source,omitempty" enum:"persona,project,default,local"`
	Steps             []ReadinessStep `json:"steps"`
	Reason            string          `json:"reason,omitempty" doc:"Motivo do bloqueio: o primeiro passo que falhou."`
	Trigger           string          `json:"trigger,omitempty" enum:"manual,periodic,hire,update"`
	DurationMs        int             `json:"duration_ms,omitempty"`
	CheckedAt         *time.Time      `json:"checked_at,omitempty"`
	FailingSince      *time.Time      `json:"failing_since,omitempty"`
	Blocking          bool            `json:"blocking" doc:"O despacho da persona está bloqueado por este resultado."`
}

func readinessOut(personaID domain.ID, r *domain.Readiness, agent, model string) PersonaReadiness {
	if r == nil {
		return PersonaReadiness{PersonaID: idStr(personaID), Status: "untested", Steps: []ReadinessStep{}}
	}
	o := PersonaReadiness{PersonaID: idStr(r.PersonaID), Status: string(r.Status), Agent: r.Agent, Model: r.Model, EnvironmentID: idPtr(r.EnvironmentID),
		EnvironmentSource: string(r.EnvironmentSource), Steps: make([]ReadinessStep, len(r.Steps)), Reason: r.Reason, Trigger: string(r.Trigger),
		DurationMs: r.DurationMs, FailingSince: r.FailingSince, Blocking: r.Blocks(agent, model)}
	t := r.CheckedAt
	o.CheckedAt = &t
	for i, st := range r.Steps {
		o.Steps[i] = ReadinessStep{Name: st.Name, Status: string(st.Status), Detail: st.Detail}
	}
	return o
}

// CatalogModel é um modelo selecionável de um harness.
type CatalogModel struct {
	Agent      string   `json:"agent,omitempty" doc:"Harness do modelo; vazio = serve a qualquer harness."`
	Model      string   `json:"model"`
	Origins    []string `json:"origins" doc:"builtin (alias do harness), price (model_prices), probe (visto num teste de readiness), run (usado numa execução)."`
	Priced     bool     `json:"priced" doc:"Há preço próprio; sem ele o custo é estimado com o preço de reserva."`
	PriceModel string   `json:"price_model,omitempty" doc:"Prefixo de model_prices que casou."`
}

func catalogModelOut(m domain.CatalogModel) CatalogModel {
	o := CatalogModel{Agent: m.Agent, Model: m.Model, Priced: m.Priced, PriceModel: m.PriceModel, Origins: make([]string, len(m.Origins))}
	for i, x := range m.Origins {
		o.Origins[i] = string(x)
	}
	return o
}

type listModelsIn struct {
	CompanyPath
	Agent string `query:"agent" enum:"claude,codex,acp,fake," doc:"Só os modelos deste harness (e os que servem a qualquer um)."`
}

type listReadinessIn struct {
	CompanyPath
}

func (s *server) registerReadiness(api huma.API) {
	huma.Register(api, op("get-persona-readiness", http.MethodGet, "/companies/{company}/personas/{persona}/readiness", "personas",
		"Último teste de readiness da persona (binário, login e modelo do harness)"),
		func(ctx context.Context, in *PersonaPath) (*ItemOut[PersonaReadiness], error) {
			rb, err := s.readiness()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := rb.GetPersonaReadiness(ctx, sc.Company.ID, p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaReadiness]{Body: readinessOut(p.ID, r, p.Agent, p.Model)}, nil
		})

	huma.Register(api, op("test-persona-readiness", http.MethodPost, "/companies/{company}/personas/{persona}/readiness", "personas",
		"Testa agora o harness da persona no ambiente dela (admin); falha bloqueia o despacho e abre o alerta persona_not_ready"),
		func(ctx context.Context, in *PersonaPath) (*ItemOut[PersonaReadiness], error) {
			rb, err := s.readiness()
			if err != nil {
				return nil, err
			}
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := rb.TestPersonaReadiness(ctx, sc.Actor, sc.Company.ID, p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaReadiness]{Body: readinessOut(p.ID, &r, p.Agent, p.Model)}, nil
		})

	huma.Register(api, op("list-readiness", http.MethodGet, "/companies/{company}/readiness", "personas",
		"Último teste de readiness de cada persona testada da empresa"),
		func(ctx context.Context, in *listReadinessIn) (*ListOut[PersonaReadiness], error) {
			rb, err := s.readiness()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			personas, err := s.svc.ListPersonas(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			all, err := rb.ListPersonaReadiness(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := make([]PersonaReadiness, 0, len(personas))
			for _, p := range personas {
				var r *domain.Readiness
				if x, ok := all[p.ID]; ok {
					r = &x
				}
				out = append(out, readinessOut(p.ID, r, p.Agent, p.Model))
			}
			return &ListOut[PersonaReadiness]{Body: ListBody[PersonaReadiness]{Items: out, Limit: len(out)}}, nil
		})

	huma.Register(api, op("list-models", http.MethodGet, "/companies/{company}/models", "personas",
		"Catálogo de modelos: aliases do harness, model_prices, o que testes e execuções relataram, com aviso de modelo sem preço"),
		func(ctx context.Context, in *listModelsIn) (*ListOut[CatalogModel], error) {
			rb, err := s.readiness()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := rb.ModelCatalog(ctx, sc.Company.ID, in.Agent)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ListOut[CatalogModel]{Body: ListBody[CatalogModel]{Items: modelCatalogOut(list), Limit: len(list)}}, nil
		})
}

func modelCatalogOut(list []domain.CatalogModel) []CatalogModel {
	out := make([]CatalogModel, len(list))
	for i, m := range list {
		out[i] = catalogModelOut(m)
	}
	return out
}
