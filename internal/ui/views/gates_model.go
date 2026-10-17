package views

// GateRow is a gate as the lists, the inbox and the task banner show it.
type GateRow struct {
	ID          string
	Short       string
	Href        string
	Title       string
	Kind        string
	KindLabel   string
	Badge       Badge
	TaskKey     string
	TaskHref    string
	TaskTitle   string
	Persona     string
	PersonaHref string
	Excerpt     string
	Created     string // relative: "há 19 min"
	Decided     string
	DecidedBy   string
	Note        string
	Pending     bool
	Aged        bool // pending for more than 24 h
	DecideHref  string
	// Revisão automática (outro modelo) feita antes do gate; a decisão segue humana.
	Review *ReviewInfo
}

// ReviewInfo é o parecer do revisor automático exibido junto do gate.
type ReviewInfo struct {
	Badge         Badge
	Reviewer      string
	Justification string
	SHA           string
	Rejected      bool
}

type GatesVM struct {
	Subtitle string
	Status   []TabLink
	Kinds    []TabLink
	Rows     []GateRow
	Notice   string
	Empty    string // explains an empty list
}

type GateVM struct {
	Row      GateRow
	Body     HTML
	HasBody  bool
	RunHref  string
	RunShort string
	Created  string // absolute
	Decided  string // absolute
	Notice   string
	// Example é o "Salvar como exemplo" de um gate decidido (ADR-0034).
	Example *ExampleFormVM
}

type DecisionsVM struct {
	Subtitle string
	Tabs     []TabLink
	Rows     []GateRow
}

// ---- inbox ----

type InboxTask struct {
	Key         string
	Href        string
	Title       string
	Persona     string
	When        string
	ActionHref  string // POST: back to "A fazer"
	ActionLabel string
}

type InboxRun struct {
	Name       string
	Short      string
	Href       string
	TaskKey    string
	TaskHref   string
	TaskTitle  string
	Persona    string
	Error      string
	When       string
	ActionHref string // POST: wake the persona again
}

type InboxPersona struct {
	Name       string
	Href       string
	Title      string
	Error      string
	Failures   int
	When       string
	ActionHref string // POST: reactivate
}

// InboxAlert é um alerta operacional aberto (condição que some sozinha quando resolvida).
type InboxAlert struct {
	Message  string
	KindText string // "Persona sem heartbeat", "Execução presa", "Orçamento"
	Critical bool
	Href     string
	Link     string // texto do link para o sujeito
	When     string
}

// InboxSub is a subscription whose weekly guard tripped.
type InboxSub struct {
	Name    string
	Href    string
	Usage   string
	Limit   string
	Percent string
	Guard   int
	Week    string
}

type InboxVM struct {
	Subs         []InboxSub
	Subtitle     string
	Tabs         []TabLink
	Gates        []GateRow
	Blocked      []InboxTask
	Failed       []InboxRun
	Personas     []InboxPersona
	Alerts       []InboxAlert
	Reviewing    []InboxReview     // revisões automáticas em andamento (informativo; não contam como pendência)
	Goals        []AchievedGoalRow // metas atingidas há pouco (informativo; não contam como pendência)
	Total        int               // items shown in the current tab
	Notice       string
	Pending      int
	Aged         int
	DecidedToday int
	DecisionsURL string
	GatesURL     string
}

// ---- organograma ----

type OrgNodeVM struct {
	ID          string // persona id (the island's key)
	Slug        string
	ParentID    string // reports_to ("" = top of the chart)
	X, Y, W, H  int
	Name        string
	Title       string
	Meta        string // "3 tasks abertas"
	Status      string
	StatusClass string // idle|run|paused|error|gate
	Href        string
	Label       string // aria-label
	Gate        bool   // a gate of this persona waits for a human
	Review      bool   // requires review
	Initials    string
	Icon        string // glifo da persona (domain.PersonaIcons), desenhado por <use href="#pg-…">
}

type OrgEdgeVM struct{ D string }

type OrgVM struct {
	Subtitle string
	Width    int
	Height   int
	Nodes    []OrgNodeVM
	Edges    []OrgEdgeVM
	Total    int
	HireHref string
	// Island: props of the interactive org chart (ADR-0026); the static SVG stays as the no-JS version.
	IslandNodes string // JSON
	IslandAPI   string
	IslandMsgs  string // JSON
	CanEdit     bool   // admin/owner may drag
}
