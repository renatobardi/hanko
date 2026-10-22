package i18n

// enM12 holds the English texts of M12 (ADR-0056): the Decision model screen and `hanko decision`, the new
// harnesses (devin, pi, cursor, http) in the hire form, Adapters and `hanko persona probe`. init merges them into
// the same catalog as the rest (en.go). Same rules: the key is the pt-BR text, and a key that another file already
// translates is not repeated here.
var enM12 = map[string]string{
	// web UI: Settings › Decision model
	"Decision model": "Decision model",
	"Um classificador externo, barato, que o próprio Hanko chama para decisões pequenas (triagem, fase, nome e resumo). A chave é um segredo da empresa e cada chamada aparece em Custos com o ator sistema.": "A cheap external classifier that Hanko itself calls for small decisions (triage, phase, name and summary). The key is a company secret and every call shows up in Costs with the system actor.",
	"Endpoint compatível com chat completions":                                                            "chat-completions-compatible endpoint",
	"Em branco usa o OpenRouter. O catálogo de modelos vem deste endpoint.":                               "Leave empty to use OpenRouter. The model catalog comes from this endpoint.",
	"Chave (segredo da empresa)":                                                                          "Key (company secret)",
	"Crie a chave em Segredos e informe a referência. A chave não é guardada aqui nem volta para a tela.": "Create the key under Secrets and enter its reference. The key is not stored here and never comes back to the screen.",
	"Modelo do catálogo":        "Catalog model",
	"Prazo de cada chamada (s)": "Timeout of each call (s)",
	"Salvar desligado":          "Save turned off",
	"Salvar decision model":     "Save decision model",
	"Features":                  "Features",
	"Cada feature nasce desligada: nada chama o modelo sem você ligar. Cada uma pode ter um modelo próprio.": "Every feature starts turned off: nothing calls the model until you turn it on. Each one can have its own model.",
	"Feature":            "Feature",
	"Modelo próprio":     "Own model",
	"o da empresa":       "the company's",
	"Últimas invocações": "Latest invocations",
	"Ator: sistema. O custo soma ao gasto do mês da empresa e aparece em": "Actor: system. The cost adds to the company's monthly spend and shows up in",
	"Nenhuma invocação ainda.":                            "No invocations yet.",
	"Decision model salvo.":                               "Decision model saved.",
	"Decision model removido.":                            "Decision model removed.",
	"Feature atualizada.":                                 "Feature updated.",
	"Catálogo indisponível agora; digite o id do modelo.": "Catalog unavailable right now; type the model id.",
	"Use uma referência ${secret:NOME} a um segredo que exista na empresa.":                        "Use a ${secret:NAME} reference to a secret that exists in the company.",
	"Escolha um modelo que esteja no catálogo do endpoint.":                                        "Choose a model that is in the endpoint's catalog.",
	"Endpoint inválido: use uma URL http(s) de uma API compatível com chat completions.":           "Invalid endpoint: use an http(s) URL of a chat-completions-compatible API.",
	"O prazo deve ficar entre 1 e 300 segundos.":                                                   "The timeout must be between 1 and 300 seconds.",
	"%s chamadas feitas pelo sistema (não por personas). O custo soma ao gasto do mês da empresa.": "%s calls made by the system (not by personas). The cost adds to the company's monthly spend.",
	"Chamadas": "Calls",
	"Erros":    "Errors",

	// the registered features (domain.DecisionFeatures)
	"Triagem": "Triage",
	"Sugere a persona e a prioridade para uma task nova, como comentário do sistema.": "Suggests the persona and priority for a new task, as a system comment.",
	"Classificador de fase": "Phase classifier",
	"Sugere em qual etapa de um pipeline um texto se encaixa.": "Suggests which stage of a pipeline a text fits.",
	"Nome e resumo": "Name and summary",
	"Gera um nome curto e um resumo para um texto longo.": "Generates a short name and a summary for a long text.",

	// web UI: hire form and Adapters
	"Local · Devin via ACP · o executável é do operador":                                                                          "Local · Devin over ACP · the executable is the operator's",
	"Local · saída JSON · tools do Hanko pela CLI":                                                                                "Local · JSON output · Hanko tools through the CLI",
	"Local · stream-json · tools do Hanko pela CLI":                                                                               "Local · stream-json · Hanko tools through the CLI",
	"Sem CLI · endpoint compatível com chat completions · tools do Hanko":                                                         "No CLI · chat-completions-compatible endpoint · Hanko tools",
	"Ou uma ferramenta conhecida":                                                                                                 "Or a known tool",
	"Preenche o comando e os argumentos da ferramenta; deixe o comando em branco.":                                                "Fills in the tool's command and arguments; leave the command empty.",
	"O Hanko roda o loop de tool-use: o modelo pede as tools do Hanko e o Hanko as executa. Informe o modelo no campo abaixo.":    "Hanko runs the tool-use loop: the model asks for Hanko tools and Hanko executes them. Enter the model in the field below.",
	"Referência a um segredo criado em Configurações › Segredos. Vazio para um endpoint local sem chave.":                         "Reference to a secret created under Settings › Secrets. Empty for a local endpoint without a key.",
	"Harness desconhecido: use claude, codex, acp, devin, pi, cursor, http ou fake.":                                              "Unknown harness: use claude, codex, acp, devin, pi, cursor, http or fake.",
	"Informe o endpoint compatível com chat completions.":                                                                         "Enter the chat-completions-compatible endpoint.",
	"Informe o modelo do endpoint.":                                                                                               "Enter the endpoint's model.",
	"Devin pelo motor ACP (devin acp); o executável é definido por HANKO_DEVIN_BIN e a autenticação é feita fora do Hanko.":       "Devin through the ACP engine (devin acp); the executable is set by HANKO_DEVIN_BIN and authentication is done outside Hanko.",
	"pi --mode json -p; as tools do Hanko vão pela CLI hanko autenticada com o token da execução.":                                "pi --mode json -p; Hanko tools go through the hanko CLI authenticated with the run token.",
	"cursor-agent em stream-json; as tools do Hanko vão pela CLI hanko autenticada com o token da execução.":                      "cursor-agent in stream-json; Hanko tools go through the hanko CLI authenticated with the run token.",
	"Loop de tool-use do próprio Hanko sobre um endpoint compatível com chat completions; o endpoint e a chave ficam na persona.": "Hanko's own tool-use loop over an chat-completions-compatible endpoint; the endpoint and key live on the persona.",

	// CLI: hanko decision
	"Decision model da empresa: classificador externo (OpenRouter) para triagem e afins": "The company's decision model: an external classifier (OpenRouter) for triage and the like",
	"Mostra o decision model e o estado das features":                                    "Shows the decision model and the state of the features",
	"Decision model não configurado.":                                                    "Decision model not configured.",
	"Modelo: %s\nEndpoint: %s\nChave: %s\nLigado: %s\n":                                  "Model: %s\nEndpoint: %s\nKey: %s\nOn: %s\n",
	"FEATURE": "FEATURE",
	"LIGADA":  "ON",
	"Lista os modelos do catálogo do provedor (com preço por milhão de tokens)": "Lists the models in the provider's catalog (with price per million tokens)",
	"CONTEXTO":      "CONTEXT",
	"ENTRADA US$/M": "INPUT US$/M",
	"SAÍDA US$/M":   "OUTPUT US$/M",
	"outro endpoint compatível com chat completions (padrão: o configurado, ou o do OpenRouter)": "another chat-completions-compatible endpoint (default: the configured one, or OpenRouter's)",
	"referência ${secret:NOME} da chave desse endpoint":                                          "${secret:NAME} reference to that endpoint's key",
	"<modelo>": "<model>",
	"Configura o decision model: modelo do catálogo, endpoint e chave (${secret:NOME})": "Configures the decision model: catalog model, endpoint and key (${secret:NAME})",
	"Decision model configurado: %s\n":                                                  "Decision model configured: %s\n",
	"Aviso: %s\n":                                                                       "Warning: %s\n",
	"endpoint compatível com chat completions (padrão: OpenRouter)":                     "chat-completions-compatible endpoint (default: OpenRouter)",
	"referência ${secret:NOME} ao segredo com a chave (nunca a chave em claro)":         "${secret:NAME} reference to the secret holding the key (never the key in clear)",
	"prazo de cada chamada em segundos (padrão 30)":                                     "timeout of each call in seconds (default 30)",
	"salva desligado": "save turned off",
	"Remove o decision model e desliga as features":                    "Removes the decision model and turns the features off",
	"<triage|phase_classifier|name_summary|fast_response> <on|off>":    "<triage|phase_classifier|name_summary|fast_response> <on|off>",
	"Liga ou desliga uma feature (--model dá um modelo próprio a ela)": "Turns a feature on or off (--model gives it its own model)",
	"use on ou off, não %q":                                            "use on or off, not %q",
	"Feature %s: %s\n":                                                 "Feature %s: %s\n",
	"modelo próprio da feature (vazio usa o da empresa)":               "the feature's own model (empty uses the company's)",
	"Últimas invocações do decision model (ator system, com custo)":    "Latest decision model invocations (system actor, with cost)",
	"Nenhuma invocação.":                                               "No invocations.",
	"CUSTO US$":                                                        "COST US$",
	"MS":                                                               "MS",
	"quantas mostrar":                                                  "how many to show",
	"Triagem: o decision model sugere persona e prioridade (comentário do sistema; não atribui)": "Triage: the decision model suggests persona and priority (system comment; does not assign)",
	"%s: sugerida a persona %s, prioridade %s (custo US$ %.6f)\n":                                "%s: persona %s suggested, priority %s (cost US$ %.6f)\n",
	"<pipeline> <texto>":                           "<pipeline> <text>",
	"Sugere a etapa de um pipeline para um texto":  "Suggests the stage of a pipeline for a text",
	"Etapa sugerida: %s (%s)\n":                    "Suggested stage: %s (%s)\n",
	"<texto>":                                      "<text>",
	"Gera um nome curto e um resumo para um texto": "Generates a short name and a summary for a text",

	// CLI: hanko persona hire / probe
	"harness: claude, codex, acp, devin, pi, cursor, http ou fake (obrigatório); gemini/opencode: acp com --config-json '{\"preset\":\"gemini\"}'": "harness: claude, codex, acp, devin, pi, cursor, http or fake (required); gemini/opencode: acp with --config-json '{\"preset\":\"gemini\"}'",
	"Confere se o harness da persona está pronto (binário, autenticação, endpoint), sem executar trabalho":                                         "Checks whether the persona's harness is ready (binary, authentication, endpoint) without running any work",
	"PASSO":   "STEP",
	"DETALHE": "DETAIL",
	"a persona não está pronta: corrija o passo que falhou": "the persona is not ready: fix the step that failed",
	"Persona pronta.": "Persona ready.",
}

func init() {
	for k, v := range enM12 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
