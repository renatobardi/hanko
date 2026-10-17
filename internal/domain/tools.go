package domain

import (
	"encoding/json"
	"slices"
	"time"
)

// Apps, gateway de tools e perfis de ferramentas (ADR-0036). O "app" é a integração MCP (Integration kind mcp);
// estes tipos descrevem o catálogo das tools que ela expõe, os perfis que decidem o que cada persona (ou
// gateway nomeado) pode chamar e a auditoria das chamadas.

// ToolRisk é o risco derivado das anotações que o servidor MCP declara para a tool.
type ToolRisk string

const (
	ToolRiskRead        ToolRisk = "read"        // readOnlyHint: só lê
	ToolRiskWrite       ToolRisk = "write"       // escreve, e declara que não destrói (destructiveHint=false)
	ToolRiskDestructive ToolRisk = "destructive" // escreve e pode destruir (destructiveHint=true)
	ToolRiskUnknown     ToolRisk = "unknown"     // não declarou anotações
)

// ToolRisks lista os riscos na ordem de exibição.
var ToolRisks = []ToolRisk{ToolRiskRead, ToolRiskWrite, ToolRiskDestructive, ToolRiskUnknown}

func (r ToolRisk) Valid() bool { return slices.Contains(ToolRisks, r) }

// Writes informa se a tool pode mudar algo fora do Hanko (tudo que não declarou que só lê).
func (r ToolRisk) Writes() bool { return r != ToolRiskRead }

// ToolAnnotations são as dicas do protocolo MCP sobre o comportamento de uma tool. Ponteiro nil = não declarada.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// Declared informa se alguma dica de comportamento foi declarada.
func (a ToolAnnotations) Declared() bool {
	return a.ReadOnlyHint != nil || a.DestructiveHint != nil || a.IdempotentHint != nil
}

// DeriveToolRisk deriva o risco das anotações: readOnlyHint=true é leitura; destructiveHint=false é escrita;
// destructiveHint=true (ou readOnlyHint=false sem destructiveHint, que pelo protocolo vale como destrutiva) é
// destrutiva; nenhuma dica é desconhecido.
func DeriveToolRisk(a ToolAnnotations) ToolRisk {
	switch {
	case a.ReadOnlyHint != nil && *a.ReadOnlyHint:
		return ToolRiskRead
	case a.DestructiveHint != nil && !*a.DestructiveHint:
		return ToolRiskWrite
	case a.DestructiveHint != nil && *a.DestructiveHint, a.ReadOnlyHint != nil:
		return ToolRiskDestructive
	}
	return ToolRiskUnknown
}

// ToolStatus é o estado de uma tool no catálogo.
type ToolStatus string

const (
	ToolActive      ToolStatus = "active"      // pode ser oferecida (sujeita ao perfil)
	ToolQuarantined ToolStatus = "quarantined" // nova ou mudou e escreve: espera a revisão de um admin
	ToolDisabled    ToolStatus = "disabled"    // desligada por um admin
	ToolRemoved     ToolStatus = "removed"     // o servidor deixou de expor
)

func (s ToolStatus) Valid() bool {
	return s == ToolActive || s == ToolQuarantined || s == ToolDisabled || s == ToolRemoved
}

// IntegrationTool é uma tool do catálogo de uma integração MCP.
type IntegrationTool struct {
	ID              ID              `json:"id"`
	CompanyID       ID              `json:"company_id"`
	IntegrationID   ID              `json:"integration_id"`
	IntegrationSlug string          `json:"integration_slug,omitempty"`
	Name            string          `json:"name"`
	Title           string          `json:"title,omitempty"`
	Description     string          `json:"description,omitempty"`
	InputSchema     json.RawMessage `json:"input_schema"`
	Annotations     ToolAnnotations `json:"annotations"`
	SchemaSHA256    string          `json:"schema_sha256"`
	Risk            ToolRisk        `json:"risk"`
	Status          ToolStatus      `json:"status"`
	FirstSeenAt     time.Time       `json:"first_seen_at"`
	LastSeenAt      time.Time       `json:"last_seen_at"`
	ReviewedBy      *Actor          `json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time      `json:"reviewed_at,omitempty"`
}

// ToolCatalogState é o estado da descoberta do catálogo de uma integração.
type ToolCatalogState struct {
	IntegrationID ID         `json:"integration_id"`
	SyncedAt      *time.Time `json:"synced_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	ToolsCount    int        `json:"tools_count"`
}

