// Package views holds the templ components of the Hanko UI and the plain view
// models they render. View models are built by package ui from service types;
// they carry display-ready strings so templates stay logic-free.
package views

import "github.com/renatobardi/hanko/internal/domain"

// HTML is markup that has already been sanitised by its producer
// (markdown renderer, log-line renderer). Templates emit it verbatim.
type HTML string

// CompanyRef identifies the company a page belongs to.
type CompanyRef struct {
	Slug string
	Name string
}

// Crumb is one breadcrumb element; the last one is the current page.
type Crumb struct {
	Label string
	Href  string
}

// Action is the primary call to action shown in the topbar.
type Action struct {
	Label string
	Href  string
	Icon  string
}

// Shell is the data the layout needs on every page.
type Shell struct {
	Title   string
	Company CompanyRef
	Active  string // nav key
	Crumbs  []Crumb
	Action  *Action
	Version string
	Owner   string            // display name of the human owner
	Role    domain.MemberRole // papel da pessoa nesta empresa (só UX; o service é a barreira)
	Flush   bool              // content area without padding (two-pane pages)
}

// Base returns the company-scoped URL prefix, e.g. "/c/hanko".
func (s Shell) Base() string { return "/c/" + s.Company.Slug }

// Badge is a status pill. Class is one of run, done, outline, danger, warn, soft.
type Badge struct {
	Label string
	Class string
	Live  bool
}

// Option is an <option> of a select.
type Option struct {
	Value    string
	Label    string
	Selected bool
	Slug     string // optional: slug of the thing the option stands for (projects)
}

// ---- dashboard ----

type Step struct {
	Label string
	Href  string
	Done  bool
	Now   bool
	N     int
}

type Bar struct {
	Label string
	Class string
	Count int
	Pct   int
}

type ActivityItem struct {
	Actor   string
	Persona bool
	System  bool   // the actor is the system (shown with the bolt icon)
	Text    string // verb phrase
	Subject string
	Href    string
	When    string
	Detail  string
}

type DashboardVM struct {
	Subtitle        string
	ActivePersonas  int
	TasksInProgress int
	MonthCost       string
	PendingGates    int
	HasPersonas     bool
	Steps           []Step
	StepsDone       int
	StepsPct        int
	Bars            []Bar
	OpenTasks       int
	Activity        []ActivityItem
	Tasks           []TaskRow
	Gates           []GateRow // pending decisions, oldest first (at most 5)
	Notice          string
	Budget          *BudgetBox // company monthly budget, when set
}

// ---- tasks ----

type TabLink struct {
	Key   string
	Label string
	Href  string
	Count int
	On    bool
}

type TaskRow struct {
	Key           string
	Href          string
	Title         string
	Note          string
	Persona       string
	Project       string
	Badge         Badge
	Priority      string // critical|high|medium|low
	PriorityLabel string
	Updated       string
}

type TaskGroup struct {
	Label  string
	Count  int
	Rows   []TaskRow
	Status string // domain.TaskStatus (colunas do quadro)
}

type TasksVM struct {
	NewHref  string
	Subtitle string
	Tabs     []TabLink
	Query    string
	Tab      string
	Groups   []TaskGroup
	Total    int
	// Board é a visão em quadro (?visao=quadro): Columns tem uma coluna por status da aba, vazias inclusive.
	Board               bool
	Columns             []TaskGroup
	ListHref, BoardHref string
	// Project filters the list (?projeto=): slug, name, link to its page and the link that clears the filter.
	Project, ProjectName, ProjectHref, ClearHref string
	// Goal filters the list (?meta=): slug, title, link to its page and the link that clears the filter.
	Goal, GoalTitle, GoalHref, GoalClear string
}

type Msg struct {
	Author   string
	Initials string
	Human    bool // the owner: bubble on the right
	Body     HTML
	When     string
	RunHref  string
	// Quote is the passage an anchored comment is about; QuoteLost = its anchor no longer exists in the document.
	Quote     string
	QuoteLost bool
}

type RunMini struct {
	Name    string // nome amigável (pode estar vazio em runs antigas)
	Short   string
	Href    string
	Label   string
	When    string
	Dot     string // live|bad|warn|""
	Active  bool
	Summary string
}

