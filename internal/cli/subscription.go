package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

func weeklyLine(s api.Subscription) string {
	if s.WeeklyLimitUSD == nil {
		return "sem trava semanal"
	}
	state := "livre"
	if s.Blocked {
		state = "TRAVADA: novas execuções bloqueadas"
	}
	return fmt.Sprintf("%s de %s (%.0f%%), trava em %d%% — %s", usd(s.WeekUsageUSD), usd(*s.WeeklyLimitUSD), s.WeekPercent, s.WeeklyGuardPct, state)
}

func prefixesLine(p []string) string {
	if len(p) == 0 {
		return "todos os modelos do harness"
	}
	return strings.Join(p, ", ")
}

func (e *env) subscriptionCmd() *cobra.Command {
	c := &cobra.Command{Use: "subscription", Aliases: []string{"sub"}, Short: "Assinaturas: mensalidade, modelos cobertos e trava semanal"}

	var (
		agent           string
		monthly, weekly float64
		pct             int
		prefixes        []string
		inactive        bool
	)
	create := &cobra.Command{
		Use: "create <nome>", Short: "Cria uma assinatura", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.CreateSubscriptionReq{Name: args[0], Agent: agent, MonthlyUSD: monthly, ModelPrefixes: prefixes,
				WeeklyGuardPct: pct, Inactive: inactive}
			if cmd.Flags().Changed("weekly-limit") {
				req.WeeklyLimitUSD = &weekly
			}
			s, err := e.client().CreateSubscription(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, "Assinatura criada: %s — %s, %s por mês, cobre %s\n", s.Name, s.Agent, usd(s.MonthlyUSD), prefixesLine(s.ModelPrefixes))
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&agent, "agent", "", "harness/provedor do plano: claude, codex ou fake (obrigatório)")
	cf.Float64Var(&monthly, "monthly", 0, "mensalidade em dólares")
	cf.StringSliceVar(&prefixes, "models", nil, "prefixos de modelo cobertos (vírgula ou repetido); vazio = todos do harness")
	cf.Float64Var(&weekly, "weekly-limit", 0, "teto semanal de uso estimado em dólares (liga a trava semanal)")
	cf.IntVar(&pct, "guard-pct", 0, "trava semanal: percentual do teto que bloqueia novas execuções (padrão 90)")
	cf.BoolVar(&inactive, "inactive", false, "cria desligada")
	_ = create.MarkFlagRequired("agent")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista as assinaturas e o uso da semana", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ss, err := e.client().Subscriptions(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ss)
			}
			if len(ss) == 0 {
				fmt.Fprintln(e.out, "Nenhuma assinatura. Crie uma com: hanko subscription create <nome> --agent claude --monthly 200")
				return nil
			}
			rows := make([][]string, 0, len(ss))
			for _, s := range ss {
				week := "-"
				if s.WeeklyLimitUSD != nil {
					week = fmt.Sprintf("%s / %s", usd(s.WeekUsageUSD), usd(*s.WeeklyLimitUSD))
					if s.Blocked {
						week += " TRAVADA"
					}
				}
				rows = append(rows, []string{s.Name, s.Agent, usd(s.MonthlyUSD), onOff2(s.Active), week})
			}
			e.table([]string{"NOME", "HARNESS", "MENSALIDADE", "ESTADO", "USO DA SEMANA"}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show <assinatura>", Short: "Mostra uma assinatura", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			s, err := e.client().Subscription(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			e.kv([2]string{"Assinatura", s.Name}, [2]string{"ID", s.ID}, [2]string{"Harness", s.Agent},
				[2]string{"Mensalidade", usd(s.MonthlyUSD)}, [2]string{"Estado", onOff2(s.Active)},
				[2]string{"Modelos cobertos", prefixesLine(s.ModelPrefixes)}, [2]string{"Semana", weeklyLine(s)})
			return nil
		},
	}

	var (
		upName, upAgent, upModels string
		upMonthly, upWeekly       float64
		upPct                     int
		upNoWeekly                bool
		upOn, upOff               bool
	)
	update := &cobra.Command{
		Use: "update <assinatura>", Short: "Altera uma assinatura", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if upOn && upOff {
				return usageError{errors.New("--on e --off não combinam")}
			}
			req := client.UpdateSubscriptionReq{ClearWeeklyLimit: upNoWeekly}
			if f.Changed("name") {
				req.Name = &upName
			}
			if f.Changed("agent") {
				req.Agent = &upAgent
			}
			if f.Changed("monthly") {
				req.MonthlyUSD = &upMonthly
			}
			if f.Changed("models") {
				var p []string
				for _, m := range strings.Split(upModels, ",") {
					if m = strings.TrimSpace(m); m != "" {
						p = append(p, m)
					}
				}
				if p == nil {
					p = []string{}
				}
				req.ModelPrefixes = &p
			}
			if f.Changed("weekly-limit") {
				req.WeeklyLimitUSD = &upWeekly
			}
			if f.Changed("guard-pct") {
				req.WeeklyGuardPct = &upPct
			}
			if upOn || upOff {
				on := upOn
				req.Active = &on
			}
			changed := f.Changed("name") || f.Changed("agent") || f.Changed("monthly") || f.Changed("models") || f.Changed("weekly-limit") ||
				f.Changed("guard-pct") || upNoWeekly || upOn || upOff
			if !changed {
				return usageError{errors.New("nada a alterar: veja hanko subscription update --help")}
			}
			s, err := e.client().UpdateSubscription(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, "Assinatura atualizada: %s — %s por mês, %s\n", s.Name, usd(s.MonthlyUSD), weeklyLine(s))
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&upName, "name", "", "novo nome")
	uf.StringVar(&upAgent, "agent", "", "harness/provedor")
	uf.Float64Var(&upMonthly, "monthly", 0, "mensalidade em dólares")
	uf.StringVar(&upModels, "models", "", "prefixos de modelo cobertos, separados por vírgula (vazio = todos)")
	uf.Float64Var(&upWeekly, "weekly-limit", 0, "teto semanal em dólares")
	uf.BoolVar(&upNoWeekly, "no-weekly-limit", false, "remove o teto semanal (e a trava)")
	uf.IntVar(&upPct, "guard-pct", 0, "percentual do teto que trava novas execuções")
	uf.BoolVar(&upOn, "on", false, "liga a assinatura")
	uf.BoolVar(&upOff, "off", false, "desliga a assinatura")

	del := &cobra.Command{
		Use: "delete <assinatura>", Aliases: []string{"rm"}, Short: "Remove uma assinatura sem execuções (com histórico, use update --off)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteSubscription(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "Assinatura %s removida.\n", args[0])
			return nil
		},
	}

	c.AddCommand(create, ls, show, update, del)
	return c
}

func onOff2(b bool) string {
	if b {
		return "ligada"
	}
	return "desligada"
}
