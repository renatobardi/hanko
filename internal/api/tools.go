package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Apps, gateway de tools e perfis de ferramentas (ADR-0036). Leitura de catálogo, perfis, vínculos e gateways é
// de qualquer membro; a auditoria e toda escrita são de admin (o service confere o papel). Run tokens não
// configuram nada aqui. O token de gateway (hkgw_) só volta na criação.

type toolsBackend interface {
	ListIntegrationTools(ctx context.Context, companyID, integrationID domain.ID) (service.IntegrationToolsView, error)
	RefreshIntegrationTools(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID) (service.ToolCatalogResult, error)
	SetToolStatus(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID, tool string, status domain.ToolStatus) (domain.IntegrationTool, error)
	ReviewQuarantinedTools(ctx context.Context, actor domain.Actor, companyID, integrationID domain.ID, tools []string) (int, error)
	CreateToolProfile(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateToolProfileInput) (service.ToolProfileView, error)
	GetToolProfile(ctx context.Context, companyID domain.ID, ref string) (service.ToolProfileView, error)
	ListToolProfiles(ctx context.Context, companyID domain.ID) ([]service.ToolProfileView, error)
	UpdateToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.UpdateToolProfileInput) (service.ToolProfileView, error)
	DeleteToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID) error
	AddToolProfileRule(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.ToolRuleInput) (domain.ToolRule, error)
	DeleteToolProfileRule(ctx context.Context, actor domain.Actor, companyID, profileID, ruleID domain.ID) error
	SetIntegrationToolChoices(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.IntegrationToolChoices) (service.ToolProfileView, error)
	BindToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID domain.ID, in service.ToolBindingInput) (domain.ToolBinding, error)
	UnbindToolProfile(ctx context.Context, actor domain.Actor, companyID, profileID, bindingID domain.ID) error
	EffectiveToolsFor(ctx context.Context, companyID, personaID domain.ID, projectID *domain.ID) (service.EffectiveTools, error)
	CreateToolGateway(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateToolGatewayInput) (service.ToolGatewayView, error)
	ListToolGateways(ctx context.Context, companyID domain.ID) ([]domain.ToolGateway, error)
	GetToolGateway(ctx context.Context, companyID domain.ID, ref string) (service.ToolGatewayView, error)
	UpdateToolGateway(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID, in service.UpdateToolGatewayInput) (service.ToolGatewayView, error)
	DeleteToolGateway(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID) error
	CreateGatewayToken(ctx context.Context, actor domain.Actor, companyID, gatewayID domain.ID, in service.CreateGatewayTokenInput) (domain.ToolGatewayToken, string, error)
	RevokeGatewayToken(ctx context.Context, actor domain.Actor, companyID, gatewayID, tokenID domain.ID) (domain.ToolGatewayToken, error)
	ListToolInvocations(ctx context.Context, companyID domain.ID, f service.ToolInvocationFilter) ([]domain.ToolInvocation, error)
}

func (s *server) tools() (toolsBackend, error) {
	b, ok := s.svc.(toolsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "gateway de tools indisponível neste servidor")
	}
	return b, nil
}

// ---------- DTOs ----------

// ToolAnnotations são as dicas que o servidor MCP declarou para a tool (ausente = não declarada).
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"read_only_hint,omitempty" doc:"Só lê."`
	DestructiveHint *bool  `json:"destructive_hint,omitempty" doc:"Pode destruir (vale quando não é só leitura)."`
	IdempotentHint  *bool  `json:"idempotent_hint,omitempty"`
	OpenWorldHint   *bool  `json:"open_world_hint,omitempty"`
}

// IntegrationTool é uma tool do catálogo de uma integração MCP.
type IntegrationTool struct {
	ID              string          `json:"id" format:"uuid"`
	IntegrationID   string          `json:"integration_id" format:"uuid"`
	IntegrationSlug string          `json:"integration_slug"`
	Name            string          `json:"name" example:"create_issue"`
	Title           string          `json:"title,omitempty"`
	Description     string          `json:"description,omitempty"`
	InputSchema     map[string]any  `json:"input_schema" doc:"JSON Schema de entrada (objeto)."`
	Annotations     ToolAnnotations `json:"annotations"`
	SchemaSHA256    string          `json:"schema_sha256" doc:"Hash da definição (nome, descrição, schema e anotações): mudou, a tool mudou de contrato."`
	Risk            string          `json:"risk" enum:"read,write,destructive,unknown" doc:"Derivado das anotações."`
	Status          string          `json:"status" enum:"active,quarantined,disabled,removed" doc:"quarantined: nova ou alterada e pode escrever; espera a revisão de um admin."`
	FirstSeenAt     time.Time       `json:"first_seen_at"`
	LastSeenAt      time.Time       `json:"last_seen_at"`
	ReviewedBy      *ActorRef       `json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time      `json:"reviewed_at,omitempty"`
}

// ToolCatalog é o catálogo de uma integração com o estado da descoberta.
type ToolCatalog struct {
	IntegrationID   string            `json:"integration_id" format:"uuid"`
	IntegrationSlug string            `json:"integration_slug"`
	Transport       string            `json:"transport" enum:"http,stdio"`
	Stdio           bool              `json:"stdio" doc:"MCP stdio não passa pelo gateway: não há catálogo e o perfil só inclui ou exclui a integração inteira."`
	SyncedAt        *time.Time        `json:"synced_at,omitempty" doc:"Última descoberta bem-sucedida."`
	LastError       string            `json:"last_error,omitempty" doc:"Erro da última tentativa (sem segredos)."`
	ToolsCount      int               `json:"tools_count"`
	Tools           []IntegrationTool `json:"tools"`
}

