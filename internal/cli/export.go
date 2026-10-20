package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
)

func (e *env) exportCmd() *cobra.Command {
	var withRuns, withLogs, withArtifacts bool
	var outFile string
	c := &cobra.Command{
		Use: "export", Short: e.t("Exporta a empresa como arquivo JSON portátil (sem segredos): hanko export > arq.json"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			raw, err := e.client().ExportCompany(cmd.Context(), co, client.ExportOpts{Runs: withRuns, Logs: withLogs, Artifacts: withArtifacts})
			if err != nil {
				return err
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf(e.t("resposta inválida do servidor: %w"), err)
			}
			if outFile == "" {
				return e.printJSON(v)
			}
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Errorf(e.t("codificar JSON: %w"), err)
			}
			if err := writeFileAtomic(outFile, append(b, '\n')); err != nil {
				return fmt.Errorf(e.t("gravar %s: %w"), outFile, err)
			}
			fmt.Fprintf(e.out, e.t("exportado para %s\n"), outFile)
			return nil
		},
	}
	c.Flags().BoolVar(&withRuns, "with-runs", false, e.t("inclui as execuções encerradas (sem logs)"))
	c.Flags().BoolVar(&withLogs, "with-logs", false, e.t("inclui também os logs das execuções (implica --with-runs)"))
	c.Flags().BoolVar(&withArtifacts, "with-artifacts", false, e.t("inclui as versões dos artefatos, com o conteúdo (até 20 MiB no total)"))
	c.Flags().StringVarP(&outFile, "output", "o", "", e.t("grava neste arquivo (permissão 0600) em vez da saída padrão"))
	return c
}

func (e *env) importCmd() *cobra.Command {
	var slug, into, onConflict string
	var resolve []string
	var active, paused, dryRun bool
	c := &cobra.Command{
		Use: "import " + e.t("<arquivo|->"), Short: e.t("Importa um arquivo exportado: cria uma empresa NOVA, pausada, ou (--into) mescla a configuração numa existente; --dry-run mostra a prévia"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var b []byte
			var err error
			if args[0] == "-" {
				b, err = io.ReadAll(e.in)
			} else {
				b, err = os.ReadFile(args[0])
			}
			if err != nil {
				return fmt.Errorf(e.t("ler %s: %w"), args[0], err)
			}
			if active && paused {
				return usageError{errors.New(e.t("--active e --paused são excludentes"))}
			}
			if !json.Valid(b) {
				return usageError{errors.New(e.t("o arquivo não é um JSON válido"))}
			}
			if into == "" && (onConflict != "" || len(resolve) > 0) {
				return usageError{errors.New(e.t("--on-conflict e --resolve só valem com --into"))}
			}
			res, err := e.client().ImportCompanyWith(cmd.Context(), b, client.ImportOpts{Slug: slug, Active: active, DryRun: dryRun,
				Into: into, OnConflict: onConflict, Resolve: resolve})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			e.printImportPreview(res)
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, e.t("só mostra a prévia (contagens, o que seria feito de cada item, avisos); não grava nada"))
	c.Flags().StringVar(&into, "into", "", e.t("empresa EXISTENTE (id ou slug) que recebe só a configuração; sem isso cria uma empresa nova"))
	c.Flags().StringVar(&onConflict, "on-conflict", "", e.t("com --into: o que fazer com o que já existe: skip (padrão), rename ou replace"))
	c.Flags().StringArrayVar(&resolve, "resolve", nil, e.t("com --into: exceção por item, tipo/chave=ação (ex.: personas/dev=replace); repetível"))
	c.Flags().StringVar(&slug, "slug", "", e.t("slug da empresa nova (padrão: o do arquivo)"))
	c.Flags().BoolVar(&active, "active", false, e.t("liga o que o arquivo traz ligado (padrão: personas pausadas e fluxos desligados)"))
	c.Flags().BoolVar(&paused, "paused", false, e.t("(padrão) personas entram pausadas e fluxos desligados"))
	_ = c.Flags().MarkHidden("paused")
	return c
}

// writeFileAtomic grava num arquivo temporário (0600) no mesmo diretório e só o renomeia para path no fim: em
// qualquer erro o destino fica como estava (nunca um arquivo pela metade) e o temporário é removido.
func writeFileAtomic(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".hanko-export-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
