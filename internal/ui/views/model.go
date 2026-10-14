// Package views holds the templ components of the Hanko UI and the plain view
// models they render. View models are built by package ui from service types;
// they carry display-ready strings so templates stay logic-free.
package views

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
	Owner   string // display name of the human owner
	Flush   bool   // content area without padding (two-pane pages)
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
	Label string
	Count int
	Rows  []TaskRow
}

type TasksVM struct {
	NewHref  string
	Subtitle string
	Tabs     []TabLink
	Query    string
	Tab      string
	Groups   []TaskGroup
	Total    int
}

type Msg struct {
	Author   string
	Initials string
	Human    bool // the owner: bubble on the right
	Body     HTML
	When     string
	RunHref  string
}

type RunMini struct {
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
	Notice          string // flash after an action
	Error           string
	Draft           string
}

type NewTaskVM struct {
	Title           string
	Description     string
	PriorityOptions []Option
	StatusOptions   []Option
	PersonaOptions  []Option
	ProjectOptions  []Option
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
	Error         string
	FieldErr      map[string]string
}

type PersonaVM struct {
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
	OrgAction     string   // POST target of the organisation form
	OrgOptions    []Option // "Reporta a" choices
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
	Short       string
	ID          string
	Badge       Badge
	Persona     string
	PersonaHref string
	TaskKey     string
	TaskHref    string
	TaskTitle   string
	Trigger     string
	Started     string
	Duration    string
	Events      int
	Tokens      string
	Cost        string
	Agent       string
	Model       string
	Session     string
	ExitCode    string
	Error       string
	Summary     HTML
	Active      bool
	Terminal    bool
	CancelHref  string
	SSEHref     string
	Lines       []LogLine
	LastSeq     int64
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

type CostsVM struct {
	Subtitle   string
	Tabs       []TabLink
	Total      string
	TotalNote  string
	Runs       string
	Estimated  string
	EstNote    string
	Company    BudgetBox
	Personas   []BudgetBox
	Chart      DayChart
	Tables     []CostTableVM
	Notice     string
	Error      string
	Month      string
	HasSpend   bool
	BudgetKpi  string
	BudgetNote string
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
