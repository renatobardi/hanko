package i18n

// enCofrePendencias holds the English texts of the #95 leftovers of the vault (ADR-0068 §2.6): the collection choice on
// the company secret form and the `hanko secret create --collection` flag. Pairs, not a map, so gofmt does not realign
// them; init merges them into the catalog like the other en_*.go files (a key another file already translates would panic
// there, so it is not repeated).
var enCofrePendencias = [][2]string{
	{"Quem alcança a coleção usa o segredo. Sem escolha, o segredo entra na padrão.", "Whoever reaches the collection can use the secret. Without a choice, the secret goes into the default collection."},
	{"coleção que recebe o segredo (nome ou id); sem ela, a padrão", "collection that receives the secret (name or id); without it, the default one"},
}

func init() {
	for _, p := range enCofrePendencias {
		if _, dup := en[p[0]]; dup {
			panic("i18n: duplicate catalog key " + p[0])
		}
		en[p[0]] = p[1]
	}
}
