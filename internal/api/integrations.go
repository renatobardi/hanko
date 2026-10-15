package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Integrações (ADR-0029): servidores MCP da empresa e webhooks de saída. Leitura de qualquer membro (a URL
// completa só para admin); escrita e teste de admin (o service confere o papel). Segredos nunca voltam,
// exceto o segredo de webhook gerado pelo servidor, uma única vez, na criação ou rotação.

// integrationsBackend é o subconjunto do service para integrações; opcional pelo mesmo motivo de skillsBackend.
type integrationsBackend interface {
	CreateIntegration(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateIntegrationInput) (service.IntegrationView, error)
	UpdateIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateIntegrationInput) (service.IntegrationView, error)
	DeleteIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	ListIntegrations(ctx context.Context, companyID domain.ID) ([]service.IntegrationView, error)
	GetIntegration(ctx context.Context, companyID domain.ID, ref string) (service.IntegrationView, error)
	ListIntegrationPersonas(ctx context.Context, companyID, id domain.ID) ([]domain.IntegrationPersona, error)
	ListPersonaIntegrations(ctx context.Context, companyID, personaID domain.ID) ([]service.PersonaIntegration, error)
	SetPersonaIntegration(ctx context.Context, actor domain.Actor, companyID, personaID, integrationID domain.ID, on bool) error
	ListWebhookDeliveries(ctx context.Context, companyID, integrationID domain.ID, limit int) ([]domain.WebhookDelivery, error)
	TestIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (service.IntegrationTest, error)
	RekeyIntegrationSecrets(ctx context.Context) (int, error)
}

func (s *server) integrations() (integrationsBackend, error) {
	b, ok := s.svc.(integrationsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "integrações indisponíveis neste servidor")
	}
	return b, nil
}

// IntegrationConfig é a parte não secreta da configuração.
type IntegrationConfig struct {
	Transport   string   `json:"transport,omitempty" enum:"http,stdio" doc:"MCP: http ou stdio."`
	URL         string   `json:"url,omitempty" doc:"MCP http: endereço do servidor. Webhook: destino https (a URL completa só para admin; os demais veem só a origem)."`
	Command     string   `json:"command,omitempty" doc:"MCP stdio: executável."`
	Args        []string `json:"args,omitempty" doc:"MCP stdio: argumentos."`
	HeaderNames []string `json:"header_names,omitempty" doc:"Nomes dos headers guardados (os valores nunca são devolvidos)."`
	EnvNames    []string `json:"env_names,omitempty" doc:"Nomes das variáveis de ambiente guardadas (os valores nunca são devolvidos)."`
}

