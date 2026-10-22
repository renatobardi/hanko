package views

// Etapas do projeto (ADR-0048): a aba "Etapas" do projeto, o painel da task e os cards de status no Painel.

// StageFormRow é uma linha do formulário de etapas: uma etapa existente ou uma linha em branco para acrescentar.
type StageFormRow struct {
	N             int
	Key           string
	Name          string
	Instructions  string
	Tasks         int
	StatusOptions []Option
	PersonaOption []Option
	GateOptions   []Option
}

// StageLearningRow é um aprendizado de etapa (decisão de gate com nota).
type StageLearningRow struct {
	When     string
	Stage    string
	TaskKey  string
	TaskHref string
	Decision string
	Class    string
	Note     string
}

// ProjectStagesVM é a aba Etapas do projeto.
type ProjectStagesVM struct {
	Action    string // POST do formulário (admin)
	CanEdit   bool
	Error     string
	Empty     bool // o projeto ainda não tem etapas
	Rows      []StageFormRow
	Learnings []StageLearningRow
}

// TaskStageVM é o painel "Etapa do projeto" da task.
type TaskStageVM struct {
	Show        bool // a task pertence a um projeto com etapas
	InPipeline  bool
	CanWrite    bool
	StageName   string
	Position    int
	Total       int
	State       string
	StateClass  string
	Persona     string
	Status      string
	Next        string
	ExitGate    string
	Learnings   []StageLearningRow
	StartAction string
	StartStages []Option // etapas de entrada possíveis
	CompleteURL string
	AdvanceURL  string
	LeaveURL    string
	BackOptions []Option // etapas para as quais voltar
	CanComplete bool
	History     []StageHistoryRow
}

// StageHistoryRow é uma linha do histórico do pipeline da task.
type StageHistoryRow struct {
	When string
	Text string
	Note string
}

// DashboardCardVM é um card de status no Painel.
type DashboardCardVM struct {
	Title        string
	Href         string // o card
	Summary      HTML   // markdown já renderizado
	Updated      string
	Next         string
	Revision     string
	Engine       string
	ArtifactHref string
	ArtifactName string
	State        string
}
