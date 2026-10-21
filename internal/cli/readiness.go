package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
)

// Readiness da persona, catálogo de modelos e ambiente do projeto (ADR-0055).

func (e *env) readinessStatus(s string) string {
	switch s {
	case "ready":
		return e.t("pronta")
	case "not_ready":
		return e.t("NÃO PRONTA")
	}
	return e.t("nunca testada")
}

func (e *env) printReadiness(r api.PersonaReadiness) {
	pairs := [][2]string{{e.t("Resultado"), e.readinessStatus(r.Status)}}
	if r.Agent != "" {
		pairs = append(pairs, [2]string{e.t("Harness"), strings.TrimSpace(r.Agent + " " + r.Model)})
	}
	if r.EnvironmentSource != "" {
		pairs = append(pairs, [2]string{e.t("Ambiente (origem)"), r.EnvironmentSource})
	}
	for _, st := range r.Steps {
		pairs = append(pairs, [2]string{st.Name, st.Status + " — " + dash(st.Detail)})
	}
	if r.Reason != "" {
		pairs = append(pairs, [2]string{e.t("Motivo do bloqueio"), r.Reason})
	}
	if r.CheckedAt != nil {
		pairs = append(pairs, [2]string{e.t("Testada em"), e.fmtTimePtr(r.CheckedAt)})
	}
	if r.Blocking {
		pairs = append(pairs, [2]string{e.t("Despacho"), e.t("bloqueado até um teste passar")})
	}
	e.kv(pairs...)
}

// personaTestCmd é "hanko persona test": roda o teste de readiness agora; sai com erro se a persona não está pronta.
func (e *env) personaTestCmd() *cobra.Command {
	return &cobra.Command{
		Use: "test " + e.t("<persona>"), Short: e.t("Testa agora o harness da persona no ambiente dela: binário, login e modelo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().TestPersonaReadiness(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				if err := e.printJSON(r); err != nil {
					return err
				}
			} else {
				e.printReadiness(r)
			}
			if r.Status != "ready" {
				return fmt.Errorf("%s", e.t("a persona não está pronta; o despacho fica bloqueado até um teste passar"))
			}
			return nil
		},
	}
}

// personaReadinessCmd é "hanko persona readiness [persona]": o último teste de uma persona ou o quadro da empresa.
func (e *env) personaReadinessCmd() *cobra.Command {
	return &cobra.Command{
		Use: "readiness [" + e.t("persona") + "]", Short: e.t("Mostra o último teste de readiness de uma persona, ou de todas"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if len(a) == 1 {
				r, err := e.client().PersonaReadiness(cmd.Context(), co, a[0])
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(r)
				}
				e.printReadiness(r)
				return nil
			}
			rs, err := e.client().Readiness(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(rs)
			}
			ps, err := e.client().Personas(cmd.Context(), co)
			if err != nil {
				return err
			}
			names := map[string]string{}
			for _, p := range ps {
				names[p.ID] = p.Slug
			}
			rows := make([][]string, 0, len(rs))
			for _, r := range rs {
				rows = append(rows, []string{dash(names[r.PersonaID]), e.readinessStatus(r.Status), dash(strings.TrimSpace(r.Agent + " " + r.Model)),
					dash(r.EnvironmentSource), e.fmtTimePtr(r.CheckedAt), oneLine(dash(r.Reason), 60)})
			}
			e.table([]string{e.t("PERSONA"), e.t("ESTADO"), e.t("HARNESS"), e.t("AMBIENTE"), e.t("TESTADA EM"), e.t("MOTIVO")}, rows)
			return nil
		},
	}
}

// modelCmd é "hanko model list": o catálogo híbrido de modelos.
func (e *env) modelCmd() *cobra.Command {
	c := &cobra.Command{Use: "model", Short: e.t("Catálogo de modelos dos harnesses")}
	var agent string
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os modelos: aliases do harness, tabela de preços e o que testes e execuções relataram"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ms, err := e.client().Models(cmd.Context(), co, agent)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ms)
			}
			rows := make([][]string, 0, len(ms))
			for _, m := range ms {
				price := e.t("sem preço (usa o de reserva)")
				if m.Priced {
					price = m.PriceModel
				}
				rows = append(rows, []string{dash(m.Agent), m.Model, strings.Join(m.Origins, ","), price})
			}
			e.table([]string{e.t("HARNESS"), e.t("MODELO"), e.t("ORIGEM"), e.t("PREÇO")}, rows)
			return nil
		},
	}
	ls.Flags().StringVar(&agent, "agent", "", e.t("só os modelos deste harness (claude, codex, acp, fake)"))
	c.AddCommand(ls)
	return c
}

// projectEnvCmd é "hanko project env <projeto> <ambiente>": o ambiente das tasks do projeto.
func (e *env) projectEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use: "env " + e.t("<projeto> <ambiente|default>"), Short: e.t("Escolhe o ambiente de execução do projeto (a persona tem precedência; default = o da empresa)"),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().SetProjectEnvironment(cmd.Context(), co, a[0], a[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Ambiente do projeto %s: %s\n"), p.Slug, a[1])
			return nil
		},
	}
}
