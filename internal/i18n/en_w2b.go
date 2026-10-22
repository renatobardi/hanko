package i18n

// enW2B holds the English texts of the continuous track, wave 2 part B: `hanko mcp` (stdio bridge).
// Same rules as en.go: the key is the pt-BR text, and a key another catalog already has is not repeated here.
var enW2B = map[string]string{
	"Servidor MCP em stdio que fala com o Hanko remoto usando o seu token": "MCP server over stdio that talks to the remote Hanko with your token",
	"Lê requisições MCP do stdin e as repassa ao endpoint /mcp do servidor Hanko (--url ou o contexto salvo por 'hanko login'), " +
		"autenticando com a API key ou o token da CLI. O protocolo passa sem alteração: as tools do servidor aparecem no cliente.": "Reads MCP requests from stdin and forwards them to the Hanko server's /mcp endpoint (--url or the context saved by 'hanko login'), " +
		"authenticating with the API key or CLI token. The protocol passes through unchanged: the server's tools show up in the client.",
}

func init() {
	for k, v := range enW2B {
		if _, dup := en[k]; dup {
			panic("i18n: duplicate catalog key " + k)
		}
		en[k] = v
	}
}
