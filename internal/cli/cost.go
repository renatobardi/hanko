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
	"github.com/renatobardi/hanko/internal/i18n"
)

// usd formata dólares: "US$ 1.00" em pt-BR, "$1.00" em inglês.
func (e *env) usd(v float64) string { return e.money("%.2f", v) }

func (e *env) centsUSD(c int64) string { return e.usd(float64(c) / 100) }

func (e *env) money(format string, v float64) string {
	if e.lang() == i18n.EN {
		return "$" + fmt.Sprintf(format, v)
	}
	return "US$ " + fmt.Sprintf(format, v)
}

func (e *env) budgetLine(b api.Budget) string {
	if b.BudgetMonthlyCents == nil {
		return e.t("sem teto definido")
	}
	state := ""
	switch b.State {
	case "ok":
		state = e.t("dentro do teto")
	case "warning":
		state = e.t("atenção: acima de 80%")
	case "exceeded":
		state = e.t("estourado: execuções travadas")
	}
	return e.tf("%s de %s (%.0f%%) — %s", e.usd(b.SpentUSD), e.centsUSD(*b.BudgetMonthlyCents), b.Percent, state)
}

func (e *env) costCmd() *cobra.Command {
	var by, from, to, project string
	c := &cobra.Command{
		Use: "cost", Short: e.t("Mostra os custos do mês (ou de um período) por dimensão"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			switch by {
			case "", "day", "persona", "project", "model", "harness":
			default:
				return usageError{fmt.Errorf(e.t("--by %q inválido: use day, persona, project, model ou harness"), by)}
			}
			sum, err := e.client().Costs(cmd.Context(), co, client.CostQuery{From: from, To: to, GroupBy: by, Project: project})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(sum)
			}
			if len(sum.Rows) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum custo no período."))
			} else {
				rows := make([][]string, 0, len(sum.Rows)+1)
				for _, r := range sum.Rows {
					est := "-"
					if r.EstimatedUSD > 0 {
						est = e.usd(r.EstimatedUSD)
					}
					paid := "-"
					switch {
					case r.Unused:
						paid = e.t("sem uso")
					case r.PaidUSD > 0:
						paid = e.usd(r.PaidUSD)
					}
					rows = append(rows, []string{dash(r.Label), e.usd(r.ReportedUSD), est, e.usd(r.ListUSD), paid, strconv.Itoa(r.Runs),
						strconv.FormatInt(r.InputTokens, 10), strconv.FormatInt(r.OutputTokens, 10)})
				}
				e.table([]string{strings.ToUpper(sum.GroupBy), e.t("REAL"), e.t("ESTIMADO"), e.t("LISTA"), e.t("PAGO"), e.t("EXECUÇÕES"), e.t("TOKENS ENTRADA"), e.t("TOKENS SAÍDA")}, rows)
			}
			fmt.Fprintf(e.out, e.t("\nTotal: %s (real %s, estimado %s) em %d execuções\nCusto de lista: %s · pago (mensalidades rateadas): %s · sem uso: %s\nOrçamento do mês: %s\n"),
				e.usd(sum.TotalUSD), e.usd(sum.ReportedUSD), e.usd(sum.EstimatedUSD), sum.Runs, e.usd(sum.ListUSD), e.usd(sum.PaidUSD), e.usd(sum.IdleUSD), e.budgetLine(sum.Budget))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&by, "by", "day", e.t("agrupar por: day, persona, project, model ou harness"))
	f.StringVar(&from, "from", "", e.t("início (AAAA-MM-DD); padrão: início do mês"))
	f.StringVar(&to, "to", "", e.t("fim inclusivo (AAAA-MM-DD); padrão: fim do mês"))
	f.StringVar(&project, "project", "", e.t("só as execuções das tasks de um projeto (id ou slug)"))
	return c
}

// parseUSD converte "50" ou "12.50" em centavos.
func (e *env) parseUSD(s string) (int64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, usageError{fmt.Errorf(e.t("valor inválido %q: informe dólares, ex.: 50 ou 12.50"), s)}
	}
	return int64(math.Round(v * 100)), nil
}

