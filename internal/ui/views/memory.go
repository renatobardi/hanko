package views

// MemoryVM drives the "Memória" section of the company settings (memory consolidation, ADR-0030).
type MemoryVM struct {
	Show      bool // the backend supports memory consolidation
	Available bool // there is a memory provider (not none)
	Provider  string
	CanAdmin  bool // the viewer may configure and run it

	Enabled       bool
	IntervalHours int
	CuratorID     string
	Curators      []Option // personas of the company; the first option is "no curator"
	Workspaces    []Option // for "run now"; the first option is "all workspaces"
	LastFire      string
	NextFire      string
	Pending       int
	InboxHref     string

	Consolidations []MemoryConsolidationRow
	Proposals      []MemoryProposalRow

	Notice string
	Error  string
}

// MemoryConsolidationRow is one curation round in the history.
type MemoryConsolidationRow struct {
	When      string
	Workspace string
	Origin    string
	Status    Badge
	TaskKey   string
	TaskHref  string
	RunHref   string
	Proposals int
	Cost      string
	Summary   string
}

// MemoryProposalRow is one proposal in the history (the decision is made in the Inbox gate).
type MemoryProposalRow struct {
	ID        string
	When      string
	Kind      string
	Workspace string
	Target    string
	Status    Badge
	GateHref  string
	Error     string
	NextTry   string
	Diff      string
	CanRetry  bool
	RetryHref string
}
