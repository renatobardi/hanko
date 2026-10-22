package i18n

// enFixComposer holds the English texts of the composer fixes (chat, room and task comments): errors that used to
// vanish now show in the page's flash.
var enFixComposer = map[string]string{
	"Sem conexão com o servidor: nada foi enviado. Confira a rede e tente de novo.": "No connection to the server: nothing was sent. Check the network and try again.",
	"O servidor não conseguiu concluir o pedido. Tente de novo em instantes.":       "The server could not complete the request. Try again in a moment.",
}

func init() {
	for k, v := range enFixComposer {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
