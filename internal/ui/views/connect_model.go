package views

import "strings"

// ConnectVM é a tela do assistente Conectar (ADR-0089). Sem Slug é o passo 1 (servidor); com Slug é o estado da
// integração: passo 2 (descoberta), e, se a descoberta deu certo, os passos 3 (personas), 4 (modelo) e 5 (conferir
// e concluir). O passo 5 só aparece quando personas e modelo estão escolhidos na própria URL.
type ConnectVM struct {
	// Passo 1: formulário do servidor.
	Action     string
	CancelHref string
	Notice     string
	Error      string
	FieldErr   map[string]string
	Name       string
	URL        string
	OAuth      bool
	Scopes     string
	ClientID   string

	// Estado da integração (passos 2 em diante).
	Slug            string
	Base            string // /c/{empresa}/integracoes/conectar/{integração}
	IntegrationHref string
	ConsentHref     string // OAuth sem consentimento: POST para o autorizar da conexão (ferramentas/conexoes)
	CatalogErr      string
	CanComplete     bool
	PersonasLinked  int
	Tools           []ConnectToolRow
	Quarantined     int
	Personas        []ConnectPersonaOpt
	Templates       []ConnectTemplateOpt
	Preview         *ConnectPreviewVM
	DiscoverHref    string
	DiscardHref     string
	CompleteHref    string
	NewProfileHref  string
	PersonasURL     string // GET com personas e modelo escolhidos: o passo 5 aparece
	ChosenTemplate  string
}

// ConnectToolRow é uma tool do catálogo no passo 2, com o risco e a quarentena.
type ConnectToolRow struct {
	Name        string
	Title       string
	Risk        Badge
	Quarantined bool
}

// ConnectPersonaOpt é uma persona da escolha do passo 3. Nunca vem marcada por padrão.
type ConnectPersonaOpt struct {
	ID      string
	Name    string
	Checked bool
}

// ConnectTemplateOpt é um modelo de acesso do passo 4: o que cada risco recebe. Sugerido aparece, mas não vem marcado.
type ConnectTemplateOpt struct {
	ID          string
	Name        string
	Description string
	Suggested   bool
	Checked     bool
	Read        Badge
	Write       Badge
	Destructive Badge
}

// ConnectPreviewVM é o passo 5: o que cada persona escolhida poderá chamar depois de concluir.
type ConnectPreviewVM struct {
	Template string
	Personas []ConnectPreviewPersonaVM
	Released []string
}

// ConnectPreviewPersonaVM é uma persona do passo 5 com as tools dela.
type ConnectPreviewPersonaVM struct {
	Name  string
	Tools []ConnectPreviewToolVM
}

// ConnectPreviewToolVM é a tool de uma persona no passo 5: o modo do modelo e se a persona poderá chamar.
type ConnectPreviewToolVM struct {
	Name     string
	Risk     Badge
	Mode     Badge
	Callable Badge
	Note     string
}

// connectJoinNames junta nomes de tools para uma linha de texto.
func connectJoinNames(names []string) string { return strings.Join(names, ", ") }
