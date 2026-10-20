package views

// Coordenação de tasks na UI (ADR-0040): árvore, dependências, pausa herdada e política de revisão.

// CoordCrumb é um ancestral no breadcrumb da task.
type CoordCrumb struct {
	Key   string
	Href  string
	Title string
}

// CoordRow é uma task numa lista curta (dependência, dependente ou subtask).
type CoordRow struct {
	Key    string
	Href   string
	Title  string
	Badge  Badge
	Who    string
	Closed bool
	// RemoveHref remove a dependência (só nas linhas "depende de").
	RemoveHref string
}

// CoordHold é a pausa efetiva da task.
type CoordHold struct {
	Reason     string
	By         string
	When       string
	OriginKey  string
	OriginHref string
	Inherited  bool
	// ResumeHref aparece só quando a pausa foi gravada nesta task (a herdada se retoma na origem).
	ResumeHref string
}

// CoordReview resume a política de revisão e deixa um admin alterar a sobreposição da task.
type CoordReview struct {
	Summary         string // "Revisão obrigatória (origem: projeto) · revisor: persona X (origem: persona)"
	CanEdit         bool
	Action          string
	RequireOptions  []Option // herdar | exigir | dispensar
	ReviewerOptions []Option // herdar | nenhum | personas
}

// TaskCoordVM junta tudo o que a página da task mostra da coordenação.
type TaskCoordVM struct {
	Crumbs      []CoordCrumb
	Hold        *CoordHold
	Blocked     bool
	OpenDeps    int
	DependsOn   []CoordRow
	Dependents  []CoordRow
	Children    []CoordRow
	ChildrenOpn int
	ParentKey   string
	// Actions (só para quem pode escrever).
	AddDepHref     string
	ParentHref     string
	HoldHref       string
	ForceCloseHref string
	NewSubtaskHref string
	Review         CoordReview
	// CanForceClose: há subtask aberta e a task ainda está aberta (mostra o override humano com motivo).
	CanForceClose bool
}

// InboxHeld é uma raiz de pausa na Caixa de entrada.
type InboxHeld struct {
	Key        string
	Href       string
	Title      string
	Persona    string
	Reason     string
	By         string
	When       string
	ResumeHref string
	Ctl        InboxCtl
}
