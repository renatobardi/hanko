package i18n

// enFix2 holds the English texts added by the review fixes (chat, memory consolidation, projects/workspaces).
// init merges them into the catalog; same rules as en.go (the key is the pt-BR text, and a key that is
// already translated is not repeated here).
var enFix2 = map[string]string{
	"Abrir conversa": "Open conversation",
	"Mensagens esperando resposta: %d. Envie uma nova mensagem para a persona recolhê-las.": "Messages waiting for an answer: %d. Send a new message so the persona picks them up.",
}

func init() {
	for k, v := range enFix2 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
