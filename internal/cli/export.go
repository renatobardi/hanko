package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

func (e *env) exportCmd() *cobra.Command {
	var withRuns, withLogs bool
	var outFile string
	c := &cobra.Command{
		Use: "export", Short: "Exporta a empresa como arquivo JSON portátil (sem segredos): hanko export > arq.json", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			raw, err := e.client().ExportCompany(cmd.Context(), co, withRuns, withLogs)
			if err != nil {
				return err
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("resposta inválida do servidor: %w", err)
			}
			if outFile == "" {
				return e.printJSON(v)
			}
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return fmt.Errorf("codificar JSON: %w", err)
			}
			if err := os.WriteFile(outFile, append(b, '\n'), 0o600); err != nil {
				return fmt.Errorf("gravar %s: %w", outFile, err)
			}
			fmt.Fprintf(e.out, "exportado para %s\n", outFile)
			return nil
		},
	}
	c.Flags().BoolVar(&withRuns, "with-runs", false, "inclui as execuções encerradas (sem logs)")
	c.Flags().BoolVar(&withLogs, "with-logs", false, "inclui também os logs das execuções (implica --with-runs)")
	c.Flags().StringVarP(&outFile, "output", "o", "", "grava neste arquivo (permissão 0600) em vez da saída padrão")
	return c
}

func (e *env) importCmd() *cobra.Command {
	var slug string
	var paused bool
	c := &cobra.Command{
		Use: "import <arquivo|->", Short: "Cria uma empresa NOVA a partir de um arquivo exportado (tudo ou nada)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var b []byte
			var err error
			if args[0] == "-" {
				b, err = io.ReadAll(e.in)
			} else {
				b, err = os.ReadFile(args[0])
			}
			if err != nil {
				return fmt.Errorf("ler %s: %w", args[0], err)
			}
			if !json.Valid(b) {
				return usageError{errors.New("o arquivo não é um JSON válido")}
			}
			res, err := e.client().ImportCompany(cmd.Context(), b, slug, paused)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			fmt.Fprintf(e.out, "empresa %s (%s) criada\n", res.Company.Name, res.Company.Slug)
			keys := make([]string, 0, len(res.Counts))
			for k := range res.Counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, []string{k, fmt.Sprint(res.Counts[k])})
			}
			e.table([]string{"ITEM", "QTD"}, rows)
			return nil
		},
	}
	c.Flags().StringVar(&slug, "slug", "", "slug da empresa nova (padrão: o do arquivo)")
	c.Flags().BoolVar(&paused, "paused", false, "personas entram pausadas e fluxos desligados")
	return c
}
