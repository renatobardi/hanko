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

type InboxVM struct {
	Subtitle     string
	Tabs         []TabLink
	Gates        []GateRow
	Blocked      []InboxTask
	Failed       []InboxRun
	Personas     []InboxPersona
	Total        int // items shown in the current tab
	Notice       string
	Pending      int
	Aged         int
	DecidedToday int
	DecisionsURL string
	GatesURL     string
}

// ---- organograma ----

type OrgNodeVM struct {
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
}