func (e *env) budgetCmd() *cobra.Command {
	c := &cobra.Command{Use: "budget", Short: e.t("Orçamento mensal (hard stop ao atingir 100%)")}
	var persona, project string
	var clear bool
	set := &cobra.Command{
		Use: "set " + e.t("[valor-em-dólares]"), Short: e.t("Define o orçamento mensal da empresa (ou de uma persona com --persona, de um projeto com --project)"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if clear == (len(args) == 1) {
				return usageError{errors.New(e.t("informe o valor em dólares ou --clear (um dos dois)"))}
			}
			var cents *int64
			if !clear {
				v, err := e.parseUSD(args[0])
				if err != nil {
					return err
				}
				cents = &v
			}
			if persona != "" && project != "" {
				return usageError{errors.New(e.t("use --persona ou --project, não os dois"))}
			}
			if project != "" {
				p, err := e.client().UpdateProject(cmd.Context(), co, project, client.UpdateProjectReq{BudgetMonthlyCents: cents, ClearBudget: clear})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				fmt.Fprintf(e.out, e.t("Orçamento do projeto %s: %s\n"), p.Name, e.budgetValue(p.BudgetMonthlyCents))
				return nil
			}
			if persona != "" {
				p, err := e.client().UpdatePersona(cmd.Context(), co, persona, client.UpdatePersonaReq{BudgetMonthlyCents: cents, ClearBudget: clear})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				fmt.Fprintf(e.out, e.t("Orçamento de %s: %s\n"), p.Name, e.budgetValue(p.BudgetMonthlyCents))
				return nil
			}
			cm, err := e.client().UpdateCompany(cmd.Context(), co, client.UpdateCompanyReq{BudgetMonthlyCents: cents, ClearBudget: clear})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cm)
			}
			fmt.Fprintf(e.out, e.t("Orçamento da empresa %s: %s\n"), cm.Name, e.budgetValue(cm.BudgetMonthlyCents))
			return nil
		},
	}
	set.Flags().StringVar(&persona, "persona", "", e.t("persona (id ou slug); sem isso vale para a empresa"))
	set.Flags().StringVar(&project, "project", "", e.t("projeto (id ou slug)"))
	set.Flags().BoolVar(&clear, "clear", false, e.t("remove o teto"))
	show := &cobra.Command{
		Use: "show", Short: e.t("Mostra o orçamento da empresa e das personas"), Args: cobra.NoArgs,
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
			prs, err := e.client().Projects(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"company": sum.Budget, "personas": ps, "projects": prs})
			}
			fmt.Fprintf(e.out, e.t("Empresa: %s\n"), e.budgetLine(sum.Budget))
			spent := map[string]float64{}
			for _, r := range sum.Rows {
				spent[r.Key] = r.CostUSD
			}
			rows := [][]string{}
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, e.budgetValue(p.BudgetMonthlyCents), e.usd(spent[p.ID]), p.Status})
			}
			if len(rows) > 0 {
				fmt.Fprintln(e.out)
				e.table([]string{e.t("PERSONA"), e.t("TETO"), e.t("GASTO NO MÊS"), e.t("STATUS")}, rows)
			}
			prows := [][]string{}
			for _, p := range prs {
				if p.BudgetMonthlyCents == nil || p.Stats == nil {
					continue
				}
				prows = append(prows, []string{p.Slug, e.budgetValue(p.BudgetMonthlyCents), e.usd(p.Stats.Budget.SpentUSD), p.Stats.Budget.State})
			}
			if len(prows) > 0 {
				fmt.Fprintln(e.out)
				e.table([]string{e.t("PROJETO"), e.t("TETO"), e.t("GASTO NO MÊS"), e.t("ESTADO")}, prows)
			}
			return nil
		},
	}
	c.AddCommand(set, show)
	return c
}

func (e *env) budgetValue(c *int64) string {
	if c == nil {
		return e.t("sem teto")
	}
	return e.tf("%s por mês", e.centsUSD(*c))
}