// ToolCatalogRefresh é o resultado de uma descoberta.
type ToolCatalogRefresh struct {
	OK          bool     `json:"ok"`
	Error       string   `json:"error,omitempty" doc:"Falha de conexão ou recusa do servidor; o catálogo anterior segue valendo."`
	Tools       int      `json:"tools"`
	New         int      `json:"new"`
	Changed     int      `json:"changed"`
	Quarantined int      `json:"quarantined"`
	Removed     int      `json:"removed"`
	Skipped     []string `json:"skipped,omitempty" doc:"Nomes inválidos ou acima do teto de 500 tools."`
	Baseline    bool     `json:"baseline" doc:"Primeira descoberta: tudo entra ativo."`
}

// ToolRule é uma regra de perfil.
type ToolRule struct {
	ID              string    `json:"id" format:"uuid"`
	Target          string    `json:"target" enum:"integration,tool,risk"`
	IntegrationID   string    `json:"integration_id,omitempty" format:"uuid"`
	IntegrationSlug string    `json:"integration,omitempty"`
	Tool            string    `json:"tool,omitempty"`
	Risk            string    `json:"risk,omitempty" enum:"read,write,destructive,unknown"`
	Effect          string    `json:"effect" enum:"include,exclude" doc:"Excluir vence incluir."`
	Mode            string    `json:"mode" enum:"allow,ask" doc:"Na inclusão: allow chama direto; ask abre um gate tool antes de cada chamada nova."`
	CreatedAt       time.Time `json:"created_at"`
}

// ToolBinding é um vínculo de perfil.
type ToolBinding struct {
	ID         string    `json:"id" format:"uuid"`
	ProfileID  string    `json:"profile_id" format:"uuid"`
	Scope      string    `json:"scope" enum:"company,project,persona"`
	ProjectID  string    `json:"project_id,omitempty" format:"uuid"`
	PersonaID  string    `json:"persona_id,omitempty" format:"uuid"`
	TargetName string    `json:"target_name,omitempty" doc:"Nome do projeto ou da persona."`
	Priority   int       `json:"priority" doc:"No mesmo escopo, a maior vence; empatando, o vínculo mais novo."`
	CreatedAt  time.Time `json:"created_at"`
}

