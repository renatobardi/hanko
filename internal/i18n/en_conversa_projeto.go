package i18n

// enConversaProjeto holds the English texts of the project of a conversation (ADR-0090): the chat and room forms and
// the chat page chip.
var enConversaProjeto = map[string]string{
	"A persona trabalha no workspace e no ambiente do projeto quando não tem os seus.":    "The persona works in the project's workspace and environment when it has none of its own.",
	"As personas trabalham no workspace e no ambiente do projeto quando não têm os seus.": "The personas work in the project's workspace and environment when they have none of their own.",
	"Escolha um projeto da lista.": "Pick a project from the list.",
	"projeto da nova conversa (id ou slug): as execuções usam o workspace e o ambiente dele quando a persona não tem os seus": "project of the new conversation (id or slug): runs use its workspace and environment when the persona has none of its own",
}

func init() {
	for k, v := range enConversaProjeto {
		if _, dup := en[k]; !dup {
			en[k] = v
		}
	}
}
