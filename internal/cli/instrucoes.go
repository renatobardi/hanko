package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
)

// Revisões das instruções da persona (ADR-0052): subcomandos de `hanko persona`.

func (e *env) instructionCmds() []*cobra.Command {
	return []*cobra.Command{e.personaHistoryCmd(), e.personaRestoreCmd()}
}

func printDiffLines(e *env, d *api.ArtifactDiff) {
	fmt.Fprintf(e.out, "\n"+e.t("Diff da revisão %d para a %d (+%d -%d):")+"\n", d.From, d.To, d.Added, d.Removed)
	for _, l := range d.Lines {
		mark := " "
		switch l.Op {
		case "insert":
			mark = "+"
		case "delete":
			mark = "-"
		}
		fmt.Fprintf(e.out, "%s %s\n", mark, l.Text)
	}
}

func actorText2(a api.ActorRef) string {
	if a.ID != "" {
		return a.Type + ":" + a.ID[:min(8, len(a.ID))]
	}
	return a.Type
}

func (e *env) personaHistoryCmd() *cobra.Command {
	var show, against int
	c := &cobra.Command{
		Use: "history " + e.t("<persona>"), Aliases: []string{"revisions"},
		Short: e.t("Lista as revisões das instruções da persona (ou mostra uma com --show e compara com --against)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if show > 0 {
				v, err := e.client().InstructionRevision(cmd.Context(), co, args[0], show, against)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(v)
				}
				e.kv([2]string{e.t("Revisão"), strconv.Itoa(v.Revision)}, [2]string{e.t("Origem"), v.Origin}, [2]string{e.t("Autor"), actorText2(v.Author)},
					[2]string{e.t("Aplicada por"), actorText2(v.Actor)}, [2]string{e.t("Motivo"), dash(v.Reason)}, [2]string{"sha256", v.SHA256},
					[2]string{e.t("Criada"), e.fmtTime(v.CreatedAt)})
				if v.Diff != nil {
					printDiffLines(e, v.Diff)
					return nil
				}
				fmt.Fprintf(e.out, "\n%s\n", v.Body)
				return nil
			}
			vs, err := e.client().InstructionRevisions(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(vs)
			}
			rows := make([][]string, 0, len(vs))
			for _, v := range vs {
				cur := ""
				if v.Current {
					cur = "*"
				}
				rows = append(rows, []string{strconv.Itoa(v.Revision) + cur, v.Origin, actorText2(v.Author), actorText2(v.Actor),
					fmt.Sprintf("%d B", v.BodyBytes), v.SHA256[:12], dash(v.Reason), e.fmtTime(v.CreatedAt)})
			}
			e.table([]string{e.t("REVISÃO"), e.t("ORIGEM"), e.t("AUTOR"), e.t("APLICADA POR"), e.t("TEXTO"), "SHA256", e.t("MOTIVO"), e.t("CRIADA")}, rows)
			return nil
		},
	}
	c.Flags().IntVar(&show, "show", 0, e.t("mostra esta revisão, com o texto"))
	c.Flags().IntVar(&against, "against", 0, e.t("com --show, mostra o diff desta revisão para a pedida"))
	return c
}

func (e *env) personaRestoreCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use: "restore " + e.t("<persona> <revisão>"), Short: e.t("Restaura o texto de uma revisão das instruções (cria uma revisão nova)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rev, err := strconv.Atoi(args[1])
			if err != nil || rev < 1 {
				return usageError{errors.New(e.t("revisão inválida: use um número a partir de 1"))}
			}
			p, err := e.client().RestoreInstructions(cmd.Context(), co, args[0], rev, reason)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Instruções de %s restauradas da revisão %d: agora na revisão %d\n"), p.Slug, rev, p.InstructionRevision)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", e.t("motivo (fica na revisão)"))
	return c
}

// readInstructions lê o texto novo de --instructions ou --instructions-file (nil = não mexer).
func (e *env) readInstructions(cmd *cobra.Command, text, file string) (*string, error) {
	switch {
	case file != "" && cmd.Flags().Changed("instructions"):
		return nil, usageError{errors.New(e.t("--instructions e --instructions-file não combinam"))}
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf(e.t("ler %s: %w"), file, err)
		}
		s := string(b)
		return &s, nil
	case cmd.Flags().Changed("instructions"):
		return &text, nil
	}
	return nil, nil
}
