package views

import (
	"context"

	"github.com/renatobardi/hanko/internal/i18n"
)

// Modelos das telas de apps, gateway de tools e perfis de ferramentas (ADR-0036): abas Tools, Perfis, Gateways e
// Auditoria dentro de Integrações, o painel "Tools efetivas" da persona.

// ToolsCatalogVM é a aba Tools: o catálogo de uma integração MCP.
type ToolsCatalogVM struct {
	Notice          string
	Error           string
	Integrations    []TabLink // seletor de integração
	HasMCP          bool
	Name, Slug      string
	Stdio           bool
	Synced          string // quando foi a última descoberta ("" = nunca)
	LastError       string
	RefreshHref     string
	ReviewHref      string // libera todas as que estão em quarentena
	Quarantined     int
	Rows            []ToolRowVM
	IntegrationHref string
}

// ToolRowVM é uma tool do catálogo.
type ToolRowVM struct {
	Name        string
	Title       string
	Description string
	Risk        Badge
	Status      Badge
	Seen        string
	Schema      string // JSON indentado, para a revisão
	Quarantined bool
	Removed     bool
	Disabled    bool
	ReleaseHref string // POST: libera (active)
	DisableHref string // POST: desliga
}

// ToolProfilesVM é a aba Perfis.
type ToolProfilesVM struct {
	Notice  string
	NewHref string
	Rows    []ToolProfileRowVM
}

// ToolProfileRowVM é um perfil na lista.
type ToolProfileRowVM struct {
	Name, Slug, Href string
	Default          string
	Rate             string
	Rules, Bindings  int
	Gateways         int
}

// ToolProfileFormVM é o passo 1 do assistente (nome e padrão) e a edição.
type ToolProfileFormVM struct {
	Action      string
	CancelHref  string
	Edit        bool
	Name        string
	Description string
	Default     string // deny | allow
	Rate        string
	Error       string
	FieldErr    map[string]string
}

// ToolProfileVM é o detalhe do perfil com os três passos do assistente.
type ToolProfileVM struct {
	Notice        string
	Name, Slug    string
	Step          int
	Steps         []TabLink
	Form          ToolProfileFormVM
	Integrations  []ProfileIntegrationVM
	ChoiceOpts    []Option // Padrão do perfil / Desligado / Perguntar / Permitido
	RiskRules     []RiskRuleVM
	RiskAction    string
	RiskOpts      []Option
	RiskIntegOpts []Option
	Bindings      []BindingRowVM
	BindAction    string
	ProjectOpts   []Option
	PersonaOpts   []Option
	DeleteHref    string
	Gateways      int
	NextHref      string
	BackHref      string
	ToolsHref     string
}

// ProfileIntegrationVM é uma integração no passo 2: a escolha do grupo e as escolhas por tool.
type ProfileIntegrationVM struct {
	Name, Slug string
	Action     string
	Stdio      bool
	Group      string
	NoCatalog  bool
	Tools      []ProfileToolVM
}

// ProfileToolVM é uma tool no passo 2.
type ProfileToolVM struct {
	Name   string
	Risk   Badge
	Status Badge
	Choice string
}

// RiskRuleVM é uma regra por nível de risco.
type RiskRuleVM struct {
	Label      string
	Effect     string
	DeleteHref string
}

// BindingRowVM é um vínculo do perfil.
type BindingRowVM struct {
	Scope, Target string
	Priority      int
	RemoveHref    string
}

// ToolGatewaysVM é a aba Gateways.
type ToolGatewaysVM struct {
	Notice      string
	Error       string
	CreateHref  string
	ProfileOpts []Option
	Rows        []GatewayRowVM
	NewName     string
}

// GatewayRowVM é um gateway na lista.
type GatewayRowVM struct {
	Name, Slug, Href string
	Profile          string
	Tokens           int
	LastUsed         string
	Enabled          bool
	ToggleHref       string
}

