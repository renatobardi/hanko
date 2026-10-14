package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

var gateHeader = []string{"ID", "STATUS", "TIPO", "TASK", "PEDIDO POR", "TÍTULO"}

func gateRows(gs []api.Gate) [][]string {
	rows := make([][]string, 0, len(gs))
	for _, g := range gs {
		by := g.RequestedBy.Type
		if g.PersonaName != "" {
			by = g.PersonaName
		}
		rows = append(rows, []string{shortID(g.ID), g.Status, g.Kind, dash(g.TaskKey), dash(by), oneLine(g.Title, 60)})
	}
	return rows
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// resolveGate aceita o id completo ou um prefixo único (como mostrado em `gate list`).
func (e *env) resolveGate(ctx context.Context, company, ref string) (string, error) {
	if len(ref) >= 36 {
		return ref, nil
	}
	gs, err := e.client().Gates(ctx, company, client.GateQuery{Limit: 200})
	if err != nil {
		return "", err
	}
	var found []string
	for _, g := range gs {
		if strings.HasPrefix(g.ID, ref) {
			found = append(found, g.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("gate %q não encontrado", ref)
	case 1:
		return found[0], nil
	}
	return "", usageError{fmt.Errorf("prefixo %q é ambíguo (%d gates)", ref, len(found))}
}

func (e *env) gateCmd() *cobra.Command {
	c := &cobra.Command{Use: "gate", Short: "Gates: decisões humanas pedidas pelas personas"}

	var q client.GateQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista gates (pendentes primeiro)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			gs, err := e.client().Gates(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(gs)
			}
			if len(gs) == 0 {
				fmt.Fprintln(e.out, "Nenhum gate.")
				return nil
			}
			e.table(gateHeader, gateRows(gs))
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Status, "status", "", "pending, approved, rejected, changes_requested ou expired")
	lf.StringVar(&q.Kind, "kind", "", "approval, task_review ou hire")
	lf.StringVar(&q.Task, "task", "", "task (id ou chave)")
	lf.StringVar(&q.Persona, "persona", "", "persona (id ou slug)")
	lf.IntVar(&q.Limit, "limit", 0, "limite")

	show := &cobra.Command{
		Use: "show <gate>", Short: "Mostra um gate", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveGate(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			g, err := e.client().Gate(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			by := g.RequestedBy.Type
			if g.PersonaName != "" {
				by = g.PersonaName
			}
			e.kv([2]string{"Gate", g.Title}, [2]string{"ID", g.ID}, [2]string{"Status", g.Status}, [2]string{"Tipo", g.Kind},
				[2]string{"Task", strings.TrimSpace(g.TaskKey + " " + g.TaskTitle)}, [2]string{"Pedido por", by},
				[2]string{"Criado em", fmtTime(g.CreatedAt)}, [2]string{"Decidido em", fmtTimePtr(g.DecidedAt)},
				[2]string{"Nota da decisão", g.DecisionNote})
			if g.Body != "" {
				fmt.Fprintf(e.out, "\n%s\n", strings.TrimSpace(g.Body))
			}
			return nil
		},
	}

	decide := func(use, decision, short, done string) *cobra.Command {
		var note string
		cmd := &cobra.Command{
			Use: use + " <gate>", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				if decision != "approved" && strings.TrimSpace(note) == "" {
					return usageError{errors.New("informe o motivo com --note")}
				}
				id, err := e.resolveGate(cmd.Context(), co, args[0])
				if err != nil {
					return err
				}
				g, err := e.client().DecideGate(cmd.Context(), co, id, client.DecideGateReq{Decision: decision, Note: note})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(g)
				}
				fmt.Fprintf(e.out, "Gate %s %s: %s\n", shortID(g.ID), done, g.Title)
				return nil
			},
		}
		cmd.Flags().StringVarP(&note, "note", "n", "", "nota da decisão (vira comentário na task)")
		return cmd
	}
	c.AddCommand(ls, show,
		decide("approve", "approved", "Aprova um gate", "aprovado"),
		decide("reject", "rejected", "Rejeita um gate", "rejeitado"),
		decide("request-changes", "changes_requested", "Pede mudanças em um gate", "devolvido com pedido de mudanças"))
	return c
}

func (e *env) orgCmd() *cobra.Command {
	return &cobra.Command{
		Use: "org", Short: "Mostra o organograma da empresa", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			oc, err := e.client().OrgChart(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(oc)
			}
			if oc.Total == 0 {
				fmt.Fprintln(e.out, "Nenhuma persona contratada.")
				return nil
			}
			var walk func(n api.OrgNode, prefix string, last, root bool)
			walk = func(n api.OrgNode, prefix string, last, root bool) {
				branch, next := "", prefix
				if !root {
					branch, next = "├─ ", prefix+"│  "
					if last {
						branch, next = "└─ ", prefix+"   "
					}
				}
				extra := ""
				if n.PendingGates > 0 {
					extra = fmt.Sprintf(" · %d gate(s) pendente(s)", n.PendingGates)
				}
				fmt.Fprintf(e.out, "%s%s%s (%s) [%s] %d tasks abertas%s\n", prefix, branch, n.Persona.Name, n.Persona.Slug, n.Persona.Status, n.OpenTasks, extra)
				for i, ch := range n.Children {
					walk(ch, next, i == len(n.Children)-1, false)
				}
			}
			for _, r := range oc.Roots {
				walk(r, "", true, true)
			}
			return nil
		},
	}
}
