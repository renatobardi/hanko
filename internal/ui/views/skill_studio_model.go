package views

// Modelos das telas de versões, fontes e estúdio de skills (ADR-0035).

// SourceRow é uma fonte na lista.
type SourceRow struct {
	Label      string
	URL        string
	Ref        string
	Href       string
	Badge      Badge
	StatusText string
	Commit     string
	Imported   int
	Found      int
	SyncHref   string
}

type SkillSourcesVM struct {
	Tabs    []TabLink
	Enabled bool // interruptor do operador (HANKO_SKILL_SOURCES)
	NewHref string
	Notice  string
	Error   string
	Rows    []SourceRow
}

// DiscoverRow é uma skill encontrada na descoberta (árvore simples por caminho).
type DiscoverRow struct {
	Path        string
	Name        string
	Slug        string
	Description string
	Note        string
	Depth       int
	Checked     bool
	Disabled    bool
	HasScripts  bool
}

type SkillSourceFormVM struct {
	Tabs           []TabLink
	Enabled        bool
	Action         string // conectar
	DiscoverAction string
	CancelHref     string
	URL            string
	Ref            string
	PathPrefix     string
	Excluded       string // um caminho por linha
	AutoUpdate     bool
	ImportNew      bool
	Discovered     bool
	Commit         string
	Truncated      bool
	Rows           []DiscoverRow
	Error          string
	FieldErr       map[string]string
}

// SourceEntryRow é um diretório da fonte, com a escolha do admin.
type SourceEntryRow struct {
	Path        string
	Name        string
	Description string
	Depth       int
	Selection   string
	Options     []Option
	Present     bool
	HasScripts  bool
	Modified    bool
	SkillSlug   string
	SkillHref   string
	Error       string
}

type SkillSourceVM struct {
	Tabs            []TabLink
	Enabled         bool
	Label           string
	URL             string
	Ref             string
	Badge           Badge
	StatusText      string
	Commit          string
	LastSuccess     string
	NextSync        string
	LastError       string
	Notice          string
	SyncHref        string
	SettingsAction  string
	SelectionAction string
	DeleteAction    string
	PathPrefix      string
	Excluded        string
	AutoUpdate      bool
	ImportNew       bool
	SourceEnabled   bool
	Entries         []SourceEntryRow
}

// TestRunRow é uma execução de teste na lista do estúdio.
type TestRunRow struct {
	ID         string
	Href       string
	Skill      string
	Revision   int
	Persona    string
	Badge      Badge
	When       string
	Cost       string
	Excerpt    string
	Active     bool
	CancelHref string
	RerunHref  string
}

type SkillStudioVM struct {
	Tabs            []TabLink
	HasSkills       bool
	SkillSlug       string
	Skills          []Option
	Personas        []Option
	Inputs          []Option
	Revisions       []Option
	Input           string
	RunAction       string
	SaveInputAction string
	DeleteInputs    []SavedInputRow
	CompareAction   string
	RunsHref        string // fragmento da lista (polling)
	Runs            []TestRunRow
	AnyActive       bool
	Notice          string
	Error           string
	MaxInputKiB     int
}

// SavedInputRow é uma entrada salva, com o botão de apagar.
type SavedInputRow struct {
	Name       string
	DeleteHref string
}

type SkillTestVM struct {
	Row         TestRunRow
	Input       string
	Output      HTML
	OutputText  string
	Error       string
	Self        string
	RunHref     string
	RunName     string
	CompareHref string
	BackHref    string
}

type SkillCompareVM struct {
	A, B     SkillTestVM
	Diff     DiffVM
	BackHref string
}

// SkillVersionRow é uma versão na aba Versões.
type SkillVersionRow struct {
	Revision      int
	Name          string
	Size          string
	SHA           string
	Commit        string
	Author        string
	When          string
	ViewHref      string
	DiffHref      string
	RestoreAction string
	Current       bool
}

type SkillVersionView struct {
	Revision    int
	Name        string
	Description string
	Body        HTML
	CloseHref   string
}

type SkillVersionsVM struct {
	Tabs   []TabLink
	Name   string
	Slug   string
	Notice string
	Rows   []SkillVersionRow
	View   *SkillVersionView
	Diff   *DiffVM
}

// SkillPinRow é uma persona na aba Personas, com o liga/desliga e a versão fixada.
type SkillPinRow struct {
	Name       string
	Href       string
	Enabled    bool
	ToggleHref string
	PinAction  string
	Options    []Option
	Pinned     string
}

type SkillPersonasVM struct {
	Tabs    []TabLink
	Name    string
	Slug    string
	Version int
	Notice  string
	Rows    []SkillPinRow
}
