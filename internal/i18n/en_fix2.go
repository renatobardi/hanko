package i18n

// enFix2 holds the English texts added by the review fixes (chat, memory consolidation, projects/workspaces).
// init merges them into the catalog; same rules as en.go (the key is the pt-BR text, and a key that is
// already translated is not repeated here).
var enFix2 = map[string]string{
	"Abrir conversa":           "Open conversation",
	"fora da pasta da empresa": "outside the company folder",
	"O caminho está fora da pasta da empresa na raiz dos workspaces; ele segue funcionando, mas novos caminhos precisam ficar dentro dela.": "The path is outside the company folder in the workspaces root; it keeps working, but new paths must be inside it.",
	"Este caminho está fora da pasta da empresa (%s). Ele segue funcionando, mas ao editar o caminho ele precisa ficar dentro dela.":        "This path is outside the company folder (%s). It keeps working, but when you edit the path it must be inside it.",
	"Mensagens esperando resposta: %d. Envie uma nova mensagem para a persona recolhê-las.":                                                 "Messages waiting for an answer: %d. Send a new message so the persona picks them up.",
}

func init() {
	for k, v := range enFix2 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
