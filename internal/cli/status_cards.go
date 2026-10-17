package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Cards de status (ADR-0032).

// resolveCard aceita o id completo ou um prefixo único (como mostrado em `status-card list`).
func (e *env) resolveCard(ctx context.Context, company, ref string) (string, error) {
	if len(ref) >= 36 {
		return ref, nil
	}
	cs, err := e.client().StatusCards(ctx, company, "all")
	if err != nil {
		return "", err
	}
	var found []string
	for _, c := range cs {
		if strings.HasPrefix(c.ID, ref) {
			found = append(found, c.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf(e.t("card de status %q não encontrado"), ref)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf(e.t("prefixo %q é ambíguo: use mais caracteres do id"), ref)
}

func (e *env) cardTitle(c api.StatusCard) string {
	if c.Title == "" {
		return e.t("Card sem título")
	}
	return c.Title
}

func (e *env) printCard(c api.StatusCard) {
	e.kv([2]string{"ID", c.ID}, [2]string{e.t("Título"), e.cardTitle(c)}, [2]string{e.t("Estado"), c.ViewState},
		[2]string{e.t("Resumidora"), dash(c.SummarizerSlug)}, [2]string{e.t("Política"), c.Policy.Mode},
		[2]string{e.t("Filtros"), fmt.Sprintf("v%d (%d)", c.QueryVersion, len(c.Queries))}, [2]string{e.t("Versão do resumo"), strconv.Itoa(c.SummaryRevision)},
		[2]string{e.t("Mudanças pendentes"), strconv.Itoa(c.PendingChangeCount)}, [2]string{e.t("Gerado em"), e.fmtTimePtr(c.LastGeneratedAt)},
		[2]string{e.t("Próxima verificação"), e.fmtTimePtr(c.NextEvalAt)})
	if c.FailureReason != "" {
		fmt.Fprintln(e.out, "\n"+e.t("Falha:")+" "+c.FailureReason)
	}
	fmt.Fprintln(e.out, "\n"+e.t("Interesse:")+"\n"+c.Interest)
	if c.Summary != "" {
		fmt.Fprintln(e.out, "\n"+c.Summary)
	}
}

func (e *env) statusCardCmd() *cobra.Command {
	c := &cobra.Command{Use: "status-card", Aliases: []string{"status-cards", "card"}, Short: e.t("Cards de status: resumos vivos das tasks que importam")}

	var archived string
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os cards de status"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cs, err := e.client().StatusCards(cmd.Context(), co, archived)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			rows := make([][]string, 0, len(cs))
			for _, x := range cs {
				rows = append(rows, []string{short(x.ID), e.cardTitle(x), x.ViewState, dash(x.SummarizerSlug), x.Policy.Mode,
					strconv.Itoa(x.PendingChangeCount), e.fmtTimePtr(x.LastGeneratedAt)})
			}
			e.table([]string{"ID", e.t("TÍTULO"), e.t("ESTADO"), e.t("RESUMIDORA"), e.t("POLÍTICA"), e.t("PENDENTES"), e.t("GERADO EM")}, rows)
			return nil
		},
	}
	ls.Flags().StringVar(&archived, "archived", "false", e.t("false (ativos), true (arquivados) ou all"))

	show := &cobra.Command{
		Use: "show " + e.t("<id>"), Short: e.t("Mostra um card com o resumo atual"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				x, err := e.client().StatusCard(cmd.Context(), co, id)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(x)
				}
				e.printCard(x)
				return nil
			})
		},
	}

	var title, summarizer, mode string
	var pin bool
	create := &cobra.Command{
		Use: "create " + e.t("<interesse>"), Short: e.t("Cria um card de status; a resumidora monta os filtros"), Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			in := client.CreateStatusCardReq{Interest: strings.Join(args, " "), Title: title, TitlePinned: pin, Summarizer: summarizer}
			if mode != "" {
				in.Policy = &api.StatusCardPolicy{Mode: mode}
			}
			x, err := e.client().CreateStatusCard(cmd.Context(), co, in)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(x)
			}
			fmt.Fprintf(e.out, e.t("Card de status %s criado (%s).\n"), short(x.ID), x.ViewState)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&title, "title", "", e.t("título do card (padrão: a resumidora propõe)"))
	cf.BoolVar(&pin, "pin-title", false, e.t("fixa o título (a resumidora não o troca)"))
	cf.StringVar(&summarizer, "summarizer", "", e.t("persona resumidora (id ou slug)"))
	cf.StringVar(&mode, "mode", "", e.t("política: manual, interval ou reactive (padrão reactive)"))

	var uInterest, uTitle, uSummarizer, uMode string
	var uPin, uUnpin bool
	update := &cobra.Command{
		Use: "update " + e.t("<id>"), Short: e.t("Altera um card (mudar o interesse remonta os filtros)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				in := client.UpdateStatusCardReq{Summarizer: uSummarizer}
				if cmd.Flags().Changed("interest") {
					in.Interest = &uInterest
				}
				if cmd.Flags().Changed("title") {
					in.Title = &uTitle
				}
				if uPin || uUnpin {
					v := uPin
					in.TitlePinned = &v
				}
				if uMode != "" {
					in.Policy = &api.StatusCardPolicy{Mode: uMode}
				}
				x, err := e.client().UpdateStatusCard(cmd.Context(), co, id, in)
				if err != nil {
					return err
				}
				return e.cardDone(x, e.t("Card atualizado."))
			})
		},
	}
	uf := update.Flags()
	uf.StringVar(&uInterest, "interest", "", e.t("novo interesse"))
	uf.StringVar(&uTitle, "title", "", e.t("novo título"))
	uf.BoolVar(&uPin, "pin-title", false, e.t("fixa o título (a resumidora não o troca)"))
	uf.BoolVar(&uUnpin, "unpin-title", false, e.t("deixa a resumidora propor o título"))
	uf.StringVar(&uSummarizer, "summarizer", "", e.t("persona resumidora (id ou slug)"))
	uf.StringVar(&uMode, "mode", "", e.t("política: manual, interval ou reactive (padrão reactive)"))

	archive := e.cardArchiveCmd("archive", e.t("Arquiva um card (encerra a geração em andamento)"), true, e.t("Card arquivado."))
	restore := e.cardArchiveCmd("restore", e.t("Restaura um card arquivado (e refaz o resumo do zero)"), false, e.t("Card restaurado."))

	del := &cobra.Command{
		Use: "delete " + e.t("<id>"), Short: e.t("Apaga um card com o histórico (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				if err := e.client().DeleteStatusCard(cmd.Context(), co, id); err != nil {
					return err
				}
				fmt.Fprintln(e.out, e.t("Card apagado."))
				return nil
			})
		},
	}

	var full bool
	refresh := &cobra.Command{
		Use: "refresh " + e.t("<id>"), Short: e.t("Atualiza o resumo agora"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				x, err := e.client().RefreshStatusCard(cmd.Context(), co, id, full)
				if err != nil {
					return err
				}
				return e.cardDone(x, e.t("Atualização pedida."))
			})
		},
	}
	refresh.Flags().BoolVar(&full, "full", false, e.t("refaz o resumo do zero"))

	recompile := &cobra.Command{
		Use: "recompile " + e.t("<id>"), Short: e.t("Pede a montagem dos filtros de novo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				x, err := e.client().RecompileStatusCard(cmd.Context(), co, id)
				if err != nil {
					return err
				}
				return e.cardDone(x, e.t("Montagem pedida."))
			})
		},
	}

	history := &cobra.Command{
		Use: "history " + e.t("<id>"), Short: e.t("Lista as rodadas do card com tokens e custo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				us, err := e.client().StatusCardUpdates(cmd.Context(), co, id)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(us)
				}
				rows := make([][]string, 0, len(us))
				for _, u := range us {
					cost := "-"
					if u.CostUSD != nil {
						cost = fmt.Sprintf("%.4f", *u.CostUSD)
					}
					rows = append(rows, []string{e.fmtTime(u.StartedAt), u.Kind, u.Trigger, u.Status, strconv.Itoa(len(u.Changes)),
						strconv.FormatInt(u.InputTokens+u.OutputTokens, 10), cost, dash(u.TaskKey)})
				}
				e.table([]string{e.t("INÍCIO"), e.t("TIPO"), e.t("GATILHO"), "STATUS", e.t("MUDANÇAS"), "TOKENS", "US$", "TASK"}, rows)
				return nil
			})
		},
	}

	preview := &cobra.Command{
		Use: "preview " + e.t("<id>"), Short: e.t("Mostra os filtros e as tasks que casam hoje"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				p, err := e.client().StatusCardPreview(cmd.Context(), co, id)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				for i, q := range p.Queries {
					fmt.Fprintf(e.out, "%d. %s (%d)\n", i+1, q.Description, q.Matches)
				}
				fmt.Fprintln(e.out)
				rows := make([][]string, 0, len(p.Tasks))
				for _, t := range p.Tasks {
					rows = append(rows, []string{t.Key, t.Status, dash(t.PersonaName), t.Title})
				}
				e.table([]string{"TASK", "STATUS", "PERSONA", e.t("TÍTULO")}, rows)
				return nil
			})
		},
	}

	c.AddCommand(ls, show, create, update, archive, restore, del, refresh, recompile, history, preview)
	return c
}

func (e *env) withCard(cmd *cobra.Command, ref string, fn func(company, id string) error) error {
	co, err := e.needCompany()
	if err != nil {
		return err
	}
	id, err := e.resolveCard(cmd.Context(), co, ref)
	if err != nil {
		return err
	}
	return fn(co, id)
}

func (e *env) cardDone(x api.StatusCard, msg string) error {
	if e.asJSON {
		return e.printJSON(x)
	}
	fmt.Fprintf(e.out, "%s %s · %s\n", msg, short(x.ID), x.ViewState)
	return nil
}

func (e *env) cardArchiveCmd(use, desc string, archived bool, msg string) *cobra.Command {
	return &cobra.Command{
		Use: use + " " + e.t("<id>"), Short: desc, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.withCard(cmd, args[0], func(co, id string) error {
				v := archived
				x, err := e.client().UpdateStatusCard(cmd.Context(), co, id, client.UpdateStatusCardReq{Archived: &v})
				if err != nil {
					return err
				}
				return e.cardDone(x, msg)
			})
		},
	}
}
