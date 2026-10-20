package i18n

// enTrilhaO1 holds the English texts of the continuous track, wave 1 (ADR-0058): the first-owner claim page, the
// rate-limit settings, `hanko auth claim-link`, and the /design-guide and /dev/ux development screens. init merges
// them into the same catalog as the rest (en.go). Same rules: the key is the pt-BR text, and a key that another file
// already translates is not repeated here.
var enTrilhaO1 = map[string]string{
	// first-owner claim
	"Primeiro acesso":       "First access",
	"Criar o primeiro dono": "Create the first owner",
	"Este link é de uso único: a conta criada aqui vira dona desta instalação e o link deixa de valer.": "This link is single-use: the account created here becomes the owner of this installation and the link stops working.",
	"Link inválido":             "Invalid link",
	"Link inválido ou expirado": "Invalid or expired link",
	"Este link de primeiro acesso não vale mais. No servidor, rode `hanko auth claim-link` para emitir outro, ou entre com a sua conta.": "This first-access link is no longer valid. On the server, run `hanko auth claim-link` to issue another one, or sign in with your account.",
	"Ir para a entrada": "Go to sign-in",
	"Ou abra o link de uso único que o servidor imprimiu no boot (`hanko auth claim-link` emite outro).": "Or open the single-use link the server printed at boot (`hanko auth claim-link` issues another one).",

	// rate limit (Settings › Instance)
	"Limite de taxa":           "Rate limit",
	"Ligar o rate limit geral": "Turn on the general rate limit",
	"Token bucket por API key, usuário e IP para escritas, MCP e webhooks de entrada (429 + Retry-After).": "Token bucket per API key, user and IP for writes, MCP and incoming webhooks (429 + Retry-After).",
	"Escritas por minuto (por API key ou usuário)":                                                         "Writes per minute (per API key or user)",
	"Escritas por minuto (por IP, somando identidades)":                                                    "Writes per minute (per IP, across identities)",
	"Chamadas MCP por minuto (por identidade ou run)":                                                      "MCP calls per minute (per identity or run)",
	"Webhooks de entrada por minuto (por IP)":                                                              "Incoming webhooks per minute (per IP)",
	"Erros de navegador por minuto (por IP)":                                                               "Browser errors per minute (per IP)",

	// CLI
	"Emite um novo link de uso único para criar o primeiro dono (invalida os anteriores)":                                                                                                                       "Issue a new single-use link to create the first owner (invalidates the previous ones)",
	"Enquanto não há usuário, o servidor imprime no boot um link /claim/<token> que cria o primeiro dono. Este comando emite outro sem reiniciar o servidor; `create-owner` continua valendo como alternativa.": "While there is no user, the server prints a /claim/<token> link at boot that creates the first owner. This command issues another one without restarting the server; `create-owner` remains available as an alternative.",
	"Abra o link para criar o primeiro dono (uso único, vale até %s):\n%s\n":                                                                                                                                    "Open the link to create the first owner (single-use, valid until %s):\n%s\n",

	// development screens
	"Desenvolvimento":          "Development",
	"Telas de desenvolvimento": "Development screens",
	"Design guide":             "Design guide",
	"UX labs":                  "UX labs",
	"Guia do design system":    "Design system guide",
	"Kubo DS · catálogo vivo":  "Kubo DS · living catalog",
	"Tokens e componentes reais do Hanko, em claro e escuro. Base do screenshot diff do e2e.": "Real Hanko tokens and components, in light and dark. Basis of the e2e screenshot diff.",
	"Tema do catálogo":              "Catalog theme",
	"Lado a lado":                   "Side by side",
	"Tema claro":                    "Light theme",
	"Tema escuro":                   "Dark theme",
	"Formulários":                   "Forms",
	"Onboarding":                    "Onboarding",
	"Cores":                         "Colors",
	"Tipografia e raios":            "Typography and radii",
	"Título de página":              "Page title",
	"Título de seção":               "Section title",
	"Subtítulo":                     "Subtitle",
	"Texto de leitura":              "Reading text",
	"Texto de interface (padrão)":   "Interface text (default)",
	"Legenda e metadados":           "Caption and metadata",
	"Microtexto":                    "Micro text",
	"Botões e badges":               "Buttons and badges",
	"Primário":                      "Primary",
	"Secundário":                    "Secondary",
	"Contorno":                      "Outline",
	"Fantasma":                      "Ghost",
	"Perigo":                        "Danger",
	"Desabilitado":                  "Disabled",
	"Grande":                        "Large",
	"Pequeno":                       "Small",
	"Mínimo":                        "Tiny",
	"Suave":                         "Soft",
	"chip":                          "chip",
	"chip suave":                    "soft chip",
	"Revisar o plano da semana":     "Review the weekly plan",
	"Texto de apoio do campo.":      "Field help text.",
	"Campo com erro":                "Field with error",
	"Mínimo de 3 caracteres.":       "At least 3 characters.",
	"Escreva em Markdown.":          "Write in Markdown.",
	"Avisos, KPIs e estados vazios": "Alerts, KPIs and empty states",
	"Aviso informativo: o orçamento da semana está em 60%.": "Informational notice: the weekly budget is at 60%.",
	"Aguardando a sua decisão em um gate.":                  "Waiting for your decision on a gate.",
	"A execução falhou: autenticação do harness expirada.":  "The run failed: harness authentication expired.",
	"Execuções hoje":  "Runs today",
	"Aguardando você": "Waiting on you",
	"Nada por aqui":   "Nothing here",
	"Quando houver itens, eles aparecem nesta lista.": "When there are items, they show up in this list.",
	"Criar o primeiro":         "Create the first one",
	"Abas, tabela e progresso": "Tabs, table and progress",
	"Exemplo de abas":          "Tabs example",
	"Plano da semana":          "Weekly plan",
	"Revisão de custos":        "Cost review",
	"Thread e log de run":      "Thread and run log",
	"Laboratórios de UX":       "UX labs",
	"Cenários montados com dados sintéticos para exercitar a interface sob carga. Nada é gravado no banco.": "Scenarios assembled with synthetic data to exercise the interface under load. Nothing is written to the database.",
	"Thread longo":           "Long thread",
	"comentários sintéticos": "synthetic comments",
	"Run com 10 mil linhas":  "Run with 10,000 lines",
	"eventos sintéticos, servidos em páginas pela API do laboratório":            "synthetic events, served in pages by the lab API",
	"Uma task com %d comentários (persona e humano) no markup real do thread.":   "A task with %d comments (persona and human) in the real thread markup.",
	"O log virtualizado da run com %d eventos, paginados por uma API sintética.": "The virtualized run log with %d events, paged by a synthetic API.",
}

func init() {
	for k, v := range enTrilhaO1 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
