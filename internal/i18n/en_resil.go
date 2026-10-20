package i18n

// enResil holds the English texts of the run resilience feature (ADR-0039): the diagnosis card, the operator's
// actions (Tentar de novo / Continuar / Encerrar), steering, the follow-up queue and the `hanko run` / `hanko
// followup` commands. init merges them into the same catalog as the rest (en.go); same rules: the key is the pt-BR
// text, and a key already translated elsewhere is not repeated here (a duplicate panics at start-up and fails the
// tests).
var enResil = map[string]string{
	// diagnosis card
	"o processo do Hanko caiu":            "the Hanko process went down",
	"o Hanko foi encerrado":               "Hanko was shut down",
	"erro do agente":                      "agent error",
	"tempo limite":                        "time limit",
	"continuação automática":              "automatic continuation",
	"espera a sua decisão":                "waiting for your decision",
	"já continuada":                       "already continued",
	"Continuar":                           "Continue",
	"Encerrar":                            "Close",
	"recuperação":                         "recovery",
	"mudança de rumo":                     "change of course",
	"tentar de novo":                      "retry",
	"continuar":                           "continue",
	"operador":                            "operator",
	"continuação %d de %d":                "continuation %d of %d",
	"Diagnóstico da execução":             "Run diagnosis",
	"Diagnóstico":                         "Diagnosis",
	"Causa":                               "Cause",
	"Tentativa":                           "Attempt",
	"Próxima ação":                        "Next action",
	"Continuação de":                      "Continuation of",
	"Continuada por":                      "Continued by",
	"Execuções paradas esperando decisão": "Stopped runs waiting for a decision",
	"Nota para a próxima execução (opcional)":            "Note for the next run (optional)",
	"Recomeça do zero, sem a sessão anterior":            "Starts over, without the previous session",
	"Retoma a sessão com o briefing do que já foi feito": "Resumes the session with a briefing of what was already done",
	"Fecha o card sem abrir outra execução":              "Closes the card without opening another run",
	"Decisão do operador: %s.":                           "Operator's decision: %s.",
	"Desfecho incerto: a tool %s estava em andamento quando a execução parou e pode ter produzido efeito externo. Confira o estado real antes de continuar.": "Uncertain outcome: the tool %s was in progress when the run stopped and may have had an external effect. Check the real state before continuing.",

	// steering
	"Mudar o rumo":                      "Change course",
	"injeta ao vivo":                    "injects live",
	"interrompe e continua":             "interrupts and continues",
	"não aceita":                        "not supported",
	"Diga à persona o que mudar agora.": "Tell the persona what to change now.",
	"Mensagem para a execução":          "Message for the run",
	"A mensagem entra na execução em andamento; a persona muda de rumo sem recomeçar.":                   "The message goes into the run in progress; the persona changes course without starting over.",
	"Este harness não aceita mensagem no meio: a execução é interrompida e continua com a sua mensagem.": "This harness takes no message mid-run: the run is interrupted and continues with your message.",

	// follow-up queue
	"Fila de follow-ups": "Follow-up queue",
	"Nada na fila. O que você enviar enquanto a execução trabalha espera aqui, na ordem.": "Nothing queued. What you send while the run works waits here, in order.",
	"editado":            "edited",
	"Texto do follow-up": "Follow-up text",
	"Descartar":          "Discard",
	"Follow-up":          "Follow-up",
	"Enfileirar":         "Queue",
	"Enfileire uma instrução: a próxima execução da task a recebe, na ordem.": "Queue an instruction: the task's next run receives it, in order.",
	"Pode editar ou descartar enquanto não for entregue.":                     "You can edit or discard it until it is delivered.",
	"injetar ao vivo":                                "inject live",
	"interromper e continuar":                        "interrupt and continue",
	"espera a execução terminar":                     "waits for the run to finish",
	"será injetado na execução ativa":                "will be injected into the active run",
	"a execução será interrompida e continuada":      "the run will be interrupted and continued",
	"espera a sua decisão sobre uma execução parada": "waits for your decision on a stopped run",
	"a persona não pode trabalhar agora":             "the persona cannot work right now",
	"a task não tem persona":                         "the task has no persona",
	"a próxima execução está abrindo":                "the next run is opening",

	// activity feed
	"continuada":                 "continued",
	"diagnosticada":              "diagnosed",
	"redirecionada":              "redirected",
	"descartado":                 "discarded",
	"entregue":                   "delivered",
	"abriu a continuação da":     "opened the continuation of",
	"diagnosticou a":             "diagnosed",
	"decidiu sobre a":            "decided on",
	"mudou o rumo da":            "changed the course of",
	"enfileirou um follow-up em": "queued a follow-up on",
	"editou um follow-up em":     "edited a follow-up on",
	"descartou um follow-up em":  "discarded a follow-up on",
	"entregou um follow-up em":   "delivered a follow-up on",

	// CLI
	"espera a sua decisão: Tentar de novo, Continuar ou Encerrar": "waiting for your decision: Retry, Continue or Close",
	"\nContinuação de: %s (%s, tentativa %d)\n":                   "\nContinuation of: %s (%s, attempt %d)\n",
	"Continuada por: %s\n":                                        "Continued by: %s\n",
	"Steer: %s\n":                                                 "Steer: %s\n",
	"\nDiagnóstico:":                                              "\nDiagnosis:",
	"  Causa":                                                     "  Cause",
	"  Tentativa":                                                 "  Attempt",
	"  Próxima ação":                                              "  Next action",
	"  Dono":                                                      "  Owner",
	"  Desfecho incerto: a tool %s estava em andamento (%s); verifique o efeito antes de continuar.\n": "  Uncertain outcome: the tool %s was in progress (%s); check the effect before continuing.\n",
	"  Decisão: %s %s\n": "  Decision: %s %s\n",
	"Tentar de novo: abre uma execução nova do zero para a execução que parou":                             "Retry: opens a brand new run, from scratch, for the run that stopped",
	"Continuar: retoma a sessão da execução que parou, com o briefing do que já foi feito":                 "Continue: resumes the session of the run that stopped, with a briefing of what was already done",
	"Encerrar: fecha o card da execução que parou, sem abrir outra (a task em andamento vai para blocked)": "Close: closes the card of the run that stopped, without opening another (an in-progress task goes to blocked)",
	"Execução %s: decisão registrada (%s).\n":                                                              "Run %s: decision recorded (%s).\n",
	"nota para a execução seguinte (entra no briefing)":                                                    "note for the next run (goes into the briefing)",
	"<run> <mensagem>": "<run> <message>",
	"Muda o rumo de uma execução ativa (injeta a mensagem ou interrompe e continua, conforme o harness)": "Changes the course of an active run (injects the message or interrupts and continues, depending on the harness)",
	"Mensagem enviada (%s). Follow-up %s.\n":                             "Message sent (%s). Follow-up %s.\n",
	"Fila de follow-ups: o que você mandou enquanto a execução trabalha": "Follow-up queue: what you sent while the run works",
	"Lista a fila (--task ou --conversation): ordem e motivo da espera":  "Lists the queue (--task or --conversation): order and reason for the wait",
	"informe --task ou --conversation":                                   "pass --task or --conversation",
	"Nenhum follow-up na fila.":                                          "No follow-up in the queue.",
	"ESPERA":                                                             "WAIT",
	"conversa (id ou prefixo)":                                           "conversation (id or prefix)",
	"<mensagem>":                                                         "<message>",
	"Enfileira uma instrução para a task: a execução seguinte a recebe, na ordem": "Queues an instruction for the task: the next run receives it, in order",
	"informe --task": "pass --task",
	"Follow-up %s na fila (posição %d): %s\n": "Follow-up %s queued (position %d): %s\n",
	"<id> <mensagem>": "<id> <message>",
	"Edita um follow-up que ainda não foi entregue":    "Edits a follow-up that was not delivered yet",
	"Follow-up %s editado.\n":                          "Follow-up %s edited.\n",
	"Descarta um follow-up que ainda não foi entregue": "Discards a follow-up that was not delivered yet",
	"Follow-up %s descartado.\n":                       "Follow-up %s discarded.\n",
}

func init() {
	for k, v := range enResil {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
