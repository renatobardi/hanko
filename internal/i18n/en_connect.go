package i18n

// enConnect holds the English texts of the connect assistant (ADR-0089, #45): `hanko integration connect`.
var enConnect = map[string]string{
	"<nome|integração>": "<name|integration>",
	"Assistente: liga um servidor MCP a personas com um modelo de acesso (ADR-0089)":  "Assistant: connects an MCP server to personas with an access template (ADR-0089)",
	"começa: endereço do servidor MCP http (omitido, retoma a integração do <ref>)":   "starts: URL of the MCP http server (omitted, resumes the integration at <ref>)",
	"header NOME=VALOR com o segredo do servidor (repita a flag)":                     "header NAME=VALUE with the server secret (repeat the flag)",
	"conecta por OAuth 2.1 com PKCE em vez de header secreto":                         "connects by OAuth 2.1 with PKCE instead of a secret header",
	"OAuth: escopo pedido (repita a flag)":                                            "OAuth: requested scope (repeat the flag)",
	"OAuth: cliente já registrado, para servidor sem registro dinâmico":               "OAuth: client already registered, for a server without dynamic registration",
	"repete a descoberta de tools (depois de um erro)":                                "repeats the tool discovery (after an error)",
	"descarta o rascunho: apaga a integração criada pelo assistente":                  "discards the draft: deletes the integration the assistant created",
	"passo 4: id ou slug do modelo de acesso":                                         "step 4: id or slug of the access template",
	"passo 3: persona que recebe a ferramenta (repita a flag; nunca todas)":           "step 3: persona that receives the tool (repeat the flag; never all)",
	"aprova a gravação; sem ela, o comando só mostra o que cada persona poderá fazer": "approves the write; without it, the command only shows what each persona will be able to do",
	"Rascunho descartado: %s\n":                                                       "Draft discarded: %s\n",
	"descoberta falhou: %s. Tente de novo com --discover, ou descarte com --discard":  "discovery failed: %s. Try again with --discover, or discard with --discard",
	"escolha as personas com --persona (passo 3; nunca todas por padrão)":             "choose the personas with --persona (step 3; never all by default)",
	"escolha o modelo de acesso com --template (passo 4)":                             "choose the access template with --template (step 4)",
	"modelos disponíveis: ":                                                           "available templates: ",
	"Integração: %s (%s), %d tool(s) descoberta(s)\n":                                 "Integration: %s (%s), %d tool(s) discovered\n",
	" [quarentena]": " [quarantine]",
	"Modelo %s:\n":  "Template %s:\n",
	"Persona %s:\n": "Persona %s:\n",
	"bloqueada":     "blocked",
	"liberada (%s)": "allowed (%s)",
	"nada foi gravado: repita com --yes para aplicar estas permissões":                                                  "nothing was written: repeat with --yes to apply these permissions",
	"Conectado: %s, perfil %s, %d persona(s), %d tool(s) liberada(s) da quarentena.\n":                                  "Connected: %s, profile %s, %d persona(s), %d tool(s) released from quarantine.\n",
	"Próximo passo: escolha o modelo com --template (passo 4) e as personas com --persona (passo 3). Nada foi gravado.": "Next step: choose the template with --template (step 4) and the personas with --persona (step 3). Nothing was written.",
}

func init() { addCatalog("connect", enConnect) }
