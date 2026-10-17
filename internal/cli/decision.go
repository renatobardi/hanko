package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
)

// Decision model (ADR-0056): hanko decision ...

func (e *env) decisionCmd() *cobra.Command {
	c := &cobra.Command{Use: "decision", Short: e.t("Decision model da empresa: classificador externo (OpenRouter) para triagem e afins")}

	show := &cobra.Command{
		Use: "show", Short: e.t("Mostra o decision model e o estado das features"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			d, err := e.client().GetDecision(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			if d.Config == nil {
				fmt.Fprintln(e.out, e.t("Decision model não configurado."))
			} else {
				fmt.Fprintf(e.out, e.t("Modelo: %s\nEndpoint: %s\nChave: %s\nLigado: %s\n"), d.Config.Model, d.Config.BaseURL, dash(d.Config.APIKeyRef), e.yesNo(d.Config.Enabled))
			}
			rows := make([][]string, len(d.Features))
			for i, f := range d.Features {
				rows[i] = []string{f.Feature, e.yesNo(f.Enabled), dash(f.Model), f.Title}
			}
			e.table([]string{e.t("FEATURE"), e.t("LIGADA"), e.t("MODELO"), e.t("DESCRIÇÃO")}, rows)
			return nil
		},
	}

	var baseURL, keyRef string
	catalog := &cobra.Command{
		Use: "catalog", Short: e.t("Lista os modelos do catálogo do provedor (com preço por milhão de tokens)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ms, err := e.client().DecisionCatalog(cmd.Context(), co, baseURL, keyRef)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ms)
			}
			rows := make([][]string, len(ms))
			for i, m := range ms {
				rows[i] = []string{m.ID, strconv.FormatInt(m.ContextLength, 10), fmt.Sprintf("%.2f", m.PromptUSD*1e6), fmt.Sprintf("%.2f", m.CompletionUSD*1e6)}
			}
			e.table([]string{e.t("MODELO"), e.t("CONTEXTO"), e.t("ENTRADA US$/M"), e.t("SAÍDA US$/M")}, rows)
			return nil
		},
	}
	catalog.Flags().StringVar(&baseURL, "base-url", "", e.t("outro endpoint OpenAI-compatível (padrão: o configurado, ou o do OpenRouter)"))
	catalog.Flags().StringVar(&keyRef, "api-key-ref", "", e.t("referência ${secret:NOME} da chave desse endpoint"))

	var sBase, sKey string
	var sTimeout int
	var sOff bool
	set := &cobra.Command{
		Use: "set " + e.t("<modelo>"), Short: e.t("Configura o decision model: modelo do catálogo, endpoint e chave (${secret:NOME})"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.SetDecisionReq{BaseURL: sBase, APIKeyRef: sKey, Model: a[0], TimeoutSec: sTimeout}
			if sOff {
				off := false
				req.Enabled = &off
			}
			d, err := e.client().SetDecision(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			fmt.Fprintf(e.out, e.t("Decision model configurado: %s\n"), d.Config.Model)
			if d.Warning != "" {
				fmt.Fprintf(e.out, e.t("Aviso: %s\n"), d.Warning)
			}
			return nil
		},
	}
	sf := set.Flags()
	sf.StringVar(&sBase, "base-url", "", e.t("endpoint OpenAI-compatível (padrão: OpenRouter)"))
	sf.StringVar(&sKey, "api-key-ref", "", e.t("referência ${secret:NOME} ao segredo com a chave (nunca a chave em claro)"))
	sf.IntVar(&sTimeout, "timeout", 0, e.t("prazo de cada chamada em segundos (padrão 30)"))
	sf.BoolVar(&sOff, "off", false, e.t("salva desligado"))

	rm := &cobra.Command{
		Use: "rm", Short: e.t("Remove o decision model e desliga as features"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteDecision(cmd.Context(), co); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Decision model removido."))
			return nil
		},
	}

	var fModel string
	feature := &cobra.Command{
		Use: "feature " + e.t("<triage|phase_classifier|name_summary> <on|off>"), Short: e.t("Liga ou desliga uma feature (--model dá um modelo próprio a ela)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var on bool
			switch strings.ToLower(a[1]) {
			case "on":
				on = true
			case "off":
			default:
				return usageError{fmt.Errorf(e.t("use on ou off, não %q"), a[1])}
			}
			d, err := e.client().SetDecisionFeature(cmd.Context(), co, a[0], on, fModel)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			fmt.Fprintf(e.out, e.t("Feature %s: %s\n"), a[0], e.yesNo(on))
			return nil
		},
	}
	feature.Flags().StringVar(&fModel, "model", "", e.t("modelo próprio da feature (vazio usa o da empresa)"))

	var iLimit int
	inv := &cobra.Command{
		Use: "invocations", Aliases: []string{"log"}, Short: e.t("Últimas invocações do decision model (ator system, com custo)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			l, err := e.client().DecisionInvocations(cmd.Context(), co, iLimit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma invocação."))
				return nil
			}
			rows := make([][]string, len(l))
			for i, x := range l {
				rows[i] = []string{e.fmtTime(x.CreatedAt), x.Feature, x.Model, x.Status, fmt.Sprintf("%.6f", x.CostUSD), x.CostSource, strconv.Itoa(x.LatencyMs)}
			}
			e.table([]string{e.t("QUANDO"), e.t("FEATURE"), e.t("MODELO"), e.t("STATUS"), e.t("CUSTO US$"), e.t("ORIGEM"), e.t("MS")}, rows)
			return nil
		},
	}
	inv.Flags().IntVar(&iLimit, "limit", 20, e.t("quantas mostrar"))

	triage := &cobra.Command{
		Use: "triage " + e.t("<task>"), Short: e.t("Triagem: o decision model sugere persona e prioridade (comentário do sistema; não atribui)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().TriageTask(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("%s: sugerida a persona %s, prioridade %s (custo US$ %.6f)\n"), t.TaskKey, t.Persona, t.Priority, t.Invocation.CostUSD)
			if t.Reason != "" {
				fmt.Fprintln(e.out, t.Reason)
			}
			return nil
		},
	}

	phase := &cobra.Command{
		Use: "phase " + e.t("<pipeline> <texto>"), Short: e.t("Sugere a etapa de um pipeline para um texto"), Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().ClassifyPhase(cmd.Context(), co, a[0], strings.Join(a[1:], " "))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Etapa sugerida: %s (%s)\n"), p.Stage, p.StageName)
			return nil
		},
	}

	name := &cobra.Command{
		Use: "name " + e.t("<texto>"), Short: e.t("Gera um nome curto e um resumo para um texto"), Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			n, err := e.client().NameSummary(cmd.Context(), co, strings.Join(a, " "))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(n)
			}
			fmt.Fprintf(e.out, "%s\n%s\n", n.Name, n.Summary)
			return nil
		},
	}

	c.AddCommand(show, catalog, set, rm, feature, inv, triage, phase, name)
	return c
}
