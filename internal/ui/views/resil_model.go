package views

// ---- resiliência de run (ADR-0039) ----

// DiagVM é o card de diagnóstico de uma execução que parou: causa, tentativa, próxima ação e dono, mais as ações do
// operador quando ela espera uma decisão.
type DiagVM struct {
	Cause         string
	Detail        string
	Attempt       string // "continuação 1 de 2"
	NextAction    string
	Owner         string
	Uncertain     string // a tool de efeito externo em andamento quando a execução parou ("" = nenhuma)
	Exhausted     bool
	NeedsDecision bool
	Decided       string // a decisão tomada, quando já houve ("" = não)
	Note          string
	ActionHref    string // POST: Tentar de novo / Continuar / Encerrar
	Back          string // para onde voltar depois da ação (caminho dentro da empresa)
	OfHref        string // execução que esta continua
	OfName        string
	ByHref        string // execução que continuou esta
	ByName        string
	Reason        string // por que esta execução é continuação ("" = não é)
}

// SteerVM é a caixa de "mudar o rumo" de uma execução ativa.
type SteerVM struct {
	Href      string
	Mode      string // inject | restart
	ModeLabel string // "injeta ao vivo" / "interrompe e continua"
	Hint      string
	Back      string
}

// FollowupItem é um item da fila de follow-ups (chat + task).
type FollowupItem struct {
	ID          string
	Pos         int
	Body        string
	Delivery    string // rótulo: "na fila", "injetar ao vivo", "interromper e continuar"
	Wait        string // motivo da espera, já escrito
	WaitKey     string // run_active, steer_pending…
	Edited      bool
	EditHref    string
	DiscardHref string
}

// FollowupsVM é o painel da fila de follow-ups de uma task ou conversa.
type FollowupsVM struct {
	Items   []FollowupItem
	Back    string
	AddHref string // POST: enfileirar (só na task; vazio no chat, onde o compositor já enfileira)
	Steer   *SteerVM
	Active  bool // há execução ativa
	Key     string
}
