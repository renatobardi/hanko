package i18n

// enCofreSenha holds the English texts of the password mark on vault fields (ADR-0074, #61): the CLI flags and the
// message that lists them. Pairs, not a map, so gofmt does not realign them; init merges them into the catalog like the
// other en_*.go files (a key another file already translates would panic there, so it is not repeated).
var enCofreSenha = [][2]string{
	{"marca o campo como senha (só texto secreto): entra no relatório de saúde", "marks the field as a password (secret text only): it goes into the health report"},
	{"marca o campo como senha (só texto secreto)", "marks the field as a password (secret text only)"},
	{"tira a marca de senha do campo", "removes the password mark from the field"},
	{"use --password ou --no-password, não os dois", "use --password or --no-password, not both"},
	{"nada para mudar: use --visible, --secret, --password, --no-password ou --position", "nothing to change: use --visible, --secret, --password, --no-password or --position"},
	{"Muda a marca (--visible ou --secret), a de senha (--password ou --no-password) e a posição de um campo", "Changes the mark (--visible or --secret), the password mark (--password or --no-password) and the position of a field"},
}

func init() {
	for _, p := range enCofreSenha {
		if _, dup := en[p[0]]; dup {
			panic("i18n: duplicate catalog key " + p[0])
		}
		en[p[0]] = p[1]
	}
}
