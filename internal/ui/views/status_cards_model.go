package views

// Cards de status (ADR-0032): modelos das telas /cards-de-status.

// StatusCardsVM é a grade de cards.
type StatusCardsVM struct {
	Subtitle string
	Notice   string
	NewHref  string
	Cards    []StatusCardTile
	Archived []StatusCardTile
	// ShowArchived abre a seção recolhida dos arquivados.
	ShowArchived bool
	// GridHref é o fragmento da grade (polling htmx enquanto algum card monta ou atualiza).
	GridHref string
	Busy     bool
}

// StatusCardTile é um card na grade.
type StatusCardTile struct {
	Title      string
	Untitled   bool
	Href       string
	Badge      Badge
	Summary    HTML
	Summarizer string
	Updated    string
	Pending    int
	Compiling  bool
	// CompileFailed: a montagem não terminou (sem filtros); o botão é "Montar de novo".
	CompileFailed bool
	FailMsg       string
	Archived      bool
	RefreshURL    string
	RecompileURL  string
	ArchiveURL    string
	RestoreURL    string
	CanRefresh    bool
	CanArchive    bool
}

// StatusCardVM é o detalhe de um card.
type StatusCardVM struct {
	Title    string
	Untitled bool
	Badge    Badge
	Interest string
	Tabs     []TabLink
	Tab      string
	Notice   string
	Error    string
	Live     StatusCardLiveVM
	// Resumo
	RevisionOptions []Option
	RevisionAction  string
	Entered         []StatusCardChangeRow
	Mentioned       []StatusCardTaskRow
	// Configuração
	Form StatusCardFormVM
	// Tasks observadas e filtros
	Tasks        []StatusCardTaskRow
	Truncated    bool
	Queries      []StatusCardQueryRow
	QueryVersion int
	Compiled     string
	// Histórico
	Updates []StatusCardUpdateRow
	// Ações
	RecompileURL string
	ArchiveURL   string
	RestoreURL   string
	DeleteURL    string
	Archived     bool
	CanArchive   bool
	// Decision: o motor é o decision model (não há montagem nem resumidora). Engine é o rótulo.
	Decision bool
	Engine   string
}

// StatusCardLiveVM é a parte do detalhe que se atualiza sozinha (polling htmx) enquanto o card monta ou
// atualiza: estado, mensagens e o resumo.
type StatusCardLiveVM struct {
	Href          string
	Busy          bool
	Badge         Badge
	Compiling     bool
	CompileFailed bool
	Failure       string
	// Fallback: houve falha e o resumo mostrado é o último bom.
	Fallback      bool
	Summary       HTML
	HasSummary    bool
	RevisionLabel string
	Updated       string
	Pending       int
	PausedMsg     string
	RefreshURL    string
	RecompileURL  string
	CanRefresh    bool
	// O resultado também é um artefato versionado (ADR-0048).
	ArtifactHref string
	ArtifactName string
	Engine       string
}

// StatusCardChangeRow é uma mudança mostrada.
type StatusCardChangeRow struct {
	Key  string
	Href string
	Text string
}

// StatusCardTaskRow é uma task observada ou citada.
type StatusCardTaskRow struct {
	Key          string
	Href         string
	Title        string
	Badge        Badge
	Persona      string
	Updated      string
	HumanComment string
	CommentWhen  string
}

// StatusCardQueryRow é um filtro com quantas tasks casam.
type StatusCardQueryRow struct {
	N           int
	Description string
	Matches     int
}

// StatusCardUpdateRow é uma rodada no histórico.
type StatusCardUpdateRow struct {
	When     string
	Kind     string
	Trigger  string
	Badge    Badge
	Changes  int
	Tokens   string
	Cost     string
	TaskKey  string
	TaskHref string
	RunName  string
	RunHref  string
	Summary  string
	Error    string
}

// StatusCardFormVM é o formulário de criação e configuração.
type StatusCardFormVM struct {
	Action            string
	CancelHref        string
	Edit              bool
	Title             string
	TitlePinned       bool
	Interest          string
	SummarizerOptions []Option
	Mode              string
	IntervalMinutes   string
	DebounceSeconds   string
	MaxPerHour        string
	DailyTokenCap     string
	TrStatus          bool
	TrMembership      bool
	TrHuman           bool
	TrAssignee        bool
	TrAny             bool
	HoursStart        string
	HoursEnd          string
	HoursTZ           string
	Error             string
	FieldErr          map[string]string
	// ADR-0048: motor do resumo, cron, Painel e os filtros do motor decision.
	Engine         string // persona | decision
	EngineLabel    string
	CronExpr       string
	CronTZ         string
	CronSkip       bool
	OnDashboard    bool
	QueryProject   []Option
	QueryStatuses  string // status separados por vírgula
	QueryOpen      bool
	DecisionFields bool // mostra os filtros do motor decision
}
