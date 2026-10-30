package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Tags de persona (ADR-0087): `hanko persona tag`. `<tag>` é o id ou o nome, sem distinguir caixa.

func (e *env) personaTagCmd() *cobra.Command {
	c := &cobra.Command{Use: "tag", Aliases: []string{"tags"}, Short: e.t("Gerencia as tags de persona (cadastro, aplicar, tirar e recorte do organograma)")}
	c.AddCommand(e.tagLsCmd(), e.tagCreateCmd(), e.tagEditCmd(), e.tagArchiveCmd(true), e.tagArchiveCmd(false), e.tagDeleteCmd(),
		e.tagAddCmd(), e.tagRemoveCmd(), e.tagPersonasCmd())
	return c
}

// resolveTag acha a tag pelo id ou pelo nome, sem distinguir caixa (as arquivadas entram).
func (e *env) resolveTag(ctx context.Context, co, ref string) (api.PersonaTag, error) {
	if _, err := uuid.Parse(ref); err == nil {
		return e.client().GetPersonaTag(ctx, co, ref)
	}
	all, err := e.client().ListPersonaTags(ctx, co, true)
	if err != nil {
		return api.PersonaTag{}, err
	}
	key := strings.ToLower(strings.TrimSpace(ref))
	for _, t := range all {
		if strings.ToLower(t.Name) == key {
			return t, nil
		}
	}
	return api.PersonaTag{}, usageError{fmt.Errorf(e.t("tag %q não encontrada"), ref)}
}

func (e *env) tagLsCmd() *cobra.Command {
	var archived bool
	c := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: e.t("Lista o cadastro de tags (sem as arquivadas, a menos que --archived)"), Args: cobra.NoArgs,
		RunE: e.inCompany(func(cmd *cobra.Command, co string, _ []string) error {
			l, err := e.client().ListPersonaTags(cmd.Context(), co, archived)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma tag."))
				return nil
			}
			rows := make([][]string, 0, len(l))
			for _, t := range l {
				state := e.t("ativa")
				if t.Archived {
					state = e.t("arquivada")
				}
				rows = append(rows, []string{t.Name, t.Color, strconv.Itoa(t.PersonaCount), state, t.Description})
			}
			e.table([]string{e.t("NOME"), e.t("COR"), e.t("PERSONAS"), e.t("ESTADO"), e.t("DESCRIÇÃO")}, rows)
			return nil
		}),
	}
	c.Flags().BoolVar(&archived, "archived", false, e.t("inclui as tags arquivadas"))
	return c
}

func (e *env) printTag(t api.PersonaTag, msg string) error {
	if e.asJSON {
		return e.printJSON(t)
	}
	fmt.Fprintf(e.out, msg, t.Name, t.Color)
	return nil
}

func (e *env) tagCreateCmd() *cobra.Command {
	var color, desc string
	c := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria uma tag de persona"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			t, err := e.client().CreatePersonaTag(cmd.Context(), co, client.PersonaTagReq{Name: a[0], Color: color, Description: desc})
			if err != nil {
				return err
			}
			return e.printTag(t, e.t("Tag criada: %s (%s)\n"))
		}),
	}
	c.Flags().StringVar(&color, "color", "", e.t("cor: gray, red, amber, green, teal, blue, violet ou pink (padrão gray)"))
	c.Flags().StringVar(&desc, "description", "", e.t("descrição, até 200 caracteres"))
	return c
}

func (e *env) tagEditCmd() *cobra.Command {
	var name, color, desc string
	c := &cobra.Command{
		Use: "edit " + e.t("<tag>"), Short: e.t("Edita nome, cor ou descrição de uma tag (o id e as personas não mudam)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			req := client.UpdatePersonaTagReq{}
			f := cmd.Flags()
			if f.Changed("name") {
				req.Name = &name
			}
			if f.Changed("color") {
				req.Color = &color
			}
			if f.Changed("description") {
				req.Description = &desc
			}
			if req.Name == nil && req.Color == nil && req.Description == nil {
				return usageError{errors.New(e.t("informe --name, --color ou --description"))}
			}
			cur, err := e.resolveTag(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			t, err := e.client().UpdatePersonaTag(cmd.Context(), co, cur.ID, req)
			if err != nil {
				return err
			}
			return e.printTag(t, e.t("Tag atualizada: %s (%s)\n"))
		}),
	}
	c.Flags().StringVar(&name, "name", "", e.t("nome novo"))
	c.Flags().StringVar(&color, "color", "", e.t("cor nova"))
	c.Flags().StringVar(&desc, "description", "", e.t("descrição nova (vazia apaga)"))
	return c
}

