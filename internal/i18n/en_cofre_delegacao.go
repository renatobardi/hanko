package i18n

// enCofreDelegacao holds the English texts of the delegation of a personal secret (ADR-0068 §6, #117 slice 2): the
// web screen of the secret's fields and the `secret delegation` commands. init merges them into the same catalog as
// the rest (en.go). Same rules: the key is the pt-BR text, and a key that another file already translates is not
// repeated here.
var enCofreDelegacao = map[string]string{
	// tela
	"Delegações deste segredo": "Delegations of this secret",
	"Vale pelo prazo escolhido (padrão 24 h, máximo 7 dias) ou até ser revogada. Só o trabalho que você pediu pode recebê-la; a run usa o segredo sem mostrar o valor.": "It is valid for the chosen term (default 24 h, maximum 7 days) or until it is revoked. Only the work you requested can receive it; the run uses the secret without showing the value.",
	"Nenhuma delegação deste segredo.": "No delegation of this secret.",
	"Expira em":                        "Expires at",
	"Tipo do alvo":                     "Target type",
	"ID do alvo":                       "Target ID",
	"1 hora":                           "1 hour",
	"24 horas (padrão)":                "24 hours (default)",
	"7 dias (máximo)":                  "7 days (maximum)",
	"Delegar":                          "Delegate",
	"Id da task ou da conversa que você pediu, ou de uma run em curso dela.": "Id of the task or conversation you requested, or of one of its running runs.",
	"Delegação criada.":   "Delegation created.",
	"Delegação revogada.": "Delegation revoked.",
	"Alvo inválido: informe o id de uma task, conversa ou run sua.": "Invalid target: enter the id of a task, conversation or run of yours.",
	"Prazo inválido: de 0 a 7 dias.":                                "Invalid term: 0 to 7 days.",
	"inativa":                                                       "inactive",

	// CLI (`hanko secret delegation`)
	"Delegações de segredo pessoal a uma task, conversa ou run sua (só você; nunca mostra valor)": "Delegations of a personal secret to a task, conversation or run of yours (only you; never shows the value)",
	"Delega um segredo pessoal ao trabalho que você pediu (padrão 24 h, máximo 7 dias)":           "Delegates a personal secret to work you requested (default 24 h, maximum 7 days)",
	"task, conversation ou run":                                      "task, conversation or run",
	"id da task, da conversa ou da run (run só se estiver em curso)": "id of the task, the conversation or the run (a run only while it is running)",
	"prazo, como 24h ou 168h (padrão 24h, máximo 168h)":              "term, such as 24h or 168h (default 24h, maximum 168h)",
	"prazo inválido: de 0 a 7 dias (ex.: 24h, 168h)":                 "invalid term: 0 to 7 days (e.g. 24h, 168h)",
	"Delegação criada: %s para %s %s (até %s)\n":                     "Delegation created: %s for %s %s (until %s)\n",
	"Lista as suas delegações (segredo, alvo, prazo e estado)":       "Lists your delegations (secret, target, term and state)",
	"Nenhuma delegação.":                                             "No delegation.",
	"Revoga uma delegação sua; a run deixa de resolver o segredo":    "Revokes one of your delegations; the run stops resolving the secret",
	"Delegação %s revogada.\n":                                       "Delegation %s revoked.\n",
	"SEGREDO":                                                        "SECRET",
	"EXPIRA EM":                                                      "EXPIRES AT",
}

func init() {
	for k, v := range enCofreDelegacao {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_cofre_delegacao.go: " + k)
		}
		en[k] = v
	}
}