type TaskVM struct {
	Key             string
	Title           string
	Description     HTML
	HasDescription  bool
	Badge           Badge
	Priority        string
	PriorityLabel   string
	PersonaName     string
	PersonaHref     string
	ProjectName     string
	ProjectHref     string
	ProjectOptions  []Option
	GoalOptions     []Option // metas ativas (e a da task); só vale com mais de uma opção
	GoalName        string
	GoalHref        string
	Artifacts       []ArtifactMini
	ArtifactsHref   string
	Created         string
	Updated         string
	StatusOptions   []Option
	PriorityOptions []Option
	PersonaOptions  []Option
	Thread          []Msg
	Gates           []GateRow // pending gates of this task
	Runs            []RunMini
	CanWake         bool
	ActiveRun       *RunMini
	Cost            string
	Model           string // manual model of the task (wins over the persona's)
	Notice          string // flash after an action
	Error           string
	Draft           string
	// Anchored-comment island on the description (ADR-0026).
	TaskKey     string
	DescDoc     string // description as anchors see it (\n line endings)
	DescAnchors string // JSON
	IslandAPI   string
	AnchorMsgs  string // JSON
}

type NewTaskVM struct {
	Title           string
	Model           string
	Description     string
	PriorityOptions []Option
	StatusOptions   []Option
	PersonaOptions  []Option
	ProjectOptions  []Option
	GoalOptions     []Option
	Error           string
	FieldErr        map[string]string
}

// ---- personas ----

type PersonaRow struct {
	Name      string
	Initials  string
	Title     string
	Href      string
	Badge     Badge
	Agent     string
	Model     string
	OpenTasks int
	Heartbeat string
}

type PersonasVM struct {
	Subtitle string
	Rows     []PersonaRow
	Active   int
	Running  int
	Paused   int
	Awaiting int
}

type HirePersonaVM struct {
	Name          string
	Title         string
	Agent         string
	Model         string
	Instructions  string
	Heartbeat     string
	ReportsTo     []Option // "Reporta a": the human owner or a persona
	RequireReview bool
	Icon          string // um de domain.PersonaIcons
	Error         string
	FieldErr      map[string]string
	// ACP harness (shown only while the ACP card is selected).
	AcpCommand    string
	AcpArgs       string   // one argument per line
	AcpPermission []Option // allow | reject | read_only
}

type PersonaVM struct {
	Icon          string // glifo atual (domain.PersonaIcons)
	IconAction    string // POST que troca o glifo
	Name          string
	Initials      string
	Title         string
	Badge         Badge
	Agent         string
	Model         string
	Heartbeat     string
	Instructions  string
	Hired         string
	Slug          string
	Tasks         []TaskRow
	Chats         []ChatRow // conversations with this persona
	NewChatHref   string    // opens a conversation with it ("" = the viewer cannot write)
	Runs          []RunRow
	Budget        *BudgetBox       // monthly spend vs ceiling
	Notice        string           // flash after an action
	LastError     string           // last failed run, shown while the persona is failing
	LastErrorAt   string           // when it happened
	Failures      int              // failed runs in a row
	Controls      []PersonaControl // pause / resume / reactivate buttons
	ReportsTo     string           // name of the superior ("" = reports to the owner)
	ReportsHref   string
	Reports       []PersonaRef // personas that report to this one
	RequireReview bool
	Subscription  string   // name of the subscription that covers the persona ("" = none)
	SubOptions    []Option // subscriptions to link
	SubAction     string
	OrgAction     string   // POST target of the organisation form
	OrgOptions    []Option // "Reporta a" choices
	Reviewer      ReviewerVM
	Extras        PersonaExtrasVM // skills e servidores MCP (ADR-0029)
}

// ReviewerVM is the reviewer block of the persona page: the configured reviewer (summary) and the
// edit form with its three modes (none, another persona, same role on another harness/model).
type ReviewerVM struct {
	Summary string // human text of the configured reviewer ("" = none)
	Href    string // link to the reviewer persona, when it is one
	Action  string // POST target
	Mode    string // none | persona | harness
	Persona []Option
	Agent   []Option
	Model   string
	Error   string
}

// CompanyLink is one entry of the company switcher.
type CompanyLink struct {
	Slug    string
	Name    string
	Current bool
}

// PersonaRef is a link to another persona.
type PersonaRef struct {
	Name string
	Href string
}

// PersonaControl is one action button on the persona page (a POST to Href).
type PersonaControl struct {
	Label string
	Href  string
	Icon  string
	Hint  string
}

// ---- runs ----

type RunRow struct {
	Name      string // nome amigável (pode estar vazio em runs antigas)
	Short     string
	Href      string
	Persona   string
	TaskKey   string
	TaskHref  string
	TaskTitle string
	Badge     Badge
	Agent     string
	Duration  string
	Cost      string
	Tokens    string
	When      string
}

type RunsVM struct {
	Subtitle string
	Tabs     []TabLink
	Rows     []RunRow
	Live     int
}

type LogLine struct {
	Seq   int64
	Time  string
	Kind  string
	Label string
	Text  string
}

