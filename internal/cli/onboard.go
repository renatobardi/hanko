package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// `hanko onboard` (#67, ADR-0061): o assistente de primeiros passos pela CLI. Segue os mesmos passos e o mesmo estado da
// tela /c/{empresa}/primeiros-passos: empresa → harness (com probe) → primeira persona ou pacote de time → primeira task.
// Cada chamada executa os passos que as flags permitem, na ordem, e para no primeiro que faltar informação, dizendo o
// comando exato para continuar. Pode ser rodado de novo quantas vezes for preciso.

func (e *env) stepLabel(k string) string {
	switch k {
	case "company":
		return e.t("Empresa")
	case "harness":
		return e.t("Harness (assinatura ou login)")
	case "persona":
		return e.t("Primeira persona ou pacote de time")
	case "task":
		return e.t("Primeira task rodando")
	}
	return k
}

func (e *env) printOnboarding(st api.OnboardingState) {
	for _, s := range st.Steps {
		mark := "[ ]"
		switch {
		case s.Done:
			mark = "[x]"
		case s.Skipped:
			mark = "[-]"
		}
		fmt.Fprintf(e.out, "  %s %s\n", mark, e.stepLabel(s.Key))
	}
}

func (e *env) printOnboardingProbe(p api.OnboardingProbe) {
	for _, s := range p.Steps {
		fmt.Fprintf(e.out, "    %-12s %-5s %s\n", s.Name, s.Status, oneLine(s.Detail, 100))
	}
	if p.Status == "ready" {
		fmt.Fprintf(e.out, e.t("  harness %s pronto (%d ms)\n"), p.Agent, p.DurationMs)
		return
	}
	fmt.Fprintf(e.out, e.t("  harness %s NÃO está pronto: %s\n"), p.Agent, p.Reason)
}

