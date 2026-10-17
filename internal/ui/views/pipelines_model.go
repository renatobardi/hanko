package views

// Pipelines, cases, fila de revisão e aprendizados (ADR-0034): modelos das telas.

// ---- pipelines ----

type PipelinesVM struct {
	Rows         []PipelineRow
	Notice       string
	Form         PipelineFormVM
	ShowArchived bool
	ArchivedHref string
}

type PipelineRow struct {
	Name, Key, Href, Description string
	Stages                       int
	StageNames                   string
	Open, InReview               int
	Archived                     bool
}

type PipelineFormVM struct {
	Action, Name, Key, Description, Stages string
	Error                                  string
}

type PipelineVM struct {
	Name, Key, Description string
	Href                   string
	Archived, Strict       bool
	Tab                    string
	Tabs                   []TabLink
	Notice, Error          string
	Columns                []StageColumn
	Add                    CaseAddFormVM
	Stages                 []StageEditVM
	NewStage               StageEditVM
	Transitions            TransitionsVM
	Events                 []CaseEventRow
	MetaAction             string
	ArchiveAction          string
	DeleteAction           string
	ReviewHref             string
	TotalCases             int
}

type StageColumn struct {
	Key, Name, Kind, KindLabel string
	Disabled                   bool
	DisabledReason             string
	Approval                   bool
	Automation                 string
	Count                      int
	Cards                      []CaseCard
}

type CaseCard struct {
	Identifier, Title, Href, MoveAction string
	Version                             int
	Type, Updated                       string
	Badges                              []Badge
}

type FieldInput struct {
	Key, Label, Type, Value string
	Options                 []Option
	Required                bool
}

type CaseAddFormVM struct {
	Action, Title string
	Fields        []FieldInput
	StageName     string
	Error         string
}

type StageEditVM struct {
	Key, Name, Kind          string
	KindOptions              []Option
	First, Last              bool
	WhatHappensHere          string
	Disabled                 bool
	DisabledReason           string
	ApprovalRequired         bool
	ApproverOptions          []Option
	AutomationOptions        []Option
	Instructions             string
	PriorityOptions          []Option
	ApproveToOptions         []Option
	RejectToOptions          []Option
	ChangesToOptions         []Option
	RequireRejectReason      bool
	RequireChangesReason     bool
	ChildrenTerminal         bool
	FieldsText               string
	Action, DeleteAction     string
	UpAction, DownAction     string
	ApprovalSummary, Summary string
}

type TransitionsVM struct {
	Action string
	Strict bool
	Names  []string
	Rows   []TransitionRow
}

type TransitionRow struct {
	Name  string
	Cells []TransitionCell
}

type TransitionCell struct {
	Value string // "from>to"
	On    bool
	Self  bool
}

type CaseEventRow struct {
	When, Text, Actor, Reason string
	CaseIdentifier, CaseHref  string
	Icon                      string
	Example                   *ExampleFormVM
}

// ExampleFormVM é o "Salvar como exemplo" de uma decisão (gate ou revisão de case).
type ExampleFormVM struct {
	Action, SourceKind, SourceID, Next string
	Saved                              bool
	SavedHref                          string
}

// ---- cases ----

type CasesVM struct {
	Notice          string
	Empty           bool
	Tree            bool
	Q               string
	Groups          []CaseGroup
	Total           int
	StatusOptions   []Option
	TypeOptions     []Option
	PipelineOptions []Option
	ProjectOptions  []Option
	GroupOptions    []Option
	SortOptions     []Option
	ListHref        string
	TreeHref        string
	Form            CaseNewFormVM
}

type CaseNewFormVM struct {
	Action, Title, Type, Key, Summary, Fields string
	StatusOptions                             []Option
	PipelineOptions                           []Option
	Error                                     string
}

type CaseGroup struct {
	Label string
	Rows  []CaseRow
}

type CaseRow struct {
	Identifier, Title, Href, Type string
	Badge                         Badge
	Where, WhereHref              string
	Project, Updated              string
	Depth                         int
	Children                      string
	Flags                         []Badge
}

type CaseVM struct {
	Identifier, Title, Type, Key string
	Summary                      HTML
	SummaryRaw                   string
	HasSummary                   bool
	Badge                        Badge
	Version                      int
	Notice, Error                string
	PipelineName, PipelineHref   string
	StageName, StageKind         string
	ProjectName, ProjectHref     string
	ParentID, ParentTitle        string
	ParentHref                   string
	Created, Updated             string
	Fields                       []CaseFieldRow
	FieldsText                   string
	Links                        []CaseLinkRow
	Children                     []CaseRow
	ChildrenText                 string
	Blockers                     []CaseRefRow
	Blocking                     []CaseRefRow
	BlockersText                 string
	Events                       []CaseEventRow
	Base                         string
	EditAction                   string
	MoveAction                   string
	MoveOptions                  []Option
	Strict                       bool
	StatusOptions                []Option
	Loose                        bool
	Review                       *CaseReviewVM
	Suggestion                   *CaseSuggestionVM
	Attention                    string
	AttentionAction              string
	ConversationAction           string
	LinkAction                   string
	BlockersAction               string
	ReapprovalRequired           bool
	Closed                       bool
}

type CaseFieldRow struct {
	Key, Label, Value string
	Required          bool
}

type CaseLinkRow struct {
	Key, Title, Href, Role, Status string
	Persona, UnlinkAction          string
}

type CaseRefRow struct {
	Identifier, Title, Href string
	Resolved                bool
}

type CaseReviewVM struct {
	Action                              string
	Version                             int
	ApproveTo, RejectTo, ChangesTo      string
	RequireRejectReason, RequireChanges bool
	CanDecide                           bool
	WhatHappensHere                     string
}

type CaseSuggestionVM struct {
	Action, ToStage, Rationale, Confidence, Who, When string
	Version                                           int
	InReview                                          bool
}

// ---- fila de revisão ----

type ReviewQueueVM struct {
	Notice, Error string
	Rows          []ReviewRow
	BulkAction    string
	Failures      []string
	Tabs          []TabLink
}

type ReviewRow struct {
	Kind, KindLabel                     string
	KindBadge                           Badge
	CaseID, Identifier, Title, Href     string
	Version                             int
	Reason                              string
	Pipeline, Since                     string
	Action                              string
	ApproveTo, RejectTo, ChangesTo      string
	RequireRejectReason, RequireChanges bool
	SuggestionAction, SuggestedStage    string
	AttentionAction                     string
	Selectable                          bool
}

// ---- aprendizados ----

type LearningsVM struct {
	Tab             string
	Tabs            []TabLink
	Notice          string
	Days            []LearningDay
	PipelineOptions []Option
	StageOptions    []Option
	DecisionOptions []Option
	Examples        []ExampleRow
	ExportHref      string
	Empty           bool
}

type LearningDay struct {
	Label string
	Items []LearningItem
}

type LearningItem struct {
	Text, Reason, CaseHref, CaseIdentifier, PipelineHref, PipelineName, When, Icon string
	Example                                                                        *ExampleFormVM
}

type ExampleRow struct {
	ID, SourceKind, SourceLabel, SourceHref, Outcome, Cutoff, Notes string
	Mine                                                            bool
	EditAction, DeleteAction                                        string
}

// TaskCaseRow é um case ligado a uma task (painel "Cases" da task).
type TaskCaseRow struct {
	Identifier, Title, Href, Role, Where string
}