type RunVM struct {
	Name         string // nome amigável (pode estar vazio em runs antigas)
	Short        string
	ID           string
	Badge        Badge
	Persona      string
	PersonaHref  string
	TaskKey      string
	TaskHref     string
	TaskTitle    string
	Chat         bool // the run answers a conversation, not a task (TaskKey/TaskHref/TaskTitle then point at it)
	Trigger      string
	Started      string
	Duration     string
	Events       int
	Tokens       string
	Cost         string
	ListCost     string // tokens x list price in force when the run finished
	CostSource   string // where Cost comes from: real, estimated…
	Agent        string
	Model        string
	ModelSource  string // who chose the model: task, persona or harness default
	Subscription string
	Session      string
	ExitCode     string
	Error        string
	Summary      HTML
	Active       bool
	Terminal     bool
	CancelHref   string
	SSEHref      string
	Lines        []LogLine
	LastSeq      int64
	// Islands (ADR-0026): virtualized log and anchored comments on the summary.
	IslandAPI      string
	LogMsgs        string // JSON
	TZ             string
	Lang           string
	SummaryDoc     string // summary as anchors see it
	SummaryAnchors string // JSON
	AnchorMsgs     string // JSON
	// Artefatos que a execução publicou (todas as versões).
	Artifacts     []ArtifactMini
	ArtifactsHref string
}

// ---- activity ----

type ActivityDay struct {
	Label string
	Items []ActivityItem
}

type ActivityVM struct {
	Subtitle string
	Days     []ActivityDay
}

// ---- misc ----

type OnboardingVM struct {
	Name       string
	TaskPrefix string
	Error      string
}

type ErrorVM struct {
	Status  int
	Title   string
	Message string
	Back    string
}

type SoonVM struct {
	Label     string
	Milestone string
	Blurb     string
}

// ---- custos ----

// BudgetBox is spend versus monthly ceiling. State: none|ok|warning|exceeded.
type BudgetBox struct {
	Label      string
	Spent      string
	Limit      string // "" = no ceiling
	Percent    int    // 0..100, for the bar
	PercentTxt string
	State      string
	StateText  string
	Value      string // current ceiling in USD for the form ("" = none)
	Action     string // POST target of the form; "" = read-only
	Href       string // link to the owner page (persona), "" = none
	Note       string
}

type CostRowVM struct {
	Label     string
	Href      string
	Cost      string
	List      string // list cost
	Paid      string // share of the subscription fees
	Runs      string
	Tokens    string
	Share     int
	Estimated bool // part of the cost was estimated from the price table
}

type CostTableVM struct {
	Title string
	Rows  []CostRowVM
}

// ChartBar is one day of the SVG bar chart (coordinates in the chart's viewBox).
type ChartBar struct {
	X, W   int
	Y, H   int // reported part
	EY, EH int // estimated part, stacked on top of the reported one
	Title  string
	Peak   bool
}

type DayChart struct {
	W, H    int
	Bars    []ChartBar
	First   string
	Last    string
	Peak    string // "pico 12 out US$ 61,40"
	Max     string
	HasData bool
	Mid     string
}

// DayCostVM is one day of the four-costs table.
type DayCostVM struct {
	Label     string
	Real      string
	Estimated string
	List      string
	Paid      string
	Unused    bool // a subscription existed but nobody used it that day: "sem uso"
	Idle      string
	HasData   bool
}

// SubRowVM is a subscription on the costs page.
type SubRowVM struct {
	Name         string
	Agent        string
	Monthly      string
	Models       string
	Active       bool
	Blocked      bool
	WeekUsage    string
	WeekLimit    string // "" = no weekly ceiling
	WeekPercent  int
	WeekPctTxt   string
	GuardPct     int
	Paid         string // period
	Idle         string // period
	Runs         string
	MonthlyValue string // form
	LimitValue   string
	ModelsValue  string
	UpdateAction string
	DeleteAction string
	CanDelete    bool
}

type CostsVM struct {
	Subtitle  string
	Paid      string
	PaidNote  string
	Days      []DayCostVM
	HasSubs   bool
	Subs      []SubRowVM
	SubCreate string // POST target of the new-subscription form
	AgentOpts []Option
	Tabs      []TabLink
	Total     string
	TotalNote string
	Runs      string
	Estimated string
	EstNote   string
	Company   BudgetBox
	Personas  []BudgetBox
	Projects  []BudgetBox // projetos com orçamento próprio (ADR-0025)
	// FilterProject filtra a tela por projeto (?projeto=): nome, link da página e link que limpa o filtro.
	FilterProject, FilterHref, ClearHref string
	Chart                                DayChart
	Tables                               []CostTableVM
	Notice                               string
	Error                                string
	Month                                string
	HasSpend                             bool
	BudgetKpi                            string
	BudgetNote                           string
}

