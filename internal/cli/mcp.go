package cli

import (
	"io"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/mcpbridge"
)

// mcpCmd é o `hanko mcp`: servidor MCP em stdio que repassa tudo ao MCP do servidor Hanko remoto (/mcp), com o
// token da CLI (--token, HANKO_TOKEN ou o contexto salvo por `hanko login`). Serve para IDEs e clientes que só
// falam stdio, sem instalar nada além deste binário.
func (e *env) mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: e.t("Servidor MCP em stdio que fala com o Hanko remoto usando o seu token"),
		Long:  e.t("Lê requisições MCP do stdin e as repassa ao endpoint /mcp do servidor Hanko (--url ou o contexto salvo por 'hanko login'), autenticando com a API key ou o token da CLI. O protocolo passa sem alteração: as tools do servidor aparecem no cliente."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := e.client()
			local := &sdk.IOTransport{Reader: io.NopCloser(e.in), Writer: nopWriteCloser{e.out}}
			return mcpbridge.Run(cmd.Context(), local, mcpbridge.RemoteTransport(mcpbridge.Endpoint(c.BaseURL), c.Token, nil))
		},
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