// WebhookDelivery é uma entrega de webhook e o resultado da última tentativa.
type WebhookDelivery struct {
	ID            string     `json:"id" format:"uuid"`
	Event         string     `json:"event" doc:"Evento entregue; ping é a entrega de teste."`
	Test          bool       `json:"test"`
	Status        string     `json:"status" enum:"pending,retrying,success,failed"`
	Attempts      int        `json:"attempts"`
	HTTPStatus    *int       `json:"http_status,omitempty"`
	LatencyMs     *int       `json:"latency_ms,omitempty"`
	Error         string     `json:"error,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
}

// Integration é um serviço externo conectado à empresa.
type Integration struct {
	ID           string            `json:"id" format:"uuid"`
	CompanyID    string            `json:"company_id" format:"uuid"`
	Kind         string            `json:"kind" enum:"mcp,webhook"`
	Slug         string            `json:"slug" example:"docs"`
	Name         string            `json:"name" example:"Docs internos"`
	Enabled      bool              `json:"enabled"`
	AllPersonas  bool              `json:"all_personas" doc:"MCP: vale para todas as personas."`
	Config       IntegrationConfig `json:"config"`
	Events       []string          `json:"events" doc:"Webhook: eventos assinados."`
	SecretSet    bool              `json:"secret_set" doc:"Há segredo guardado."`
	SecretHint   string            `json:"secret_hint,omitempty" doc:"Webhook com segredo gerado pelo servidor: os primeiros caracteres, para reconhecê-lo."`
	Secret       string            `json:"secret,omitempty" doc:"Webhook com segredo gerado pelo servidor: o valor completo, SÓ nesta resposta (criação ou rotação). Guarde-o: não é possível ler de novo."`
	LastDelivery *WebhookDelivery  `json:"last_delivery,omitempty" doc:"Webhook: a entrega mais recente."`
	CreatedBy    ActorRef          `json:"created_by"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// IntegrationPersona é o estado de uma persona para um servidor MCP.
type IntegrationPersona struct {
	PersonaID string `json:"persona_id" format:"uuid"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
}

// PersonaIntegration é o estado de um servidor MCP para uma persona.
type PersonaIntegration struct {
	IntegrationID string `json:"integration_id" format:"uuid"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled" doc:"A integração está ligada."`
	AllPersonas   bool   `json:"all_personas" doc:"Vale para todas as personas."`
	Linked        bool   `json:"linked" doc:"Ligada especificamente a esta persona."`
}

// IntegrationTestResult é o resultado do teste de conexão ou de entrega.
type IntegrationTestResult struct {
	OK        bool             `json:"ok"`
	Status    int              `json:"status,omitempty" doc:"Código HTTP da resposta, quando houve."`
	LatencyMs int              `json:"latency_ms,omitempty"`
	Error     string           `json:"error,omitempty"`
	Note      string           `json:"note,omitempty"`
	Delivery  *WebhookDelivery `json:"delivery,omitempty" doc:"Webhook: a entrega de teste registrada."`
}

// RekeyResult é o resultado da rotação de chave.
type RekeyResult struct {
	Rewritten int `json:"rewritten" doc:"Quantos segredos foram reescritos com a chave atual."`
}

func deliveryOut(d domain.WebhookDelivery) WebhookDelivery {
	return WebhookDelivery{ID: idStr(d.ID), Event: d.Event, Test: d.Test, Status: d.Status, Attempts: d.Attempts, HTTPStatus: d.HTTPStatus,
		LatencyMs: d.LatencyMs, Error: d.Error, NextAttemptAt: d.NextAttemptAt, CreatedAt: d.CreatedAt, DeliveredAt: d.DeliveredAt}
}

func integrationOut(v service.IntegrationView) Integration {
	ev := v.Events
	if ev == nil {
		ev = []string{}
	}
	o := Integration{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Kind: string(v.Kind), Slug: v.Slug, Name: v.Name, Enabled: v.Enabled,
		AllPersonas: v.AllPersonas, Events: ev, SecretSet: v.SecretSet, SecretHint: v.SecretHint, Secret: v.RevealedSecret,
		Config: IntegrationConfig{Transport: v.Config.Transport, URL: v.Config.URL, Command: v.Config.Command, Args: v.Config.Args,
			HeaderNames: v.Config.HeaderNames, EnvNames: v.Config.EnvNames},
		CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if v.LastDelivery != nil {
		d := deliveryOut(*v.LastDelivery)
		o.LastDelivery = &d
	}
	return o
}

type IntegrationPath struct {
	CompanyPath
	Integration string `path:"integration" doc:"Id ou slug da integração." example:"docs"`
}

type IntegrationPersonaPath struct {
	IntegrationPath
	Persona string `path:"persona" doc:"Id ou slug da persona." example:"ana"`
}

type PersonaIntegrationPath struct {
	CompanyPath
	Persona     string `path:"persona" doc:"Id ou slug da persona." example:"ana"`
	Integration string `path:"integration" doc:"Id ou slug da integração (MCP)." example:"docs"`
}

type createIntegrationIn struct {
	CompanyPath
	Body struct {
		Kind    string `json:"kind" enum:"mcp,webhook"`
		Name    string `json:"name" minLength:"1" maxLength:"120" example:"Docs internos"`
		Slug    string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Derivado do nome quando omitido."`
		Enabled *bool  `json:"enabled,omitempty" doc:"Padrão: ligada."`
		URL     string `json:"url,omitempty" doc:"MCP http: endereço do servidor. Webhook: destino https."`

		Transport   string            `json:"transport,omitempty" enum:"http,stdio" doc:"MCP. Padrão http. stdio só quando o operador o liberou no servidor."`
		Command     string            `json:"command,omitempty" doc:"MCP stdio: executável."`
		Args        []string          `json:"args,omitempty" doc:"MCP stdio: argumentos."`
		Headers     map[string]string `json:"headers,omitempty" doc:"MCP http: headers enviados ao servidor (segredos, ex.: Authorization). Guardados cifrados; nunca devolvidos."`
		Env         map[string]string `json:"env,omitempty" doc:"MCP stdio: variáveis de ambiente do processo (segredos). Guardadas cifradas; nunca devolvidas."`
		AllPersonas bool              `json:"all_personas,omitempty" doc:"MCP: vale para todas as personas."`
		Personas    []string          `json:"personas,omitempty" doc:"MCP: slugs ou ids das personas que o recebem (quando não vale para todas)."`

		Events []string `json:"events,omitempty" doc:"Webhook: eventos assinados (task.created, task.completed, gate.opened, gate.decided, run.failed, alert.opened)."`
		Secret string   `json:"secret,omitempty" doc:"Webhook: segredo HMAC (mínimo de 16 caracteres). Omitido, o servidor gera um e o devolve uma vez."`
	}
}

type updateIntegrationIn struct {
	IntegrationPath
	Body struct {
		Name         *string           `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Enabled      *bool             `json:"enabled,omitempty"`
		URL          *string           `json:"url,omitempty"`
		Command      *string           `json:"command,omitempty"`
		Args         *[]string         `json:"args,omitempty" doc:"Substitui os argumentos."`
		SetHeaders   map[string]string `json:"set_headers,omitempty" doc:"Define ou troca estes headers; os demais ficam."`
		DropHeaders  []string          `json:"drop_headers,omitempty"`
		SetEnv       map[string]string `json:"set_env,omitempty"`
		DropEnv      []string          `json:"drop_env,omitempty"`
		AllPersonas  *bool             `json:"all_personas,omitempty"`
		Personas     *[]string         `json:"personas,omitempty" doc:"Substitui o conjunto de personas do MCP."`
		Events       *[]string         `json:"events,omitempty" doc:"Substitui os eventos assinados."`
		Secret       *string           `json:"secret,omitempty" doc:"Novo segredo HMAC do webhook."`
		RotateSecret bool              `json:"rotate_secret,omitempty" doc:"O servidor gera um novo segredo e o devolve uma vez."`
	}
}

type listIntegrationsIn struct {
	CompanyPath
	Page
}

type deliveriesIn struct {
	IntegrationPath
	Limit int `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"Máximo de entregas (mais recentes primeiro)."`
}

type setIntegrationPersonaIn struct {
	IntegrationPersonaPath
	Body EnabledBody
}

type setPersonaIntegrationIn struct {
	PersonaIntegrationPath
	Body EnabledBody
}

type personaIntegrationsIn struct {
	CompanyPath
	Persona string `path:"persona" doc:"Id ou slug da persona." example:"ana"`
}

// personaIDs resolve referências (slug ou id) de personas da empresa.
func (s *server) personaIDs(ctx context.Context, companyID domain.ID, refs []string) ([]domain.ID, error) {
	out := make([]domain.ID, 0, len(refs))
	for _, r := range refs {
		p, err := s.svc.GetPersona(ctx, companyID, r)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		out = append(out, p.ID)
	}
	return out, nil
}

func (s *server) registerIntegrations(api huma.API) {
	huma.Register(api, created(op("create-integration", http.MethodPost, "/companies/{company}/integrations", "integrations", "Cria uma integração (servidor MCP ou webhook)")),
		func(ctx context.Context, in *createIntegrationIn) (*ItemOut[Integration], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			ids, err := s.personaIDs(ctx, sc.Company.ID, b.Personas)
			if err != nil {
				return nil, err
			}
			v, err := ib.CreateIntegration(ctx, sc.Actor, sc.Company.ID, service.CreateIntegrationInput{
				Kind: domain.IntegrationKind(b.Kind), Name: b.Name, Slug: b.Slug, Enabled: b.Enabled, URL: b.URL,
				Transport: b.Transport, Command: b.Command, Args: b.Args, Headers: b.Headers, Env: b.Env,
				AllPersonas: b.AllPersonas, PersonaIDs: ids, Events: b.Events, Secret: b.Secret})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Integration]{Body: integrationOut(v)}, nil
		})

	huma.Register(api, op("list-integrations", http.MethodGet, "/companies/{company}/integrations", "integrations", "Lista as integrações da empresa"),
		func(ctx context.Context, in *listIntegrationsIn) (*ListOut[Integration], error) {
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := ib.ListIntegrations(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, integrationOut), nil
		})

	huma.Register(api, op("get-integration", http.MethodGet, "/companies/{company}/integrations/{integration}", "integrations", "Obtém uma integração"),
		func(ctx context.Context, in *IntegrationPath) (*ItemOut[Integration], error) {
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := ib.GetIntegration(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Integration]{Body: integrationOut(v)}, nil
		})

	huma.Register(api, op("update-integration", http.MethodPatch, "/companies/{company}/integrations/{integration}", "integrations", "Atualiza uma integração"),
		func(ctx context.Context, in *updateIntegrationIn) (*ItemOut[Integration], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := ib.GetIntegration(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			up := service.UpdateIntegrationInput{Name: b.Name, Enabled: b.Enabled, URL: b.URL, Command: b.Command, Args: b.Args,
				SetHeaders: b.SetHeaders, DropHeaders: b.DropHeaders, SetEnv: b.SetEnv, DropEnv: b.DropEnv, AllPersonas: b.AllPersonas,
				Events: b.Events, Secret: b.Secret, RotateSecret: b.RotateSecret}
			if b.Personas != nil {
				ids, err := s.personaIDs(ctx, sc.Company.ID, *b.Personas)
				if err != nil {
					return nil, err
				}
				up.PersonaIDs = &ids
			}
			v, err := ib.UpdateIntegration(ctx, sc.Actor, sc.Company.ID, cur.ID, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Integration]{Body: integrationOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-integration", http.MethodDelete, "/companies/{company}/integrations/{integration}", "integrations", "Apaga uma integração (e o histórico de entregas)")),
		func(ctx context.Context, in *IntegrationPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := ib.GetIntegration(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if err := ib.DeleteIntegration(ctx, sc.Actor, sc.Company.ID, cur.ID); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("test-integration", http.MethodPost, "/companies/{company}/integrations/{integration}/test", "integrations",
		"Testa a integração: webhook envia um evento ping assinado; MCP http faz o initialize do protocolo"),
		func(ctx context.Context, in *IntegrationPath) (*ItemOut[IntegrationTestResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := ib.GetIntegration(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			r, err := ib.TestIntegration(ctx, sc.Actor, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := IntegrationTestResult{OK: r.OK, Status: r.Status, LatencyMs: r.LatencyMs, Error: r.Error, Note: r.Note}
			if r.Delivery != nil {
				d := deliveryOut(*r.Delivery)
				out.Delivery = &d
			}
			return &ItemOut[IntegrationTestResult]{Body: out}, nil
		})

	huma.Register(api, op("list-integration-deliveries", http.MethodGet, "/companies/{company}/integrations/{integration}/deliveries", "integrations", "Lista as últimas entregas de um webhook"),
		func(ctx context.Context, in *deliveriesIn) (*ListOut[WebhookDelivery], error) {
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := ib.GetIntegration(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := ib.ListWebhookDeliveries(ctx, sc.Company.ID, cur.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: in.Limit}, deliveryOut), nil
		})

	huma.Register(api, op("list-integration-personas", http.MethodGet, "/companies/{company}/integrations/{integration}/personas", "integrations", "Lista as personas com o estado do servidor MCP"),
		func(ctx context.Context, in *IntegrationPath) (*ListOut[IntegrationPersona], error) {
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := ib.GetIntegration(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := ib.ListIntegrationPersonas(ctx, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(p domain.IntegrationPersona) IntegrationPersona {
				return IntegrationPersona{PersonaID: idStr(p.PersonaID), Slug: p.Slug, Name: p.Name, Enabled: p.Enabled}
			}), nil
		})

	huma.Register(api, noContent(op("set-integration-persona", http.MethodPut, "/companies/{company}/integrations/{integration}/personas/{persona}", "integrations", "Liga ou desliga o servidor MCP para uma persona")),
		func(ctx context.Context, in *setIntegrationPersonaIn) (*struct{}, error) {
			return s.setPersonaIntegration(ctx, in.CompanyPath, in.Persona, in.Integration, in.Body.Enabled)
		})

	huma.Register(api, op("list-persona-integrations", http.MethodGet, "/companies/{company}/personas/{persona}/integrations", "integrations", "Lista os servidores MCP com o estado para a persona"),
		func(ctx context.Context, in *personaIntegrationsIn) (*ListOut[PersonaIntegration], error) {
			ib, err := s.integrations()
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
			list, err := ib.ListPersonaIntegrations(ctx, sc.Company.ID, p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(x service.PersonaIntegration) PersonaIntegration {
				return PersonaIntegration{IntegrationID: idStr(x.IntegrationID), Slug: x.Slug, Name: x.Name, Enabled: x.Enabled, AllPersonas: x.AllPersonas, Linked: x.Linked}
			}), nil
		})

	huma.Register(api, noContent(op("set-persona-integration", http.MethodPut, "/companies/{company}/personas/{persona}/integrations/{integration}", "integrations", "Liga ou desliga um servidor MCP para a persona")),
		func(ctx context.Context, in *setPersonaIntegrationIn) (*struct{}, error) {
			return s.setPersonaIntegration(ctx, in.CompanyPath, in.Persona, in.Integration, in.Body.Enabled)
		})

	huma.Register(api, op("rekey-integration-secrets", http.MethodPost, "/integrations/rekey-secrets", "integrations",
		"Reescreve com a chave atual os segredos cifrados com a chave anterior (rotação de HANKO_SECRET_KEY); dono da instalação"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[RekeyResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.integrations()
			if err != nil {
				return nil, err
			}
			n, err := ib.RekeyIntegrationSecrets(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[RekeyResult]{Body: RekeyResult{Rewritten: n}}, nil
		})
}

func (s *server) setPersonaIntegration(ctx context.Context, cp CompanyPath, personaRef, integRef string, on bool) (*struct{}, error) {
	if err := forbidPersona(ctx); err != nil {
		return nil, err
	}
	ib, err := s.integrations()
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx, cp.Company)
	if err != nil {
		return nil, err
	}
	p, err := s.svc.GetPersona(ctx, sc.Company.ID, personaRef)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	cur, err := ib.GetIntegration(ctx, sc.Company.ID, integRef)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	if err := ib.SetPersonaIntegration(ctx, sc.Actor, sc.Company.ID, p.ID, cur.ID, on); err != nil {
		return nil, s.fail(ctx, err)
	}
	return &struct{}{}, nil
}