// ToolDefaultAction é o que um perfil faz com a tool que nenhuma regra alcança.
type ToolDefaultAction string

const (
	ToolDefaultDeny  ToolDefaultAction = "deny"
	ToolDefaultAllow ToolDefaultAction = "allow"
)

func (a ToolDefaultAction) Valid() bool { return a == ToolDefaultDeny || a == ToolDefaultAllow }

// ToolRuleTarget é o que uma regra alcança.
type ToolRuleTarget string

const (
	ToolTargetIntegration ToolRuleTarget = "integration" // a integração inteira
	ToolTargetTool        ToolRuleTarget = "tool"        // uma tool de uma integração
	ToolTargetRisk        ToolRuleTarget = "risk"        // um nível de risco (de todas as integrações ou de uma)
)

func (t ToolRuleTarget) Valid() bool {
	return t == ToolTargetIntegration || t == ToolTargetTool || t == ToolTargetRisk
}

// ToolRuleEffect: incluir ou excluir. Excluir vence incluir.
type ToolRuleEffect string

const (
	ToolInclude ToolRuleEffect = "include"
	ToolExclude ToolRuleEffect = "exclude"
)

func (e ToolRuleEffect) Valid() bool { return e == ToolInclude || e == ToolExclude }

// ToolMode é como uma tool incluída pode ser chamada.
type ToolMode string

const (
	ToolModeAllow ToolMode = "allow" // chama direto
	ToolModeAsk   ToolMode = "ask"   // pergunta antes: abre um gate tool e só passa depois da aprovação
)

func (m ToolMode) Valid() bool { return m == ToolModeAllow || m == ToolModeAsk }

// ToolRule é uma regra de um perfil.
type ToolRule struct {
	ID              ID             `json:"id"`
	Target          ToolRuleTarget `json:"target"`
	IntegrationID   *ID            `json:"integration_id,omitempty"`
	IntegrationSlug string         `json:"integration_slug,omitempty"`
	ToolName        string         `json:"tool_name,omitempty"`
	Risk            ToolRisk       `json:"risk,omitempty"`
	Effect          ToolRuleEffect `json:"effect"`
	Mode            ToolMode       `json:"mode"`
	CreatedAt       time.Time      `json:"created_at"`
}

// ToolBindingScope é onde um perfil vale.
type ToolBindingScope string

const (
	ToolScopeCompany ToolBindingScope = "company"
	ToolScopeProject ToolBindingScope = "project"
	ToolScopePersona ToolBindingScope = "persona"
)

func (s ToolBindingScope) Valid() bool {
	return s == ToolScopeCompany || s == ToolScopeProject || s == ToolScopePersona
}

// rank: quanto mais estreito, maior.
func (s ToolBindingScope) rank() int {
	switch s {
	case ToolScopePersona:
		return 3
	case ToolScopeProject:
		return 2
	case ToolScopeCompany:
		return 1
	}
	return 0
}

// ToolBinding liga um perfil a um escopo.
type ToolBinding struct {
	ID          ID               `json:"id"`
	ProfileID   ID               `json:"profile_id"`
	Scope       ToolBindingScope `json:"scope"`
	ProjectID   *ID              `json:"project_id,omitempty"`
	PersonaID   *ID              `json:"persona_id,omitempty"`
	TargetName  string           `json:"target_name,omitempty"` // nome do projeto ou da persona, para exibição
	Priority    int              `json:"priority"`
	CreatedAt   time.Time        `json:"created_at"`
	ProfileSlug string           `json:"profile_slug,omitempty"`
}

