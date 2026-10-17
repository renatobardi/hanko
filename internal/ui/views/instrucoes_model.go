package views

// Modelos da tela de instruções da persona: edição com motivo, histórico de revisões, diff e restauração (ADR-0052).

// InstructionRow é uma revisão na lista.
type InstructionRow struct {
	Revision      int
	Origin        string // rótulo já traduzido
	Author        string
	Actor         string
	Reason        string
	Size          string
	SHA           string
	When          string
	ViewHref      string
	DiffHref      string // compara com a anterior ("" = é a primeira)
	RestoreAction string
	Current       bool
}

// InstructionView é o texto de uma revisão aberta.
type InstructionView struct {
	Revision  int
	Body      string
	CloseHref string
}

// InstructionProposal é uma proposta da própria persona esperando decisão.
type InstructionProposal struct {
	Title string
	Href  string
	When  string
}

// InstructionsVM é a página /personas/{p}/instrucoes.
type InstructionsVM struct {
	Name      string
	Slug      string
	Back      string // volta à página da persona
	Notice    string
	Error     string
	Revision  int    // a vigente
	Current   string // texto vigente, para o formulário
	EditURL   string // POST; vazio = só leitura
	Reason    string // motivo digitado (quando o envio falhou)
	Rows      []InstructionRow
	View      *InstructionView
	Diff      *DiffVM
	Proposals []InstructionProposal
}
