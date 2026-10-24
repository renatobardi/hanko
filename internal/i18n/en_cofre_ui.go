package i18n

// enCofreUI holds the English texts of the vault web screens (ADR-0068, slice 1): the fields of a secret and the
// collections page. init merges them into the same catalog as the rest (en.go). Same rules: the key is the pt-BR
// text, and a key that another file already translates is not repeated here.
var enCofreUI = map[string]string{
	"Versões de histórico por campo": "History versions per field",
	"Retenção do histórico de cada campo: as últimas N versões ou as de até D dias, o que guardar menos (ADR-0068 §3.2).": "History retention of each field: the last N versions or those up to D days old, whichever keeps fewer (ADR-0068 §3.2).",
	"Dias de histórico por campo": "History days per field",
	"Trocar valor":                "Replace value",
	"Visibilidade":                "Visibility",
	"Mudar marca":                 "Change flag",
	"Campos do segredo. Campo visível mostra o texto; campo secreto nunca volta para a tela.": "Fields of the secret. A visible field shows its text; a secret field never comes back to the screen.",
	"Voltar aos segredos": "Back to secrets",
	"Um segredo pode ter vários campos, como usuário e senha, cada um com a sua marca de visível ou secreto.": "A secret can have several fields, such as user and password, each flagged visible or secret.",
	"Valor / revelado": "Value / revealed",
	"Revelar um campo secreto, ver o histórico de versões e restaurar uma versão: requer reautenticação (fatia 2, #57).": "Revealing a secret field, viewing the version history and restoring a version: requires re-authentication (slice 2, #57).",
	"Novo campo": "New field",
	"O valor é cifrado. Campo secreto nunca volta para a tela; em branco com a opção abaixo, o Hanko gera um e o mostra uma única vez.": "The value is encrypted. A secret field never comes back to the screen; left blank with the option below, Hanko generates one and shows it only once.",
	"Adicionar campo":      "Add field",
	"Coleções de segredos": "Secret collections",
	"Agrupam segredos para dar acesso e cobrar a rotação de uma vez.": "They group secrets to grant access and demand rotation at once.",
	"Prazo de rotação (dias)":                                         "Rotation deadline (days)",
	"Nova coleção":                                                    "New collection",
	"Criar coleção":                                                   "Create collection",
	"Voltar às coleções":                                              "Back to collections",
	"Prazo de rotação: %s dias.":                                      "Rotation deadline: %s days.",
	"Nenhum segredo nesta coleção.":                                   "No secrets in this collection.",
	"Tirar da coleção":                                                "Remove from collection",
	"Concessões da coleção: %d. Conceder e revogar: requer reautenticação (fatia 2, #57).": "Collection grants: %d. Granting and revoking: requires re-authentication (slice 2, #57).",
	"Pôr um segredo na coleção":                        "Put a secret in the collection",
	"Nome do segredo":                                  "Secret name",
	"Pôr na coleção":                                   "Put in collection",
	"Apagar coleção":                                   "Delete collection",
	"Campo adicionado.":                                "Field added.",
	"Valor do campo trocado: a versão do campo subiu.": "Field value replaced: the field version went up.",
	"Marca do campo atualizada.":                       "Field flag updated.",
	"Coleção criada.":                                  "Collection created.",
	"Coleção apagada.":                                 "Collection deleted.",
	"Segredo posto na coleção.":                        "Secret put in the collection.",
	"Segredo tirado da coleção.":                       "Secret removed from the collection.",
	"Nome inválido.":                                   "Invalid name.",
	"Descrição inválida: até 300 caracteres.":          "Invalid description: up to 300 characters.",
	"Prazo de rotação inválido: de 1 a 3650 dias.":     "Invalid rotation deadline: 1 to 3650 days.",
	"A coleção padrão não se apaga.":                   "The default collection cannot be deleted.",
	"Conflito: já existe um item com esse nome, ou o estado atual não permite a ação.": "Conflict: an item with that name already exists, or the current state does not allow the action.",
}

func init() {
	for k, v := range enCofreUI {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_cofre_ui.go: " + k)
		}
		en[k] = v
	}
}
