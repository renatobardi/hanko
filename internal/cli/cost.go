package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

func usd(v float64) string { return fmt.Sprintf("US$ %.2f", v) }

func centsUSD(c int64) string { return fmt.Sprintf("US$ %.2f", float64(c)/100) }

func budgetLine(b api.Budget) string {
	if b.BudgetMonthlyCents == nil {
		return "sem teto definido"
	}
	state := map[string]string{"ok": "dentro do teto", "warning": "atenção: acima de 80%", "exceeded": "estourado: execuções travadas"}[b.State]
	return fmt.Sprintf("%s de %s (%.0f%%) — %s", usd(b.SpentUSD), centsUSD(*b.BudgetMonthlyCents), b.Percent, state)
}

func (e *env) costCmd() *cobra.Command {
	var by, from, to string
	c := &cobra.Command{
		Use: "cost", Short: "Mostra os custos do mês (ou de um período) por dimensão", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			switch by {
			case "", "day", "persona", "project", "model", "harness":
			default:
				return usageError{fmt.Errorf("--by %q inválido: use day, persona, project, model ou harness", by)}
			}
			sum, err := e.client().Costs(cmd.Context(), co, client.CostQuery{From: from, To: to, GroupBy: by})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(sum)
			}
			if len(sum.Rows) == 0 {
				fmt.Fprintln(e.out, "Nenhum custo no período.")
			} else {
				rows := make([][]string, 0, len(sum.Rows)+1)
				for _, r := range sum.Rows {
					est := "-"
					if r.EstimatedUSD > 0 {
						est = usd(r.EstimatedUSD)
					}
					rows = append(rows, []string{dash(r.Label), usd(r.CostUSD), est, strconv.Itoa(r.Runs),
						strconv.FormatInt(r.InputTokens, 10), strconv.FormatInt(r.OutputTokens, 10)})
				}
				e.table([]string{strings.ToUpper(sum.GroupBy), "CUSTO", "ESTIMADO", "EXECUÇÕES", "TOKENS ENTRADA", "TOKENS SAÍDA"}, rows)
			}
			fmt.Fprintf(e.out, "\nTotal: %s (informado %s, estimado %s) em %d execuções\nOrçamento do mês: %s\n",
				usd(sum.TotalUSD), usd(sum.ReportedUSD), usd(sum.EstimatedUSD), sum.Runs, budgetLine(sum.Budget))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&by, "by", "day", "agrupar por: day, persona, project, model ou harness")
	f.StringVar(&from, "from", "", "início (AAAA-MM-DD); padrão: início do mês")
	f.StringVar(&to, "to", "", "fim inclusivo (AAAA-MM-DD); padrão: fim do mês")
	return c
}

// parseUSD converte "50" ou "12.50" em centavos.
func parseUSD(s string) (int64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, usageError{fmt.Errorf("valor inválido %q: informe dólares, ex.: 50 ou 12.50", s)}
	}
	return int64(math.Round(v * 100)), nil
}

func (e *env) budgetCmd() *cobra.Command {
	c := &cobra.Command{Use: "budget", Short: "Orçamento mensal (hard stop ao atingir 100%)"}
	var persona string
	var clear bool
	set := &cobra.Command{
		Use: "set [valor-em-dólares]", Short: "Define o orçamento mensal da empresa (ou de uma persona com --persona)", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if clear == (len(args) == 1) {
				return usageError{errors.New("informe o valor em dólares ou --clear (um dos dois)")}
			}
			var cents *int64
			if !clear {
				v, err := parseUSD(args[0])
				if err != nil {
					return err
				}
				cents = &v
			}
			if persona != "" {
				p, err := e.client().UpdatePersona(cmd.Context(), co, persona, client.UpdatePersonaReq{BudgetMonthlyCents: cents, ClearBudget: clear})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				fmt.Fprintf(e.out, "Orçamento de %s: %s\n", p.Name, budgetValue(p.BudgetMonthlyCents))
				return nil
			}
			cm, err := e.client().UpdateCompany(cmd.Context(), co, client.UpdateCompanyReq{BudgetMonthlyCents: cents, ClearBudget: clear})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cm)
			}
			fmt.Fprintf(e.out, "Orçamento da empresa %s: %s\n", cm.Name, budgetValue(cm.BudgetMonthlyCents))
			return nil
		},
	}
	set.Flags().StringVar(&persona, "persona", "", "persona (id ou slug); sem isso vale para a empresa")
	set.Flags().BoolVar(&clear, "clear", false, "remove o teto")
	show := &cobra.Command{
		Use: "show", Short: "Mostra o orçamento da empresa e das personas", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			sum, err := e.client().Costs(cmd.Context(), co, client.CostQuery{GroupBy: "persona"})
			if err != nil {
				return err
			}
			ps, err := e.client().Personas(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"company": sum.Budget, "personas": ps})
			}
			fmt.Fprintf(e.out, "Empresa: %s\n", budgetLine(sum.Budget))
			spent := map[string]float64{}
			for _, r := range sum.Rows {
				spent[r.Key] = r.CostUSD
			}
			rows := [][]string{}
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, budgetValue(p.BudgetMonthlyCents), usd(spent[p.ID]), p.Status})
			}
			if len(rows) > 0 {
				fmt.Fprintln(e.out)
				e.table([]string{"PERSONA", "TETO", "GASTO NO MÊS", "STATUS"}, rows)
			}
			return nil
		},
	}
	c.AddCommand(set, show)
	return c
}

func budgetValue(c *int64) string {
	if c == nil {
		return "sem teto"
	}
	return centsUSD(*c) + " por mês"
}
