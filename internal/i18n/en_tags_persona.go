package i18n

// enTagsPersona holds the English texts of the persona tags (ADR-0087, #118): the `persona tag` commands. init merges
// them into the same catalog as the rest (en.go). Same rules: the key is the pt-BR text, and a key that another file
// already translates is not repeated here.
var enTagsPersona = map[string]string{
	// prévia do import
	"Tags de persona": "Persona tags",

	// CLI (`hanko persona tag`)
	"Gerencia as tags de persona (cadastro, aplicar, tirar e recorte do organograma)": "Manages the persona tags (registry, apply, remove and org chart slice)",
	"Lista o cadastro de tags (sem as arquivadas, a menos que --archived)":            "Lists the tag registry (without the archived ones, unless --archived)",
	"Cria uma tag de persona": "Creates a persona tag",
	"Edita nome, cor ou descrição de uma tag (o id e as personas não mudam)": "Edits the name, color or description of a tag (the id and the personas do not change)",
	"Arquiva uma tag: sai do seletor e fica nas personas (admin; se desfaz)": "Archives a tag: it leaves the picker and stays on the personas (admin; can be undone)",
	"Desarquiva uma tag (admin)":                                        "Unarchives a tag (admin)",
	"Deleta uma tag e tira de todas as personas (admin; não se desfaz)": "Deletes a tag and removes it from every persona (admin; cannot be undone)",
	"Aplica uma tag a uma persona (--create cria a tag se não existe)":  "Applies a tag to a persona (--create creates the tag if it does not exist)",
	"Tira uma tag de uma persona":                                       "Removes a tag from a persona",
	"Lista as personas do organograma por tag e por nível":              "Lists the org chart personas by tag and by level",
	"<persona> <tag>":                "<persona> <tag>",
	"<tag>":                          "<tag>",
	"COR":                            "COLOR",
	"Nenhuma tag.":                   "No tags.",
	"Nenhuma persona nesse recorte.": "No persona in this slice.",
	"Tag criada: %s (%s)\n":          "Tag created: %s (%s)\n",
	"Tag atualizada: %s (%s)\n":      "Tag updated: %s (%s)\n",
	"Tag arquivada: %s (%s)\n":       "Tag archived: %s (%s)\n",
	"Tag desarquivada: %s (%s)\n":    "Tag unarchived: %s (%s)\n",
	"Tag %s aplicada a %s.\n":        "Tag %s applied to %s.\n",
	"Tag %s tirada de %s.\n":         "Tag %s removed from %s.\n",
	"Tag %s deletada; %d persona(s) perderam a tag.\n":                       "Tag %s deleted; %d persona(s) lost the tag.\n",
	"a tag %s sai de %d persona(s) e não se desfaz: confirme com --yes":      "tag %s leaves %d persona(s) and this cannot be undone: confirm with --yes",
	"tag %q não encontrada":                                                  "tag %q not found",
	"informe --name, --color ou --description":                               "pass --name, --color or --description",
	"inclui as tags arquivadas":                                              "includes the archived tags",
	"cor: gray, red, amber, green, teal, blue, violet ou pink (padrão gray)": "color: gray, red, amber, green, teal, blue, violet or pink (default gray)",
	"descrição, até 200 caracteres":                                          "description, up to 200 characters",
	"nome novo":                                                              "new name",
	"cor nova":                                                               "new color",
	"descrição nova (vazia apaga)":                                           "new description (empty clears it)",
	"confirma que a tag sai de todas as personas":                            "confirms that the tag leaves every persona",
	"cria a tag se não existe":                                               "creates the tag if it does not exist",
	"cor da tag criada com --create":                                         "color of the tag created with --create",
	"tags (id ou nome), separadas por vírgula":                               "tags (id or name), comma-separated",
	"exige todas as tags (padrão: alguma delas)":                             "requires every tag (default: any of them)",
	"inclui as personas sem tag":                                             "includes the personas without tags",
	"níveis, separados por vírgula (1 = topo)":                               "levels, comma-separated (1 = top)",
	"corta abaixo deste nível (0 = sem corte)":                               "cuts below this level (0 = no cut)",
	"aceita tags arquivadas no filtro":                                       "accepts archived tags in the filter",
}

func init() {
	for k, v := range enTagsPersona {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_tags_persona.go: " + k)
		}
		en[k] = v
	}
}
