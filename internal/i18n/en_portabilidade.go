package i18n

// enPortabilidade holds the English texts of the .env import and the encrypted vault export (ADR-0068 §8, #62). init
// merges them into the same catalog as the rest (en.go). Same rules as en_saude.go: the key is the pt-BR text, and a key
// another file already translates is not repeated here.
var enPortabilidade = map[string]string{
	"Importar .env": "Import .env",
	"Cada variável vira um segredo, ou todas viram campos de um segredo só. Veja a prévia antes de gravar.": "Each variable becomes a secret, or all of them become fields of a single secret. Check the preview before writing.",
	"Coleção de destino":      "Target collection",
	"Situação":                "Status",
	"já existe":               "already exists",
	"novo":                    "new",
	"Linhas ignoradas:":       "Skipped lines:",
	"Conteúdo do .env":        ".env content",
	"Como importar":           "How to import",
	"Um segredo por variável": "One secret per variable",
	"Um segredo só, com um campo por variável":   "A single secret with one field per variable",
	"Nome do segredo (só no modo de um segredo)": "Secret name (single-secret mode only)",
	"Coleção": "Collection",
	"padrão":  "default",
	"Rotacionar o que já existe (sem marcar, o que existe é pulado)": "Rotate what already exists (unchecked, what exists is skipped)",
	"Senha da conta, para confirmar (se o servidor pedir)":           "Account password, to confirm (if the server asks)",
	"Gravar":                    "Write",
	"Import do .env concluído.": ".env import finished.",
	"O servidor pede uma prova recente de identidade: informe a senha da conta e grave de novo.": "The server asks for recent proof of identity: enter the account password and write again.",
	"Senha da conta incorreta.":              "Wrong account password.",
	"linha":                                  "line",
	"a senha do arquivo veio vazia no stdin": "the file password came empty on stdin",
	"informe a senha do arquivo com --password-stdin ou %s": "give the file password with --password-stdin or %s",
	"Coleção de destino: %s\n":                              "Target collection: %s\n",
	" (já existe)":                                          " (already exists)",
	"  ignorada, %s\n":                                      "  skipped, %s\n",
	"Importa um .env para o cofre: mostra a prévia e grava (exige reautenticação; --dry-run só mostra)": "Imports a .env into the vault: shows the preview and writes (needs re-authentication; --dry-run only shows)",
	"abrir %s: %w":                  "open %s: %w",
	"ler o .env: %w":                "read the .env: %w",
	"o .env passa de %d bytes":      "the .env is over %d bytes",
	"Nada foi gravado (--dry-run).": "Nothing was written (--dry-run).",
	"Importado: %d segredo(s) novo(s), %d campo(s), %d atualizado(s), %d já existente(s) mantido(s).\n":           "Imported: %d new secret(s), %d field(s), %d updated, %d existing kept.\n",
	"um segredo só, com um campo por variável (a primeira vira o campo value); sem isto, um segredo por variável": "a single secret with one field per variable (the first becomes the value field); without it, one secret per variable",
	"coleção de destino (nome ou id; padrão: a padrão)":                                                           "target collection (name or id; default: the default one)",
	"rotaciona o que já existe em vez de pular":                                                                   "rotates what already exists instead of skipping it",
	"só mostra a prévia, sem gravar":                                                                              "only shows the preview, without writing",
	"Exporta o cofre cifrado com uma senha, para importar em outra instância (admin; exige reautenticação)":       "Exports the vault encrypted with a password, to import into another instance (admin; needs re-authentication)",
	"informe o arquivo de saída com --out":                                                                        "give the output file with --out",
	"resposta do servidor inválida: %w":                                                                           "invalid server response: %w",
	"o arquivo recebido não bate com o SHA-256 informado pelo servidor":                                           "the received file does not match the SHA-256 the server reported",
	"gravar %s (use --force para sobrescrever): %w":                                                               "write %s (use --force to overwrite): %w",
	"Cofre exportado em %s: %d segredo(s), SHA-256 %s.\n":                                                         "Vault exported to %s: %d secret(s), SHA-256 %s.\n",
	"arquivo de saída (.hkv)":                                                                                     "output file (.hkv)",
	"lê a senha do arquivo da primeira linha do stdin (ou use HANKO_VAULT_PASSWORD)":                              "reads the file password from the first line of stdin (or use HANKO_VAULT_PASSWORD)",
	"sobrescreve o arquivo de saída se ele já existir":                                                            "overwrites the output file if it already exists",
	"<arquivo.hkv>": "<file.hkv>",
	"Importa um cofre exportado por outra instância (admin; exige reautenticação; tudo ou nada)":                                                             "Imports a vault exported by another instance (admin; needs re-authentication; all or nothing)",
	"Importado: %d segredo(s) novo(s), %d campo(s), %d já existente(s) mantido(s), %d coleção(ões), %d concessão(ões), %d concessão(ões) não aplicada(s).\n": "Imported: %d new secret(s), %d field(s), %d existing kept, %d collection(s), %d grant(s), %d grant(s) not applied.\n",
}

func init() {
	for k, v := range enPortabilidade {
		if _, dup := en[k]; dup {
			panic("i18n: chave duplicada em en_portabilidade.go: " + k)
		}
		en[k] = v
	}
}
