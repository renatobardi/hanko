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

func taskRows(ts []api.Task) [][]string {
	rows := make([][]string, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, []string{t.Key, t.Status, t.Priority, dash(t.PersonaName), oneLine(t.Title, 70)})
	}
	return rows
}

var taskHeader = []string{"CHAVE", "STATUS", "PRIORIDADE", "PERSONA", "TÍTULO"}

func (e *env) taskCmd() *cobra.Command {
	c := &cobra.Command{Use: "task", Short: "Gerencia tasks"}

	var (
		desc, persona, project, priority, status, ws, parent, eventID string
	)
	create := &cobra.Command{
		Use: "create <título>", Short: "Cria uma task", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().CreateTask(cmd.Context(), co, client.CreateTaskReq{Title: args[0], Description: desc, Status: status,
				Priority: priority, Persona: persona, Project: project, Workspace: ws, Parent: parent, EventID: eventID})
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
			fmt.Fprintf(e.out, "Task criada: %s %s [%s]%s\n", t.Key, t.Title, t.Status, who)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVarP(&desc, "description", "d", "", "descrição")
	cf.StringVarP(&persona, "persona", "p", "", "persona responsável (id ou slug)")
	cf.StringVar(&project, "project", "", "projeto (id ou slug)")
	cf.StringVar(&priority, "priority", "", "critical, high, medium ou low (padrão medium)")
	cf.StringVar(&status, "status", "", "status inicial (padrão todo)")
	cf.StringVar(&ws, "workspace", "", "workspace (id ou slug)")
	cf.StringVar(&parent, "parent", "", "task pai (id ou chave)")
	cf.StringVar(&eventID, "event-id", "", "chave de idempotência")

	var q client.TaskQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista tasks", Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, "Nenhuma task encontrada.")
				return nil
			}
			e.table(taskHeader, taskRows(ts))
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Status, "status", "", "status separados por vírgula (ex.: todo,in_progress)")
	lf.StringVarP(&q.Persona, "persona", "p", "", "filtra por persona (id ou slug)")
	lf.StringVar(&q.Project, "project", "", "filtra por projeto (id ou slug)")
	lf.StringVarP(&q.Q, "query", "q", "", "busca em título e descrição")
	lf.IntVar(&q.Limit, "limit", 50, "máximo de itens")
	lf.IntVar(&q.Offset, "offset", 0, "itens a pular")

	show := &cobra.Command{
		Use: "show <task>", Short: "Mostra uma task com thread e execuções", Args: cobra.ExactArgs(1),
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
			if e.asJSON {
				return e.printJSON(map[string]any{"task": t, "comments": cs, "runs": rs})
			}
			fmt.Fprintf(e.out, "%s  %s\n\n", t.Key, t.Title)
			persona := dash(t.PersonaName)
			e.kv([2]string{"Status", t.Status}, [2]string{"Prioridade", t.Priority}, [2]string{"Persona", persona},
				[2]string{"Projeto", dash(t.ProjectName)}, [2]string{"Execução ativa", dash(t.ActiveRunID)},
				[2]string{"Criada em", fmtTime(t.CreatedAt)}, [2]string{"Atualizada em", fmtTime(t.UpdatedAt)})
			if strings.TrimSpace(t.Description) != "" {
				fmt.Fprintf(e.out, "\nDescrição:\n%s\n", strings.TrimSpace(t.Description))
			}
			fmt.Fprintf(e.out, "\nComentários (%d):\n", len(cs))
			for _, cm := range cs {
				fmt.Fprintf(e.out, "  [%s] %s: %s\n", fmtTime(cm.CreatedAt), authorLabel(cm.Author), oneLine(cm.Body, 300))
			}
			fmt.Fprintf(e.out, "\nExecuções (%d):\n", len(rs))
			if len(rs) > 0 {
				e.table(runHeader, runRows(rs))
			}
			return nil
		},
	}

	var none bool
	assign := &cobra.Command{
		Use: "assign <task> [persona]", Short: "Atribui a task a uma persona (ou --none para remover)", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			switch {
			case none && len(args) == 2, !none && len(args) == 1:
				return usageError{errors.New("informe a persona ou use --none")}
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
				fmt.Fprintf(e.out, "%s sem responsável.\n", t.Key)
			} else {
				fmt.Fprintf(e.out, "%s atribuída a %s.\n", t.Key, dash(t.PersonaName))
			}
			return nil
		},
	}
	assign.Flags().BoolVar(&none, "none", false, "remove a atribuição")

	statusCmd := &cobra.Command{
		Use: "status <task> <status>", Short: "Muda o status (backlog, todo, in_progress, in_review, blocked, done, cancelled)", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if !domain.TaskStatus(args[1]).Valid() {
				return usageError{fmt.Errorf("status desconhecido %q (válidos: backlog, todo, in_progress, in_review, blocked, done, cancelled)", args[1])}
			}
			st := args[1]
			t, err := e.client().UpdateTask(cmd.Context(), co, args[0], client.UpdateTaskReq{Status: &st})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, "%s agora está em %s.\n", t.Key, t.Status)
			return nil
		},
	}

	comment := &cobra.Command{
		Use: "comment <task> <texto...>", Short: "Comenta na task (use '-' para ler do stdin)", Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			body := strings.Join(args[1:], " ")
			if body == "-" {
				b, err := io.ReadAll(e.in)
				if err != nil {
					return fmt.Errorf("ler stdin: %w", err)
				}
				body = strings.TrimSpace(string(b))
			}
			if body == "" {
				return usageError{errors.New("comentário vazio")}
			}
			cm, err := e.client().AddComment(cmd.Context(), co, args[0], client.AddCommentReq{Body: body})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cm)
			}
			fmt.Fprintf(e.out, "Comentário adicionado em %s.\n", args[0])
			return nil
		},
	}

	wake := &cobra.Command{
		Use: "wake <task>", Short: "Acorda a persona atribuída para trabalhar na task", Args: cobra.ExactArgs(1),
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
			fmt.Fprintf(e.out, "Persona de %s acordada; acompanhe com: hanko run list --task %s\n", args[0], args[0])
			return nil
		},
	}

	c.AddCommand(create, ls, show, assign, statusCmd, comment, wake)
	return c
}

func authorLabel(a api.ActorRef) string {
	switch a.Type {
	case "human":
		return "humano"
	case "persona":
		return "persona " + short(a.ID)
	}
	return a.Type
}
