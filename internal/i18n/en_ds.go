package i18n

// enDS holds the English texts added with the Kubo DS adherence pass (persona glyph, theme toggle,
// task board). Same rules as en.go: the key is the pt-BR text, never repeated across files.
var enDS = map[string]string{
	"7 dias":      "7 days",
	"Sem persona": "No persona",
	"Timeline":    "Timeline",
	"Quem trabalhou em quê, e quando. Cada barra é uma execução; clique para abrir o log.": "Who worked on what, and when. Each bar is a run; click to open its log.",
	"Tempo de trabalho":            "Work time",
	"Nenhuma execução no período.": "No runs in this period.",
	"Quando uma persona trabalhar, a execução aparece aqui como uma barra na faixa dela.": "When a persona works, the run shows up here as a bar on its lane.",
	"Timeline das execuções": "Run timeline",
	"Preto: rodando · cinza: concluída · vermelho: falhou · tracejado: interrompida, cancelada ou expirada.": "Black: running · gray: done · red: failed · dashed: interrupted, cancelled or expired.",
	"Sem workspace.": "No workspace.",
	"Sem workspace, as execuções do projeto usam o workspace da persona ou um diretório temporário.": "Without a workspace, the project runs use the persona workspace or a temporary directory.",
	"Escolher workspace": "Choose workspace",
	"Abrir workspace":    "Open workspace",
	"O teto mensal do projeto é definido na aba Configuração.":                                       "The project monthly cap is set in the Settings tab.",
	"Nenhuma execução neste workspace.":                                                              "No runs in this workspace.",
	"As execuções aparecem aqui com o log completo assim que uma persona trabalhar neste diretório.": "Runs show up here with the full log as soon as a persona works in this directory.",
	"Minhas":                          "Mine",
	"Todas as prioridades":            "All priorities",
	"Agrupar por status":              "Group by status",
	"Agrupar por prioridade":          "Group by priority",
	"Agrupar por persona":             "Group by persona",
	"Agrupar por projeto":             "Group by project",
	"Sem agrupar":                     "No grouping",
	"Atualizadas primeiro":            "Recently updated",
	"Criadas primeiro":                "Recently created",
	"Maior prioridade primeiro":       "Highest priority first",
	"Título (A–Z)":                    "Title (A–Z)",
	"Agrupar":                         "Group",
	"Ordenar":                         "Sort",
	"Aplicar":                         "Apply",
	"Sem projeto":                     "No project",
	"Claro":                           "Light",
	"Escuro":                          "Dark",
	"Tema":                            "Theme",
	"tema desconhecido":               "unknown theme",
	"Nada aqui.":                      "Nothing here.",
	"Quadro":                          "Board",
	"Visualização":                    "View",
	"Escolha um dos ícones da lista.": "Pick one of the icons in the list.",
	"Identifica a persona em listas, chats e execuções.": "Identifies the persona in lists, chats and runs.",
	"Salvar ícone":      "Save icon",
	"Ícone":             "Icon",
	"Ícone atualizado.": "Icon updated.",
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
