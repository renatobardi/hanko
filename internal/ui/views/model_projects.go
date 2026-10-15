package views

// Projetos e workspaces (ADR-0025): view models das telas /projetos e /workspaces.

// ProjectRow é uma linha da lista de projetos.
type ProjectRow struct {
	Name        string
	Href        string
	Description string
	Archived    bool
	Workspace   string
	Open        int
	Total       int
	Progress    int // 0..100
	ProgressTxt string
	Spent       string
	Limit       string // "" = sem orçamento
	BudgetState string // none | ok | warning | exceeded
	BudgetPct   string
}

type ProjectsVM struct {
	NewHref  string
	Subtitle string
	Notice   string
	Tabs     []TabLink
	Rows     []ProjectRow
	Active   int
	Archived int
	OpenTask int
	Spent    string
	Empty    bool // não há nenhum projeto (nem arquivado)
}

// StatusCount é uma contagem de tasks por status, com o link para a lista filtrada.
type StatusCount struct {
	Label string
	Count int
	Href  string
	Class string
}

type ProjectPersonaRow struct {
	Name  string
	Href  string
	Open  int
	Total int
}

type ProjectFormVM struct {
	Action           string
	CancelHref       string
	Edit             bool
	Name             string
	Description      string
	Budget           string // dólares digitados ("" = sem teto)
	WorkspaceOptions []Option
	FieldErr         map[string]string
	Error            string
}

type ProjectVM struct {
	Name          string
	Description   string
	Archived      bool
	Badge         Badge
	Notice        string
	Error         string
	Form          ProjectFormVM
	Budget        BudgetBox
	Workspace     string
	WorkspaceURL  string
	Statuses      []StatusCount
	Total         int
	Progress      int
	ProgressTxt   string
	Spent         string
	Personas      []ProjectPersonaRow
	Tasks         []TaskRow
	TasksHref     string
	NewTaskHref   string
	CostHref      string
	ToggleHref    string // arquivar / reativar
	Goals         []GoalMini
	GoalsHref     string
	Artifacts     []ArtifactMini
	ArtifactsHref string
}

// WorkspaceRow é uma linha da lista de workspaces.
type WorkspaceRow struct {
	Name      string
	Href      string
	Path      string
	RepoURL   string
	Branch    string
	Personas  int
	Projects  int
	OpenTasks int
	InUse     bool
	Missing   bool // o diretório não existe neste servidor
	Outside   bool // o caminho está fora da pasta da empresa na raiz dos workspaces
}

type WorkspacesVM struct {
	NewHref  string
	Subtitle string
	Notice   string
	Rows     []WorkspaceRow
	Root     string
	InUse    int
}

type WorkspaceFormVM struct {
	Action     string
	CancelHref string
	Edit       bool
	Name       string
	Path       string
	RepoURL    string
	Branch     string
	Root       string
	FieldErr   map[string]string
	Error      string
}

type UsageLink struct {
	Name string
	Href string
}

type WorkspaceVM struct {
	Name         string
	Path         string
	Missing      bool
	Outside      bool   // o caminho está fora da pasta da empresa na raiz dos workspaces
	Root         string // pasta da empresa na raiz dos workspaces
	Notice       string
	Error        string
	Form         WorkspaceFormVM
	Personas     []UsageLink
	Projects     []UsageLink
	OpenTasks    int
	InUse        bool
	DeleteAction string
	TasksNote    bool
}
