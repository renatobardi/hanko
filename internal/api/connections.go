package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Conexões OAuth (ADR-0043). Ler é de qualquer membro (as pessoais, só do dono); criar, autorizar, testar e apagar é de
// admin (as pessoais, do dono). Run tokens só pedem uma conexão (POST /connections/request); nenhuma resposta traz token.

type connectionBackend interface {
	CreateConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateConnectionInput) (service.ConnectionView, error)
	ListConnections(ctx context.Context, companyID domain.ID) ([]service.ConnectionView, error)
	GetConnection(ctx context.Context, companyID domain.ID, ref string) (service.ConnectionView, error)
	UpdateConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in service.UpdateConnectionInput) (service.ConnectionView, error)
	AuthorizeConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionAuthStart, error)
	ProbeConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionView, error)
	RefreshConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionView, error)
	DisconnectConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectionView, error)
	DeleteConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error
	RequestConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.RequestConnectionInput) (service.RequestConnectionResult, error)
}

func (s *server) connections() (connectionBackend, error) {
	b, ok := s.svc.(connectionBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "conexões OAuth indisponíveis neste servidor")
	}
	return b, nil
}

// Connection é a autorização OAuth de um servidor MCP remoto. Nunca traz token.
type Connection struct {
	ID               string     `json:"id" format:"uuid"`
	Slug             string     `json:"slug" doc:"Também é o slug da integração MCP criada para ela."`
	Name             string     `json:"name"`
	URL              string     `json:"url" doc:"Endereço do servidor MCP."`
	Identity         string     `json:"identity" enum:"company,user" doc:"company: personas usam o token; user: só o dono."`
	Status           string     `json:"status" enum:"draft,pending_auth,connected,expired,error,revoked"`
	Setup            string     `json:"setup,omitempty" enum:"authorize,finish,reconnect,probe," doc:"O passo seguinte do que está incompleto."`
	IntegrationID    string     `json:"integration_id,omitempty" format:"uuid"`
	IntegrationSlug  string     `json:"integration_slug,omitempty"`
	Issuer           string     `json:"issuer,omitempty"`
	Scopes           []string   `json:"scopes"`
	ClientRegistered bool       `json:"client_registered"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty" doc:"Validade do access token."`
	ConnectedAt      *time.Time `json:"connected_at,omitempty"`
	LastRefreshAt    *time.Time `json:"last_refresh_at,omitempty"`
	LastProbeAt      *time.Time `json:"last_probe_at,omitempty"`
	LastProbeStatus  string     `json:"last_probe_status,omitempty" enum:"ok,unauthorized,forbidden,unreachable,"`
	LastError        string     `json:"last_error,omitempty"`
	GateID           string     `json:"gate_id,omitempty" format:"uuid"`
	CreatedBy        ActorRef   `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func connectionOut(v service.ConnectionView) Connection {
	return Connection{ID: idStr(v.ID), Slug: v.Slug, Name: v.Name, URL: v.URL, Identity: string(v.Identity), Status: string(v.Status),
		Setup: string(v.Setup), IntegrationID: idPtr(v.IntegrationID), IntegrationSlug: v.IntegrationSlug, Issuer: v.Issuer,
		Scopes: v.Scopes, ClientRegistered: v.ClientRegistered, ExpiresAt: v.ExpiresAt, ConnectedAt: v.ConnectedAt,
		LastRefreshAt: v.LastRefreshAt, LastProbeAt: v.LastProbeAt, LastProbeStatus: v.LastProbeStatus, LastError: v.LastError,
		GateID: idPtr(v.GateID), CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

// ConnectionAuthStart é a URL para o navegador do humano.
type ConnectionAuthStart struct {
	AuthorizationURL string    `json:"authorization_url" doc:"Abra no navegador: o servidor de autorização pede o consentimento e devolve para /oauth/callback."`
	ExpiresAt        time.Time `json:"expires_at"`
}

// ConnectionRequestResult é o resultado do pedido de uma persona.
type ConnectionRequestResult struct {
	Connection Connection `json:"connection"`
	Connected  bool       `json:"connected" doc:"Já estava autorizada: nenhum gate foi aberto."`
	Gate       *Gate      `json:"gate,omitempty" doc:"O gate kind=connection pendente."`
}

type connectionPath struct {
	CompanyPath
	Connection string `path:"connection" doc:"Id ou slug da conexão."`
}

type createConnectionIn struct {
	CompanyPath
	Body struct {
		Name            string   `json:"name" minLength:"1" maxLength:"120" example:"Docs remoto"`
		Slug            string   `json:"slug,omitempty" doc:"Derivado do nome quando vazio."`
		URL             string   `json:"url" example:"https://mcp.example.com/mcp"`
		Identity        string   `json:"identity,omitempty" enum:"company,user" doc:"Padrão company (admin)."`
		Scopes          []string `json:"scopes,omitempty" doc:"Escopos pedidos; vazio usa os que o servidor sugere."`
		ClientID        string   `json:"client_id,omitempty" doc:"Cliente OAuth já registrado, para servidor sem registro dinâmico."`
		ClientSecretRef string   `json:"client_secret_ref,omitempty" doc:"Nome de um segredo da empresa com o segredo do cliente (nunca o valor)."`
	}
}

type updateConnectionIn struct {
	connectionPath
	Body struct {
		Name            *string   `json:"name,omitempty"`
		Scopes          *[]string `json:"scopes,omitempty"`
		ClientID        *string   `json:"client_id,omitempty"`
		ClientSecretRef *string   `json:"client_secret_ref,omitempty"`
	}
}

type requestConnectionIn struct {
	CompanyPath
	Body struct {
		Slug   string `json:"slug,omitempty" doc:"Conexão existente da empresa."`
		Name   string `json:"name,omitempty" doc:"Nome da conexão proposta (com url)."`
		URL    string `json:"url,omitempty" doc:"Endereço do servidor MCP de uma conexão nova (nasce como rascunho)."`
		Reason string `json:"reason,omitempty" maxLength:"1000" doc:"Por que a persona precisa dela; aparece no gate."`
		Task   string `json:"task,omitempty" doc:"Id ou chave da task; com run token, padrão é a task da execução."`
	}
}

func (s *server) registerConnections(api huma.API) {
	prelude := func(ctx context.Context, company string, write bool) (connectionBackend, scope, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return nil, scope{}, err
			}
		}
		b, err := s.connections()
		if err != nil {
			return nil, scope{}, err
		}
		sc, err := s.scope(ctx, company)
		return b, sc, err
	}
	one := func(v service.ConnectionView, err error, ctx context.Context) (*ItemOut[Connection], error) {
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &ItemOut[Connection]{Body: connectionOut(v)}, nil
	}

	huma.Register(api, op("list-connections", http.MethodGet, "/companies/{company}/connections", "connections",
		"Lista as conexões OAuth (as da empresa e as pessoais do chamador)"),
		func(ctx context.Context, in *struct {
			CompanyPath
			Page
		}) (*ListOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			list, err := b.ListConnections(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, connectionOut), nil
		})

	huma.Register(api, created(op("create-connection", http.MethodPost, "/companies/{company}/connections", "connections",
		"Cria uma conexão (rascunho, ainda sem autorização); admin, ou qualquer membro para a pessoal")),
		func(ctx context.Context, in *createConnectionIn) (*ItemOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := b.CreateConnection(ctx, sc.Actor, sc.Company.ID, service.CreateConnectionInput{Name: in.Body.Name, Slug: in.Body.Slug,
				URL: in.Body.URL, Identity: domain.ConnectionIdentity(in.Body.Identity), Scopes: in.Body.Scopes, ClientID: in.Body.ClientID,
				ClientSecretRef: in.Body.ClientSecretRef})
			return one(v, err, ctx)
		})

	huma.Register(api, op("get-connection", http.MethodGet, "/companies/{company}/connections/{connection}", "connections", "Obtém uma conexão"),
		func(ctx context.Context, in *connectionPath) (*ItemOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			v, err := b.GetConnection(ctx, sc.Company.ID, in.Connection)
			return one(v, err, ctx)
		})

	huma.Register(api, op("update-connection", http.MethodPatch, "/companies/{company}/connections/{connection}", "connections",
		"Edita nome, escopos e cliente OAuth (mudar escopos ou cliente exige reconectar)"),
		func(ctx context.Context, in *updateConnectionIn) (*ItemOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := b.UpdateConnection(ctx, sc.Actor, sc.Company.ID, in.Connection, service.UpdateConnectionInput{Name: in.Body.Name,
				Scopes: in.Body.Scopes, ClientID: in.Body.ClientID, ClientSecretRef: in.Body.ClientSecretRef})
			return one(v, err, ctx)
		})

	huma.Register(api, op("authorize-connection", http.MethodPost, "/companies/{company}/connections/{connection}/authorize", "connections",
		"Começa (ou recomeça) a autorização OAuth e devolve a URL para o navegador; só humanos"),
		func(ctx context.Context, in *connectionPath) (*ItemOut[ConnectionAuthStart], error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			st, err := b.AuthorizeConnection(ctx, sc.Actor, sc.Company.ID, in.Connection)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConnectionAuthStart]{Body: ConnectionAuthStart{AuthorizationURL: st.URL, ExpiresAt: st.ExpiresAt}}, nil
		})

	huma.Register(api, op("probe-connection", http.MethodPost, "/companies/{company}/connections/{connection}/probe", "connections",
		"Testa a autenticação (initialize do MCP com o token); renova uma vez se o servidor recusar"),
		func(ctx context.Context, in *connectionPath) (*ItemOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := b.ProbeConnection(ctx, sc.Actor, sc.Company.ID, in.Connection)
			return one(v, err, ctx)
		})

	huma.Register(api, op("refresh-connection", http.MethodPost, "/companies/{company}/connections/{connection}/refresh", "connections",
		"Renova o token agora"),
		func(ctx context.Context, in *connectionPath) (*ItemOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := b.RefreshConnection(ctx, sc.Actor, sc.Company.ID, in.Connection)
			return one(v, err, ctx)
		})

	huma.Register(api, op("disconnect-connection", http.MethodPost, "/companies/{company}/connections/{connection}/disconnect", "connections",
		"Apaga os tokens, desliga a integração e volta a conexão a rascunho"),
		func(ctx context.Context, in *connectionPath) (*ItemOut[Connection], error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := b.DisconnectConnection(ctx, sc.Actor, sc.Company.ID, in.Connection)
			return one(v, err, ctx)
		})

	huma.Register(api, noContent(op("delete-connection", http.MethodDelete, "/companies/{company}/connections/{connection}", "connections",
		"Apaga a conexão, a integração e os tokens")),
		func(ctx context.Context, in *connectionPath) (*struct{}, error) {
			b, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			if err := b.DeleteConnection(ctx, sc.Actor, sc.Company.ID, in.Connection); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("request-connection", http.MethodPost, "/companies/{company}/connections/request", "connections",
		"Uma persona (run token) pede uma conexão; se não estiver autorizada, abre um gate kind=connection e a task espera"),
		func(ctx context.Context, in *requestConnectionIn) (*ItemOut[ConnectionRequestResult], error) {
			b, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			task, err := s.taskRef(ctx, sc.Company.ID, in.Body.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			var runID *domain.ID
			if sc.Claims != nil { // a execução e (por padrão) a task vêm do token
				if id, err := domain.ParseID(sc.Claims.RunID); err == nil {
					runID = &id
				}
				if task == nil {
					if id, err := domain.ParseID(sc.Claims.TaskID); err == nil {
						task = &id
					}
				}
			}
			r, err := b.RequestConnection(ctx, sc.Actor, sc.Company.ID, service.RequestConnectionInput{Slug: in.Body.Slug, Name: in.Body.Name,
				URL: in.Body.URL, Reason: in.Body.Reason, TaskID: task, RunID: runID})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := ConnectionRequestResult{Connection: connectionOut(r.Connection), Connected: r.Connected}
			if r.Gate != nil {
				g := gateOut(*r.Gate)
				out.Gate = &g
			}
			return &ItemOut[ConnectionRequestResult]{Body: out}, nil
		})
}
