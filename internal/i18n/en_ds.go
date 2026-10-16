package i18n

// enDS holds the English texts added with the Kubo DS adherence pass (persona glyph, theme toggle,
// task board). Same rules as en.go: the key is the pt-BR text, never repeated across files.
var enDS = map[string]string{
	"Claro":                                              "Light",
	"Escuro":                                             "Dark",
	"Tema":                                               "Theme",
	"tema desconhecido":                                  "unknown theme",
	"Nada aqui.":                                         "Nothing here.",
	"Quadro":                                             "Board",
	"Visualização":                                       "View",
	"Escolha um dos ícones da lista.":                     "Pick one of the icons in the list.",
	"Identifica a persona em listas, chats e execuções.": "Identifies the persona in lists, chats and runs.",
	"Salvar ícone":                                       "Save icon",
	"Ícone":                                              "Icon",
	"Ícone atualizado.":                                  "Icon updated.",
	"ícone da persona (bot, code, search, pen-line, shield-check…; padrão bot)": "persona icon (bot, code, search, pen-line, shield-check…; default bot)",
}

func init() {
	for k, v := range enDS {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
