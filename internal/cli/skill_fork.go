package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
)

// hanko skill fork e hanko run skills (ADR-0044).

func (e *env) skillForkCmd() *cobra.Command {
	var slug, name string
	var revision int
	var move bool
	c := &cobra.Command{
		Use: "fork " + e.t("<skill>"), Short: e.t("Bifurca uma skill (por exemplo, de fonte) em uma cópia local editável"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			k, err := e.client().ForkSkill(cmd.Context(), co, a[0], client.ForkSkillReq{Slug: slug, Name: name, Revision: revision, MovePersonas: move})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(k)
			}
			fmt.Fprintf(e.out, e.t("Skill %s criada a partir de %s (versão %d).\n"), k.Slug, a[0], k.ForkedFromRevision)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&slug, "slug", "", e.t("slug da cópia (padrão: <skill>-fork)"))
	f.StringVar(&name, "name", "", e.t("nome da cópia (padrão: o da original)"))
	f.IntVar(&revision, "revision", 0, e.t("versão a copiar (padrão: a atual)"))
	f.BoolVar(&move, "move-personas", false, e.t("passa as personas da original para a cópia"))
	return c
}

func (e *env) runSkillsCmd() *cobra.Command {
	return &cobra.Command{
		Use: "skills " + e.t("<run>"), Short: e.t("As skills (e versões) que a execução recebeu"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveRun(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			list, err := e.client().RunSkills(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(list)
			}
			if len(list) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma skill nesta execução."))
				return nil
			}
			rows := make([][]string, 0, len(list))
			for _, s := range list {
				pin := ""
				if s.Pinned {
					pin = e.t("fixada")
				}
				rows = append(rows, []string{s.Slug, "v" + strconv.Itoa(s.Revision), shortSHA(s.SHA256), pin})
			}
			e.table([]string{e.t("SKILL"), e.t("VERSÃO"), "SHA256", ""}, rows)
			return nil
		},
	}
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
