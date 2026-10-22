package i18n

// enFix3 holds the English texts added by the independent review fixes (search/islands) and the composer fixes.
var enFix3 = map[string]string{
	"A busca demorou demais; use palavras mais específicas.": "The search took too long; use more specific words.",
	// composers (chat, room, task comments): errors that used to vanish now show in the page's flash
	"Sem conexão com o servidor: nada foi enviado. Confira a rede e tente de novo.": "No connection to the server: nothing was sent. Check the network and try again.",
	"O servidor não conseguiu concluir o pedido. Tente de novo em instantes.":       "The server could not complete the request. Try again in a moment.",
}

func init() {
	for k, v := range enFix3 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
