package i18n

// addCatalog junta um catálogo parcial ao catálogo inglês. A mesma chave com a mesma tradução em dois
// catálogos é aceita (telas diferentes usam rótulos iguais); a mesma chave com traduções diferentes é erro de
// programação e derruba o processo na inicialização, como as checagens de duplicata dos demais catálogos.
func addCatalog(name string, m map[string]string) {
	for k, v := range m {
		if old, dup := en[k]; dup && old != v {
			panic("i18n: catalog " + name + " redefines key " + k)
		}
		en[k] = v
	}
}
