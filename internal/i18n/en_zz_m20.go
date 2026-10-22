package i18n

// enM20 holds the English texts of M20 (ADR-0048): the project stages (the Stages tab of a project, the task panel and
// `hanko stage`), and the status cards with the decision model as engine, cron and the versioned result (form, detail,
// Dashboard and `hanko status-card`). init merges them into the same catalog as the rest (en.go). Same rules: the key
// is the pt-BR text, and a key that another file already translates is not repeated here.
var enM20 = map[string]string{
	// web UI: project › Stages
	"Etapas do projeto":              "Project stages",
	"Etapas do projeto salvas.":      "Project stages saved.",
	"Pipeline do projeto desligado.": "Project pipeline turned off.",
	"Uma task do projeto passa pelas etapas em ordem. Cada etapa define o status da task e a persona que trabalha nela; quando a persona conclui a etapa, a task avança e a persona seguinte é acordada. Só os gates de saída (aprovação ou revisão) param o avanço.": "A project task goes through the stages in order. Each stage sets the task status and the persona that works on it; when the persona completes the stage, the task moves on and the next persona is woken. Only exit gates (approval or review) stop it.",
	"Este projeto ainda não tem etapas.": "This project has no stages yet.",
	"Persona da etapa":                   "Stage persona",
	"Status da task na etapa":            "Task status in the stage",
	"Gate ao concluir":                   "Gate on completion",
	"O que se espera da etapa":           "What is expected of the stage",
	"A ordem das linhas é a ordem do pipeline. Deixe a chave vazia para não usar a linha; apagar todas as chaves desliga o pipeline (só sem tasks nas etapas). Uma etapa com persona precisa de status todo ou em andamento.": "The row order is the pipeline order. Leave the key empty to skip a row; clearing every key turns the pipeline off (only with no tasks in the stages). A stage with a persona needs the todo or in progress status.",
	"Salvar etapas":              "Save stages",
	"Sem persona (etapa humana)": "No persona (human stage)",
	"aprovação humana":           "human approval",
	"revisão da task":            "task review",
	"nenhum (avança sozinha)":    "none (moves on by itself)",
	"Aprendizados das etapas":    "Stage learnings",
	"As decisões de gate com nota (ou que não aprovaram) voltam no briefing da persona da etapa e ficam na memória do projeto.": "Gate decisions with a note (or that did not approve) go back into the stage persona's briefing and stay in the project memory.",
	"Nenhum aprendizado ainda: ele nasce quando um humano decide o gate de uma etapa.":                                          "No learnings yet: they appear when a human decides a stage gate.",

	// web UI: task panel
	"Etapa do projeto":   "Project stage",
	"Concluir etapa":     "Complete stage",
	"Mover à mão":        "Move by hand",
	"Tirar do pipeline":  "Take out of the pipeline",
	"Entrar no pipeline": "Enter the pipeline",
	"O projeto desta task tem etapas, mas ela não está nelas.": "This task's project has stages, but the task is not in them.",
	"O que foi entregue (opcional)":                            "What was delivered (optional)",
	"Aprendizados desta etapa":                                 "Learnings of this stage",
	"Próxima etapa":                                            "Next stage",
	"esperando o gate":                                         "waiting for the gate",
	"Task movida de etapa.":                                    "Task moved to another stage.",
	"Task no pipeline do projeto.":                             "Task in the project pipeline.",
	"Task fora do pipeline do projeto.":                        "Task out of the project pipeline.",
	"Etapa concluída.":                                         "Stage completed.",
	"entrou na etapa %s":                                       "entered stage %s",
	"etapa %s concluída":                                       "stage %s completed",
	"gate pedido na etapa %s":                                  "gate requested in stage %s",
	"gate decidido na etapa %s: %s":                            "gate decided in stage %s: %s",
	"avançou de %s para %s":                                    "moved from %s to %s",
	"voltou para %s":                                           "went back to %s",
	"atravessou a última etapa":                                "went through the last stage",
	"saiu do pipeline":                                         "left the pipeline",

	// web UI: status cards (engine, cron, Dashboard)
	"Quem escreve o resumo":                  "Who writes the summary",
	"Uma persona (run completa, com tools)":  "A persona (full run, with tools)",
	"O decision model (uma chamada, barato)": "The decision model (one call, cheap)",
	"O decision model exige o modelo configurado e a feature de resumo de card ligada em Decision model; ele usa os filtros abaixo em vez de a resumidora montá-los.": "The decision model needs the model configured and the card summary feature turned on under Decision model; it uses the filters below instead of the summarizer building them.",
	"Tasks observadas (decision model)":                              "Observed tasks (decision model)",
	"Status (separados por vírgula)":                                 "Status (comma-separated)",
	"Só tasks abertas":                                               "Open tasks only",
	"Sem nenhum filtro, o card observa as tasks abertas da empresa.": "With no filter, the card watches the company's open tasks.",
	"Num horário (cron), por exemplo toda manhã":                     "At a set time (cron), for example every morning",
	"Cron (modo horário)":                                            "Cron (time mode)",
	"Minuto hora dia-do-mês mês dia-da-semana. Ex.: 0 7 * * 1-5 = dias úteis às 7h.": "Minute hour day-of-month month day-of-week. E.g. 0 7 * * 1-5 = weekdays at 7am.",
	"Fuso do cron": "Cron time zone",
	"No horário, só atualizar se algo mudou":                                         "At that time, update only if something changed",
	"Mostrar este card no Painel":                                                    "Show this card on the Dashboard",
	"O resultado de cada atualização também vira uma versão de um artefato do card.": "The result of each update also becomes a version of the card's artifact.",
	"Resultado versionado:":                                                          "Versioned result:",
	"Decision model: o resumo sai de uma chamada só ao modelo da empresa.":           "Decision model: the summary comes from a single call to the company's model.",
	"Persona resumidora: uma run completa a cada atualização.":                       "Summarizer persona: a full run on every update.",
	"Informe uma expressão cron de 5 campos válida, por exemplo 0 7 * * 1-5.":        "Enter a valid 5-field cron expression, for example 0 7 * * 1-5.",
	"Fuso desconhecido.":                     "Unknown time zone.",
	"Escolha a persona ou o decision model.": "Choose the persona or the decision model.",
	"horário (cron)":                         "time (cron)",
	"atualizado %s":                          "updated %s",
	"próxima %s":                             "next %s",

	// the registered decision feature (domain.DecisionFeatures)
	"Resumo de card de status": "Status card summary",
	"Escreve o resumo dos cards de status que usam o decision model como motor.": "Writes the summary of the status cards that use the decision model as their engine.",

	// CLI: hanko stage
	"Pipeline do projeto: etapas que levam a task de persona em persona":                                     "Project pipeline: stages that carry the task from persona to persona",
	"Mostra as etapas do projeto":                                                                            "Shows the project stages",
	"Define o pipeline do projeto (admin): --stage chave:nome:status[:persona[:gate]], na ordem, ou --file":  "Sets the project pipeline (admin): --stage key:name:status[:persona[:gate]], in order, or --file",
	"etapa chave:nome:status[:persona[:gate]]; repita na ordem do pipeline (gate: none, approval ou review)": "stage key:name:status[:persona[:gate]]; repeat in pipeline order (gate: none, approval or review)",
	"lista JSON de etapas (chave, nome, status, persona, exit_gate, instructions)":                           "JSON list of stages (key, name, status, persona, exit_gate, instructions)",
	"arquivo de etapas inválido (esperado uma lista JSON): %w":                                               "invalid stages file (expected a JSON list): %w",
	"Desliga o pipeline do projeto (admin)":                                                                  "Turns the project pipeline off (admin)",
	"Pipeline desligado.":                                                                                    "Pipeline turned off.",
	"Aprendizados das etapas: decisões de gate com nota":                                                     "Stage learnings: gate decisions with a note",
	"só desta etapa (chave)":                                                                                 "only this stage (key)",
	"GATE":                                                                                                   "GATE",
	"NOTA":                                                                                                   "NOTE",
	"Mostra em que etapa a task está, o histórico e os aprendizados":                                         "Shows which stage the task is in, its history and the learnings",
	"Põe a task no pipeline do projeto dela":                                                                 "Puts the task in its project's pipeline",
	"etapa de entrada (padrão: a primeira)":                                                                  "entry stage (default: the first)",
	"Task no pipeline.":                                                                                      "Task in the pipeline.",
	"Conclui a etapa: a task avança ou espera o gate da etapa":                                               "Completes the stage: the task moves on or waits for the stage gate",
	"o que foi entregue":                                                                                     "what was delivered",
	"Move a task à mão (por cima dos gates) para a etapa seguinte ou para --to":                              "Moves the task by hand (over the gates) to the next stage or to --to",
	"etapa de destino (para a frente ou para trás)":                                                          "destination stage (forward or backward)",
	"Task movida.":              "Task moved.",
	"Tira a task do pipeline":   "Takes the task out of the pipeline",
	"Task fora do pipeline.":    "Task out of the pipeline.",
	"Gate de saída":             "Exit gate",
	"Próxima":                   "Next",
	"Aprendizados desta etapa:": "Learnings of this stage:",

	// CLI: hanko decision feature (the list of features grew by card_summary)
	"<triage|phase_classifier|name_summary|card_summary> <on|off>": "<triage|phase_classifier|name_summary|card_summary> <on|off>",

	// CLI: hanko status-card
	"Motor":     "Engine",
	"No Painel": "On the Dashboard",
	"política: manual, interval, reactive ou cron (padrão reactive)":                     "policy: manual, interval, reactive or cron (default reactive)",
	"motor: persona (padrão) ou decision (o decision model da empresa escreve o resumo)": "engine: persona (default) or decision (the company's decision model writes the summary)",
	"expressão cron de 5 campos (liga o modo cron), ex. \"0 7 * * 1-5\"":                 "5-field cron expression (turns on cron mode), e.g. \"0 7 * * 1-5\"",
	"fuso do cron (padrão UTC)":                            "cron time zone (default UTC)",
	"no horário do cron, só atualiza se alguma task mudou": "at the cron time, update only if some task changed",
	"mostra o card no Painel":                              "shows the card on the Dashboard",
	"tira o card do Painel":                                "takes the card off the Dashboard",
	"motor decision: status das tasks observadas":          "decision engine: status of the observed tasks",
	"motor decision: prioridades das tasks observadas":     "decision engine: priorities of the observed tasks",
	"motor decision: projeto das tasks observadas (slug)":  "decision engine: project of the observed tasks (slug)",
	"motor decision: persona das tasks observadas (slug)":  "decision engine: persona of the observed tasks (slug)",
	"motor decision: só tasks abertas":                     "decision engine: open tasks only",
}

func init() {
	// texts of earlier milestones that this one rewrote (the key is the pt-BR text itself): drop the old entry so the
	// catalog keeps no translation without a source.
	delete(en, "<triage|phase_classifier|name_summary> <on|off>")
	delete(en, "política: manual, interval ou reactive (padrão reactive)")
	for k, v := range enM20 {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_m20.go: " + k)
		}
		en[k] = v
	}
}