func (e *env) onboardCmd() *cobra.Command {
	var companyName, harness, model, team, personaName, personaTitle, personaInstr, taskTitle, taskDesc string
	var skipProbe, dismiss, resume bool
	c := &cobra.Command{
		Use: "onboard", Short: e.t("Assistente de primeiros passos: empresa, harness, primeira persona (ou pacote de time) e primeira task"),
		Long: e.t("Leva uma empresa nova até a primeira task rodando, sem abrir o navegador. Cada chamada executa os passos que as flags permitem e para no primeiro que falta, dizendo como continuar; rode de novo para retomar. O estado é o mesmo da tela /primeiros-passos."),
		Example: "  hanko onboard --company-name \"Acme\" --harness claude\n" +
			"  hanko onboard -c acme --team squad-de-produto --task \"Resumir o README\"\n" +
			"  hanko onboard -c acme --persona Ana --task \"Resumir o README\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cl := e.client()
			co := e.company
			if co == "" && companyName != "" {
				created, err := cl.CreateCompany(ctx, client.CreateCompanyReq{Name: companyName})
				if err != nil {
					return err
				}
				co = created.Slug
				fmt.Fprintf(e.out, e.t("empresa %s criada (use -c %s nas próximas chamadas)\n"), created.Name, created.Slug)
			}
			if co == "" {
				cs, err := cl.Companies(ctx)
				if err != nil {
					return err
				}
				switch len(cs) {
				case 0:
					return usageError{errors.New(e.t("não há empresa: informe --company-name \"Nome\" para criar a primeira"))}
				case 1:
					co = cs[0].Slug
				default:
					return usageError{errors.New(e.t("há mais de uma empresa: informe qual com -c/--company"))}
				}
			}
			if dismiss || resume {
				st, err := cl.OnboardingDismiss(ctx, co, dismiss)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(st)
				}
				if dismiss {
					fmt.Fprintln(e.out, e.t("assistente dispensado (volte com `hanko onboard --resume`)"))
				} else {
					fmt.Fprintln(e.out, e.t("assistente retomado"))
				}
				return nil
			}
			st, err := cl.Onboarding(ctx, co)
			if err != nil {
				return err
			}
			var lastErr error
			next := func(hint string) error {
				if e.asJSON {
					return e.printJSON(st)
				}
				fmt.Fprintln(e.out)
				e.printOnboarding(st)
				if hint != "" {
					fmt.Fprintln(e.out, "\n"+hint)
				}
				return lastErr
			}
			if st.Complete {
				return next(e.t("Tudo pronto: a empresa já tem harness, persona e uma task."))
			}

			// harness
			if st.Current == "harness" {
				switch {
				case skipProbe:
					if st, err = cl.OnboardingSkip(ctx, co, "harness"); err != nil {
						return err
					}
				case harness != "":
					p, err := cl.OnboardingProbe(ctx, co, harness, model)
					if err != nil {
						return err
					}
					if !e.asJSON {
						e.printOnboardingProbe(p)
					}
					if st, err = cl.Onboarding(ctx, co); err != nil {
						return err
					}
					if p.Status != "ready" {
						lastErr = errors.New(e.t("o harness não passou no teste: corrija e rode `hanko onboard --harness ...` de novo, ou pule com --skip-probe"))
						return next("")
					}
				default:
					return next(e.t("Próximo: teste o harness com `hanko onboard --harness claude` (ou codex, fake), ou pule com `--skip-probe`."))
				}
			}

			// persona ou pacote
			lead := ""
			if st.Current == "persona" {
				switch {
				case team != "":
					r, err := cl.OnboardingInstallTeam(ctx, co, team)
					if err != nil {
						return err
					}
					lead = r.Lead
					fmt.Fprintf(e.out, e.t("pacote %s instalado: %d persona(s); %s ativa\n"), r.Package, r.Personas, dash(r.Lead))
				case personaName != "":
					h := harness
					if h == "" {
						h = st.HarnessAgent
					}
					if h == "" {
						return usageError{errors.New(e.t("informe o harness da persona com --harness"))}
					}
					p, err := cl.HirePersona(ctx, co, client.HirePersonaReq{Name: personaName, Title: personaTitle, Instructions: personaInstr, Agent: h, Model: model})
					if err != nil {
						return err
					}
					lead = p.Slug
					fmt.Fprintf(e.out, e.t("persona %s contratada\n"), p.Name)
				default:
					return next(e.t("Próximo: contrate uma persona com `hanko onboard --persona Nome` ou instale um time com `hanko onboard --team <pacote>` (veja `hanko teams catalog`)."))
				}
				if st, err = cl.Onboarding(ctx, co); err != nil {
					return err
				}
			}

			// primeira task
			if st.Current == "task" {
				if taskTitle == "" {
					return next(e.t("Próximo: crie a primeira task com `hanko onboard --task \"Título\"`."))
				}
				if lead == "" {
					ps, err := cl.Personas(ctx, co)
					if err != nil {
						return err
					}
					for _, p := range ps {
						if p.Status == "active" || p.Status == "idle" {
							lead = p.Slug
							break
						}
					}
				}
				if lead == "" {
					return usageError{errors.New(e.t("não há persona ativa para receber a task"))}
				}
				p, err := cl.Persona(ctx, co, lead)
				if err != nil {
					return err
				}
				tk, err := cl.OnboardingFirstTask(ctx, co, p.ID, taskTitle, taskDesc)
				if err != nil {
					return err
				}
				fmt.Fprintf(e.out, e.t("task %s criada para %s: acompanhe com `hanko task show %s`\n"), tk.Key, p.Name, tk.Key)
				if st, err = cl.Onboarding(ctx, co); err != nil {
					return err
				}
			}
			if st.Complete {
				return next(e.t("Pronto: a primeira task está rodando."))
			}
			return next("")
		},
	}
	f := c.Flags()
	f.StringVar(&companyName, "company-name", "", e.t("cria a empresa com este nome (quando ainda não há -c)"))
	f.StringVar(&harness, "harness", "", e.t("harness a testar e usar: claude, codex ou fake"))
	f.StringVar(&model, "model", "", e.t("modelo (padrão do harness)"))
	f.BoolVar(&skipProbe, "skip-probe", false, e.t("pula o teste do harness (login já feito na máquina, ou uso do fake)"))
	f.StringVar(&team, "team", "", e.t("instala um pacote de time do catálogo e ativa a persona de topo"))
	f.StringVar(&personaName, "persona", "", e.t("contrata uma persona com este nome"))
	f.StringVar(&personaTitle, "persona-title", "", e.t("cargo da persona"))
	f.StringVar(&personaInstr, "persona-instructions", "", e.t("instruções da persona"))
	f.StringVar(&taskTitle, "task", "", e.t("título da primeira task"))
	f.StringVar(&taskDesc, "description", "", e.t("descrição da primeira task"))
	f.BoolVar(&dismiss, "dismiss", false, e.t("dispensa o assistente desta empresa"))
	f.BoolVar(&resume, "resume", false, e.t("volta a oferecer o assistente dispensado"))
	return c
}

// ---------- perfil, favoritos, novidades ----------