// ToolGatewayVM é o detalhe do gateway.
type ToolGatewayVM struct {
	Notice        string
	Error         string
	Name, Slug    string
	Profile       string
	Enabled       bool
	ToggleHref    string
	ProfileAction string
	ProfileOpts   []Option
	EndpointURL   string
	Tokens        []GatewayTokenRowVM
	TokenAction   string
	Revealed      string // o token recém-gerado: só nesta resposta
	DeleteHref    string
}

// GatewayTokenRowVM é um token do gateway (sem o valor).
type GatewayTokenRowVM struct {
	Prefix     string
	Label      string
	Actions    string
	Expires    string
	LastUsed   string
	State      Badge
	Active     bool
	RevokeHref string
}

// ToolAuditVM é a aba Auditoria.
type ToolAuditVM struct {
	Action          string
	IntegrationOpts []Option
	PersonaOpts     []Option
	DecisionOpts    []Option
	Tool            string
	Rows            []AuditRowVM
}

// AuditRowVM é uma chamada auditada.
type AuditRowVM struct {
	When        string
	Who         string
	WhoHref     string
	Gateway     bool
	Integration string
	Tool        string
	Decision    Badge
	Status      string
	Latency     string
	SHA         string
	GateHref    string
	Error       string
	Source      string // por onde entrou: execução, gateway, teste, hook, ACP, plugin
	Policy      string // de onde veio a decisão
	Result      string // trecho do resultado, já redigido
}

// EffectiveToolsVM é o painel "Tools efetivas" da persona.
type EffectiveToolsVM struct {
	Profile     string
	ProfileHref string
	Origin      string
	Rows        []EffectiveRowVM
	ToolsHref   string
}

// EffectiveRowVM é uma tool (ou um MCP stdio inteiro) com a decisão e a origem.
type EffectiveRowVM struct {
	Integration string
	Tool        string
	Risk        Badge
	Visible     bool
	Decision    string
	Origin      string
}

// ToolPolicyVM é a aba Política (ADR-0053): as políticas off/ask/allow, o formulário para gravar uma e o teste "como a
// persona X".
type ToolPolicyVM struct {
	Notice          string
	Error           string
	Rows            []PolicyRowVM
	CreateHref      string
	TestHref        string
	PersonaOpts     []Option // "Padrão da empresa" + personas
	TestPersonaOpts []Option
	IntegrationOpts []Option
	TestIntegration []Option
	TargetOpts      []Option
	ModeOpts        []Option
	Test            *ToolTestVM
	TestTool        string
	TestArgs        string
}

// PolicyRowVM é uma política.
type PolicyRowVM struct {
	Scope      string
	ScopeHref  string
	Target     string
	Mode       Badge
	Origin     string
	RemoveHref string
}

// ToolTestVM é o resultado de um teste de tool como uma persona.
type ToolTestVM struct {
	Persona  string
	Tool     string
	Status   Badge
	Risk     Badge
	Verdict  string
	Origin   string
	Decision Badge
	Message  string
	GateHref string
	Output   string
}

// integrationTabs monta as abas de Integrações; active é a chave da aba atual.
func integrationTabs(ctx context.Context, base, active string) []TabLink {
	tabs := []TabLink{
		{Key: "integracoes", Label: i18n.T(ctx, "Integrações"), Href: base + "/integracoes"},
		{Key: "tools", Label: i18n.T(ctx, "Tools"), Href: base + "/ferramentas"},
		{Key: "perfis", Label: i18n.T(ctx, "Perfis"), Href: base + "/ferramentas/perfis"},
		{Key: "politica", Label: i18n.T(ctx, "Política"), Href: base + "/ferramentas/politica"},
		{Key: "gateways", Label: i18n.T(ctx, "Gateways"), Href: base + "/ferramentas/gateways"},
		{Key: "auditoria", Label: i18n.T(ctx, "Auditoria"), Href: base + "/ferramentas/auditoria"},
	}
	for i := range tabs {
		tabs[i].On = tabs[i].Key == active
	}
	return tabs
}