// BindingWins informa se a vence b na resolução: escopo mais estreito, depois prioridade maior, depois o mais
// novo (e, empatando tudo, o id maior, para a ordem ser total).
func BindingWins(a, b ToolBinding) bool {
	if ra, rb := a.Scope.rank(), b.Scope.rank(); ra != rb {
		return ra > rb
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID.String() > b.ID.String()
}

// Limites dos perfis.
const (
	MaxToolProfileRules    = 500
	DefaultToolRateLimit   = 120
	MaxToolRateLimit       = 10000
	MaxToolsPerCatalog     = 500
	MaxToolProfiles        = 100
	MaxToolGateways        = 50
	MaxGatewayTokens       = 20
	DefaultGatewayTokenTTL = 90 * 24 * time.Hour
	MaxGatewayTokenTTL     = 366 * 24 * time.Hour
)

// ToolProfile é um conjunto nomeado de regras.
type ToolProfile struct {
	ID              ID                `json:"id"`
	CompanyID       ID                `json:"company_id"`
	Slug            string            `json:"slug"`
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	DefaultAction   ToolDefaultAction `json:"default_action"`
	RateLimitPerMin int               `json:"rate_limit_per_min"`
	Rules           []ToolRule        `json:"rules"`
	Bindings        []ToolBinding     `json:"bindings"`
	CreatedBy       Actor             `json:"created_by"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ToolVerdict é a decisão de um perfil sobre uma tool (ou uma integração inteira).
type ToolVerdict struct {
	Allowed bool     `json:"allowed"`
	Mode    ToolMode `json:"mode,omitempty"`
	// Source diz de onde veio a decisão: "no_profile", "default", "include" ou "exclude".
	Source string `json:"source"`
	// Rule é a regra que decidiu (nil para no_profile e default).
	Rule *ToolRule `json:"rule,omitempty"`
}

// specificity: tool > risco de uma integração > integração inteira > risco global.
func (r ToolRule) specificity() int {
	switch r.Target {
	case ToolTargetTool:
		return 4
	case ToolTargetRisk:
		if r.IntegrationID != nil {
			return 3
		}
		return 1
	case ToolTargetIntegration:
		return 2
	}
	return 0
}

func (r ToolRule) matchesTool(integrationID ID, tool string, risk ToolRisk) bool {
	switch r.Target {
	case ToolTargetIntegration:
		return r.IntegrationID != nil && *r.IntegrationID == integrationID
	case ToolTargetTool:
		return r.IntegrationID != nil && *r.IntegrationID == integrationID && r.ToolName == tool
	case ToolTargetRisk:
		return r.Risk == risk && (r.IntegrationID == nil || *r.IntegrationID == integrationID)
	}
	return false
}

// EvaluateTool decide se a tool pode ser chamada sob o perfil p (nil = sem perfil: tudo permitido, como antes).
// Excluir vence incluir; entre as regras de inclusão vale a mais específica (tool > risco da integração >
// integração > risco global) e, empatando, perguntar antes vence permitir. Sem regra, vale a ação padrão.
func EvaluateTool(p *ToolProfile, integrationID ID, tool string, risk ToolRisk) ToolVerdict {
	if p == nil {
		return ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "no_profile"}
	}
	var best *ToolRule
	for i := range p.Rules {
		r := p.Rules[i]
		if !r.matchesTool(integrationID, tool, risk) {
			continue
		}
		if r.Effect == ToolExclude {
			rr := r
			return ToolVerdict{Allowed: false, Source: "exclude", Rule: &rr}
		}
		if best == nil || r.specificity() > best.specificity() ||
			r.specificity() == best.specificity() && r.Mode == ToolModeAsk && best.Mode != ToolModeAsk {
			rr := r
			best = &rr
		}
	}
	if best != nil {
		return ToolVerdict{Allowed: true, Mode: best.Mode, Source: "include", Rule: best}
	}
	if p.DefaultAction == ToolDefaultAllow {
		return ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "default"}
	}
	return ToolVerdict{Allowed: false, Source: "default"}
}

// EvaluateIntegration decide se uma integração inteira entra na run. É o que vale para MCP stdio, que não passa
// pelo gateway: só as regras de integração (e a ação padrão) contam, e "perguntar antes" não tem como valer, então
// uma inclusão com ask deixa o stdio de fora.
func EvaluateIntegration(p *ToolProfile, integrationID ID) ToolVerdict {
	if p == nil {
		return ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "no_profile"}
	}
	var inc *ToolRule
	for i := range p.Rules {
		r := p.Rules[i]
		if r.Target != ToolTargetIntegration || r.IntegrationID == nil || *r.IntegrationID != integrationID {
			continue
		}
		if r.Effect == ToolExclude {
			rr := r
			return ToolVerdict{Allowed: false, Source: "exclude", Rule: &rr}
		}
		rr := r
		inc = &rr
	}
	if inc != nil {
		if inc.Mode == ToolModeAsk {
			return ToolVerdict{Allowed: false, Mode: ToolModeAsk, Source: "include", Rule: inc}
		}
		return ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "include", Rule: inc}
	}
	if p.DefaultAction == ToolDefaultAllow {
		return ToolVerdict{Allowed: true, Mode: ToolModeAllow, Source: "default"}
	}
	return ToolVerdict{Allowed: false, Source: "default"}
}

// Ações que um token de gateway pode ter.
const (
	GatewayActionList = "tools/list"
	GatewayActionCall = "tools/call"
)

// GatewayActions lista as ações possíveis.
var GatewayActions = []string{GatewayActionList, GatewayActionCall}

// GatewayTokenPrefix distingue o token de gateway das API keys (hk_) e dos run tokens (JWT).
const GatewayTokenPrefix = "hkgw_"

// ToolGateway é um endpoint MCP nomeado para clientes de fora.
type ToolGateway struct {
	ID           ID         `json:"id"`
	CompanyID    ID         `json:"company_id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	ProfileID    ID         `json:"profile_id"`
	ProfileSlug  string     `json:"profile_slug,omitempty"`
	ProfileName  string     `json:"profile_name,omitempty"`
	Enabled      bool       `json:"enabled"`
	ActiveTokens int        `json:"active_tokens"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedBy    Actor      `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ToolGatewayToken é um token de gateway, sem o valor (só o prefixo).
type ToolGatewayToken struct {
	ID         ID         `json:"id"`
	GatewayID  ID         `json:"gateway_id"`
	Label      string     `json:"label,omitempty"`
	Prefix     string     `json:"prefix"`
	Actions    []string   `json:"actions"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedBy  Actor      `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Active informa se o token vale em now.
func (t ToolGatewayToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// ToolDecision é o que o gateway decidiu sobre uma chamada.
type ToolDecision string

const (
	ToolDecisionAllow       ToolDecision = "allow"
	ToolDecisionDeny        ToolDecision = "deny"
	ToolDecisionAsk         ToolDecision = "ask"
	ToolDecisionQuarantined ToolDecision = "quarantined"
	ToolDecisionError       ToolDecision = "error"
)

func (d ToolDecision) Valid() bool {
	switch d {
	case ToolDecisionAllow, ToolDecisionDeny, ToolDecisionAsk, ToolDecisionQuarantined, ToolDecisionError:
		return true
	}
	return false
}

// Resultado de uma chamada auditada.
const (
	ToolCallOK      = "ok"
	ToolCallFailed  = "failed"
	ToolCallBlocked = "blocked"
	ToolCallPending = "pending"
)

// ToolInvocation é uma chamada auditada. Nunca carrega os argumentos: só o sha256 e o tamanho.
type ToolInvocation struct {
	ID              ID           `json:"id"`
	RunID           *ID          `json:"run_id,omitempty"`
	GatewayID       *ID          `json:"gateway_id,omitempty"`
	GatewaySlug     string       `json:"gateway_slug,omitempty"`
	PersonaID       *ID          `json:"persona_id,omitempty"`
	PersonaName     string       `json:"persona_name,omitempty"`
	IntegrationID   *ID          `json:"integration_id,omitempty"`
	IntegrationSlug string       `json:"integration_slug"`
	Tool            string       `json:"tool"`
	ArgsSHA256      string       `json:"args_sha256"`
	ArgsBytes       int          `json:"args_bytes"`
	Decision        ToolDecision `json:"decision"`
	GateID          *ID          `json:"gate_id,omitempty"`
	Status          string       `json:"status"`
	LatencyMs       int          `json:"latency_ms"`
	Error           string       `json:"error,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
}
