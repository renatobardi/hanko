package i18n

// enACP holds the English texts of the ACP harness (hire form, CLI flags). Same rules as en.go:
// the key is the pt-BR text, and a key that another catalog already has is not repeated here.
var enACP = map[string]string{
	"Local · qualquer harness que fale ACP · você informa o comando": "Local · any harness that speaks ACP · you provide the command",
	"Comando do harness ACP": "ACP harness command",
	"Só o executável, que fala ACP por stdio e precisa estar instalado no servidor. Argumentos vão no campo abaixo; variáveis de ambiente extras, pela API ou pela CLI.": "Just the executable; it speaks ACP over stdio and must be installed on the server. Arguments go in the field below; extra environment variables go through the API or the CLI.",
	"Permissões do harness": "Harness permissions",
	"Somente leitura aprova leitura e busca e recusa edição e execução. Permitir tudo aprova qualquer pedido do harness.": "Read-only approves reading and search and refuses editing and execution. Allow all approves every request from the harness.",
	"Argumentos do comando (um por linha)": "Command arguments (one per line)",
	"Somente leitura (padrão)":             "Read-only (default)",
	"Permitir tudo":                        "Allow all",
	"Recusar tudo":                         "Refuse all",
	"Informe o comando do harness ACP.":    "Enter the ACP harness command.",
	"O comando deve ser só o executável; ponha os argumentos no campo de argumentos.":                "The command must be just the executable; put the arguments in the arguments field.",
	"O ACP não pode ser o harness alternativo do revisor; use uma persona revisora que rode em ACP.": "ACP cannot be the reviewer's alternate harness; use a reviewer persona that runs on ACP.",

	// CLI
	"harness/provedor do plano: claude, codex, acp ou fake (obrigatório)":                                                                          "plan harness/provider: claude, codex, acp or fake (required)",
	"--config-json não é um objeto JSON válido: %w":                                                                                                "--config-json is not a valid JSON object: %w",
	"adapter_config em JSON, para listas e objetos (ex.: acp: '{\"command\":\"meu-agente\",\"args\":[\"--stdio\"]}'); --config sobrepõe as chaves": "adapter_config as JSON, for lists and objects (e.g. acp: '{\"command\":\"my-agent\",\"args\":[\"--stdio\"]}'); --config overrides keys",
}

func init() {
	for k, v := range enACP {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
