package i18n

// enFix3 holds the English texts added by the independent review fixes (search/islands).
var enFix3 = map[string]string{
	"A busca demorou demais; use palavras mais específicas.": "The search took too long; use more specific words.",
}

func init() {
	for k, v := range enFix3 {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
