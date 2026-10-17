package views

// Modelos das telas de Skills e Integrações (ADR-0029).

// ---- skills ----

type SkillRow struct {
	Name        string
	Slug        string
	Href        string
	Description string
	Version     int
	Personas    int
	Updated     string
	// FromSource/Modified: selos "de fonte" e "modificada" (ADR-0035).
	FromSource bool
	Modified   bool
}

type SkillsVM struct {
	Subtitle string
	NewHref  string
	Notice   string
	Rows     []SkillRow
	// Tabs são as abas Biblioteca/Fontes/Estúdio; Filters, Todas/Locais/De fonte (ADR-0035).
	Tabs    []TabLink
	Filters []TabLink
}

// ToggleRow é uma linha com interruptor que liga ou desliga uma coisa para uma persona (ou o inverso).
type ToggleRow struct {
	Name       string
	Slug       string
	Href       string // página da persona/skill/integração
	Note       string
	Enabled    bool
	ToggleHref string // POST; o campo enabled vem do formulário
	Fixed      bool   // vale sempre (ex.: MCP de todas as personas): sem interruptor
}

type SkillEditorVM struct {
	Edit        bool
	Name        string
	Slug        string
	Description string
	Body        string
	Action      string
	CancelHref  string
	PreviewHref string
	DeleteHref  string
	Version     int
	Updated     string
	UpdatedBy   string
	MaxBodyKiB  int
	Notice      string
	Error       string
	FieldErr    map[string]string
	Preview     HTML
	Personas    []ToggleRow
	// Tabs são as abas Visão geral/Versões/Personas; SourceNote avisa que a skill veio de uma fonte (ADR-0035).
	Tabs       []TabLink
	SourceNote string
}

// ---- persona ----

// PersonaExtrasVM são os painéis de skills e servidores MCP na página da persona.
type PersonaExtrasVM struct {
	Show             bool
	Skills           []ToggleRow
	MCPs             []ToggleRow
	SkillsHref       string
	IntegrationsHref string
}

// ---- integrações ----

type IntegrationRow struct {
	Name       string
	Slug       string
	Href       string
	Target     string
	Scope      string
	Enabled    bool
	ToggleHref string
	Last       string
	LastBadge  Badge
}

type IntegrationsVM struct {
	Subtitle       string
	NewMCPHref     string
	NewWebhookHref string
	Notice         string
	MCP            []IntegrationRow
	Webhooks       []IntegrationRow
}

type EventOption struct {
	Value   string
	Label   string
	Checked bool
}

type IntegrationFormVM struct {
	Kind       string // mcp | webhook
	Edit       bool
	Action     string
	CancelHref string

	Name string
	URL  string

	// MCP
	Transport        string
	TransportOptions []Option
	Command          string
	ArgsText         string // um argumento por linha
	HeadersText      string // "Nome: valor", um por linha (define ou troca)
	DropHeaders      string // nomes separados por vírgula
	EnvText          string // "NOME=valor", um por linha
	DropEnv          string
	HeaderNames      []string // já guardados (só os nomes)
	EnvNames         []string
	AllPersonas      bool

	// Webhook
	EventOptions []EventOption
	Secret       string
	RotateSecret bool
	SecretSet    bool
	SecretHint   string

	Enabled  bool
	FieldErr map[string]string
	Error    string
}

type DeliveryRow struct {
	When     string
	Event    string
	Test     bool
	Badge    Badge
	Attempts int
	HTTP     string
	Latency  string
	Error    string
	Next     string
}

type IntegrationVM struct {
	Name       string
	Kind       string
	KindLabel  string
	Badge      Badge
	Target     string
	Scope      string
	Enabled    bool
	ToggleHref string
	TestHref   string
	DeleteHref string
	Notice     string
	Error      string
	// RevealedSecret só vem preenchido na resposta de criação/rotação de um webhook cujo segredo o servidor gerou.
	RevealedSecret string
	Form           IntegrationFormVM
	Deliveries     []DeliveryRow
	Personas       []ToggleRow
}

type IntegrationTestVM struct {
	OK      bool
	Status  string
	Latency string
	Error   string
	Note    string
}
