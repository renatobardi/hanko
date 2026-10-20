package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Coordenação de tasks (ADR-0040): dependências, árvore, pausa herdada e política de revisão por task/projeto.

func (e *env) coordinationCmds() []*cobra.Command {
	return []*cobra.Command{e.taskDepCmd(), e.taskParentCmd(), e.taskHoldCmd(), e.taskResumeCmd(), e.taskHeldCmd(), e.taskReviewPolicyCmd(), e.taskGitCmd(), e.taskNewSessionCmd()}
}

func (e *env) refRows(rs []api.TaskRef) [][]string {
	rows := make([][]string, 0, len(rs))
	for _, r := range rs {
		state := e.t("aberta")
		if r.Closed {
			state = e.t("fechada")
		}
		rows = append(rows, []string{r.Key, r.Status, state, dash(r.PersonaName), oneLine(r.Title, 70)})
	}
	return rows
}

func (e *env) refHeader() []string {
	return []string{e.t("CHAVE"), e.t("STATUS"), e.t("SITUAÇÃO"), e.t("PERSONA"), e.t("TÍTULO")}
}

func (e *env) taskDepCmd() *cobra.Command {
	c := &cobra.Command{Use: "dep", Short: e.t("Dependências bloqueantes entre tasks")}
	add := &cobra.Command{
		Use: "add " + e.t("<task>") + " " + e.t("<depende-de>"), Short: e.t("Faz a task depender de outra (sem ciclos): ela só é despachada quando todas as dependências fecharem"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().AddTaskDependency(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			state := ""
			if t.Blocked {
				state = " " + e.t("(bloqueada)")
			}
			fmt.Fprintf(e.out, e.t("%s depende de %s%s.\n"), t.Key, args[1], state)
			return nil
		},
	}
	rm := &cobra.Command{
		Use: "rm " + e.t("<task>") + " " + e.t("<depende-de>"), Aliases: []string{"remove"}, Short: e.t("Remove uma dependência; a última dependência aberta removida libera a task"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().RemoveTaskDependency(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("%s não depende mais de %s.\n"), t.Key, args[1])
			return nil
		},
	}
	ls := &cobra.Command{
		Use: "list " + e.t("<task>"), Aliases: []string{"ls"}, Short: e.t("Mostra de quem a task depende e quem depende dela"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rel, err := e.client().TaskRelations(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(rel)
			}
			if len(rel.DependsOn) == 0 && len(rel.Dependents) == 0 {
				fmt.Fprintln(e.out, e.t("Sem dependências."))
				return nil
			}
			if len(rel.DependsOn) > 0 {
				fmt.Fprintln(e.out, e.t("Depende de:"))
				e.table(e.refHeader(), e.refRows(rel.DependsOn))
			}
			if len(rel.Dependents) > 0 {
				fmt.Fprintln(e.out, e.t("Esperam por esta:"))
				e.table(e.refHeader(), e.refRows(rel.Dependents))
			}
			return nil
		},
	}
	c.AddCommand(add, rm, ls)
	return c
}

func (e *env) taskParentCmd() *cobra.Command {
	var none bool
	c := &cobra.Command{
		Use: "parent " + e.t("<task>") + " " + e.t("[pai]"), Short: e.t("Muda a task pai (ou torna a task raiz com --none)"), Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if none == (len(args) == 2) {
				return usageError{errors.New(e.t("informe o pai ou use --none (um dos dois)"))}
			}
			req := client.UpdateTaskReq{ClearParent: none}
			if !none {
				req.Parent = args[1]
			}
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			if none {
				fmt.Fprintf(e.out, e.t("%s agora é raiz.\n"), t.Key)
			} else {
				fmt.Fprintf(e.out, e.t("%s agora é subtask de %s.\n"), t.Key, args[1])
			}
			return nil
		},
	}
	c.Flags().BoolVar(&none, "none", false, e.t("torna a task raiz"))
	return c
}

func (e *env) taskHoldCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use: "hold " + e.t("<task>"), Short: e.t("Pausa a task e toda a subárvore: nenhuma execução abre até retomar"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if strings.TrimSpace(reason) == "" {
				return usageError{errors.New(e.t("informe o motivo com --reason"))}
			}
			t, err := e.client().HoldTask(cmd.Context(), co, args[0], reason)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("%s pausada (com a subárvore): %s\n"), t.Key, reason)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", e.t("motivo da pausa (obrigatório)"))
	return c
}

func (e *env) taskResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "resume " + e.t("<task>"), Short: e.t("Retoma a task pausada e acorda os wakes que foram recusados durante a pausa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().ResumeTask(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("%s retomada.\n"), t.Key)
			return nil
		},
	}
}

func (e *env) taskHeldCmd() *cobra.Command {
	return &cobra.Command{
		Use: "held", Short: e.t("Lista as tasks pausadas (raízes de pausa)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ts, err := e.client().HeldTasks(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ts)
			}
			if len(ts) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma task pausada."))
				return nil
			}
			rows := make([][]string, 0, len(ts))
			for _, t := range ts {
				reason := ""
				if t.Hold != nil {
					reason = oneLine(t.Hold.Reason, 60)
				}
				rows = append(rows, []string{t.Key, oneLine(t.Title, 50), reason})
			}
			e.table([]string{e.t("CHAVE"), e.t("TÍTULO"), e.t("MOTIVO")}, rows)
			return nil
		},
	}
}

