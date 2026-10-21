package views

// Gates estendidos (ADR-0041): pergunta, reenvio com revisões, contratação com ficha, incidentes de orçamento,
// persona principal e política de plano.

// QuestionOptionVM é uma opção de um campo de escolha.
type QuestionOptionVM struct {
	Value string
	Label string
}

// QuestionFieldVM é um campo do formulário de uma pergunta.
type QuestionFieldVM struct {
	Key      string
	Label    string
	Type     string // single | multi | text
	Help     string
	Required bool
	Options  []QuestionOptionVM
}

// AnswerLine é uma resposta já dada (rótulo do campo → valores legíveis).
type AnswerLine struct {
	Label string
	Value string
}

// QuestionVM é o formulário de um gate question e, depois, a resposta.
type QuestionVM struct {
	Fields      []QuestionFieldVM
	Answers     []AnswerLine
	AnswerHref  string
	DismissHref string
	ReopenHref  string
	Dismissed   bool
}

// HireVM resume a ficha de um gate hire.
type HireVM struct {
	Name, Slug, Title, Agent, Model string
	Reports                         string
	Budget                          string
	Instructions                    string
	RequireReview                   bool
}

// GateRevisionVM é uma versão anterior de um gate reenviado.
type GateRevisionVM struct {
	Revision int
	When     string
	Note     string // o que o humano pediu ao devolvê-la
	Body     HTML
}

// GateHistoryVM são as versões anteriores e o diff até a vigente.
type GateHistoryVM struct {
	Revision  int
	Revisions []GateRevisionVM
	Diff      string
	Added     int
	Removed   int
}

// InboxIncident é um incidente de orçamento na Caixa.
type InboxIncident struct {
	Scope       string // "empresa" | "projeto" | "persona"
	Target      string
	TargetHref  string
	Month       string
	Spent       string
	Budget      string
	When        string
	ResolveHref string
	Released    int // runs avulsos liberados e ainda não consumidos
}

// PersonaPlusVM são os controles do M15 na página da persona.
type PersonaPlusVM struct {
	Enabled       bool
	System        bool
	PrimaryHref   string // POST que escolhe/limpa a principal ("" = sem usuário autenticado)
	IsPrimary     bool
	SystemHref    string // POST do owner que marca/desmarca
	PlanHref      string // POST da política de plano da persona
	PlanOptions   []Option
	PlanSummary   string
	CanEditSystem bool
}

// CoordPlan é a política de plano (plan-review) no painel da task.
type CoordPlan struct {
	Enabled bool
	Summary string
	Action  string
	Options []Option // herdar | exigir | dispensar
}

// CompanyPolicyVM é a política de contratação por gate (tela de membros).
type CompanyPolicyVM struct {
	Enabled bool
	On      bool
	Action  string
}
