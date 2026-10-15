package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

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
	var slug string
	var active, paused bool
	c := &cobra.Command{
		Use: "import " + e.t("<arquivo|->"), Short: e.t("Cria uma empresa NOVA, pausada, a partir de um arquivo exportado (tudo ou nada)"), Args: cobra.ExactArgs(1),
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
			res, err := e.client().ImportCompany(cmd.Context(), b, slug, active)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			fmt.Fprintf(e.out, e.t("empresa %s (%s) criada\n"), res.Company.Name, res.Company.Slug)
			keys := make([]string, 0, len(res.Counts))
			for k := range res.Counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, []string{k, fmt.Sprint(res.Counts[k])})
			}
			e.table([]string{e.t("ITEM"), e.t("QTD")}, rows)
			return nil
		},
	}
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
