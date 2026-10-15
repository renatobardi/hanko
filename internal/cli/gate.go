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

func (e *env) gateHeader() []string {
	return []string{e.t("ID"), e.t("STATUS"), e.t("TIPO"), e.t("TASK"), e.t("PEDIDO POR"), e.t("TÍTULO")}
}

func (e *env) gateRows(gs []api.Gate) [][]string {
	rows := make([][]string, 0, len(gs))
	for _, g := range gs {
		by := g.RequestedBy.Type
		if g.PersonaName != "" {
			by = g.PersonaName
		}
		rows = append(rows, []string{shortID(g.ID), g.Status, g.Kind, dash(g.TaskKey), dash(by), oneLine(e.reviewTag(g)+g.Title, 60)})
	}
	return rows
}

// reviewLine resume o parecer do revisor automático; a decisão segue humana.
func (e *env) reviewLine(r *api.GateReviewOut) string {
	v := e.t("sem veredito")
	switch r.Verdict {
	case "approved":
		v = "aprovado"
	case "rejected":
		v = e.t("REPROVADO")
	}
	s := r.Reviewer + ": " + v
	if r.Justification != "" {
		s += " — " + oneLine(r.Justification, 200)
	}
	return s
}

// reviewTag marca na lista os gates que o revisor automático reprovou.
func (e *env) reviewTag(g api.Gate) string {
	if g.Review != nil && g.Review.Verdict == "rejected" {
		return e.t("[reprovado pelo revisor]") + " "
	}
	return ""
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
		return "", fmt.Errorf(e.t("gate %q não encontrado"), ref)
	case 1:
		return found[0], nil
	}
	return "", usageError{fmt.Errorf(e.t("prefixo %q é ambíguo (%d gates)"), ref, len(found))}
}

func (e *env) gateCmd() *cobra.Command {
	c := &cobra.Command{Use: "gate", Short: e.t("Gates: decisões humanas pedidas pelas personas")}

	var q client.GateQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista gates (pendentes primeiro)"), Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, e.t("Nenhum gate."))
				return nil
			}
			e.table(e.gateHeader(), e.gateRows(gs))
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Status, "status", "", e.t("pending, approved, rejected, changes_requested ou expired"))
	lf.StringVar(&q.Kind, "kind", "", e.t("approval, task_review ou hire"))
	lf.StringVar(&q.Task, "task", "", e.t("task (id ou chave)"))
	lf.StringVar(&q.Persona, "persona", "", e.t("persona (id ou slug)"))
	lf.IntVar(&q.Limit, "limit", 0, e.t("limite"))

	show := &cobra.Command{
		Use: "show " + e.t("<gate>"), Short: e.t("Mostra um gate"), Args: cobra.ExactArgs(1),
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
			e.kv([2]string{e.t("Gate"), g.Title}, [2]string{e.t("ID"), g.ID}, [2]string{e.t("Status"), g.Status}, [2]string{e.t("Tipo"), g.Kind},
				[2]string{e.t("Task"), strings.TrimSpace(g.TaskKey + " " + g.TaskTitle)}, [2]string{e.t("Pedido por"), by},
				[2]string{e.t("Criado em"), e.fmtTime(g.CreatedAt)}, [2]string{e.t("Decidido em"), e.fmtTimePtr(g.DecidedAt)},
				[2]string{e.t("Nota da decisão"), g.DecisionNote})
			if rv := g.Review; rv != nil {
				e.kv([2]string{e.t("Revisão automática"), e.reviewLine(rv)}, [2]string{e.t("sha256 revisado"), dash(rv.ArtifactSHA256)})
			}
			if g.Body != "" {
				fmt.Fprintf(e.out, "\n%s\n", strings.TrimSpace(g.Body))
			}
			return nil
		},
	}

	decide := func(use, decision, short, done string) *cobra.Command {
		var note string
		cmd := &cobra.Command{
			Use: use + " " + e.t("<gate>"), Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				if decision != "approved" && strings.TrimSpace(note) == "" {
					return usageError{errors.New(e.t("informe o motivo com --note"))}
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
				fmt.Fprintf(e.out, e.t("Gate %s %s: %s\n"), shortID(g.ID), done, g.Title)
				return nil
			},
		}
		cmd.Flags().StringVarP(&note, "note", "n", "", e.t("nota da decisão (vira comentário na task)"))
		return cmd
	}
	c.AddCommand(ls, show,
		decide("approve", "approved", e.t("Aprova um gate"), e.t("aprovado")),
		decide("reject", "rejected", e.t("Rejeita um gate"), e.t("rejeitado")),
		decide("request-changes", "changes_requested", e.t("Pede mudanças em um gate"), e.t("devolvido com pedido de mudanças")))
	return c
}

func (e *env) orgCmd() *cobra.Command {
	return &cobra.Command{
		Use: "org", Short: e.t("Mostra o organograma da empresa"), Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, e.t("Nenhuma persona contratada."))
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
					extra = e.tf(" · %d gate(s) pendente(s)", n.PendingGates)
				}
				fmt.Fprintf(e.out, e.t("%s%s%s (%s) [%s] %d tasks abertas%s\n"), prefix, branch, n.Persona.Name, n.Persona.Slug, n.Persona.Status, n.OpenTasks, extra)
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
