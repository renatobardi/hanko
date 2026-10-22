package views

// ---- Configurações › Decision model (ADR-0056) ----

// DecisionFormVM é o formulário do decision model. A chave nunca é digitada aqui: o campo é a referência
// ${secret:NOME} de um segredo da empresa.
type DecisionFormVM struct {
	Action    string
	BaseURL   string
	KeyRef    string
	Model     string
	Timeout   string
	Off       bool
	FieldErr  map[string]string
	Configure bool // já há configuração salva
}

// DecisionFeatureVM é uma feature registrada e o formulário que a liga.
type DecisionFeatureVM struct {
	Key     string
	Title   string
	Desc    string
	Enabled bool
	Model   string
	Action  string
}

// DecisionInvRowVM é uma invocação recente.
type DecisionInvRowVM struct {
	When     string
	Feature  string
	Model    string
	OK       bool
	Cost     string
	Source   string
	Tokens   string
	ErrorMsg string
}

// DecisionCostRowVM é o custo do decision model por feature e modelo no Custos.
type DecisionCostRowVM struct {
	Feature, Model, Calls, Errors, Cost, Tokens string
}

// DecisionCostVM é o bloco "Decision model" do Custos (ator sistema, fora das execuções de persona).
type DecisionCostVM struct {
	Total string
	Calls string
	Href  string
	Rows  []DecisionCostRowVM
}

// DecisionVM é a página Configurações › Decision model.
type DecisionVM struct {
	Sections    []TabLink
	Notice      string
	Error       string
	Warning     string
	Configured  bool
	Form        DecisionFormVM
	Models      []string // catálogo do provedor, para escolher o modelo
	CatalogNote string
	Secrets     []string // referências ${secret:NOME} disponíveis
	Features    []DecisionFeatureVM
	Invocations []DecisionInvRowVM
	RemoveHref  string
	CostsHref   string
}
