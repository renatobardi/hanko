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
	Hint         string // "Até 64 MiB por arquivo, 10 arquivos e 128 MiB por envio."
	AllHref      string
}

// DraftFields liga uma caixa de texto ao rascunho do servidor: o autosave por htmx e o texto inicial.
type DraftFields struct {
	Action string // PUT que grava o rascunho ("" = sem rascunho)
	Text   string // rascunho salvo, já dentro do campo
}

// livePoll é o hx-trigger das conversas que se atualizam sozinhas (chat, sala, task): a cada intervalo, menos durante
// um envio do compositor (o hanko.js também cancela uma atualização em andamento quando o envio sai). Uma resposta
// atrasada trocaria a lista nova por uma velha: o htmx escolhe o alvo quando a requisição sai.
func livePoll(every string) string {
	return "every " + every + " [!document.querySelector('form[data-composer].htmx-request')]"
}
