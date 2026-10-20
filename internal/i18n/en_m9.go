package i18n

// enM9 holds the English texts of the tool policy engine (ADR-0053): the Policy tab under Integrations, the test of a
// tool as a persona, "approve and always allow" on tool gates, the quarantine warning in the inbox, and the
// `hanko tool-policy`, `hanko hook` and `integration tools --test` commands. init merges them into the same catalog as
// the rest (en.go). Same rules: the key is the pt-BR text, and a key that another file already translates is not
// repeated here.
var enM9 = map[string]string{
	// CLI
	"Política de tools: desligar, perguntar antes ou permitir, por persona e por empresa": "Tool policy: turn off, ask first or allow, per persona and per company",
	"Lista as políticas":                         "Lists the policies",
	"só as políticas desta persona (id ou slug)": "only this persona's policies (id or slug)",
	"Nenhuma política: valem os perfis de ferramentas e, nas tools do harness, o modo de permissão da persona. Crie com: hanko tool-policy set ask --native Bash": "No policy: tool profiles apply and, for harness tools, the persona's permission mode. Create one with: hanko tool-policy set ask --native Bash",
	"Grava uma política (admin): o alvo sai das flags; sem flag de alvo vale para tudo":                                                                           "Saves a policy (admin): the target comes from the flags; with no target flag it covers everything",
	"persona (id ou slug); sem ela, vale como padrão da empresa":                                                                                                  "persona (id or slug); without it, it is the company default",
	"integração (id ou slug)":                                  "integration (id or slug)",
	"tool da integração":                                       "tool of the integration",
	"tool do harness ou de plugin pelo nome (ex.: Bash, Edit)": "harness or plugin tool by name (e.g. Bash, Edit)",
	"--native não combina com --integration/--tool":            "--native cannot be combined with --integration/--tool",
	"--tool precisa de --integration":                          "--tool needs --integration",
	"Política gravada: %s › %s › %s\n":                         "Policy saved: %s › %s › %s\n",
	"Apaga uma política (admin)":                               "Deletes a policy (admin)",
	"política %q não encontrada ou ambígua":                    "policy %q not found or ambiguous",
	"Política apagada.":                                        "Policy deleted.",
	"sempre permitir":                                          "always allow",
	"nativa":                                                   "native",
	"integração":                                               "integration",
	"tudo":                                                     "everything",
	"ESCOPO":                                                   "SCOPE",
	"MODO":                                                     "MODE",
	"testa uma tool como uma persona (simula a decisão; com --execute chama de verdade)": "tests a tool as a persona (simulates the decision; with --execute it really calls it)",
	"persona cuja visão o teste usa (com --test)":                                        "persona whose view the test uses (with --test)",
	"argumentos do teste em JSON (com --test)":                                           "test arguments as JSON (with --test)",
	"com --test, chama a tool de verdade (passa pelo gate e entra na auditoria)":         "with --test, really calls the tool (goes through the gate and into the audit log)",
	"--test precisa de --as <persona>":                                                   "--test needs --as <persona>",
	"--args não é um objeto JSON: %w":                                                    "--args is not a JSON object: %w",
	"Como %s: %s/%s\n":                                                                   "As %s: %s/%s\n",
	"  estado: %s · risco: %s\n":                                                         "  state: %s · risk: %s\n",
	"  veredito: %s · origem: %s\n":                                                      "  verdict: %s · origin: %s\n",
	"  decisão: %s\n":                                                                    "  decision: %s\n",
	"  gate: %s\n":                                                                       "  gate: %s\n",
	"  resultado (redigido): %s\n":                                                       "  result (redacted): %s\n",
	"em gate de tool: sempre permitir (grava a política da persona; admin)":              "on a tool gate: always allow (saves the persona's policy; admin)",
	"Hooks de harness (uso interno: o Hanko os liga sozinho nas runs)":                   "Harness hooks (internal use: Hanko wires them into runs by itself)",
	"Hook PreToolUse do claude: aplica a política de tools do Hanko (lê o JSON do hook em stdin)": "Claude PreToolUse hook: applies Hanko's tool policy (reads the hook JSON from stdin)",
	"ler o hook": "read the hook input",

	// web UI: Integrations › Policy
	"Política":                     "Policy",
	"Empresa (padrão)":             "Company (default)",
	"qualquer tool":                "any tool",
	"integração %s":                "integration %s",
	"tool nativa %s":               "native tool %s",
	"Qualquer tool":                "Any tool",
	"Uma integração":               "One integration",
	"Uma tool de integração":       "One integration tool",
	"Tool do harness ou de plugin": "Harness or plugin tool",
	"Permitida":                    "Allowed",
	"Política gravada.":            "Policy saved.",
	"não é um JSON válido":         "not valid JSON",
	"Desligar, perguntar antes ou permitir cada tool por persona, com um padrão da empresa. A política mais específica decide e vale por cima do perfil de ferramentas.": "Turn each tool off, ask first or allow it per persona, with a company default. The most specific policy decides and takes precedence over the tool profile.",
	"Nenhuma política.": "No policy.",
	"Sem política valem os perfis de ferramentas e, nas tools do harness (Bash, Edit...), o modo de permissão da persona.": "Without a policy, tool profiles apply and, for harness tools (Bash, Edit...), the persona's permission mode.",
	"Apagar esta política?": "Delete this policy?",
	"Nova política":         "New policy",
	"Uma política com o mesmo alvo e escopo é trocada. Persona vence empresa; tool vence integração vence tudo; no empate, desligar vence perguntar vence permitir.": "A policy with the same target and scope is replaced. Persona beats company; tool beats integration beats everything; on a tie, off beats ask beats allow.",
	"Gravar política":                            "Save policy",
	"create_issue, ou Bash para tool do harness": "create_issue, or Bash for a harness tool",
	"Testar uma tool como uma persona":           "Test a tool as a persona",
	"Mostra o que a persona veria: o estado no catálogo, o veredito do perfil e das políticas e de onde ele veio. Marcar chamar de verdade repassa ao servidor, passa pelo gate e entra na auditoria como teste.": "Shows what the persona would see: the catalog state, the verdict of the profile and policies and where it came from. Ticking call for real forwards to the server, goes through the gate and enters the audit log as a test.",
	"Argumentos (JSON)":  "Arguments (JSON)",
	"Chamar de verdade":  "Call for real",
	"Resultado do teste": "Test result",
	"Estado no catálogo": "Catalog state",
	"Veredito":           "Verdict",
	"Abrir o gate":       "Open the gate",

	// web UI: audit, effective tools
	"gateway":             "gateway",
	"teste como %s":       "test as %s",
	"hook do harness":     "harness hook",
	"ACP":                 "ACP",
	"plugin":              "plugin",
	"política da empresa": "company policy",
	"política da persona": "persona policy",

	// web UI: tool gate, inbox
	"Aprovar e sempre permitir": "Approve and always allow",
	"Aprova e grava uma política de permitir para esta persona e esta tool: as próximas chamadas passam direto.": "Approves and saves an allow policy for this persona and this tool: the next calls go straight through.",
	"Tools em quarentena": "Quarantined tools",
	"Quarentena":          "Quarantine",
	"Revisar":             "Review",
	"Tool nova (ou que mudou) e que pode escrever: nenhuma persona a chama até um admin revisar.": "A tool that is new (or changed) and may write: no persona calls it until an admin reviews it.",
}

func init() {
	for k, v := range enM9 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
