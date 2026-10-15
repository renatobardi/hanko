package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/domain"
)

func (e *env) taskRows(ts []api.Task) [][]string {
	rows := make([][]string, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, []string{t.Key, t.Status, t.Priority, dash(t.PersonaName), oneLine(t.Title, 70)})
	}
	return rows
}

func (e *env) taskHeader() []string {
	return []string{e.t("CHAVE"), e.t("STATUS"), e.t("PRIORIDADE"), e.t("PERSONA"), e.t("TÍTULO")}
}

func (e *env) taskCmd() *cobra.Command {
	c := &cobra.Command{Use: "task", Short: e.t("Gerencia tasks")}

	var (
		desc, persona, project, priority, status, ws, parent, eventID, model, goal string
	)
	create := &cobra.Command{
		Use: "create " + e.t("<título>"), Short: e.t("Cria uma task"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().CreateTask(cmd.Context(), co, client.CreateTaskReq{Title: args[0], Description: desc, Status: status,
				Priority: priority, Persona: persona, Project: project, Workspace: ws, Goal: goal, Parent: parent, Model: model, EventID: eventID})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			who := ""
			if t.PersonaName != "" {
				who = " → " + t.PersonaName
			}
			fmt.Fprintf(e.out, e.t("Task criada: %s %s [%s]%s\n"), t.Key, t.Title, t.Status, who)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVarP(&desc, "description", "d", "", e.t("descrição"))
	cf.StringVarP(&persona, "persona", "p", "", e.t("persona responsável (id ou slug)"))
	cf.StringVar(&project, "project", "", e.t("projeto (id ou slug)"))
	cf.StringVar(&priority, "priority", "", e.t("critical, high, medium ou low (padrão medium)"))
	cf.StringVar(&status, "status", "", e.t("status inicial (padrão todo)"))
	cf.StringVar(&ws, "workspace", "", e.t("workspace (id ou slug)"))
	cf.StringVar(&goal, "goal", "", e.t("meta a que a task serve (id ou slug)"))
	cf.StringVar(&parent, "parent", "", e.t("task pai (id ou chave)"))
	cf.StringVar(&eventID, "event-id", "", e.t("chave de idempotência"))
	cf.StringVar(&model, "model", "", e.t("modelo escolhido à mão (vence o da persona)"))

	var q client.TaskQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista tasks"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ts, err := e.client().Tasks(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ts)
			}
			if len(ts) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma task encontrada."))
				return nil
			}
			e.table(e.taskHeader(), e.taskRows(ts))
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Status, "status", "", e.t("status separados por vírgula (ex.: todo,in_progress)"))
	lf.StringVarP(&q.Persona, "persona", "p", "", e.t("filtra por persona (id ou slug)"))
	lf.StringVar(&q.Project, "project", "", e.t("filtra por projeto (id ou slug)"))
	lf.StringVar(&q.Goal, "goal", "", e.t("filtra por meta (id ou slug)"))
	lf.StringVarP(&q.Q, "query", "q", "", e.t("busca em título e descrição"))
	lf.IntVar(&q.Limit, "limit", 50, e.t("máximo de itens"))
	lf.IntVar(&q.Offset, "offset", 0, e.t("itens a pular"))

	show := &cobra.Command{
		Use: "show " + e.t("<task>"), Short: e.t("Mostra uma task com thread e execuções"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cl, ctx := e.client(), cmd.Context()
			t, err := cl.Task(ctx, co, args[0])
			if err != nil {
				return err
			}
			cs, err := cl.Comments(ctx, co, t.ID)
			if err != nil {
				return err
			}
			rs, err := cl.Runs(ctx, co, client.RunQuery{Task: t.ID, Limit: 50})
			if err != nil {
				return err
			}
			// os artefatos são um complemento: um servidor sem a rota (versão anterior) não derruba o show
			arts, _ := cl.Artifacts(ctx, co, client.ArtifactQuery{Task: t.ID, Limit: 100})
			goalLine := "-"
			if t.GoalID != "" {
				if g, err := cl.Goal(ctx, co, t.GoalID); err == nil {
					goalLine = g.Title + " (" + g.Slug + ")"
				}
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"task": t, "comments": cs, "runs": rs, "artifacts": arts})
			}
			fmt.Fprintf(e.out, "%s  %s\n\n", t.Key, t.Title)
			persona := dash(t.PersonaName)
			e.kv([2]string{e.t("Status"), t.Status}, [2]string{e.t("Prioridade"), t.Priority}, [2]string{e.t("Persona"), persona},
				[2]string{e.t("Projeto"), dash(t.ProjectName)}, [2]string{e.t("Meta"), goalLine}, [2]string{e.t("Modelo (manual)"), dash(t.Model)}, [2]string{e.t("Execução ativa"), dash(t.ActiveRunID)},
				[2]string{e.t("Criada em"), e.fmtTime(t.CreatedAt)}, [2]string{e.t("Atualizada em"), e.fmtTime(t.UpdatedAt)})
			if strings.TrimSpace(t.Description) != "" {
				fmt.Fprintf(e.out, e.t("\nDescrição:\n%s\n"), strings.TrimSpace(t.Description))
			}
			if len(arts) > 0 {
				fmt.Fprintf(e.out, e.t("\nArtefatos (%d):\n"), len(arts))
				for _, a := range arts {
					fmt.Fprintf(e.out, "  %s v%d (%s, %s) %s\n", a.Name, a.Version, a.Mime, humanBytes(a.SizeBytes), short(a.ID))
				}
			}
			fmt.Fprintf(e.out, e.t("\nComentários (%d):\n"), len(cs))
			for _, cm := range cs {
				fmt.Fprintf(e.out, "  [%s] %s: %s\n", e.fmtTime(cm.CreatedAt), e.authorLabel(cm.Author), oneLine(cm.Body, 300))
				if cm.Anchor != nil {
					lost := ""
					if cm.AnchorState != nil && cm.AnchorState.Status == "lost" {
						lost = " " + e.t("(âncora perdida)")
					}
					fmt.Fprintf(e.out, e.t("      sobre: “%s”%s\n"), oneLine(cm.Anchor.Quote, 120), lost)
				}
			}
			fmt.Fprintf(e.out, e.t("\nExecuções (%d):\n"), len(rs))
			if len(rs) > 0 {
				e.table(e.runHeader(), e.runRows(rs))
			}
			return nil
		},
	}

	var none bool
	assign := &cobra.Command{
		Use: "assign " + e.t("<task>") + " " + e.t("[persona]"), Short: e.t("Atribui a task a uma persona (ou --none para remover)"), Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			switch {
			case none && len(args) == 2, !none && len(args) == 1:
				return usageError{errors.New(e.t("informe a persona ou use --none"))}
			}
			req := client.UpdateTaskReq{Unassign: none}
			if !none {
				req.Persona = args[1]
			}
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			if none {
				fmt.Fprintf(e.out, e.t("%s sem responsável.\n"), t.Key)
			} else {
				fmt.Fprintf(e.out, e.t("%s atribuída a %s.\n"), t.Key, dash(t.PersonaName))
			}
			return nil
		},
	}
	assign.Flags().BoolVar(&none, "none", false, e.t("remove a atribuição"))

	statusCmd := &cobra.Command{
		Use: "status " + e.t("<task>") + " " + e.t("<status>"), Short: e.t("Muda o status (backlog, todo, in_progress, in_review, blocked, done, cancelled)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if !domain.TaskStatus(args[1]).Valid() {
				return usageError{fmt.Errorf(e.t("status desconhecido %q (válidos: backlog, todo, in_progress, in_review, blocked, done, cancelled)"), args[1])}
			}
			st := args[1]
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], client.UpdateTaskReq{Status: &st})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("%s agora está em %s.\n"), t.Key, t.Status)
			return nil
		},
	}

	var noProject bool
	projectCmd := &cobra.Command{
		Use: "project " + e.t("<task>") + " " + e.t("[projeto]"), Short: e.t("Põe a task num projeto (ou a tira dele com --none)"), Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if noProject == (len(args) == 2) {
				return usageError{errors.New(e.t("informe o projeto ou use --none (um dos dois)"))}
			}
			req := client.UpdateTaskReq{ClearProject: noProject}
			if !noProject {
				req.Project = args[1]
			}
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			if t.ProjectName == "" {
				fmt.Fprintf(e.out, e.t("%s sem projeto.\n"), t.Key)
			} else {
				fmt.Fprintf(e.out, e.t("%s no projeto %s.\n"), t.Key, t.ProjectName)
			}
			return nil
		},
	}
	projectCmd.Flags().BoolVar(&noProject, "none", false, e.t("tira a task do projeto"))

	var noGoal bool
	goalCmd := &cobra.Command{
		Use: "goal " + e.t("<task>") + " " + e.t("[meta]"), Short: e.t("Liga a task a uma meta ativa (ou a desliga com --none)"), Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if noGoal == (len(args) == 2) {
				return usageError{errors.New(e.t("informe a meta ou use --none (um dos dois)"))}
			}
			req := client.UpdateTaskReq{ClearGoal: noGoal}
			if !noGoal {
				req.Goal = args[1]
			}
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			if t.GoalID == "" {
				fmt.Fprintf(e.out, e.t("%s sem meta.\n"), t.Key)
			} else {
				fmt.Fprintf(e.out, e.t("%s ligada à meta %s.\n"), t.Key, args[1])
			}
			return nil
		},
	}
	goalCmd.Flags().BoolVar(&noGoal, "none", false, e.t("desliga a task da meta"))

	var clearModel bool
	modelCmd := &cobra.Command{
		Use: "model " + e.t("<task>") + " " + e.t("[modelo]"), Short: e.t("Escolhe à mão o modelo da task (vence o da persona); --clear remove"), Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if clearModel == (len(args) == 2) {
				return usageError{errors.New(e.t("informe o modelo ou use --clear (um dos dois)"))}
			}
			m := ""
			if !clearModel {
				m = args[1]
			}
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], client.UpdateTaskReq{Model: &m})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			if t.Model == "" {
				fmt.Fprintf(e.out, e.t("%s sem modelo manual: vale o da persona.\n"), t.Key)
			} else {
				fmt.Fprintf(e.out, e.t("%s usa o modelo %s.\n"), t.Key, t.Model)
			}
			return nil
		},
	}
	modelCmd.Flags().BoolVar(&clearModel, "clear", false, e.t("remove a escolha manual"))

	var quote, quoteRun string
	comment := &cobra.Command{
		Use: "comment " + e.t("<task>") + " " + e.t("<texto...>"), Short: e.t("Comenta na task (use '-' para ler do stdin)"), Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			body := strings.Join(args[1:], " ")
			if body == "-" {
				b, err := io.ReadAll(e.in)
				if err != nil {
					return fmt.Errorf(e.t("ler stdin: %w"), err)
				}
				body = strings.TrimSpace(string(b))
			}
			if body == "" {
				return usageError{errors.New(e.t("comentário vazio"))}
			}
			req := client.AddCommentReq{Body: body}
			if quote != "" {
				a := &api.Anchor{Target: "task_description", Quote: quote}
				if quoteRun != "" {
					a.Target, a.RunID = "run_summary", quoteRun
				}
				req.Anchor = a
			} else if quoteRun != "" {
				return usageError{errors.New(e.t("--quote-run exige --quote"))}
			}
			cm, err := e.client().AddComment(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cm)
			}
			fmt.Fprintf(e.out, e.t("Comentário adicionado em %s.\n"), args[0])
			return nil
		},
	}

	comment.Flags().StringVar(&quote, "quote", "", e.t("ancora o comentário a este trecho da descrição da task (ou do resumo da execução, com --quote-run)"))
	comment.Flags().StringVar(&quoteRun, "quote-run", "", e.t("id da execução cujo resumo contém o trecho de --quote"))

	wake := &cobra.Command{
		Use: "wake " + e.t("<task>"), Short: e.t("Acorda a persona atribuída para trabalhar na task"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().WakeTask(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]bool{"queued": true})
			}
			fmt.Fprintf(e.out, e.t("Persona de %s acordada; acompanhe com: hanko run list --task %s\n"), args[0], args[0])
			return nil
		},
	}

	c.AddCommand(create, ls, show, assign, statusCmd, projectCmd, goalCmd, modelCmd, comment, wake)
	return c
}

func (e *env) authorLabel(a api.ActorRef) string {
	switch a.Type {
	case "human":
		return e.t("humano")
	case "persona":
		return e.t("persona") + " " + short(a.ID)
	}
	return a.Type
}
