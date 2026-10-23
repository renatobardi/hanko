package i18n

// enFix3 holds the English texts added by the independent review fixes (search/islands) and the composer fixes.
var enFix3 = map[string]string{
	"A busca demorou demais; use palavras mais específicas.": "The search took too long; use more specific words.",
	// composers (chat, room, task comments): errors that used to vanish now show in the page's flash
	"Sem conexão com o servidor: nada foi enviado. Confira a rede e tente de novo.": "No connection to the server: nothing was sent. Check the network and try again.",
	"O servidor não conseguiu concluir o pedido. Tente de novo em instantes.":       "The server could not complete the request. Try again in a moment.",
	// compositor: Enter envia, alfinete, setas, colar (ADR-0066 adendo)
	"Enter envia · Shift+Enter quebra a linha · Markdown · /new reinicia o contexto": "Enter sends · Shift+Enter for a new line · Markdown · /new resets the context",
	"Enter envia · Shift+Enter quebra a linha · Markdown":                            "Enter sends · Shift+Enter for a new line · Markdown",
	"Enter envia · Shift+Enter quebra a linha · /new reinicia o contexto":            "Enter sends · Shift+Enter for a new line · /new resets the context",
	"Caixa fixada embaixo: clique para soltar e deixá-la rolar junto com a conversa": "Box pinned to the bottom: click to release it and let it scroll with the conversation",
	"Fixado":                   "Pinned",
	"Ir ao início da conversa": "Go to the start of the conversation",
	"Ir ao fim da conversa":    "Go to the end of the conversation",
	"Remover anexo":            "Remove attachment",
	// uploads (ADR-0065): a soma dos arquivos de um envio tem teto
	"o envio passa de %s; mande os arquivos em partes": "the upload is over %s; send the files in parts",
}

func init() {
	for k, v := range enFix3 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
