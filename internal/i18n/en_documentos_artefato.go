package i18n

// enDocArtef holds the English texts of artifact documents with MAJOR.MINOR versions (ADR-0064): the version form,
// the change badges, restore, reclassify and tags on the artifact pages, and the matching CLI commands.
var enDocArtef = map[string]string{
	// CLI
	"Publica um arquivo como artefato; com --artifact vira a versão seguinte daquele documento": "Publishes a file as an artifact; with --artifact it becomes the next version of that document",
	"Artefato %s %s (%s) publicado (%s, %s)\n":                                                  "Artifact %s %s (%s) published (%s, %s)\n",
	"documento: %s\n": "document: %s\n",
	"atenção: o tamanho do diff contradiz a classificação declarada":                  "warning: the size of the diff contradicts the declared change",
	"documento a alterar (id): a publicação vira a versão seguinte dele":              "document to change (id): the upload becomes its next version",
	"major (mudança pesada) ou minor (ajuste); padrão: decidido pelo tamanho do diff": "major (heavy change) or minor (small adjustment); default: decided by the size of the diff",
	"motivo curto da mudança":                                    "short reason for the change",
	"tag do documento (repita para várias; substitui as atuais)": "document tag (repeat for several; replaces the current ones)",
	"DOCUMENTO": "DOCUMENT",
	"filtra pelo workspace do documento (id ou slug)":                   "filters by the document's workspace (id or slug)",
	"filtra pela tag do documento":                                      "filters by document tag",
	"só este documento (id); com --all-versions, o histórico dele":      "only this document (id); with --all-versions, its history",
	"com o id do documento: a versão MAJOR.MINOR (vazio = a mais nova)": "with the document id: the MAJOR.MINOR version (empty = the latest)",
	"MUDANÇA":            "CHANGE",
	"%s → %s: +%d −%d\n": "%s → %s: +%d −%d\n",
	"Restaura uma versão: o conteúdo dela vira a versão mais nova do documento (major)": "Restores a version: its content becomes the document's latest version (major)",
	"Restaurada como %s (%s)\n":                                         "Restored as %s (%s)\n",
	"Reclassifica a versão mais nova do documento e a renumera":         "Reclassifies the document's latest version and renumbers it",
	"Agora é a versão %s (%s)\n":                                        "Now it is version %s (%s)\n",
	"<documento>":                                                       "<document>",
	"Muda tags, projeto ou workspace de um documento (não cria versão)": "Changes a document's tags, project or workspace (does not create a version)",
	"Documento %s: tags %s\n":                                           "Document %s: tags %s\n",
	"projeto (id ou slug); vazio tira o projeto":                        "project (id or slug); empty removes the project",
	"workspace (id ou slug); vazio tira o workspace":                    "workspace (id or slug); empty removes the workspace",

	// UI
	"Nova versão": "New version",
	"%s, hoje na versão %s. Escolha se a mudança é major ou minor.": "%s, currently at version %s. Choose whether the change is major or minor.",
	"Tipo de mudança":               "Kind of change",
	"Automático":                    "Automatic",
	"Decidido pelo tamanho do diff": "Decided by the size of the diff",
	"Mudança pesada: estrutura, premissa, conclusão, reescrita": "Heavy change: structure, premise, conclusion, rewrite",
	"Ajuste pontual: correção, acréscimo pequeno":               "Small adjustment: a fix, a short addition",
	"Tags (opcional)": "Tags (optional)",
	"Tags":            "Tags",
	"Tag":             "Tag",
	"Salvar tags":     "Save tags",
	"Tags salvas.":    "Tags saved.",
	"Tags inválidas: use a-z, 0-9 e -, até 40 caracteres cada e até 20 tags.": "Invalid tags: use a-z, 0-9 and -, up to 40 characters each and up to 20 tags.",
	"Diferenças: %s → %s": "Differences: %s → %s",
	"Mudança":             "Change",
	"Documento":           "Document",
	"(%s das linhas)":     "(%s of the lines)",
	"Classificação questionada: foi declarada %s, mas %s das linhas mudaram. Confira e, se for o caso, reclassifique.": "Classification questioned: it was declared %s, but %s of the lines changed. Check it and reclassify if needed.",
	"A medida do diff contradiz a classificação declarada":                                                             "The size of the diff contradicts the declared change",
	"Reclassificar esta versão:": "Reclassify this version:",
	"restaurar":                  "restore",
	"Restaurar esta versão? O conteúdo dela vira a versão mais nova (major); nada é apagado.": "Restore this version? Its content becomes the latest version (major); nothing is deleted.",
	"restaurada de %s":                    "restored from %s",
	"Versão restaurada como a mais nova.": "Version restored as the latest one.",
	"Versão reclassificada.":              "Version reclassified.",
	"Não foi possível comparar estas duas versões: são de documentos diferentes, não são texto ou são grandes demais.": "These two versions cannot be compared: they belong to different documents, are not text or are too large.",
}

func init() {
	for k, v := range enDocArtef {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
