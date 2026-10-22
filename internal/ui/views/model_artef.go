package views

// Modelos de tela de artefatos e metas (ADR-0028). Tudo já vem formatado e traduzido pelos handlers.

// ---- artefatos ----

// ArtifactRow é uma linha da lista de artefatos (a versão mais nova de cada cadeia).
type ArtifactRow struct {
	Name        string
	Href        string
	Kind        string // rótulo do tipo
	Mime        string
	Size        string
	Version     int
	Versions    int
	TaskKey     string
	TaskHref    string
	TaskTitle   string
	Project     string
	ProjectHref string
	Persona     string
	PersonaHref string
	When        string
}

// ArtifactFilterVM é o formulário de filtros da lista.
type ArtifactFilterVM struct {
	Action         string
	Query          string
	Task           string
	ProjectOptions []Option
	PersonaOptions []Option
	KindOptions    []Option
	Active         bool
	ClearHref      string
}

// ArtifactsVM é a página /artefatos.
type ArtifactsVM struct {
	Subtitle string
	Notice   string
	NewHref  string
	Filters  ArtifactFilterVM
	Rows     []ArtifactRow
	Empty    bool // não há nenhum artefato (e nenhum filtro ligado)
	Chains   string
	Used     string
	Quota    string
	UsedPct  int
	More     bool   // a lista foi cortada no limite
	MoreHref string // próxima página
}

// ArtifactFormVM é o formulário de envio.
type ArtifactFormVM struct {
	Action         string
	CancelHref     string
	Task           string
	Name           string
	ProjectOptions []Option
	MaxFile        string // "5 MiB"
	Error          string
	FieldErr       map[string]string
}

// ArtifactVersionRow é uma versão na lista de versões do detalhe.
type ArtifactVersionRow struct {
	Version  int
	Href     string
	Size     string
	When     string
	Author   string
	Run      string
	RunHref  string
	SHA      string
	Current  bool
	DiffHref string // compara esta versão com a anterior ("" = sem diff)
	Compared bool   // é a base do diff mostrado
}

// DiffLine é uma linha do diff entre versões.
type DiffLine struct {
	Op      string // "+", "-", " " ou "~" (trecho igual omitido)
	Text    string
	OldNo   string
	NewNo   string
	Skipped int
}

// DiffVM é o diff mostrado no detalhe.
type DiffVM struct {
	FromVersion int
	ToVersion   int
	Added       int
	Removed     int
	Lines       []DiffLine
	Coarse      bool
	Cut         int // linhas omitidas no fim por passarem do teto da tela
	Same        bool
	Error       string
	CloseHref   string
}

// ArtifactVM é o detalhe de um artefato (uma versão e a cadeia dela).
type ArtifactVM struct {
	Name         string
	Mime         string
	Kind         string
	Size         string
	Version      int
	Latest       bool
	LatestHref   string
	SHA          string
	Created      string
	Author       string
	TaskKey      string
	TaskHref     string
	TaskTitle    string
	Project      string
	ProjectHref  string
	Persona      string
	PersonaHref  string
	Run          string
	RunHref      string
	ContentHref  string
	DownloadHref string
	Preview      string // markdown | text | image | none
	PreviewHTML  HTML
	PreviewText  string
	Truncated    bool
	Versions     []ArtifactVersionRow
	Diff         *DiffVM
	DeleteHref   string
	Notice       string
	Error        string
	ListHref     string
}

// ArtifactMini é um artefato citado na página de uma task, de uma run ou de um projeto.
type ArtifactMini struct {
	Name     string
	Href     string
	Version  int
	Versions int
	Size     string
	Kind     string
	When     string
}

// ---- metas ----

// GoalRow é uma linha da lista de metas.
type GoalRow struct {
	Title       string
	Href        string
	Badge       Badge
	Progress    int
	ProgressTxt string // "65%"
	Values      string // "65 de 100 seg."
	Due         string
	Overdue     bool
	Project     string
	ProjectHref string
	Owner       string
	OwnerHref   string
	Tasks       string // "2 abertas · 3 no total"
	Metric      string
	Depth       int    // nível na árvore de metas (ADR-0042): 1 = raiz
	Subs        string // "3 submetas" quando o progresso é o agregado delas
}

// GoalsVM é a página /metas.
type GoalsVM struct {
	Subtitle  string
	Notice    string
	NewHref   string
	Tabs      []TabLink
	Rows      []GoalRow
	Empty     bool
	Active    int
	Achieved  int
	Abandoned int
	Overdue   int
	Project   string // slug do filtro por projeto ("" = todos)
	ClearHref string
}

// GoalFormVM é o formulário de meta (criar e editar).
type GoalFormVM struct {
	Action         string
	CancelHref     string
	Edit           bool
	Title          string
	Description    string
	Metric         string
	Unit           string
	Start          string
	Target         string
	Current        string
	Due            string
	ProjectOptions []Option
	OwnerOptions   []Option
	ParentOptions  []Option // meta-pai (árvore de metas); vazio = só raízes possíveis
	Error          string
	FieldErr       map[string]string
}

// ChartPoint é um ponto do gráfico de progresso.
type ChartPoint struct {
	X, Y  string
	Title string
}

// ChartVM é o gráfico SVG de histórico, já em coordenadas (o servidor calcula; sem script).
type ChartVM struct {
	Width, Height int
	// PlotLeft, PlotRight, PlotTop, PlotBottom são as bordas da área do gráfico, em coordenadas do SVG.
	PlotLeft, PlotRight, PlotTop, PlotBottom string
	Polyline                                 string
	Area                                     string
	Points                                   []ChartPoint
	TargetY                                  string
	StartY                                   string
	TargetLabel                              string
	StartLabel                               string
	XFirst, XLast                            string
	Empty                                    bool
	Summary                                  string // texto para leitores de tela
}

// GoalUpdateRow é um registro do histórico de progresso.
type GoalUpdateRow struct {
	Value  string
	Pct    string
	Note   string
	Author string
	When   string
}

// GoalVM é a página de uma meta.
type GoalVM struct {
	Title       string
	Description HTML
	Badge       Badge
	Status      string
	Progress    int
	ProgressTxt string
	Metric      string
	Start       string
	Current     string
	Target      string
	Due         string
	Overdue     bool
	Project     string
	ProjectHref string
	Owner       string
	OwnerHref   string
	Achieved    string // "atingida em ..."
	Statuses    []StatusCount
	TasksTotal  int
	TasksHref   string
	Tasks       []TaskRow
	Chart       ChartVM
	Updates     []GoalUpdateRow
	Path        []Crumb    // cadeia de ancestrais, da raiz até o pai (ADR-0042)
	Subgoals    []GoalMini // submetas diretas
	Rollup      bool       // o progresso é o agregado das submetas
	CanProgress bool       // a meta está ativa
	ProgressURL string
	Value       string
	Note        string
	StatusURL   string
	StatusTo    string // abandoned | active
	StatusLabel string
	LinkURL     string
	LinkTask    string
	ListHref    string
	Notice      string
	Error       string
	Form        GoalFormVM
}

// GoalMini é uma meta citada na página de um projeto.
type GoalMini struct {
	Title       string
	Href        string
	Badge       Badge
	Progress    int
	ProgressTxt string
	Due         string
	Overdue     bool
}

// AchievedGoalRow é uma meta atingida mostrada (só informativa) na Caixa de entrada.
type AchievedGoalRow struct {
	Title string
	Href  string
	Text  string
	When  string
	Ctl   InboxCtl
}
