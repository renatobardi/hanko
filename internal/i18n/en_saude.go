package i18n

// enSaude holds the English texts of the secret health report and of the generator options (ADR-0068 §7, #61). init
// merges them into the same catalog as the rest (en.go). Same rules as en_cofre_ui.go: the key is the pt-BR text, and a
// key another file already translates is not repeated here.
var enSaude = map[string]string{
	"Opções do gerador":              "Generator options",
	"Tamanho (8 a 128 caracteres)":   "Length (8 to 128 characters)",
	"Classes de caractere":           "Character classes",
	"minúsculas":                     "lowercase",
	"maiúsculas":                     "uppercase",
	"dígitos":                        "digits",
	"símbolos":                       "symbols",
	"Frase-senha (palavras, 3 a 12)": "Passphrase (words, 3 to 12)",
	"Frase-senha não combina com tamanho nem classes.": "A passphrase does not combine with length or classes.",
	"Saúde dos segredos":                               "Secret health",
	"Repetidos, velhos, sem uso, órfãos e fracos. Calculado aqui, sem chamada a serviço externo.": "Repeated, old, unused, orphaned and weak. Computed here, with no call to an external service.",
	"Categoria":                          "Category",
	"Achados":                            "Findings",
	"Campo":                              "Field",
	"Detalhe":                            "Detail",
	"Nenhum achado.":                     "No findings.",
	"Repetido":                           "Repeated",
	"Velho (passou do prazo de rotação)": "Old (past the rotation deadline)",
	"Sem uso":                            "Unused",
	"Fraco":                              "Weak",
	"Rotação de segredo":                 "Secret rotation",
	"Ver saúde dos segredos":             "View secret health",
}

func init() {
	for k, v := range enSaude {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_saude.go: " + k)
		}
		en[k] = v
	}
}