// ToolProfile é um perfil de ferramentas.
type ToolProfile struct {
	ID              string        `json:"id" format:"uuid"`
	Slug            string        `json:"slug" example:"seguro"`
	Name            string        `json:"name" example:"Seguro"`
	Description     string        `json:"description,omitempty"`
	DefaultAction   string        `json:"default_action" enum:"deny,allow" doc:"O que vale para a tool que nenhuma regra alcança."`
	RateLimitPerMin int           `json:"rate_limit_per_min" doc:"Chamadas repassadas por minuto por (persona ou gateway, integração)."`
	Rules           []ToolRule    `json:"rules"`
	Bindings        []ToolBinding `json:"bindings"`
	Gateways        int           `json:"gateways" doc:"Gateways nomeados que usam o perfil."`
	CreatedBy       ActorRef      `json:"created_by"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// ToolVerdict é a decisão de um perfil e de onde ela veio.
type ToolVerdict struct {
	Allowed  bool   `json:"allowed"`
	Mode     string `json:"mode,omitempty" enum:"allow,ask"`
	Source   string `json:"source" enum:"no_profile,default,include,exclude,tools,policy:persona,policy:company" doc:"no_profile: sem perfil (tudo permitido); default: ação padrão; include/exclude: uma regra (rule_id); tools: alguma tool da integração está visível; policy:*: uma política de tools (policy_id) venceu o perfil."`
	RuleID   string `json:"rule_id,omitempty" format:"uuid"`
	PolicyID string `json:"policy_id,omitempty" format:"uuid"`
}

// EffectiveTool é uma tool com a decisão para a persona.
type EffectiveTool struct {
	Name    string      `json:"name"`
	Risk    string      `json:"risk" enum:"read,write,destructive,unknown"`
	Status  string      `json:"status" enum:"active,quarantined,disabled,removed"`
	Visible bool        `json:"visible" doc:"Aparece no tools/list da persona."`
	Verdict ToolVerdict `json:"verdict"`
}

// EffectiveIntegration é uma integração MCP da persona com o que ela enxerga.
type EffectiveIntegration struct {
	IntegrationID string          `json:"integration_id" format:"uuid"`
	Slug          string          `json:"slug"`
	Name          string          `json:"name"`
	Transport     string          `json:"transport" enum:"http,stdio"`
	Verdict       ToolVerdict     `json:"verdict"`
	SyncedAt      *time.Time      `json:"synced_at,omitempty"`
	Tools         []EffectiveTool `json:"tools"`
}

// EffectiveTools é o que uma persona enxerga e por quê.
type EffectiveTools struct {
	PersonaID    string                 `json:"persona_id" format:"uuid"`
	ProjectID    string                 `json:"project_id,omitempty" format:"uuid"`
	Profile      string                 `json:"profile,omitempty" doc:"Slug do perfil que vale (ausente = sem perfil)."`
	Binding      *ToolBinding           `json:"binding,omitempty" doc:"O vínculo que escolheu o perfil."`
	Policies     []ToolPolicy           `json:"policies" doc:"Políticas de tools que valem para a persona (as dela e o padrão da empresa)."`
	Integrations []EffectiveIntegration `json:"integrations"`
}

// GatewayToken é um token de gateway (o valor só na criação).
type GatewayToken struct {
	ID         string     `json:"id" format:"uuid"`
	GatewayID  string     `json:"gateway_id" format:"uuid"`
	Label      string     `json:"label,omitempty"`
	Prefix     string     `json:"prefix" example:"hkgw_Ab12Cd" doc:"Início do token, para reconhecê-lo."`
	Actions    []string   `json:"actions" doc:"tools/list e/ou tools/call."`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	Token      string     `json:"token,omitempty" doc:"O token completo, SÓ na resposta da criação. Guarde-o: não é possível ler de novo."`
}

// ToolGateway é um gateway nomeado.
type ToolGateway struct {
	ID           string         `json:"id" format:"uuid"`
	Slug         string         `json:"slug" example:"minha-ide"`
	Name         string         `json:"name"`
	ProfileID    string         `json:"profile_id" format:"uuid"`
	Profile      string         `json:"profile" doc:"Slug do perfil."`
	Enabled      bool           `json:"enabled"`
	ActiveTokens int            `json:"active_tokens"`
	LastUsedAt   *time.Time     `json:"last_used_at,omitempty"`
	Endpoint     string         `json:"endpoint" example:"/mcp/gw/acme/minha-ide" doc:"Caminho MCP (streamable http) relativo à URL pública do Hanko; use Authorization: Bearer hkgw_…"`
	Tokens       []GatewayToken `json:"tokens,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// ToolInvocation é uma chamada auditada (os argumentos nunca são guardados).
type ToolInvocation struct {
	ID            string    `json:"id" format:"uuid"`
	RunID         string    `json:"run_id,omitempty" format:"uuid"`
	GatewayID     string    `json:"gateway_id,omitempty" format:"uuid"`
	Gateway       string    `json:"gateway,omitempty"`
	PersonaID     string    `json:"persona_id,omitempty" format:"uuid"`
	Persona       string    `json:"persona,omitempty"`
	IntegrationID string    `json:"integration_id,omitempty" format:"uuid"`
	Integration   string    `json:"integration"`
	Tool          string    `json:"tool"`
	ArgsSHA256    string    `json:"args_sha256" doc:"sha256 de integração + tool + argumentos canônicos."`
	ArgsBytes     int       `json:"args_bytes"`
	Decision      string    `json:"decision" enum:"allow,deny,ask,quarantined,error"`
	GateID        string    `json:"gate_id,omitempty" format:"uuid"`
	Status        string    `json:"status" enum:"ok,failed,blocked,pending"`
	LatencyMs     int       `json:"latency_ms" doc:"Duração da chamada ao servidor."`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Source        string    `json:"source" enum:"run,gateway,test,hook,acp,plugin" doc:"Por onde a chamada entrou."`
	Actor         *ActorRef `json:"actor,omitempty" doc:"Quem fez: a persona, o humano que testou ou o sistema (gateway nomeado)."`
	ResultPreview string    `json:"result_preview,omitempty" doc:"Início do resultado, sem segredos."`
	PolicySource  string    `json:"policy_source,omitempty" doc:"De onde veio a decisão: include, exclude, default, policy:persona ou policy:company."`
}

// ReviewResult é o resultado da revisão em lote da quarentena.
type ReviewResult struct {
	Released int `json:"released" doc:"Quantas tools saíram da quarentena."`
}

// ---------- conversões ----------

func optIDStr(id *domain.ID) string { return idPtr(id) }

func toolOut(t domain.IntegrationTool) IntegrationTool {
	o := IntegrationTool{ID: idStr(t.ID), IntegrationID: idStr(t.IntegrationID), IntegrationSlug: t.IntegrationSlug, Name: t.Name, Title: t.Title,
		Description: t.Description, InputSchema: map[string]any{}, SchemaSHA256: t.SchemaSHA256, Risk: string(t.Risk), Status: string(t.Status),
		FirstSeenAt: t.FirstSeenAt, LastSeenAt: t.LastSeenAt, ReviewedAt: t.ReviewedAt,
		Annotations: ToolAnnotations{Title: t.Annotations.Title, ReadOnlyHint: t.Annotations.ReadOnlyHint, DestructiveHint: t.Annotations.DestructiveHint,
			IdempotentHint: t.Annotations.IdempotentHint, OpenWorldHint: t.Annotations.OpenWorldHint}}
	_ = json.Unmarshal(t.InputSchema, &o.InputSchema)
	if t.ReviewedBy != nil {
		a := actorRef(*t.ReviewedBy)
		o.ReviewedBy = &a
	}
	return o
}

func catalogOut(v service.IntegrationToolsView) ToolCatalog {
	o := ToolCatalog{IntegrationID: idStr(v.Integration.ID), IntegrationSlug: v.Integration.Slug, Transport: v.Integration.Config.Transport,
		Stdio: v.Stdio, SyncedAt: v.Catalog.SyncedAt, LastError: v.Catalog.LastError, ToolsCount: v.Catalog.ToolsCount, Tools: []IntegrationTool{}}
	for _, t := range v.Tools {
		o.Tools = append(o.Tools, toolOut(t))
	}
	return o
}

func ruleOut(r domain.ToolRule) ToolRule {
	return ToolRule{ID: idStr(r.ID), Target: string(r.Target), IntegrationID: optIDStr(r.IntegrationID), IntegrationSlug: r.IntegrationSlug,
		Tool: r.ToolName, Risk: string(r.Risk), Effect: string(r.Effect), Mode: string(r.Mode), CreatedAt: r.CreatedAt}
}

func bindingOut(b domain.ToolBinding) ToolBinding {
	return ToolBinding{ID: idStr(b.ID), ProfileID: idStr(b.ProfileID), Scope: string(b.Scope), ProjectID: optIDStr(b.ProjectID),
		PersonaID: optIDStr(b.PersonaID), TargetName: b.TargetName, Priority: b.Priority, CreatedAt: b.CreatedAt}
}

func profileOut(p service.ToolProfileView) ToolProfile {
	o := ToolProfile{ID: idStr(p.ID), Slug: p.Slug, Name: p.Name, Description: p.Description, DefaultAction: string(p.DefaultAction),
		RateLimitPerMin: p.RateLimitPerMin, Rules: []ToolRule{}, Bindings: []ToolBinding{}, Gateways: p.Gateways,
		CreatedBy: actorRef(p.CreatedBy), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	for _, r := range p.Rules {
		o.Rules = append(o.Rules, ruleOut(r))
	}
	for _, b := range p.Bindings {
		o.Bindings = append(o.Bindings, bindingOut(b))
	}
	return o
}

func verdictOut(v domain.ToolVerdict) ToolVerdict {
	o := ToolVerdict{Allowed: v.Allowed, Mode: string(v.Mode), Source: v.Source}
	if v.Rule != nil {
		o.RuleID = idStr(v.Rule.ID)
	}
	if v.Policy != nil {
		o.PolicyID = idStr(v.Policy.ID)
	}
	return o
}

func effectiveOut(v service.EffectiveTools) EffectiveTools {
	o := EffectiveTools{PersonaID: idStr(v.PersonaID), ProjectID: optIDStr(v.ProjectID), Policies: []ToolPolicy{}, Integrations: []EffectiveIntegration{}}
	for _, p := range v.Policies {
		o.Policies = append(o.Policies, policyOut(p))
	}
	if v.Profile != nil {
		o.Profile = v.Profile.Slug
	}
	if v.Binding != nil {
		b := bindingOut(*v.Binding)
		o.Binding = &b
	}
	for _, ei := range v.Integrations {
		e := EffectiveIntegration{IntegrationID: idStr(ei.IntegrationID), Slug: ei.Slug, Name: ei.Name, Transport: ei.Transport,
			Verdict: verdictOut(ei.Verdict), SyncedAt: ei.Catalog.SyncedAt, Tools: []EffectiveTool{}}
		for _, t := range ei.Tools {
			e.Tools = append(e.Tools, EffectiveTool{Name: t.Tool.Name, Risk: string(t.Tool.Risk), Status: string(t.Tool.Status), Visible: t.Visible,
				Verdict: verdictOut(t.Verdict)})
		}
		o.Integrations = append(o.Integrations, e)
	}
	return o
}

func gatewayTokenOut(t domain.ToolGatewayToken) GatewayToken {
	return GatewayToken{ID: idStr(t.ID), GatewayID: idStr(t.GatewayID), Label: t.Label, Prefix: t.Prefix, Actions: t.Actions,
		ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt}
}

func gatewayOut(companySlug string, g domain.ToolGateway, toks []domain.ToolGatewayToken) ToolGateway {
	o := ToolGateway{ID: idStr(g.ID), Slug: g.Slug, Name: g.Name, ProfileID: idStr(g.ProfileID), Profile: g.ProfileSlug, Enabled: g.Enabled,
		ActiveTokens: g.ActiveTokens, LastUsedAt: g.LastUsedAt, Endpoint: "/mcp/gw/" + companySlug + "/" + g.Slug,
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
	for _, t := range toks {
		o.Tokens = append(o.Tokens, gatewayTokenOut(t))
	}
	return o
}

func invocationOut(v domain.ToolInvocation) ToolInvocation {
	return ToolInvocation{ID: idStr(v.ID), RunID: optIDStr(v.RunID), GatewayID: optIDStr(v.GatewayID), Gateway: v.GatewaySlug,
		PersonaID: optIDStr(v.PersonaID), Persona: v.PersonaName, IntegrationID: optIDStr(v.IntegrationID), Integration: v.IntegrationSlug,
		Tool: v.Tool, ArgsSHA256: v.ArgsSHA256, ArgsBytes: v.ArgsBytes, Decision: string(v.Decision), GateID: optIDStr(v.GateID),
		Status: v.Status, LatencyMs: v.LatencyMs, Error: v.Error, CreatedAt: v.CreatedAt, Source: string(v.Source), ResultPreview: v.ResultPreview,
		PolicySource: v.PolicySource, Actor: actorRefPtr(v.Actor)}
}

func actorRefPtr(a *domain.Actor) *ActorRef {
	if a == nil {
		return nil
	}
	r := actorRef(*a)
	return &r
}

// ---------- entradas ----------

type IntegrationToolPath struct {
	IntegrationPath
	Tool string `path:"tool" doc:"Nome da tool." example:"create_issue"`
}

type ToolProfilePath struct {
	CompanyPath
	Profile string `path:"profile" doc:"Id ou slug do perfil." example:"seguro"`
}

type ToolProfileRulePath struct {
	ToolProfilePath
	Rule string `path:"rule" format:"uuid" doc:"Id da regra."`
}

type ToolProfileBindingPath struct {
	ToolProfilePath
	Binding string `path:"binding" format:"uuid" doc:"Id do vínculo."`
}

type ToolProfileIntegrationPath struct {
	ToolProfilePath
	Integration string `path:"integration" doc:"Id ou slug da integração MCP." example:"docs"`
}

type ToolGatewayPath struct {
	CompanyPath
	Gateway string `path:"gateway" doc:"Id ou slug do gateway." example:"minha-ide"`
}

type GatewayTokenPath struct {
	ToolGatewayPath
	Token string `path:"token" format:"uuid" doc:"Id do token."`
}

type setToolStatusIn struct {
	IntegrationToolPath
	Body struct {
		Status string `json:"status" enum:"active,disabled" doc:"active libera (inclusive da quarentena); disabled desliga."`
	}
}

type reviewToolsIn struct {
	IntegrationPath
	Body struct {
		Tools []string `json:"tools,omitempty" doc:"Tools a liberar da quarentena; vazio libera todas da integração."`
	}
}

type createToolProfileIn struct {
	CompanyPath
	Body struct {
		Name            string `json:"name" minLength:"1" maxLength:"120" example:"Seguro"`
		Slug            string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Derivado do nome quando omitido."`
		Description     string `json:"description,omitempty" maxLength:"500"`
		DefaultAction   string `json:"default_action,omitempty" enum:"deny,allow" doc:"Padrão deny."`
		RateLimitPerMin int    `json:"rate_limit_per_min,omitempty" minimum:"0" maximum:"10000" doc:"Padrão 120."`
	}
}

type updateToolProfileIn struct {
	ToolProfilePath
	Body struct {
		Name            *string `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Description     *string `json:"description,omitempty" maxLength:"500"`
		DefaultAction   *string `json:"default_action,omitempty" enum:"deny,allow"`
		RateLimitPerMin *int    `json:"rate_limit_per_min,omitempty" minimum:"1" maximum:"10000"`
	}
}

type addToolRuleIn struct {
	ToolProfilePath
	Body struct {
		Target      string `json:"target" enum:"integration,tool,risk"`
		Integration string `json:"integration,omitempty" doc:"Id ou slug da integração MCP (obrigatório para integration e tool; opcional para risk)."`
		Tool        string `json:"tool,omitempty" doc:"Nome da tool (target tool)."`
		Risk        string `json:"risk,omitempty" enum:"read,write,destructive,unknown" doc:"Nível de risco (target risk)."`
		Effect      string `json:"effect" enum:"include,exclude"`
		Mode        string `json:"mode,omitempty" enum:"allow,ask" doc:"Padrão allow; só vale na inclusão. MCP stdio não aceita ask."`
	}
}

type setToolChoicesIn struct {
	ToolProfileIntegrationPath
	Body struct {
		Group string            `json:"group" enum:"default,off,ask,allow" doc:"A integração inteira: default segue a ação padrão; off exclui tudo (excluir vence)."`
		Tools map[string]string `json:"tools,omitempty" doc:"Por tool: default, off, ask ou allow."`
	}
}

type bindToolProfileIn struct {
	ToolProfilePath
	Body struct {
		Scope    string `json:"scope" enum:"company,project,persona"`
		Project  string `json:"project,omitempty" doc:"Id ou slug do projeto (scope project)."`
		Persona  string `json:"persona,omitempty" doc:"Id ou slug da persona (scope persona)."`
		Priority int    `json:"priority,omitempty" minimum:"-1000" maximum:"1000"`
	}
}

type effectiveToolsIn struct {
	CompanyPath
	Persona string `query:"persona" required:"true" doc:"Id ou slug da persona."`
	Project string `query:"project" doc:"Id ou slug do projeto (o perfil do projeto vale para as tasks dele)."`
}

type createToolGatewayIn struct {
	CompanyPath
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"120" example:"Minha IDE"`
		Slug    string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$"`
		Profile string `json:"profile" doc:"Id ou slug do perfil de ferramentas do gateway."`
		Enabled *bool  `json:"enabled,omitempty" doc:"Padrão: ligado."`
	}
}

type updateToolGatewayIn struct {
	ToolGatewayPath
	Body struct {
		Name    *string `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Profile *string `json:"profile,omitempty"`
		Enabled *bool   `json:"enabled,omitempty"`
	}
}

type createGatewayTokenIn struct {
	ToolGatewayPath
	Body struct {
		Label         string   `json:"label,omitempty" maxLength:"120"`
		Actions       []string `json:"actions,omitempty" doc:"tools/list e/ou tools/call; padrão as duas."`
		ExpiresInDays int      `json:"expires_in_days,omitempty" minimum:"0" maximum:"366" doc:"Padrão 90 dias."`
	}
}

type listToolInvocationsIn struct {
	CompanyPath
	Integration string `query:"integration" doc:"Id ou slug da integração."`
	Persona     string `query:"persona" doc:"Id ou slug da persona."`
	Gateway     string `query:"gateway" doc:"Id ou slug do gateway nomeado."`
	Run         string `query:"run" doc:"Id da execução."`
	Decision    string `query:"decision" enum:"allow,deny,ask,quarantined,error"`
	Source      string `query:"source" enum:"run,gateway,test,hook,acp,plugin" doc:"Por onde a chamada entrou."`
	Tool        string `query:"tool"`
	Since       string `query:"since" doc:"RFC 3339; só chamadas a partir daí."`
	Page
}

func choice(c string) string {
	if c == "default" {
		return service.ToolChoiceDefault
	}
	return c
}

func (s *server) integrationID(ctx context.Context, cid domain.ID, ref string) (domain.ID, error) {
	ib, err := s.integrations()
	if err != nil {
		return domain.ID{}, err
	}
	v, err := ib.GetIntegration(ctx, cid, ref)
	if err != nil {
		return domain.ID{}, s.fail(ctx, err)
	}
	return v.ID, nil
}

func (s *server) registerTools(api huma.API) {
	// prelude devolve o backend e o escopo; write recusa run tokens.
	prelude := func(ctx context.Context, company string, write bool) (toolsBackend, scope, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return nil, scope{}, err
			}
		}
		tb, err := s.tools()
		if err != nil {
			return nil, scope{}, err
		}
		sc, err := s.scope(ctx, company)
		return tb, sc, err
	}
	profileID := func(ctx context.Context, tb toolsBackend, cid domain.ID, ref string) (domain.ID, error) {
		p, err := tb.GetToolProfile(ctx, cid, ref)
		if err != nil {
			return domain.ID{}, s.fail(ctx, err)
		}
		return p.ID, nil
	}
	gatewayID := func(ctx context.Context, tb toolsBackend, cid domain.ID, ref string) (domain.ID, error) {
		g, err := tb.GetToolGateway(ctx, cid, ref)
		if err != nil {
			return domain.ID{}, s.fail(ctx, err)
		}
		return g.ID, nil
	}

	// ---- catálogo ----
	huma.Register(api, op("list-integration-tools", http.MethodGet, "/companies/{company}/integrations/{integration}/tools", "tools",
		"Catálogo de tools de uma integração MCP (risco, estado, quarentena)"),
		func(ctx context.Context, in *IntegrationPath) (*ItemOut[ToolCatalog], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, err
			}
			v, err := tb.ListIntegrationTools(ctx, sc.Company.ID, iid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolCatalog]{Body: catalogOut(v)}, nil
		})

	huma.Register(api, op("refresh-integration-tools", http.MethodPost, "/companies/{company}/integrations/{integration}/tools/refresh", "tools",
		"Redescobre o catálogo (tools/list pelo servidor, com a proteção SSRF)"),
		func(ctx context.Context, in *IntegrationPath) (*ItemOut[ToolCatalogRefresh], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, err
			}
			r, err := tb.RefreshIntegrationTools(ctx, sc.Actor, sc.Company.ID, iid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolCatalogRefresh]{Body: ToolCatalogRefresh{OK: r.OK, Error: r.Error, Tools: r.Tools, New: r.New, Changed: r.Changed,
				Quarantined: r.Quarantined, Removed: r.Removed, Skipped: r.Skipped, Baseline: r.Baseline}}, nil
		})

	huma.Register(api, op("set-integration-tool-status", http.MethodPatch, "/companies/{company}/integrations/{integration}/tools/{tool}", "tools",
		"Libera (active) ou desliga (disabled) uma tool do catálogo"),
		func(ctx context.Context, in *setToolStatusIn) (*ItemOut[IntegrationTool], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, err
			}
			t, err := tb.SetToolStatus(ctx, sc.Actor, sc.Company.ID, iid, in.Tool, domain.ToolStatus(in.Body.Status))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[IntegrationTool]{Body: toolOut(t)}, nil
		})

	huma.Register(api, op("review-integration-tools", http.MethodPost, "/companies/{company}/integrations/{integration}/tools/review", "tools",
		"Libera da quarentena as tools revisadas (todas, se nenhuma for listada)"),
		func(ctx context.Context, in *reviewToolsIn) (*ItemOut[ReviewResult], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, err
			}
			n, err := tb.ReviewQuarantinedTools(ctx, sc.Actor, sc.Company.ID, iid, in.Body.Tools)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ReviewResult]{Body: ReviewResult{Released: n}}, nil
		})

	// ---- perfis ----
	huma.Register(api, created(op("create-tool-profile", http.MethodPost, "/companies/{company}/tool-profiles", "tools", "Cria um perfil de ferramentas")),
		func(ctx context.Context, in *createToolProfileIn) (*ItemOut[ToolProfile], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			b := in.Body
			p, err := tb.CreateToolProfile(ctx, sc.Actor, sc.Company.ID, service.CreateToolProfileInput{Name: b.Name, Slug: b.Slug,
				Description: b.Description, DefaultAction: domain.ToolDefaultAction(b.DefaultAction), RateLimitPerMin: b.RateLimitPerMin})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolProfile]{Body: profileOut(p)}, nil
		})

	huma.Register(api, op("list-tool-profiles", http.MethodGet, "/companies/{company}/tool-profiles", "tools", "Lista os perfis de ferramentas"),
		func(ctx context.Context, in *listIntegrationsIn) (*ListOut[ToolProfile], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			list, err := tb.ListToolProfiles(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, profileOut), nil
		})

	huma.Register(api, op("effective-tools", http.MethodGet, "/companies/{company}/tool-profiles/effective", "tools",
		"O que uma persona enxerga das integrações MCP e de onde vem cada permissão"),
		func(ctx context.Context, in *effectiveToolsIn) (*ItemOut[EffectiveTools], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if pid == nil {
				return nil, problem(http.StatusUnprocessableEntity, "persona é obrigatória")
			}
			proj, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := tb.EffectiveToolsFor(ctx, sc.Company.ID, *pid, proj)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[EffectiveTools]{Body: effectiveOut(v)}, nil
		})

	huma.Register(api, op("get-tool-profile", http.MethodGet, "/companies/{company}/tool-profiles/{profile}", "tools", "Obtém um perfil com regras e vínculos"),
		func(ctx context.Context, in *ToolProfilePath) (*ItemOut[ToolProfile], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			p, err := tb.GetToolProfile(ctx, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolProfile]{Body: profileOut(p)}, nil
		})

	huma.Register(api, op("update-tool-profile", http.MethodPatch, "/companies/{company}/tool-profiles/{profile}", "tools", "Atualiza um perfil"),
		func(ctx context.Context, in *updateToolProfileIn) (*ItemOut[ToolProfile], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			b := in.Body
			up := service.UpdateToolProfileInput{Name: b.Name, Description: b.Description, RateLimitPerMin: b.RateLimitPerMin}
			if b.DefaultAction != nil {
				d := domain.ToolDefaultAction(*b.DefaultAction)
				up.DefaultAction = &d
			}
			p, err := tb.UpdateToolProfile(ctx, sc.Actor, sc.Company.ID, id, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolProfile]{Body: profileOut(p)}, nil
		})

	huma.Register(api, noContent(op("delete-tool-profile", http.MethodDelete, "/companies/{company}/tool-profiles/{profile}", "tools",
		"Apaga um perfil (não pode estar em uso por um gateway)")),
		func(ctx context.Context, in *ToolProfilePath) (*struct{}, error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			if err := tb.DeleteToolProfile(ctx, sc.Actor, sc.Company.ID, id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, created(op("add-tool-profile-rule", http.MethodPost, "/companies/{company}/tool-profiles/{profile}/rules", "tools",
		"Acrescenta (ou troca) uma regra do perfil")),
		func(ctx context.Context, in *addToolRuleIn) (*ItemOut[ToolRule], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			b := in.Body
			r := service.ToolRuleInput{Target: domain.ToolRuleTarget(b.Target), ToolName: b.Tool, Risk: domain.ToolRisk(b.Risk),
				Effect: domain.ToolRuleEffect(b.Effect), Mode: domain.ToolMode(b.Mode)}
			if b.Integration != "" {
				iid, err := s.integrationID(ctx, sc.Company.ID, b.Integration)
				if err != nil {
					return nil, err
				}
				r.IntegrationID = &iid
			}
			rule, err := tb.AddToolProfileRule(ctx, sc.Actor, sc.Company.ID, id, r)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolRule]{Body: ruleOut(rule)}, nil
		})

	huma.Register(api, noContent(op("delete-tool-profile-rule", http.MethodDelete, "/companies/{company}/tool-profiles/{profile}/rules/{rule}", "tools",
		"Tira uma regra do perfil")),
		func(ctx context.Context, in *ToolProfileRulePath) (*struct{}, error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			rid, err := domain.ParseID(in.Rule)
			if err != nil {
				return nil, problem(http.StatusNotFound, "regra não encontrada")
			}
			if err := tb.DeleteToolProfileRule(ctx, sc.Actor, sc.Company.ID, id, rid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("set-tool-profile-integration", http.MethodPut, "/companies/{company}/tool-profiles/{profile}/integrations/{integration}", "tools",
		"Reescreve as escolhas do perfil para uma integração (a integração inteira e por tool)"),
		func(ctx context.Context, in *setToolChoicesIn) (*ItemOut[ToolProfile], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, err
			}
			tools := map[string]string{}
			for k, v := range in.Body.Tools {
				tools[k] = choice(v)
			}
			p, err := tb.SetIntegrationToolChoices(ctx, sc.Actor, sc.Company.ID, id, service.IntegrationToolChoices{IntegrationID: iid,
				Group: choice(in.Body.Group), Tools: tools})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolProfile]{Body: profileOut(p)}, nil
		})

	huma.Register(api, created(op("bind-tool-profile", http.MethodPost, "/companies/{company}/tool-profiles/{profile}/bindings", "tools",
		"Liga o perfil à empresa, a um projeto ou a uma persona")),
		func(ctx context.Context, in *bindToolProfileIn) (*ItemOut[ToolBinding], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			b := in.Body
			bi := service.ToolBindingInput{Scope: domain.ToolBindingScope(b.Scope), Priority: b.Priority}
			if bi.ProjectID, err = s.projectRef(ctx, sc.Company.ID, b.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			if bi.PersonaID, err = s.personaRef(ctx, sc.Company.ID, b.Persona); err != nil {
				return nil, s.fail(ctx, err)
			}
			out, err := tb.BindToolProfile(ctx, sc.Actor, sc.Company.ID, id, bi)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolBinding]{Body: bindingOut(out)}, nil
		})

	huma.Register(api, noContent(op("unbind-tool-profile", http.MethodDelete, "/companies/{company}/tool-profiles/{profile}/bindings/{binding}", "tools",
		"Tira um vínculo do perfil")),
		func(ctx context.Context, in *ToolProfileBindingPath) (*struct{}, error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := profileID(ctx, tb, sc.Company.ID, in.Profile)
			if err != nil {
				return nil, err
			}
			bid, err := domain.ParseID(in.Binding)
			if err != nil {
				return nil, problem(http.StatusNotFound, "vínculo não encontrado")
			}
			if err := tb.UnbindToolProfile(ctx, sc.Actor, sc.Company.ID, id, bid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	// ---- gateways nomeados ----
	huma.Register(api, created(op("create-tool-gateway", http.MethodPost, "/companies/{company}/tool-gateways", "tools",
		"Cria um gateway nomeado (endpoint MCP para clientes de fora)")),
		func(ctx context.Context, in *createToolGatewayIn) (*ItemOut[ToolGateway], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			pid, err := profileID(ctx, tb, sc.Company.ID, in.Body.Profile)
			if err != nil {
				return nil, err
			}
			g, err := tb.CreateToolGateway(ctx, sc.Actor, sc.Company.ID, service.CreateToolGatewayInput{Name: in.Body.Name, Slug: in.Body.Slug,
				ProfileID: pid, Enabled: in.Body.Enabled})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolGateway]{Body: gatewayOut(sc.Company.Slug, g.ToolGateway, g.Tokens)}, nil
		})

	huma.Register(api, op("list-tool-gateways", http.MethodGet, "/companies/{company}/tool-gateways", "tools", "Lista os gateways nomeados"),
		func(ctx context.Context, in *listIntegrationsIn) (*ListOut[ToolGateway], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			list, err := tb.ListToolGateways(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, func(g domain.ToolGateway) ToolGateway { return gatewayOut(sc.Company.Slug, g, nil) }), nil
		})

	huma.Register(api, op("get-tool-gateway", http.MethodGet, "/companies/{company}/tool-gateways/{gateway}", "tools", "Obtém um gateway com os tokens (sem valores)"),
		func(ctx context.Context, in *ToolGatewayPath) (*ItemOut[ToolGateway], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			g, err := tb.GetToolGateway(ctx, sc.Company.ID, in.Gateway)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := gatewayOut(sc.Company.Slug, g.ToolGateway, g.Tokens)
			if out.Tokens == nil {
				out.Tokens = []GatewayToken{}
			}
			return &ItemOut[ToolGateway]{Body: out}, nil
		})

	huma.Register(api, op("update-tool-gateway", http.MethodPatch, "/companies/{company}/tool-gateways/{gateway}", "tools", "Atualiza um gateway (nome, perfil, ligado)"),
		func(ctx context.Context, in *updateToolGatewayIn) (*ItemOut[ToolGateway], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			gid, err := gatewayID(ctx, tb, sc.Company.ID, in.Gateway)
			if err != nil {
				return nil, err
			}
			up := service.UpdateToolGatewayInput{Name: in.Body.Name, Enabled: in.Body.Enabled}
			if in.Body.Profile != nil {
				pid, err := profileID(ctx, tb, sc.Company.ID, *in.Body.Profile)
				if err != nil {
					return nil, err
				}
				up.ProfileID = &pid
			}
			g, err := tb.UpdateToolGateway(ctx, sc.Actor, sc.Company.ID, gid, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolGateway]{Body: gatewayOut(sc.Company.Slug, g.ToolGateway, g.Tokens)}, nil
		})

	huma.Register(api, noContent(op("delete-tool-gateway", http.MethodDelete, "/companies/{company}/tool-gateways/{gateway}", "tools", "Apaga um gateway e seus tokens")),
		func(ctx context.Context, in *ToolGatewayPath) (*struct{}, error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			gid, err := gatewayID(ctx, tb, sc.Company.ID, in.Gateway)
			if err != nil {
				return nil, err
			}
			if err := tb.DeleteToolGateway(ctx, sc.Actor, sc.Company.ID, gid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, created(op("create-gateway-token", http.MethodPost, "/companies/{company}/tool-gateways/{gateway}/tokens", "tools",
		"Gera um token hkgw_ para o gateway (o valor só aparece nesta resposta)")),
		func(ctx context.Context, in *createGatewayTokenIn) (*ItemOut[GatewayToken], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			gid, err := gatewayID(ctx, tb, sc.Company.ID, in.Gateway)
			if err != nil {
				return nil, err
			}
			t, tok, err := tb.CreateGatewayToken(ctx, sc.Actor, sc.Company.ID, gid, service.CreateGatewayTokenInput{Label: in.Body.Label,
				Actions: in.Body.Actions, ExpiresIn: time.Duration(in.Body.ExpiresInDays) * 24 * time.Hour})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := gatewayTokenOut(t)
			out.Token = tok
			return &ItemOut[GatewayToken]{Body: out}, nil
		})

	huma.Register(api, op("revoke-gateway-token", http.MethodDelete, "/companies/{company}/tool-gateways/{gateway}/tokens/{token}", "tools",
		"Revoga um token do gateway"),
		func(ctx context.Context, in *GatewayTokenPath) (*ItemOut[GatewayToken], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			gid, err := gatewayID(ctx, tb, sc.Company.ID, in.Gateway)
			if err != nil {
				return nil, err
			}
			tid, err := domain.ParseID(in.Token)
			if err != nil {
				return nil, problem(http.StatusNotFound, "token não encontrado")
			}
			t, err := tb.RevokeGatewayToken(ctx, sc.Actor, sc.Company.ID, gid, tid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[GatewayToken]{Body: gatewayTokenOut(t)}, nil
		})

	// ---- auditoria ----
	huma.Register(api, op("list-tool-invocations", http.MethodGet, "/companies/{company}/tool-invocations", "tools",
		"Auditoria das chamadas de tool pelo gateway (só admin; sem os argumentos)"),
		func(ctx context.Context, in *listToolInvocationsIn) (*ListOut[ToolInvocation], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			f := service.ToolInvocationFilter{Decision: domain.ToolDecision(in.Decision), Source: domain.ToolInvocationSource(in.Source), Tool: in.Tool, Limit: in.Limit, Offset: in.Offset}
			if in.Integration != "" {
				iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
				if err != nil {
					return nil, err
				}
				f.IntegrationID = &iid
			}
			if f.PersonaID, err = s.personaRef(ctx, sc.Company.ID, in.Persona); err != nil {
				return nil, s.fail(ctx, err)
			}
			if in.Gateway != "" {
				gid, err := gatewayID(ctx, tb, sc.Company.ID, in.Gateway)
				if err != nil {
					return nil, err
				}
				f.GatewayID = &gid
			}
			if in.Run != "" {
				rid, err := domain.ParseID(in.Run)
				if err != nil {
					return nil, problem(http.StatusUnprocessableEntity, "run: use o id da execução")
				}
				f.RunID = &rid
			}
			if in.Since != "" {
				t, err := time.Parse(time.RFC3339, in.Since)
				if err != nil {
					return nil, problem(http.StatusUnprocessableEntity, "since: use RFC 3339")
				}
				f.Since = &t
			}
			list, err := tb.ListToolInvocations(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, in.Page, invocationOut), nil
		})
}
