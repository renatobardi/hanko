package i18n

// enM16a holds the English texts of the per-person Inbox, notifications, "Mine" views and goal tree (ADR-0042). The file
// name sorts late so that its init runs after the other catalogs; a key another catalog already translated is kept
// as it is (parallel branches may add the same text).
var enM16a = map[string]string{
	// search (extended types)
	"Uma busca só em tasks, comentários, personas, execuções, fluxos, metas, projetos, gates, artefatos e na memória da empresa.": "One search across tasks, comments, personas, runs, flows, goals, projects, gates, artifacts and the company's memory.",
	"Busca na empresa: tasks, comentários, personas, execuções, fluxos, metas, projetos, gates, artefatos e memória":              "Searches the company: tasks, comments, personas, runs, flows, goals, projects, gates, artifacts and memory",
	"só estes tipos: task, comment, persona, run, flow, goal, project, gate, artifact, memory (repita ou separe por vírgula)":     "only these types: task, comment, persona, run, flow, goal, project, gate, artifact, memory (repeat or comma-separate)",

	// goal tree
	"\nSubmetas (%d):\n": "\nSub-goals (%d):\n",
	"Cadeia":             "Chain",
	"meta-pai (id ou slug): põe a meta na árvore, até 4 níveis": "parent goal (id or slug): puts the goal in the tree, up to 4 levels",
	"nova meta-pai (id ou slug)":                                "new parent goal (id or slug)",
	"torna a meta uma raiz":                                     "makes the goal a root",
	"%d submetas":                                               "%d sub-goals",
	"Meta-pai inválida: ela precisa existir, não estar abandonada, não descender desta meta e deixar no máximo 4 níveis.": "Invalid parent goal: it must exist, not be abandoned, not descend from this goal and leave at most 4 levels.",
	"Nenhuma (meta raiz)": "None (root goal)",
	"Meta-pai":            "Parent goal",
	"O progresso desta meta é a média das submetas; registre o valor medido nelas.": "This goal's progress is the average of its sub-goals; record the measured value on them.",
	"Opcional. Até 4 níveis; o progresso da meta-pai é a média das submetas.":       "Optional. Up to 4 levels; the parent goal's progress is the average of its sub-goals.",
	"Submetas": "Sub-goals",

	// inbox per person
	"\n%d aguardando, %d não lidos, %d arquivados\n": "\n%d waiting, %d unread, %d archived\n",
	"%d item(ns) atualizado(s)\n":                    "%d item(s) updated\n",
	"<item...>":                                      "<item...>",
	"<tipo>":                                         "<kind>",
	"A Caixa está vazia.":                            "The Inbox is empty.",
	"Arquiva itens: some da sua Caixa, a task e o gate seguem como estão": "Archives items: they leave your Inbox, the task and the gate stay as they are",
	"Caixa de entrada: o que espera por você (lido e arquivado são seus)": "Inbox: what is waiting for you (read and archived are yours)",
	"Devolve itens arquivados à Caixa":                                    "Puts archived items back in the Inbox",
	"Lista a Caixa (padrão: tudo que não está arquivado)":                 "Lists the Inbox (default: everything not archived)",
	"Marca como lidos todos os itens visíveis":                            "Marks every visible item as read",
	"Marca itens como lidos":                                              "Marks items as read",
	"Marca itens como não lidos":                                          "Marks items as unread",
	"all (padrão), unread ou archived":                                    "all (default), unread or archived",
	"item inválido %q: use tipo:id, como mostra `hanko inbox list`":       "invalid item %q: use kind:id, as `hanko inbox list` shows",
	"lido":      "read",
	"novo":      "new",
	"Não lidos": "Unread",
	"Não lido":  "Unread",
	"Arquivar: sai da sua Caixa, a task e o gate seguem como estão": "Archive: it leaves your Inbox, the task and the gate stay as they are",
	"Devolver à Caixa":      "Put back in the Inbox",
	"Marcar como lida":      "Mark as read",
	"Marcar como não lida":  "Mark as unread",
	"Marcar tudo como lido": "Mark all as read",
	"Menção":                "Mention",
	"Menções":               "Mentions",
	"Responder":             "Reply",
	"%d não lidos":          "%d unread",

	// notifications
	"Avisos: toast e web push por tipo":                                        "Notices: toast and web push by kind",
	"Aviso de teste enviado (conforme as preferências do tipo mention).":       "Test notice sent (according to the mention preferences).",
	"Define os canais de um tipo de aviso: mention, gate, run_failed ou alert": "Sets the channels of a notice kind: mention, gate, run_failed or alert",
	"Envia um aviso de teste para você":                                        "Sends you a test notice",
	"Mostra suas preferências de aviso nesta empresa":                          "Shows your notice preferences in this company",
	"aviso ao vivo na tela":                                                    "live notice on screen",
	"notificação do navegador/PWA":                                             "browser/PWA notification",
	"Alguém citou você com @ num comentário.":                                  "Someone mentioned you with @ in a comment.",
	"Aparelho removido.":                                                       "Device removed.",
	"Aviso de teste enviado conforme as suas preferências de menção.":          "Test notice sent according to your mention preferences.",
	"Gates para decidir":                                                       "Gates to decide",
	"Notificações":                                                             "Notifications",
	"Persona silenciosa, execução presa, orçamento perto do teto.":             "Silent persona, stuck run, budget near the ceiling.",
	"Preferências salvas.":                                                     "Preferences saved.",
	"Um gate novo espera por um humano.":                                       "A new gate is waiting for a human.",
	"Uma persona não conseguiu terminar o trabalho.":                           "A persona could not finish the work.",
	"Ao vivo na tela":                                                          "Live on screen",
	"Aparelho":                                                                 "Device",
	"Aparelhos com notificação":                                                "Devices with notifications",
	"Ativar neste aparelho":                                                    "Enable on this device",
	"Ative para receber a notificação neste navegador ou no app instalado (PWA). O navegador vai pedir a sua permissão.":                                                                                                                                 "Enable it to get notifications in this browser or in the installed app (PWA). The browser will ask for your permission.",
	"Como você quer ser avisado: um aviso ao vivo no canto da tela (enquanto o Hanko está aberto) e a notificação do navegador ou do app instalado, mesmo com a aba fechada. As menções e itens da Caixa chegam sempre; aqui você escolhe só os avisos.": "How you want to be notified: a live notice in the corner of the screen (while Hanko is open) and the browser or installed-app notification, even with the tab closed. Mentions and Inbox items always arrive; here you only choose the notices.",
	"Enviar aviso de teste": "Send test notice",
	"Este aparelho":         "This device",
	"Este navegador não oferece notificações push.":           "This browser does not offer push notifications.",
	"Notificação do navegador":                                "Browser notification",
	"Não foi possível ativar as notificações neste aparelho.": "Could not enable notifications on this device.",
	"O servidor não tem HANKO_SECRET_KEY: sem ela não há onde guardar a chave do web push, então só o aviso ao vivo funciona.": "The server has no HANKO_SECRET_KEY: without it there is nowhere to keep the web push key, so only the live notice works.",
	"Os avisos são de cada pessoa: entre com a sua conta para configurá-los.":                                                  "Notices are personal: sign in with your account to set them up.",
	"Permissão negada no navegador.": "Permission denied in the browser.",
	"Salvar preferências":            "Save preferences",
	"ao vivo na tela":                "live on screen",
	"notificação do navegador":       "browser notification",
	"Último envio":                   "Last sent",

	"@ cita uma persona (@slug) ou uma pessoa (@parte-do-e-mail) e avisa": "@ mentions a persona (@slug) or a person (@email-local-part) and notifies them",

	// "Mine" views and activity by actor
	"PARTICIPAÇÃO": "PARTICIPATION",
	"só as tasks em que você participa: criou, atribuiu, foi mencionado, comentou ou é da audiência de um gate": "only tasks you take part in: created, assigned, were mentioned, commented, or are in a gate's audience",
	"só os gates da sua audiência: que você pediu ou decidiu, ou de tasks em que participa":                     "only gates in your audience: that you requested or decided, or on tasks you take part in",
	"atribuí":                       "I assigned",
	"audiência de gate":             "gate audience",
	"comentei":                      "I commented",
	"criei":                         "I created",
	"fui mencionado":                "I was mentioned",
	"Nenhuma atividade deste ator.": "No activity from this actor.",
	"Gates que você pediu ou decidiu, ou de tasks em que você participa.": "Gates you requested or decided, or on tasks you take part in.",
	"Minha audiência":                          "My audience",
	"Atividade da empresa, filtrável por ator": "Company activity, filterable by actor",
	"FATO":               "FACT",
	"SUJEITO":            "SUBJECT",
	"Nenhuma atividade.": "No activity.",
	"máximo de eventos":  "maximum events",
	"quem causou o fato: system, persona:<slug>, human:<e-mail> (ou persona/human para todos do tipo)": "who caused the fact: system, persona:<slug>, human:<email> (or persona/human for everyone of the kind)",
	"tipo do sujeito (task, run, gate, goal...)":                                                       "subject type (task, run, gate, goal...)",
}

func init() {
	for k, v := range enM16a {
		if _, dup := en[k]; !dup {
			en[k] = v
		}
	}
}
