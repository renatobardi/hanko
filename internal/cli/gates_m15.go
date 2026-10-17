package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Gates estendidos (ADR-0041): responder, dispensar, reabrir e reenviar gates; política de plano; persona principal e
// de sistema; contratação por gate; incidentes de orçamento.

// answerPairs agrupa --set chave=valor (repetível; a mesma chave repetida soma valores, para campos multi).
func (e *env) answerPairs(pairs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, usageError{fmt.Errorf(e.t("--set %q: use chave=valor"), p)}
		}
		out[k] = append(out[k], v)
	}
	return out, nil
}

func (e *env) gateExtraCmds() []*cobra.Command {
	answer := &cobra.Command{
		Use: "answer " + e.t("<gate>"), Short: e.t("Responde um gate de pergunta (use --set chave=valor para cada campo)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveGate(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			sets, _ := cmd.Flags().GetStringArray("set")
			answers, err := e.answerPairs(sets)
			if err != nil {
				return err
			}
			note, _ := cmd.Flags().GetString("note")
			g, err := e.client().AnswerGate(cmd.Context(), co, id, client.AnswerGateReq{Answers: answers, Note: note})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Gate %s respondido: %s\n"), shortID(g.ID), g.Title)
			return nil
		},
	}
	answer.Flags().StringArray("set", nil, e.t("resposta de um campo, chave=valor (repetível; repita a chave para escolhas múltiplas)"))
	answer.Flags().StringP("note", "n", "", e.t("nota (vai para a thread da task)"))

	dismiss := &cobra.Command{
		Use: "dismiss " + e.t("<gate>"), Short: e.t("Dispensa uma pergunta sem responder (a persona segue sem a resposta)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveGate(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			note, _ := cmd.Flags().GetString("note")
			g, err := e.client().DismissGate(cmd.Context(), co, id, note)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Gate %s dispensado: %s\n"), shortID(g.ID), g.Title)
			return nil
		},
	}
	dismiss.Flags().StringP("note", "n", "", e.t("nota (vai para a thread da task)"))

	reopen := &cobra.Command{
		Use: "reopen " + e.t("<gate>"), Short: e.t("Reabre uma pergunta dispensada"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveGate(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			g, err := e.client().ReopenGate(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Gate %s reaberto: %s\n"), shortID(g.ID), g.Title)
			return nil
		},
	}

	var body, bodyFile, artifact, note, title string
	resubmit := &cobra.Command{
		Use: "resubmit " + e.t("<gate>"), Short: e.t("Reenvia o pedido no mesmo gate depois de mudanças pedidas (revisão +1)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if bodyFile != "" {
				b, err := os.ReadFile(bodyFile)
				if err != nil {
					return fmt.Errorf(e.t("ler %s: %w"), bodyFile, err)
				}
				body = string(b)
			}
			id, err := e.resolveGate(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			g, err := e.client().ResubmitGate(cmd.Context(), co, id, client.ResubmitGateReq{Title: title, Body: body, Artifact: artifact, Note: note})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Gate %s reenviado (revisão %d): %s\n"), shortID(g.ID), g.Revision, g.Title)
			return nil
		},
	}
	rf := resubmit.Flags()
	rf.StringVar(&title, "title", "", e.t("título novo (vazio mantém)"))
	rf.StringVar(&body, "body", "", e.t("texto novo do pedido"))
	rf.StringVar(&bodyFile, "body-file", "", e.t("arquivo com o texto novo do pedido"))
	rf.StringVar(&artifact, "artifact", "", e.t("gate de plano: artefato (nome ou id) com a versão nova do plano"))
	rf.StringVarP(&note, "note", "n", "", e.t("o que mudou"))

	history := &cobra.Command{
		Use: "history " + e.t("<gate>"), Short: e.t("Mostra as revisões de um gate e o diff até a versão vigente"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveGate(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			h, err := e.client().GateRevisions(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(h)
			}
			fmt.Fprintf(e.out, e.t("Gate %s: revisão vigente %d (%d anteriores)\n"), shortID(h.Gate.ID), h.Gate.Revision, len(h.Revisions))
			for _, r := range h.Revisions {
				fmt.Fprintf(e.out, "  r%d · %s · %s\n", r.Revision, e.fmtTime(r.CreatedAt), dash(oneLine(r.DecisionNote, 80)))
			}
			if h.Diff != "" {
				fmt.Fprintf(e.out, e.t("\nDiff (+%d −%d):\n%s"), h.Added, h.Removed, h.Diff)
			}
			return nil
		},
	}
	return []*cobra.Command{answer, dismiss, reopen, resubmit, history}
}

// planPolicyCmd é `hanko plan-policy`: a política de plan-review por task, projeto ou persona.
func (e *env) planPolicyCmd() *cobra.Command {
	c := &cobra.Command{Use: "plan-policy", Short: e.t("Plan-review: exigir um plano aprovado antes de executar (task, projeto ou persona)")}
	show := &cobra.Command{
		Use: "show " + e.t("<task>"), Short: e.t("Mostra a política de plano de uma task por camada e a fase atual"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().TaskPlanPolicy(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			layer := func(b *bool) string {
				switch {
				case b == nil:
					return e.t("herda")
				case *b:
					return e.t("exige")
				}
				return e.t("dispensa")
			}
			e.kv([2]string{e.t("Task"), layer(v.Task)}, [2]string{e.t("Projeto"), layer(v.Project)}, [2]string{e.t("Persona"), layer(v.Persona)},
				[2]string{e.t("Vale"), fmt.Sprint(v.Required)}, [2]string{e.t("Origem"), dash(v.Source)}, [2]string{e.t("Fase"), dash(v.Phase)},
				[2]string{e.t("Plano pendente"), fmt.Sprint(v.PendingPlan)})
			return nil
		},
	}
	var scope string
	var on, off, inherit bool
	set := &cobra.Command{
		Use: "set " + e.t("<alvo>"), Short: e.t("Liga, desliga ou limpa a política (alvo: chave da task, slug do projeto ou da persona)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			n := 0
			for _, b := range []bool{on, off, inherit} {
				if b {
					n++
				}
			}
			if n != 1 {
				return usageError{errors.New(e.t("informe --on, --off ou --inherit (um dos três)"))}
			}
			req := client.SetPlanPolicyReq{Scope: scope, Target: args[0]}
			if on || off {
				req.Require = &on
			}
			if err := e.client().SetPlanPolicy(cmd.Context(), co, req); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Política de plano gravada."))
			return nil
		},
	}
	sf := set.Flags()
	sf.StringVar(&scope, "scope", "task", e.t("camada: task, project ou persona"))
	sf.BoolVar(&on, "on", false, e.t("exige plano aprovado"))
	sf.BoolVar(&off, "off", false, e.t("dispensa o plano (vence a camada seguinte)"))
	sf.BoolVar(&inherit, "inherit", false, e.t("volta a herdar da camada seguinte"))
	c.AddCommand(show, set)
	return c
}

// personaExtraCmds são `persona primary` e `persona system`.
func (e *env) personaExtraCmds() []*cobra.Command {
	primary := &cobra.Command{
		Use: "primary " + e.t("[persona]"), Short: e.t("Mostra ou escolhe a sua persona principal (padrão das tasks e do chat); --clear limpa"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			clear, _ := cmd.Flags().GetBool("clear")
			if clear && len(args) == 1 {
				return usageError{errors.New(e.t("informe a persona ou --clear (um dos dois)"))}
			}
			if clear || len(args) == 1 {
				ref := ""
				if len(args) == 1 {
					ref = args[0]
				}
				p, err := e.client().SetPrimaryPersona(cmd.Context(), co, ref)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				if clear {
					fmt.Fprintln(e.out, e.t("Persona principal limpa."))
				} else {
					fmt.Fprintf(e.out, e.t("Persona principal: %s (%s)\n"), p.Name, p.Slug)
				}
				return nil
			}
			p, err := e.client().PrimaryPersona(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Persona principal: %s (%s)\n"), p.Name, p.Slug)
			return nil
		},
	}
	primary.Flags().Bool("clear", false, e.t("limpa a persona principal"))

	system := &cobra.Command{
		Use: "system " + e.t("<persona>"), Short: e.t("Marca (ou, com --off, desmarca) uma persona de sistema: não se desliga e só um owner a edita"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			off, _ := cmd.Flags().GetBool("off")
			p, err := e.client().SetPersonaSystem(cmd.Context(), co, args[0], !off)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			if p.System {
				fmt.Fprintf(e.out, e.t("%s agora é uma persona de sistema.\n"), p.Name)
			} else {
				fmt.Fprintf(e.out, e.t("%s deixou de ser uma persona de sistema.\n"), p.Name)
			}
			return nil
		},
	}
	system.Flags().Bool("off", false, e.t("desmarca"))
	return []*cobra.Command{primary, system}
}

// companyPolicyCmd é `hanko company policy`: a política de contratação por gate.
func (e *env) companyPolicyCmd() *cobra.Command {
	return &cobra.Command{
		Use: "policy " + e.t("[hire-gate=on|off]"), Short: e.t("Mostra ou muda as políticas da empresa (hire-gate: humanos também contratam por gate; owner)"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var pol api.CompanyPolicies
			if len(args) == 1 {
				v := strings.TrimPrefix(args[0], "hire-gate=")
				if v != "on" && v != "off" {
					return usageError{errors.New(e.t("use hire-gate=on ou hire-gate=off"))}
				}
				pol, err = e.client().SetCompanyPolicies(cmd.Context(), co, v == "on")
			} else {
				pol, err = e.client().CompanyPolicies(cmd.Context(), co)
			}
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(pol)
			}
			state := e.t("desligada")
			if pol.RequireHireGate {
				state = e.t("ligada")
			}
			fmt.Fprintf(e.out, e.t("Contratação por gate para humanos: %s\n"), state)
			return nil
		},
	}
}

// budgetIncidentCmds são `budget incidents` e `budget resolve`.
func (e *env) budgetIncidentCmds() []*cobra.Command {
	var all bool
	ls := &cobra.Command{
		Use: "incidents", Short: e.t("Lista os incidentes de orçamento (tetos estourados esperando decisão)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			list, err := e.client().BudgetIncidents(cmd.Context(), co, !all)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(list)
			}
			if len(list) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum incidente de orçamento."))
				return nil
			}
			rows := make([][]string, 0, len(list))
			for _, i := range list {
				state := e.t("aberto")
				if !i.Open {
					state = i.Resolution
				}
				rows = append(rows, []string{shortID(i.ID), state, i.Scope, i.TargetName, i.Month,
					fmt.Sprintf("US$ %.2f / %.2f", i.SpentUSD, float64(i.BudgetCents)/100)})
			}
			sort.SliceStable(rows, func(a, b int) bool { return rows[a][1] == e.t("aberto") && rows[b][1] != e.t("aberto") })
			e.table([]string{e.t("ID"), e.t("ESTADO"), e.t("ESCOPO"), e.t("ALVO"), e.t("MÊS"), e.t("GASTO / TETO")}, rows)
			return nil
		},
	}
	ls.Flags().BoolVar(&all, "all", false, e.t("inclui os já resolvidos"))

	var raise float64
	var remove, keep, once bool
	var note string
	resolve := &cobra.Command{
		Use: "resolve " + e.t("<incidente>"), Short: e.t("Resolve um incidente: --raise <US$> sobe o teto, --remove-ceiling tira o teto, --keep mantém pausado, --once libera só um run (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.ResolveIncidentReq{Note: note}
			n := 0
			if cmd.Flags().Changed("raise") {
				cents := int64(raise*100 + 0.5)
				req.Resolution, req.BudgetCents = "ceiling_raised", &cents
				n++
			}
			if remove {
				req.Resolution, req.RemoveCeiling = "ceiling_raised", true
				n++
			}
			if keep {
				req.Resolution = "kept_paused"
				n++
			}
			if once {
				req.Resolution = "released_once"
				n++
			}
			if n != 1 {
				return usageError{errors.New(e.t("informe exatamente um entre --raise, --remove-ceiling, --keep e --once"))}
			}
			list, err := e.client().BudgetIncidents(cmd.Context(), co, false)
			if err != nil {
				return err
			}
			var id string
			for _, i := range list {
				if strings.HasPrefix(i.ID, args[0]) {
					if id != "" {
						return usageError{fmt.Errorf(e.t("prefixo %q é ambíguo"), args[0])}
					}
					id = i.ID
				}
			}
			if id == "" {
				return fmt.Errorf(e.t("incidente %q não encontrado"), args[0])
			}
			r, err := e.client().ResolveBudgetIncident(cmd.Context(), co, id, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("Incidente %s resolvido: %s\n"), shortID(r.ID), r.Resolution)
			return nil
		},
	}
	rf := resolve.Flags()
	rf.Float64Var(&raise, "raise", 0, e.t("novo teto mensal em dólares (precisa passar do gasto do mês)"))
	rf.BoolVar(&remove, "remove-ceiling", false, e.t("remove o teto"))
	rf.BoolVar(&keep, "keep", false, e.t("mantém pausado e registra a decisão"))
	rf.BoolVar(&once, "once", false, e.t("libera só mais um run, sem mexer no teto"))
	rf.StringVarP(&note, "note", "n", "", e.t("motivo da decisão"))
	return []*cobra.Command{ls, resolve}
}
