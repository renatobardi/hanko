package i18n

// enPersonaEnv holds the English texts of the persona's environment variables section (ADR-0068 §5.2, #117 slice 3).
// init merges them into the same catalog as the rest (en.go). Same rules: the key is the pt-BR text, and a key that
// another file already translates is not repeated here.
var enPersonaEnv = map[string]string{
	"Variáveis de ambiente": "Environment variables",
	"Chegam ao processo da persona. Cada valor é uma referência a segredo; o valor em si nunca aparece aqui.": "They reach the persona's process. Each value is a reference to a secret; the value itself never appears here.",
	"Nenhuma variável.": "No variable.",
	"Referência":        "Reference",
	"Salvar variável":   "Save variable",
	"Use ${secret:NOME#campo} ou, para campo arquivo, ${secretfile:NOME#campo}. O segredo precisa existir na empresa.": "Use ${secret:NAME#field} or, for a file field, ${secretfile:NAME#field}. The secret must exist in the company.",
	"valor em claro": "plain value",
	"Troque por uma referência a segredo, com o mesmo nome, no formulário abaixo.": "Replace it with a reference to a secret, with the same name, in the form below.",
	"Variável de ambiente salva.":    "Environment variable saved.",
	"Variável de ambiente removida.": "Environment variable removed.",
}

func init() {
	for k, v := range enPersonaEnv {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_persona_env.go: " + k)
		}
		en[k] = v
	}
}
