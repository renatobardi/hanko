package views

// Workspace de execução com Git, padrões de projeto e "Nova sessão" (ADR-0054): view models.

// GitWorktreeRow é um worktree de task numa tabela (workspace ou projeto).
type GitWorktreeRow struct {
	TaskKey    string
	TaskHref   string
	TaskTitle  string
	TaskStatus string
	Branch     string
	GitHref    string // aba Git da task
	State      Badge
	Head       string
	Error      string
}

// WorkspaceGitVM é a aba Git de um workspace.
type WorkspaceGitVM struct {
	// Available: o operador ligou HANKO_GIT_WORKSPACES neste servidor.
	Available bool
	// Enabled: o Git deste workspace está ligado (há configuração).
	Enabled  bool
	RepoURL  string
	Mirror   Badge
	Base     string
	Head     string
	LastTime string
	LastErr  string
	// Credential é a referência ${secret:NOME} (o valor nunca chega aqui).
	Credential string
	Worktrees  []GitWorktreeRow
	// Ações.
	EnableAction  string
	SyncAction    string
	DisableAction string
	SecretsHref   string
}

// ProjectGitVM é a aba Git de um projeto: os worktrees das tasks dele.
type ProjectGitVM struct {
	HasWorkspace  bool
	GitEnabled    bool
	Available     bool
	WorkspaceHref string
	Rows          []GitWorktreeRow
}

// ProjectDefaultsVM é a aba Padrões de um projeto.
type ProjectDefaultsVM struct {
	Action       string
	Instructions string
	GoalOptions  []Option
	ImportSkills bool
	SkillsPath   string
	// HasRepo: o workspace do projeto tem Git ligado (o import de skills só vale com ele).
	HasRepo bool
	// Revisão: reaproveita a política do ADR-0040.
	ReviewAction    string
	ReviewSummary   string
	RequireOptions  []Option
	ReviewerOptions []Option
	Error           string
}

// GitDiffLine é uma linha do diff com a classe de cor.
type GitDiffLine struct {
	Kind string // add | del | hunk | meta | ctx
	Text string
}

// GitFileRow é um arquivo alterado no status.
type GitFileRow struct {
	XY   string
	Path string
}

// GitCommitRow é um commit do log.
type GitCommitRow struct {
	SHA     string
	Author  string
	When    string
	Subject string
}

// GitEntryRow é uma linha do browser de arquivos.
type GitEntryRow struct {
	Name string
	Href string
	Dir  bool
	Size string
}

// GitPathCrumb é um nível do caminho no browser de arquivos.
type GitPathCrumb struct {
	Name string
	Href string
}

// GitFileView é o arquivo aberto no browser.
type GitFileView struct {
	Path      string
	Size      string
	Content   string
	Binary    bool
	Truncated bool
}

// TaskGitVM é a página Git de uma task.
type TaskGitVM struct {
	Key      string
	Title    string
	TaskHref string
	Notice   string
	Error    string
	Tabs     []TabLink
	Tab      string // status | diff | log | arquivos

	Branch   string
	Base     string
	Head     string
	State    Badge
	Live     bool
	Ahead    int
	Behind   int
	Merged   bool
	WsHref   string
	WsName   string
	MirrorOK bool
	MirrorEr string
	WtError  string

	Files         []GitFileRow
	Clean         bool
	Diff          []GitDiffLine
	DiffStat      string
	DiffTruncated bool
	Log           []GitCommitRow
	Crumbs        []GitPathCrumb
	Entries       []GitEntryRow
	File          *GitFileView

	CleanAction string // admin
}