func (e *env) tagArchiveCmd(archive bool) *cobra.Command {
	use, short, msg := "unarchive", e.t("Desarquiva uma tag (admin)"), e.t("Tag desarquivada: %s (%s)\n")
	if archive {
		use, short, msg = "archive", e.t("Arquiva uma tag: sai do seletor e fica nas personas (admin; se desfaz)"), e.t("Tag arquivada: %s (%s)\n")
	}
	return &cobra.Command{
		Use: use + " " + e.t("<tag>"), Short: short, Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			cur, err := e.resolveTag(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			t, err := e.client().SetPersonaTagArchived(cmd.Context(), co, cur.ID, archive)
			if err != nil {
				return err
			}
			return e.printTag(t, msg)
		}),
	}
}

func (e *env) tagDeleteCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use: "delete " + e.t("<tag>"), Short: e.t("Deleta uma tag e tira de todas as personas (admin; não se desfaz)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			cur, err := e.resolveTag(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if !yes {
				return usageError{fmt.Errorf(e.t("a tag %s sai de %d persona(s) e não se desfaz: confirme com --yes"), cur.Name, cur.PersonaCount)}
			}
			if err := e.client().DeletePersonaTag(cmd.Context(), co, cur.ID); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Tag %s deletada; %d persona(s) perderam a tag.\n"), cur.Name, cur.PersonaCount)
			return nil
		}),
	}
	c.Flags().BoolVar(&yes, "yes", false, e.t("confirma que a tag sai de todas as personas"))
	return c
}

func (e *env) tagAddCmd() *cobra.Command {
	var create bool
	var color string
	c := &cobra.Command{
		Use: "add " + e.t("<persona> <tag>"), Short: e.t("Aplica uma tag a uma persona (--create cria a tag se não existe)"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if create {
				t, err := e.client().CreateAndTagPersona(cmd.Context(), co, a[0], client.PersonaTagReq{Name: a[1], Color: color})
				if err != nil {
					return err
				}
				fmt.Fprintf(e.out, e.t("Tag %s aplicada a %s.\n"), t.Name, a[0])
				return nil
			}
			t, err := e.resolveTag(cmd.Context(), co, a[1])
			if err != nil {
				return err
			}
			if err := e.client().TagPersona(cmd.Context(), co, a[0], t.ID); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Tag %s aplicada a %s.\n"), t.Name, a[0])
			return nil
		}),
	}
	c.Flags().BoolVar(&create, "create", false, e.t("cria a tag se não existe"))
	c.Flags().StringVar(&color, "color", "", e.t("cor da tag criada com --create"))
	return c
}

func (e *env) tagRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove " + e.t("<persona> <tag>"), Short: e.t("Tira uma tag de uma persona"), Args: cobra.ExactArgs(2),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			t, err := e.resolveTag(cmd.Context(), co, a[1])
			if err != nil {
				return err
			}
			if err := e.client().UntagPersona(cmd.Context(), co, a[0], t.ID); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Tag %s tirada de %s.\n"), t.Name, a[0])
			return nil
		}),
	}
}

func (e *env) tagPersonasCmd() *cobra.Command {
	var tags []string
	var levels []int
	var q client.OrgPersonaQuery
	c := &cobra.Command{
		Use: "personas", Short: e.t("Lista as personas do organograma por tag e por nível"), Args: cobra.NoArgs,
		RunE: e.inCompany(func(cmd *cobra.Command, co string, _ []string) error {
			for _, ref := range tags {
				t, err := e.resolveTag(cmd.Context(), co, ref)
				if err != nil {
					return err
				}
				q.TagIDs = append(q.TagIDs, t.ID)
			}
			q.Levels = levels
			l, err := e.client().ListOrgPersonas(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			return e.printOrgPersonas(l)
		}),
	}
	f := c.Flags()
	f.StringSliceVar(&tags, "tag", nil, e.t("tags (id ou nome), separadas por vírgula"))
	f.BoolVar(&q.MatchAll, "all", false, e.t("exige todas as tags (padrão: alguma delas)"))
	f.BoolVar(&q.Untagged, "untagged", false, e.t("inclui as personas sem tag"))
	f.IntSliceVar(&levels, "level", nil, e.t("níveis, separados por vírgula (1 = topo)"))
	f.IntVar(&q.MaxDepth, "max-depth", 0, e.t("corta abaixo deste nível (0 = sem corte)"))
	f.BoolVar(&q.Archived, "archived", false, e.t("aceita tags arquivadas no filtro"))
	return c
}

func (e *env) printOrgPersonas(l []api.OrgPersona) error {
	if e.asJSON {
		return e.printJSON(l)
	}
	if len(l) == 0 {
		fmt.Fprintln(e.out, e.t("Nenhuma persona nesse recorte."))
		return nil
	}
	rows := make([][]string, 0, len(l))
	for _, p := range l {
		names := make([]string, len(p.Tags))
		for i, t := range p.Tags {
			names[i] = t.Name
		}
		rows = append(rows, []string{p.Persona.Slug, p.Persona.Name, strconv.Itoa(p.Level), strings.Join(names, ", ")})
	}
	e.table([]string{"SLUG", e.t("NOME"), e.t("NÍVEL"), "TAGS"}, rows)
	return nil
}
