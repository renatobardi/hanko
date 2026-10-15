package i18n

// enIlhas holds the English texts of the React islands (log viewer, org chart, anchored comments; ADR-0026) and of the
// CLI/MCP bits that go with them. The islands carry no catalog of their own: the host translates these with T and
// hands them over in data-msgs. Same rules as the other maps: the key is the pt-BR text, and a key already in
// the catalog is not repeated here (a duplicate panics at start-up).
var enIlhas = map[string]string{
	// log of a run
	"Buscar no log":                        "Search the log",
	"Filtrar por tipo de evento":           "Filter by event type",
	"ir para o fim":                        "jump to the end",
	"Pausado":                              "Paused",
	"Seguindo o fim":                       "Following the end",
	"Carregar eventos anteriores":          "Load earlier events",
	"Carregando…":                          "Loading…",
	"{total} eventos":                      "{total} events",
	"{shown} de {total} eventos":           "{shown} of {total} events",
	"Nenhum evento corresponde ao filtro.": "No event matches the filter.",
	"reconectando…":                        "reconnecting…",
	"Não foi possível carregar o log.":     "Could not load the log.",
	"Expandir":                             "Expand",
	"Recolher":                             "Collapse",
	"Payload":                              "Payload",
	// org chart
	"Controles do organograma": "Org chart controls",
	"Aproximar":                "Zoom in",
	"Afastar":                  "Zoom out",
	"Ajustar à tela":           "Fit to screen",
	"Expandir tudo":            "Expand all",
	"Arraste uma persona sobre outra para mudar quem ela reporta · Ctrl + roda aproxima · arraste o fundo para mover": "Drag a persona onto another to change who it reports to · Ctrl + wheel zooms · drag the background to pan",
	"Ctrl + roda aproxima · arraste o fundo para mover":                                                               "Ctrl + wheel zooms · drag the background to pan",
	"Solte aqui para mover ao topo (reporta a você)":                                                                  "Drop here to move to the top (reports to you)",
	"Recolher o ramo de {name}":                                                                                       "Collapse {name}'s branch",
	"Expandir o ramo de {name} ({n} escondidas)":                                                                      "Expand {name}'s branch ({n} hidden)",
	"{name} agora reporta a {to}.":                                                                                    "{name} now reports to {to}.",
	"{name} foi para o topo e reporta a você.":                                                                        "{name} moved to the top and reports to you.",
	"Não dá: {to} já está abaixo de {name}; isso criaria um ciclo.":                                                   "Not possible: {to} is already below {name}; that would create a cycle.",
	"Uma persona não reporta a si mesma.":                                                                             "A persona cannot report to itself.",
	"Não foi possível mover: {err}":                                                                                   "Could not move: {err}",
	// anchored comments
	"Texto do documento. Selecione um trecho para comentar.": "Document text. Select a passage to comment on it.",
	"Ver comentários deste trecho":                           "See the comments on this passage",
	"Comentar neste trecho":                                  "Comment on this passage",
	"Escreva o comentário sobre o trecho":                    "Write your comment on the passage",
	"Enviar":                                                 "Send",
	"Enviando…":                                              "Sending…",
	"Comentário enviado.":                                    "Comment sent.",
	"Comentários do trecho":                                  "Comments on the passage",
	"âncora perdida":                                         "anchor lost",
	"trecho movido":                                          "passage moved",
	"Comentários com âncora perdida":                         "Comments with a lost anchor",
	"Escreva algo antes de enviar.":                          "Write something before sending.",
	"Não foi possível enviar o comentário.":                  "Could not send the comment.",
	// CLI: hanko task comment --quote / task show
	"      sobre: “%s”%s\n":     "      about: “%s”%s\n",
	"(âncora perdida)":          "(anchor lost)",
	"--quote-run exige --quote": "--quote-run requires --quote",
	"ancora o comentário a este trecho da descrição da task (ou do resumo da execução, com --quote-run)": "anchors the comment to this passage of the task description (or of the run summary, with --quote-run)",
	"id da execução cujo resumo contém o trecho de --quote":                                              "id of the run whose summary contains the --quote passage",
}

func init() {
	for k, v := range enIlhas {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