// ---- fluxos ----

type FlowRow struct {
	Name        string
	Href        string
	Description string
	Persona     string
	Project     string
	Schedule    string // human description of the cron
	Cron        string
	Enabled     bool
	Next        string
	Last        string
	ToggleHref  string
	RunHref     string
}

type FlowsVM struct {
	Subtitle string
	Active   int
	Disabled int
	Next     string
	Last     string
	Rows     []FlowRow
	NewHref  string
	Notice   string
}

type FlowFormVM struct {
	Action          string
	Edit            bool
	Name            string
	Description     string
	Cron            string
	Timezone        string
	Enabled         bool
	TaskTitle       string
	TaskDescription string
	PersonaOptions  []Option
	ProjectOptions  []Option
	PriorityOptions []Option
	Presets         []Option
	Hint            CronHintVM
	Error           string
	FieldErr        map[string]string
	CancelHref      string
	HintHref        string
}

type CronHintVM struct {
	Human string
	Next  []string
	Error string
}

type FireRow struct {
	When     string
	TaskKey  string
	TaskHref string
	Title    string
	Badge    Badge
}

type FlowVM struct {
	Name       string
	Badge      Badge
	Meta       string
	Enabled    bool
	ToggleHref string
	RunHref    string
	Schedule   string
	Cron       string
	Timezone   string
	Next       string
	Last       string
	Form       FlowFormVM
	Fires      []FireRow
	Notice     string
	Error      string
	Fired      int
}

// ---- auth ----

// FirebaseWeb is the public Firebase web configuration handed to the browser.
type FirebaseWeb struct {
	APIKey     string
	AuthDomain string
	ProjectID  string
}

// LoginVM drives the sign-in page. Provider is "local" or "firebase".
type LoginVM struct {
	Provider string
	Next     string
	Email    string
	Name     string
	Error    string
	Notice   string
	// FirstRun: no users yet. Local provider shows "Criar conta de dono".
	FirstRun bool
	// NeedsCLI: no users yet but the web form is off (public URL); the page points to `hanko auth create-owner`.
	NeedsCLI bool
	Firebase *FirebaseWeb
	Version  string
}

// APIKeyRow is one API key in the settings table.
type APIKeyRow struct {
	ID       string
	Name     string
	Prefix   string
	Created  string
	LastUsed string
	Revoked  bool
}

// SettingsVM drives the account/API keys page.
type SettingsVM struct {
	Authenticated bool
	Name, Email   string
	Provider      string
	Role          string
	Keys          []APIKeyRow
	// NewKey is shown once, right after creation.
	NewKey  string
	KeyName string
	Error   string
	// Members is the company's "Membros" section (authenticated mode only).
	Members MembersVM
	// Memory is the company's "Memória" section (memory consolidation, ADR-0030).
	Memory MemoryVM
}

// MemberRow is one member in the "Membros" table.
type MemberRow struct {
	ID        string
	Name      string
	Email     string
	Role      string // owner|admin|member|viewer
	RoleLabel string
	Joined    string
	Self      bool
	CanEdit   bool // the viewer of the page can change this member's role or remove them
}

// InviteRow is one open invite.
type InviteRow struct {
	ID        string
	Email     string
	RoleLabel string
	Expires   string
	Expired   bool
	CanRevoke bool
}

// RoleOption is a role the page's user may grant.
type RoleOption struct{ Value, Label string }

// MembersVM drives the "Membros" section of the company settings.
type MembersVM struct {
	Show      bool
	MyRole    string
	Members   []MemberRow
	Invites   []InviteRow
	CanInvite bool
	Roles     []RoleOption // roles the user may grant (invite or change to)
	// NewLink is the invite link, shown once right after creation.
	NewLink      string
	NewLinkEmail string
	Email        string // form value kept after an error
	Error        string
}

// InviteVM drives the public /convite/{token} page.
type InviteVM struct {
	Version   string
	Token     string
	Company   string
	Email     string
	RoleLabel string
	// State: "invalid" | "confirm" (signed in as the invited e-mail) | "wrong" (signed in as someone else)
	// | "login" (local, account exists) | "create" (local, no account) | "firebase".
	State    string
	Name     string
	Error    string
	Firebase *FirebaseWeb
}

// AnchorBoxVM are the props of the anchored-comments island (ADR-0026).
type AnchorBoxVM struct {
	API        string
	TaskKey    string
	Target     string // task_description | run_summary
	RunID      string
	Doc        string
	Anchors    string // JSON
	CanComment bool
	Msgs       string // JSON
}
