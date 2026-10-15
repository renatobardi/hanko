package i18n

// enBusca holds the English texts of the company-wide search (ADR-0024): the Buscar screen, the
// `hanko search` command and the hanko_search tool (the tool's own text is not translated: it is
// read by models). Same rules as en.go: the key is the pt-BR text and a key already translated
// elsewhere is not repeated here (a duplicate panics at start-up and fails the tests).
var enBusca = map[string]string{
	// UI
	"Uma busca só em tasks, comentários, personas, execuções, fluxos e na memória da empresa.": "One search across tasks, comments, personas, runs, flows and the company's memory.",
	"Buscar tasks, comentários, personas, execuções, fluxos...":                                "Search tasks, comments, personas, runs, flows...",
	"Atalho para focar a busca": "Shortcut to focus the search",
	"Busque em tudo da empresa": "Search everything in the company",
	"Digite uma palavra: a busca olha tasks, comentários, personas, execuções, fluxos e a memória. Use \"frase exata\", -excluir e OR; a chave de uma task (HNK-12) vai direto a ela.": "Type a word: the search looks at tasks, comments, personas, runs, flows and memory. Use \"exact phrase\", -exclude and OR; a task key (HNK-12) goes straight to it.",
	"Nada para buscar ainda":                                                                 "Nothing to search yet",
	"Use ao menos uma palavra (letras ou números) na busca.":                                 "Use at least one word (letters or digits) in the search.",
	"Nenhum resultado para “%s”. Tente outro termo ou menos palavras.":                       "No results for “%s”. Try another term or fewer words.",
	"%d resultados para “%s”":                                                                "%d results for “%s”",
	"A memória não respondeu agora. Os outros resultados valem; tente de novo em instantes.": "Memory did not answer right now. The other results still hold; try again in a moment.",
	"Comentários":      "Comments",
	"Memória":          "Memory",
	"Comentário de %s": "Comment by %s",
	"Pausado":          "Paused",

	// CLI
	"<termo...>": "<term...>",
	"Busca na empresa: tasks, comentários, personas, execuções, fluxos e memória": "Searches the company: tasks, comments, personas, runs, flows and memory",
	"Busca em tudo da empresa de uma vez. Várias palavras = todas; \"frase exata\"; -excluir; OR entre alternativas; a última palavra casa por prefixo. A chave exata de uma task (HNK-12) vai direto a ela.": "Searches everything in the company at once. Several words = all of them; \"exact phrase\"; -exclude; OR between alternatives; the last word matches by prefix. A task's exact key (HNK-12) goes straight to it.",
	"só estes tipos: task, comment, persona, run, flow, memory (repita ou separe por vírgula)":                                                                                                                "only these types: task, comment, persona, run, flow, memory (repeat or comma-separate)",
	"máximo de resultados por tipo": "maximum results per type",
	"%s: indisponível agora\n":      "%s: unavailable right now\n",
}

func init() {
	for k, v := range enBusca {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
