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
	var embedded bool
	var eo EmbeddedOptions
	c := &cobra.Command{
		Use:   "serve",
		Short: e.t("Inicia o servidor (API, UI, MCP e heartbeat)"),
		Long:  e.t("Inicia o servidor. Com --embedded-pg sobe antes um Postgres local descartável (initdb/pg_ctl de uma instalação do PostgreSQL, sem baixar nada) e usa-o como banco: só para desenvolvimento e trial. Produção usa um Postgres externo (HANKO_DATABASE_URL)."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if embedded {
				if e.rt.ServeEmbedded == nil {
					return e.errNotWired()
				}
				if e.getenv("HANKO_DATABASE_URL") != "" {
					return usageError{errors.New(e.t("--embedded-pg não se combina com HANKO_DATABASE_URL: o banco embutido é só para desenvolvimento e trial; para um banco seu, tire a flag"))}
				}
				fmt.Fprintln(e.errOut, e.t("AVISO: Postgres embutido é só para desenvolvimento e trial; não use em produção."))
				return e.rt.ServeEmbedded(cmd.Context(), eo)
			}
			if e.rt.Serve == nil {
				return e.errNotWired()
			}
			return e.rt.Serve(cmd.Context())
		},
	}
	f := c.Flags()
	f.BoolVar(&embedded, "embedded-pg", false, e.t("sobe um Postgres local descartável e o usa como banco (só dev/trial)"))
	f.StringVar(&eo.Dir, "pg-dir", "", e.t("diretório do Postgres embutido (padrão <HANKO_HOME>/pg)"))
	f.StringVar(&eo.BinDir, "pg-bin-dir", "", e.t("pasta com initdb e pg_ctl (padrão: procura no PATH e nos lugares comuns)"))
	f.IntVar(&eo.Port, "pg-port", 0, e.t("porta do Postgres embutido (0 = a última usada ou uma livre)"))
	return c
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
	var ingest bool
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: e.t("Imprime o OpenAPI da API (não precisa de banco)"),
		Long:  e.t("Imprime o OpenAPI gerado a partir dos handlers. Para atualizar o arquivo versionado:\n  hanko openapi > api/openapi.json"),
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			gen := e.rt.OpenAPI
			if gen == nil {
				gen = api.Spec
			}
			if ingest {
				gen = api.SpecIngest
			}
			b, err := gen()
			if err != nil {
				return err
			}
			_, err = e.out.Write(b)
			return err
		},
	}
	cmd.Flags().BoolVar(&ingest, "ingest", false, e.t("Imprime o OpenAPI da ingestão (listener próprio), em vez do da API"))
	return cmd
}