// reviewFlags são as flags comuns de task review-policy e project review-policy.
type reviewFlags struct {
	require, noRequire, noReviewer, clear bool
	reviewerPersona, reviewerAgent, model string
}

func (e *env) bindReviewFlags(c *cobra.Command, f *reviewFlags) {
	fl := c.Flags()
	fl.BoolVar(&f.require, "require", false, e.t("exige revisão no fechamento"))
	fl.BoolVar(&f.noRequire, "no-require", false, e.t("dispensa a revisão no fechamento"))
	fl.StringVar(&f.reviewerPersona, "reviewer-persona", "", e.t("persona revisora (id ou slug)"))
	fl.StringVar(&f.reviewerAgent, "reviewer-agent", "", e.t("harness revisor: claude, codex ou fake"))
	fl.StringVar(&f.model, "reviewer-model", "", e.t("modelo do revisor"))
	fl.BoolVar(&f.noReviewer, "no-reviewer", false, e.t("sem revisor automático: o gate humano abre direto"))
	fl.BoolVar(&f.clear, "clear", false, e.t("remove a sobreposição (volta a herdar)"))
}

func (f reviewFlags) request() (client.ReviewOverrideReq, error) {
	var r client.ReviewOverrideReq
	if f.require && f.noRequire {
		return r, errors.New("--require e --no-require são exclusivos")
	}
	if f.clear {
		return r, nil
	}
	if f.require {
		t := true
		r.RequireReview = &t
	}
	if f.noRequire {
		fa := false
		r.RequireReview = &fa
	}
	switch {
	case f.noReviewer:
		if f.reviewerPersona != "" || f.reviewerAgent != "" {
			return r, errors.New("--no-reviewer não combina com --reviewer-persona/--reviewer-agent")
		}
		r.ReviewerMode = "none"
	case f.reviewerPersona != "" || f.reviewerAgent != "":
		r.ReviewerMode, r.ReviewerPersona, r.ReviewerAgent, r.ReviewerModel = "custom", f.reviewerPersona, f.reviewerAgent, f.model
	}
	return r, nil
}

func (e *env) printReviewLayer(name string, o api.ReviewOverride) {
	req := e.t("herda")
	if o.RequireReview != nil {
		req = e.t("não")
		if *o.RequireReview {
			req = e.t("sim")
		}
	}
	reviewer := o.ReviewerMode
	switch {
	case o.ReviewerPersonaID != "":
		reviewer = e.t("persona") + " " + short(o.ReviewerPersonaID)
	case o.ReviewerAgent != "":
		reviewer = o.ReviewerAgent
		if o.ReviewerModel != "" {
			reviewer += " · " + o.ReviewerModel
		}
	}
	fmt.Fprintf(e.out, "  %-8s %s=%s  %s=%s\n", name, e.t("revisão"), req, e.t("revisor"), reviewer)
}

func (e *env) taskReviewPolicyCmd() *cobra.Command {
	var f reviewFlags
	c := &cobra.Command{
		Use: "review-policy " + e.t("<task>"), Short: e.t("Mostra ou altera a política de revisão da task (task > projeto > persona)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var p api.TaskReviewPolicy
			if cmd.Flags().NFlag() > 0 {
				req, err := f.request()
				if err != nil {
					return usageError{err}
				}
				p, err = e.client().SetTaskReviewPolicy(cmd.Context(), co, args[0], req)
				if err != nil {
					return err
				}
			} else if p, err = e.client().TaskReviewPolicy(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.printReviewLayer(e.t("task"), p.Task)
			e.printReviewLayer(e.t("projeto"), p.Project)
			e.printReviewLayer(e.t("persona"), p.Persona)
			ef := p.Effective
			fmt.Fprintf(e.out, e.t("Vale: revisão %v (origem: %s); revisor %v (origem: %s)\n"), ef.Required, ef.RequiredSource, ef.HasReviewer, ef.ReviewerSource)
			return nil
		},
	}
	e.bindReviewFlags(c, &f)
	return c
}

func (e *env) projectReviewPolicyCmd() *cobra.Command {
	var f reviewFlags
	c := &cobra.Command{
		Use: "review-policy " + e.t("<projeto>"), Short: e.t("Mostra ou altera a política de revisão do projeto (vale para as tasks sem sobreposição)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var o api.ReviewOverride
			if cmd.Flags().NFlag() > 0 {
				req, err := f.request()
				if err != nil {
					return usageError{err}
				}
				o, err = e.client().SetProjectReviewPolicy(cmd.Context(), co, args[0], req)
				if err != nil {
					return err
				}
			} else if o, err = e.client().ProjectReviewPolicy(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(o)
			}
			e.printReviewLayer(e.t("projeto"), o)
			return nil
		},
	}
	e.bindReviewFlags(c, &f)
	return c
}
