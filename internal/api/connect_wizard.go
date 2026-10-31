package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Assistente Conectar (ADR-0089) e modelos de acesso. Orquestração de admin: begin cria a rascunho, discover
// repete o passo 2, preview confere as tools efetivas por persona e complete escreve tudo numa transação.
// Modelos de acesso são dado por empresa: leitura de membro, escrita de admin.

// connectBackend é o subconjunto do service para o assistente e os modelos; opcional como os demais.
type connectBackend interface {
	ConnectBegin(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.ConnectBeginInput) (service.ConnectView, error)
	ConnectState(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectView, error)
	ConnectDiscover(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (service.ConnectView, error)
	ConnectDiscard(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error
	ConnectPreview(ctx context.Context, actor domain.Actor, companyID domain.ID, ref, templateRef string, personaIDs []domain.ID) (service.ConnectPreviewView, error)
	ConnectComplete(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in service.ConnectCompleteInput) (service.ConnectResultView, error)
	ListAccessTemplates(ctx context.Context, companyID domain.ID) ([]domain.AccessTemplate, error)
	GetAccessTemplate(ctx context.Context, companyID domain.ID, ref string) (domain.AccessTemplate, error)
	CreateAccessTemplate(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.AccessTemplateInput) (domain.AccessTemplate, error)
	UpdateAccessTemplate(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in service.AccessTemplateInput) (domain.AccessTemplate, error)
	DeleteAccessTemplate(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) error
}

func (s *server) connect() (connectBackend, error) {
	b, ok := s.svc.(connectBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "assistente indisponível neste servidor")
	}
	return b, nil
}

// ---------- DTOs ----------

// AccessTemplate é um modelo de acesso: o modo (allow/ask/off) para cada risco de tool.
type AccessTemplate struct {
	ID              string    `json:"id" format:"uuid"`
	Slug            string    `json:"slug" example:"leitura-livre-escrita-com-aprovacao"`
	Name            string    `json:"name" example:"Leitura livre, escrita com aprovação"`
	Description     string    `json:"description,omitempty"`
	ReadMode        string    `json:"read_mode" enum:"allow,ask,off"`
	WriteMode       string    `json:"write_mode" enum:"allow,ask,off"`
	DestructiveMode string    `json:"destructive_mode" enum:"allow,ask,off"`
	Suggested       bool      `json:"suggested" doc:"O modelo sugerido no passo 4 do assistente (um por empresa)."`
	CreatedBy       ActorRef  `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func templateOut(t domain.AccessTemplate) AccessTemplate {
	return AccessTemplate{ID: idStr(t.ID), Slug: t.Slug, Name: t.Name, Description: t.Description,
		ReadMode: string(t.ReadMode), WriteMode: string(t.WriteMode), DestructiveMode: string(t.DestructiveMode),
		Suggested: t.Suggested, CreatedBy: actorRef(t.CreatedBy), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

// ConnectWizardTool é uma tool do catálogo no assistente.
type ConnectWizardTool struct {
	IntegrationTool
	Quarantined bool `json:"quarantined" doc:"Em quarentena (ADR-0067): escreve e espera revisão."`
}

// ConnectWizard é o estado do assistente para uma integração.
type ConnectWizard struct {
	Integration    Integration         `json:"integration"`
	Transport      string              `json:"transport"`
	SyncedAt       *time.Time          `json:"synced_at,omitempty" doc:"Última descoberta bem-sucedida."`
	CatalogError   string              `json:"catalog_error,omitempty"`
	Tools          []ConnectWizardTool `json:"tools"`
	Connection     *Connection         `json:"connection,omitempty" doc:"Presente quando o assistente começou por OAuth."`
	AuthURL        string              `json:"auth_url,omitempty" doc:"OAuth: URL de autorização, quando falta consentir."`
	Templates      []AccessTemplate    `json:"templates"`
	PersonasLinked int                 `json:"personas_linked"`
	CanComplete    bool                `json:"can_complete" doc:"Descoberta feita: dá para escolher personas e concluir."`
}

func connectOut(v service.ConnectView) ConnectWizard {
	o := ConnectWizard{Integration: integrationOut(service.IntegrationView{Integration: v.Integration}), Transport: v.Transport, SyncedAt: v.Catalog.SyncedAt,
		CatalogError: v.CatalogErr, Tools: []ConnectWizardTool{}, Templates: []AccessTemplate{},
		PersonasLinked: v.PersonasLinked, CanComplete: v.CanComplete}
	for _, t := range v.Tools {
		o.Tools = append(o.Tools, ConnectWizardTool{IntegrationTool: toolOut(t.IntegrationTool), Quarantined: t.Quarantined})
	}
	if v.Connection != nil {
		c := connectionOut(service.ConnectionView{Connection: *v.Connection})
		o.Connection = &c
	}
	for _, t := range v.Templates {
		o.Templates = append(o.Templates, templateOut(t))
	}
	return o
}

// ConnectPreviewTool é o veredito de uma tool para uma persona no passo 5.
type ConnectPreviewTool struct {
	Name      string      `json:"name"`
	Title     string      `json:"title,omitempty"`
	Risk      string      `json:"risk" enum:"read,write,destructive,unknown"`
	Mode      string      `json:"mode" enum:"allow,ask,off" doc:"Modo do modelo para o risco desta tool (desconhecido conta como destrutivo)."`
	Verdict   ToolVerdict `json:"verdict"`
	Effective bool        `json:"effective" doc:"Após concluir, a persona vê e pode chamar esta tool."`
	Note      string      `json:"note,omitempty"`
}

// ConnectPreviewPersona é a conferência do passo 5 para uma persona.
type ConnectPreviewPersona struct {
	PersonaID string               `json:"persona_id" format:"uuid"`
	Name      string               `json:"name"`
	Tools     []ConnectPreviewTool `json:"tools"`
}

// ConnectPreview é o passo 5: tools efetivas por persona antes de concluir.
type ConnectPreview struct {
	Template AccessTemplate          `json:"template"`
	Personas []ConnectPreviewPersona `json:"personas"`
	Released []string                `json:"released,omitempty" doc:"Tools que saem da quarentena na conclusão."`
}

func previewOut(v service.ConnectPreviewView) ConnectPreview {
	o := ConnectPreview{Template: templateOut(v.Template), Personas: []ConnectPreviewPersona{}, Released: v.Released}
	for _, p := range v.Personas {
		row := ConnectPreviewPersona{PersonaID: idStr(p.PersonaID), Name: p.Name, Tools: []ConnectPreviewTool{}}
		for _, t := range p.Tools {
			row.Tools = append(row.Tools, ConnectPreviewTool{Name: t.Name, Title: t.Title, Risk: string(t.Risk),
				Mode: string(t.Mode), Verdict: verdictOut(t.Verdict), Effective: t.Effective, Note: t.Note})
		}
		o.Personas = append(o.Personas, row)
	}
	return o
}

// ConnectResult é o resultado da conclusão.
type ConnectResult struct {
	Integration Integration          `json:"integration"`
	Profile     ToolProfile          `json:"profile"`
	Personas    []IntegrationPersona `json:"personas"`
	Released    []string             `json:"released,omitempty"`
}

func connectResultOut(v service.ConnectResultView) ConnectResult {
	o := ConnectResult{Integration: integrationOut(service.IntegrationView{Integration: v.Integration}), Profile: profileOut(v.Profile),
		Personas: []IntegrationPersona{}, Released: v.Released}
	for _, p := range v.Personas {
		o.Personas = append(o.Personas, IntegrationPersona{PersonaID: idStr(p.PersonaID), Slug: p.Slug, Name: p.Name, Enabled: p.Enabled})
	}
	return o
}

// ---------- entrada ----------

type connectPath struct {
	CompanyPath
	Integration string `path:"integration" doc:"Id ou slug da integração criada pelo assistente." example:"docs"`
}

type connectBeginIn struct {
	CompanyPath
	Body struct {
		Name     string            `json:"name" minLength:"1" maxLength:"120" example:"GitHub"`
		URL      string            `json:"url" doc:"Endereço do servidor MCP http."`
		Headers  map[string]string `json:"headers,omitempty" doc:"Headers fixos enviados ao servidor (segredos, ex.: Authorization). Guardados cifrados; nunca devolvidos."`
		OAuth    bool              `json:"oauth,omitempty" doc:"Conectar por OAuth 2.1 com PKCE (ADR-0043) em vez de header secreto."`
		Scopes   []string          `json:"scopes,omitempty" doc:"OAuth: escopos pedidos."`
		ClientID string            `json:"client_id,omitempty" doc:"OAuth: cliente já registrado (para servidor sem registro dinâmico)."`
	}
}

type connectPreviewIn struct {
	connectPath
	Template string `query:"template" required:"true" doc:"Id ou slug do modelo de acesso."`
	Personas string `query:"personas" required:"true" doc:"Ids ou slugs das personas, separados por vírgula."`
}

type connectCompleteIn struct {
	connectPath
	Body struct {
		Template    string   `json:"template" required:"true" doc:"Id ou slug do modelo de acesso (\"Personalizado\" é feito no editor de perfis, não aqui)."`
		Personas    []string `json:"personas" minItems:"1" doc:"Ids ou slugs das personas que recebem a ferramenta (nunca \"todas\")."`
		ProfileName string   `json:"profile_name,omitempty" doc:"Nome do perfil criado; padrão \"<integração> — <modelo>\"."`
		ProfileSlug string   `json:"profile_slug,omitempty"`
	}
}

type accessTemplateBody struct {
	Name            string `json:"name" minLength:"1" maxLength:"120" example:"Somente leitura"`
	Slug            string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Derivado do nome quando omitido (só na criação)."`
	Description     string `json:"description,omitempty" maxLength:"300"`
	ReadMode        string `json:"read_mode" enum:"allow,ask,off"`
	WriteMode       string `json:"write_mode" enum:"allow,ask,off"`
	DestructiveMode string `json:"destructive_mode" enum:"allow,ask,off"`
	Suggested       bool   `json:"suggested,omitempty"`
}

type accessTemplateIn struct {
	CompanyPath
	Body accessTemplateBody
}

type accessTemplateInWithPath struct {
	accessTemplatePath
	Body accessTemplateBody
}

type accessTemplatePath struct {
	CompanyPath
	Template string `path:"template" doc:"Id ou slug do modelo." example:"somente-leitura"`
}

type listAccessTemplatesIn struct {
	CompanyPath
	Page
}

// splitRefs separa referências por vírgula (ids ou slugs).
func splitRefs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func templateIn(b accessTemplateBody) (service.AccessTemplateInput, error) {
	in := service.AccessTemplateInput{Slug: b.Slug, Name: b.Name, Description: b.Description,
		ReadMode: domain.ToolPolicyMode(b.ReadMode), WriteMode: domain.ToolPolicyMode(b.WriteMode),
		DestructiveMode: domain.ToolPolicyMode(b.DestructiveMode), Suggested: b.Suggested}
	for _, m := range []domain.ToolPolicyMode{in.ReadMode, in.WriteMode, in.DestructiveMode} {
		if !m.Valid() {
			return in, problem(http.StatusBadRequest, "modo deve ser allow, ask ou off")
		}
	}
	return in, nil
}

func (s *server) registerConnect(api huma.API) {
	// assistente
	huma.Register(api, created(op("connect-begin", http.MethodPost, "/companies/{company}/integrations/connect", "integrations",
		"Passo 1+2 do assistente Conectar: cria a integração (header secreto) ou a conexão OAuth e descobre o catálogo")),
		func(ctx context.Context, in *connectBeginIn) (*ItemOut[ConnectWizard], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := cb.ConnectBegin(ctx, sc.Actor, sc.Company.ID, service.ConnectBeginInput{
				Name: in.Body.Name, URL: in.Body.URL, Headers: in.Body.Headers, OAuth: in.Body.OAuth,
				Scopes: in.Body.Scopes, ClientID: in.Body.ClientID})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConnectWizard]{Body: connectOut(v)}, nil
		})

	huma.Register(api, op("connect-state", http.MethodGet, "/companies/{company}/integrations/connect/{integration}", "integrations",
		"Estado do assistente Conectar para uma integração"),
		func(ctx context.Context, in *connectPath) (*ItemOut[ConnectWizard], error) {
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := cb.ConnectState(ctx, sc.Actor, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConnectWizard]{Body: connectOut(v)}, nil
		})

	huma.Register(api, op("connect-discover", http.MethodPost, "/companies/{company}/integrations/connect/{integration}/discover", "integrations",
		"Repete a descoberta do passo 2 (testar de novo); OAuth sem consentimento devolve a URL de autorização"),
		func(ctx context.Context, in *connectPath) (*ItemOut[ConnectWizard], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := cb.ConnectDiscover(ctx, sc.Actor, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConnectWizard]{Body: connectOut(v)}, nil
		})

	huma.Register(api, noContent(op("connect-discard", http.MethodDelete, "/companies/{company}/integrations/connect/{integration}", "integrations",
		"Descarta o rascunho do assistente: apaga a integração (ou a conexão OAuth inteira)")),
		func(ctx context.Context, in *connectPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := cb.ConnectDiscard(ctx, sc.Actor, sc.Company.ID, in.Integration); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("connect-preview", http.MethodGet, "/companies/{company}/integrations/connect/{integration}/preview", "integrations",
		"Passo 5: tools efetivas por persona com o modelo aplicado, antes de concluir"),
		func(ctx context.Context, in *connectPreviewIn) (*ItemOut[ConnectPreview], error) {
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ids, err := s.personaIDs(ctx, sc.Company.ID, splitRefs(in.Personas))
			if err != nil {
				return nil, err
			}
			v, err := cb.ConnectPreview(ctx, sc.Actor, sc.Company.ID, in.Integration, in.Template, ids)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConnectPreview]{Body: previewOut(v)}, nil
		})

	huma.Register(api, created(op("connect-complete", http.MethodPost, "/companies/{company}/integrations/connect/{integration}/complete", "integrations",
		"Conclui o assistente: perfil com o modelo, vínculo e ligação por persona, quarentena liberada — tudo numa transação")),
		func(ctx context.Context, in *connectCompleteIn) (*ItemOut[ConnectResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ids, err := s.personaIDs(ctx, sc.Company.ID, in.Body.Personas)
			if err != nil {
				return nil, err
			}
			v, err := cb.ConnectComplete(ctx, sc.Actor, sc.Company.ID, in.Integration, service.ConnectCompleteInput{
				Template: in.Body.Template, PersonaIDs: ids, ProfileName: in.Body.ProfileName, ProfileSlug: in.Body.ProfileSlug})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConnectResult]{Body: connectResultOut(v)}, nil
		})

	// modelos de acesso
	huma.Register(api, created(op("create-access-template", http.MethodPost, "/companies/{company}/access-templates", "integrations",
		"Cria um modelo de acesso (admin)")),
		func(ctx context.Context, in *accessTemplateIn) (*ItemOut[AccessTemplate], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b, err := templateIn(in.Body)
			if err != nil {
				return nil, err
			}
			v, err := cb.CreateAccessTemplate(ctx, sc.Actor, sc.Company.ID, b)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[AccessTemplate]{Body: templateOut(v)}, nil
		})

	huma.Register(api, op("list-access-templates", http.MethodGet, "/companies/{company}/access-templates", "integrations",
		"Lista os modelos de acesso da empresa (o sugerido primeiro)"),
		func(ctx context.Context, in *listAccessTemplatesIn) (*ListOut[AccessTemplate], error) {
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := cb.ListAccessTemplates(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, templateOut), nil
		})

	huma.Register(api, op("get-access-template", http.MethodGet, "/companies/{company}/access-templates/{template}", "integrations",
		"Obtém um modelo de acesso"),
		func(ctx context.Context, in *accessTemplatePath) (*ItemOut[AccessTemplate], error) {
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := cb.GetAccessTemplate(ctx, sc.Company.ID, in.Template)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[AccessTemplate]{Body: templateOut(v)}, nil
		})

	huma.Register(api, op("update-access-template", http.MethodPatch, "/companies/{company}/access-templates/{template}", "integrations",
		"Edita um modelo de acesso (admin). Cópia no uso: conexões concluídas antes continuam iguais."),
		func(ctx context.Context, in *accessTemplateInWithPath) (*ItemOut[AccessTemplate], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b, err := templateIn(in.Body)
			if err != nil {
				return nil, err
			}
			v, err := cb.UpdateAccessTemplate(ctx, sc.Actor, sc.Company.ID, in.Template, b)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[AccessTemplate]{Body: templateOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-access-template", http.MethodDelete, "/companies/{company}/access-templates/{template}", "integrations",
		"Apaga um modelo de acesso (admin). Cópia no uso: conexões concluídas antes continuam iguais.")),
		func(ctx context.Context, in *accessTemplatePath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			cb, err := s.connect()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := cb.DeleteAccessTemplate(ctx, sc.Actor, sc.Company.ID, in.Template); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})
}
