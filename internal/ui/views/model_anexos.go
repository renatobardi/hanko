package views

// Modelos de anexos e rascunhos (ADR-0052).

// AttachmentRow é um anexo numa lista (task, comentário, mensagem).
type AttachmentRow struct {
	ID           string
	Name         string
	Href         string // conteúdo (inline só para tipos seguros; o resto baixa)
	DetailHref   string
	Size         string
	Kind         string
	Author       string
	When         string
	DeleteAction string // POST que apaga ("" = sem permissão de apagar)
}

// AttachBox é o cartão de anexos de uma task, com a zona de soltar arquivos.
type AttachBox struct {
	Rows         []AttachmentRow
	UploadAction string // POST multipart; vazio = só leitura
	Hint         string // "Até 5 MiB por arquivo, 10 arquivos por envio."
	AllHref      string
}

// DraftFields liga uma caixa de texto ao rascunho do servidor: o autosave por htmx e o texto inicial.
type DraftFields struct {
	Action string // PUT que grava o rascunho ("" = sem rascunho)
	Text   string // rascunho salvo, já dentro do campo
}
