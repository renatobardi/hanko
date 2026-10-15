package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/domain"
)

// readSkillSource lê o SKILL.md de um arquivo ("-" = entrada padrão).
func (e *env) readSkillSource(path string) (string, error) {
	var (
		b   []byte
		err error
	)
	if path == "-" {
		b, err = io.ReadAll(io.LimitReader(e.in, domain.MaxSkillBodyBytes+4096))
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", e.t("ler o arquivo da skill"), err)
	}
	return string(b), nil
}

func (e *env) skillCmd() *cobra.Command {
	c := &cobra.Command{Use: "skill", Short: e.t("Skills: habilidades reutilizáveis (SKILL.md) que as personas habilitadas carregam sob demanda")}

	var (
		file, slug, desc string
	)
	create := &cobra.Command{
		Use: "create [" + e.t("<nome>") + "]", Short: e.t("Cria uma skill a partir de um SKILL.md (--file) ou da entrada padrão"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if file == "" {
				return usageError{errors.New(e.t("informe o SKILL.md com --file (use - para a entrada padrão)"))}
			}
			body, err := e.readSkillSource(file)
			if err != nil {
				return err
			}
			req := client.CreateSkillReq{Slug: slug, Description: desc, Body: body}
			if len(args) == 1 {
				req.Name = args[0]
			}
			s, err := e.client().CreateSkill(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("Skill criada: %s (%s), versão %d\n"), s.Name, s.Slug, s.Version)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVarP(&file, "file", "f", "", e.t("arquivo SKILL.md (com ou sem frontmatter); - lê a entrada padrão"))
	cf.StringVar(&slug, "slug", "", e.t("identificador da skill (padrão: derivado do nome)"))
	cf.StringVar(&desc, "description", "", e.t("quando usar a skill (padrão: o do frontmatter)"))

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as skills da empresa"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ss, err := e.client().Skills(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ss)
			}
			if len(ss) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma skill. Crie uma com: hanko skill create --file SKILL.md"))
				return nil
			}
			rows := make([][]string, 0, len(ss))
			for _, s := range ss {
				n := 0
				if s.Personas != nil {
					n = *s.Personas
				}
				rows = append(rows, []string{s.Slug, s.Name, fmt.Sprint(s.Version), fmt.Sprint(n), oneLine(s.Description, 60)})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("VERSÃO"), e.t("PERSONAS"), e.t("DESCRIÇÃO")}, rows)
			return nil
		},
	}

	var asMD bool
	show := &cobra.Command{
		Use: "show " + e.t("<skill>"), Short: e.t("Mostra uma skill, com o corpo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			s, err := e.client().Skill(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if asMD { // o SKILL.md exatamente como o harness o recebe
				_, err := fmt.Fprint(e.out, domain.SkillMD(s.Slug, s.Description, s.Body))
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			e.kv([2]string{e.t("Skill"), s.Name}, [2]string{e.t("Slug"), s.Slug}, [2]string{e.t("ID"), s.ID},
				[2]string{e.t("Versão"), fmt.Sprint(s.Version)}, [2]string{e.t("Descrição"), s.Description},
				[2]string{e.t("Atualizada"), e.fmtTime(s.UpdatedAt)})
			fmt.Fprintf(e.out, "\n%s\n", s.Body)
			return nil
		},
	}
	show.Flags().BoolVar(&asMD, "md", false, e.t("imprime só o SKILL.md, como o harness o recebe"))

	var (
		upFile, upName, upDesc string
	)
	edit := &cobra.Command{
		Use: "edit " + e.t("<skill>"), Short: e.t("Altera uma skill (a versão sobe)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var req client.UpdateSkillReq
			if f.Changed("file") {
				body, err := e.readSkillSource(upFile)
				if err != nil {
					return err
				}
				req.Body = &body
			}
			if f.Changed("name") {
				req.Name = &upName
			}
			if f.Changed("description") {
				req.Description = &upDesc
			}
			if req.Body == nil && req.Name == nil && req.Description == nil {
				return usageError{errors.New(e.t("nada a alterar: veja hanko skill edit --help"))}
			}
			s, err := e.client().UpdateSkill(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("Skill atualizada: %s (%s), versão %d\n"), s.Name, s.Slug, s.Version)
			return nil
		},
	}
	ef := edit.Flags()
	ef.StringVarP(&upFile, "file", "f", "", e.t("novo SKILL.md; - lê a entrada padrão"))
	ef.StringVar(&upName, "name", "", e.t("novo nome"))
	ef.StringVar(&upDesc, "description", "", e.t("nova descrição"))

	del := &cobra.Command{
		Use: "delete " + e.t("<skill>"), Aliases: []string{"rm"}, Short: e.t("Apaga uma skill e os vínculos com personas"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteSkill(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Skill %s apagada.\n"), args[0])
			return nil
		},
	}

	toggle := func(use, short string, enabled bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<skill> <persona>..."), Short: short, Args: cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				for _, p := range args[1:] {
					if err := e.client().SetPersonaSkill(cmd.Context(), co, p, args[0], enabled); err != nil {
						return err
					}
					if enabled {
						fmt.Fprintf(e.out, e.t("Skill %s habilitada para %s.\n"), args[0], p)
					} else {
						fmt.Fprintf(e.out, e.t("Skill %s desabilitada para %s.\n"), args[0], p)
					}
				}
				return nil
			},
		}
	}

	personas := &cobra.Command{
		Use: "personas " + e.t("<skill>"), Short: e.t("Mostra quais personas têm a skill habilitada"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().SkillPersonas(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			var on []string
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, p.Name, e.onOff2(p.Enabled)})
				if p.Enabled {
					on = append(on, p.Slug)
				}
			}
			e.table([]string{e.t("PERSONA"), e.t("NOME"), e.t("ESTADO")}, rows)
			if len(on) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma persona a tem habilitada."))
			} else {
				fmt.Fprintf(e.out, e.t("Habilitada para: %s\n"), strings.Join(on, ", "))
			}
			return nil
		},
	}

	c.AddCommand(create, ls, show, edit, del,
		toggle("enable", e.t("Habilita a skill para uma ou mais personas"), true),
		toggle("disable", e.t("Desabilita a skill para uma ou mais personas"), false),
		personas)
	return c
}
