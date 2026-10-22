package views

// View models de variáveis, gatilhos, revisões e arquivar de fluxos (ADR-0047) e de canais externos (ADR-0046).

// RunVarField é um campo do formulário "Rodar agora": uma variável declarada do fluxo.
type RunVarField struct {
	Name     string
	Label    string
	Value    string
	Default  string
	Required bool
}

// FlowRunVM é o formulário "Rodar agora" de um fluxo com variáveis.
type FlowRunVM struct {
	Href   string
	Fields []RunVarField
	Error  string
}

// TriggerRow é um gatilho na tela do fluxo.
type TriggerRow struct {
	Name       string
	Kind       string // "webhook" ou "event"
	KindLabel  string
	Enabled    bool
	Target     string // URL do webhook ou o verbo do evento
	Detail     string // filtro e mapeamento resumidos
	Rate       int
	Last       string
	Secret     string // referência ${secret:…} do HMAC (webhook)
	ToggleHref string
	DeleteHref string
}

// FlowTriggersVM é a seção de gatilhos de um fluxo.
type FlowTriggersVM struct {
	Rows    []TriggerRow
	AddHref string
	// SecretOnce é o segredo HMAC gerado agora; só aparece uma vez, na resposta da criação.
	SecretOnce     string
	SecretOnceName string
	SecretOnceURL  string
	Error          string
	// valores devolvidos ao formulário depois de um erro
	FormKind    string
	FormName    string
	FormVerb    string
	FormFilter  string
	FormMapping string
	FormRate    string
	FormSecret  string
}

// RevisionRow é uma revisão da definição do fluxo.
type RevisionRow struct {
	N           int
	When        string
	Origin      string
	Actor       string
	Summary     string
	Current     bool
	RestoreHref string
}

// FlowM19VM agrupa o que o M19 acrescenta à tela de um fluxo.
type FlowM19VM struct {
	Run          FlowRunVM
	Triggers     FlowTriggersVM
	Revisions    []RevisionRow
	ArchiveHref  string
	Archived     bool
	ArchivedWhen string
	HasSchedule  bool
}

// ---- canais ----

// ChannelRow é um canal na lista de Configurações › Canais.
type ChannelRow struct {
	Name      string
	Kind      string
	KindLabel string
	Href      string
	Enabled   bool
	Persona   string
	Linked    int
	Pending   int
	Failed    int
}

// ChannelFormVM é o formulário de criar/editar um canal.
type ChannelFormVM struct {
	Action      string
	Edit        bool
	Kind        string
	KindOptions []Option
	Name        string
	PersonaOpts []Option
	TokenRef    string
	SecretRef   string
	Repos       string // um por linha
	Mention     string
	APIBase     string
	Secrets     []string // ${secret:NOME} existentes, para o datalist
	Error       string
	FieldErr    map[string]string
}

// ChannelsVM é a lista de canais.
type ChannelsVM struct {
	Sections []TabLink
	Rows     []ChannelRow
	Notice   string
	Error    string
	NewForm  ChannelFormVM
}

// IdentityRow é uma pessoa vinculada (ou com vínculo pendente) a um canal.
type IdentityRow struct {
	User     string
	External string
	Status   string
	Since    string
	Revoke   string
}

// MessageRow é uma mensagem do canal com o estado de entrega.
type MessageRow struct {
	When      string
	DirLabel  string
	Kind      string
	Peer      string
	Body      string
	Status    string
	StatusCls string
	Note      string
	Attempts  int
	Resend    string
}

// ChannelVM é a tela de um canal.
type ChannelVM struct {
	Sections    []TabLink
	Name        string
	Kind        string
	KindLabel   string
	Enabled     bool
	Persona     string
	Notice      string
	Error       string
	URL         string // URL de entrada que o provedor chama
	ToggleHref  string
	ConnectHref string
	CanConnect  bool
	DeleteHref  string
	LinkHref    string
	LinkCode    string // código recém-gerado (aparece uma vez)
	LinkHint    string
	MyStatus    string // "linked", "pending" ou ""
	MyRevoke    string
	Identities  []IdentityRow
	Messages    []MessageRow
	Form        ChannelFormVM
	BackHref    string
}
