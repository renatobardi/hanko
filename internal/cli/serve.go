package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
)

func (e *env) errNotWired() error {
	return errors.New(e.t("comando indisponível neste binário: runtime não conectado"))
}

func (e *env) serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: e.t("Inicia o servidor (API, UI, MCP e heartbeat)"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.rt.Serve == nil {
				return e.errNotWired()
			}
			return e.rt.Serve(cmd.Context())
		},
	}
}

func (e *env) migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: e.t("Aplica as migrações do banco (schema e fila)"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.rt.Migrate == nil {
				return e.errNotWired()
			}
			return e.rt.Migrate(cmd.Context())
		},
	}
}

func (e *env) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: e.t("Mostra a versão"),
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if e.asJSON {
				return e.printJSON(map[string]string{"version": e.version})
			}
			_, err := fmt.Fprintf(e.out, "hanko %s\n", e.version)
			return err
		},
	}
}

func (e *env) openapiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "openapi",
		Short: e.t("Imprime o OpenAPI da API (não precisa de banco)"),
		Long:  e.t("Imprime o OpenAPI gerado a partir dos handlers. Para atualizar o arquivo versionado:\n  hanko openapi > api/openapi.json"),
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			gen := e.rt.OpenAPI
			if gen == nil {
				gen = api.Spec
			}
			b, err := gen()
			if err != nil {
				return err
			}
			_, err = e.out.Write(b)
			return err
		},
	}
}