func (e *env) profileCmd() *cobra.Command {
	c := &cobra.Command{Use: "profile", Short: e.t("Perfil próprio: nome, avatar, fuso e idioma")}
	show := &cobra.Command{
		Use: "show", Short: e.t("Mostra o seu perfil"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := e.client().Profile(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.kv([2]string{e.t("e-mail"), p.Email}, [2]string{e.t("nome"), p.Name}, [2]string{e.t("avatar"), dash(p.AvatarGlyph)},
				[2]string{e.t("cor do avatar"), dash(p.AvatarTone)}, [2]string{e.t("fuso"), dash(p.Timezone)}, [2]string{e.t("idioma"), dash(p.Locale)})
			return nil
		},
	}
	var name, glyph, tone, tz, locale string
	set := &cobra.Command{
		Use: "set", Short: e.t("Altera o seu perfil (só o que for informado)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			patch := map[string]string{}
			for flag, val := range map[string]*string{"name": &name, "avatar-glyph": &glyph, "avatar-tone": &tone, "timezone": &tz, "locale": &locale} {
				if cmd.Flags().Changed(flag) {
					patch[strings.ReplaceAll(flag, "-", "_")] = *val
				}
			}
			if len(patch) == 0 {
				return usageError{errors.New(e.t("nada para alterar: use --name, --avatar-glyph, --avatar-tone, --timezone ou --locale"))}
			}
			p, err := e.client().UpdateProfile(cmd.Context(), patch)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("perfil atualizado: %s\n"), p.Name)
			return nil
		},
	}
	set.Flags().StringVar(&name, "name", "", e.t("nome de exibição"))
	set.Flags().StringVar(&glyph, "avatar-glyph", "", e.t("até 3 caracteres no avatar (vazio = a inicial)"))
	set.Flags().StringVar(&tone, "avatar-tone", "", e.t("cor do avatar: accent, green, amber, red, blue, violet, gray (vazio = padrão)"))
	set.Flags().StringVar(&tz, "timezone", "", e.t("fuso IANA, ex.: America/Sao_Paulo (vazio = o da empresa)"))
	set.Flags().StringVar(&locale, "locale", "", e.t("idioma da UI: pt-BR ou en (vazio = o do navegador)"))
	c.AddCommand(show, set)
	return c
}

func (e *env) favoriteCmd() *cobra.Command {
	c := &cobra.Command{Use: "favorite", Aliases: []string{"fav"}, Short: e.t("Favoritos e recentes da sidebar (por pessoa e empresa)")}
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os favoritos e os recentes"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			n, err := e.client().Nav(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(n)
			}
			rows := [][]string{}
			for _, it := range n.Favorites {
				rows = append(rows, []string{e.t("favorito"), it.Path, oneLine(it.Title, 60)})
			}
			for _, it := range n.Recents {
				rows = append(rows, []string{e.t("recente"), it.Path, oneLine(it.Title, 60)})
			}
			if len(rows) == 0 {
				fmt.Fprintln(e.out, e.t("nenhum favorito nem recente"))
				return nil
			}
			e.table([]string{e.t("TIPO"), e.t("PÁGINA"), e.t("TÍTULO")}, rows)
			return nil
		},
	}
	var title string
	add := &cobra.Command{
		Use: "add " + e.t("<página>"), Short: e.t("Marca uma página (relativa à empresa, ex.: /tasks/HNK-12) como favorita"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if _, err := e.client().SetFavorite(cmd.Context(), co, args[0], title, true); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("favorito: %s\n"), args[0])
			return nil
		},
	}
	add.Flags().StringVar(&title, "title", "", e.t("título a mostrar (padrão: o caminho)"))
	rm := &cobra.Command{
		Use: "rm " + e.t("<página>"), Aliases: []string{"remove"}, Short: e.t("Desmarca um favorito"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if _, err := e.client().SetFavorite(cmd.Context(), co, args[0], "", false); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("favorito removido: %s\n"), args[0])
			return nil
		},
	}
	c.AddCommand(ls, add, rm)
	return c
}

func (e *env) whatsNewCmd() *cobra.Command {
	var dismiss bool
	c := &cobra.Command{
		Use: "whats-new", Aliases: []string{"novidades"}, Short: e.t("O que há de novo na versão instalada no servidor (lido do CHANGELOG, sem rede)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := e.client()
			w, err := cl.WhatsNew(cmd.Context())
			if err != nil {
				return err
			}
			if dismiss {
				if err := cl.DismissWhatsNew(cmd.Context(), w.Key); err != nil {
					return err
				}
				fmt.Fprintf(e.out, e.t("novidades da versão %s dispensadas\n"), w.Key)
				return nil
			}
			if e.asJSON {
				return e.printJSON(w)
			}
			fmt.Fprintf(e.out, e.t("Versão instalada: %s (bloco %s)\n\n%s\n"), dash(w.Installed), w.Version, w.Body)
			return nil
		},
	}
	c.Flags().BoolVar(&dismiss, "dismiss", false, e.t("dispensa as novidades desta versão para você"))
	return c
}
