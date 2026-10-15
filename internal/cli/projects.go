package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/domain"
)

// Subcomandos de workspace e projeto (ADR-0025): detalhe, alteração e exclusão. Criar e listar ficam em resources.go.

func (e *env) workspaceShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show " + e.t("<workspace>"), Short: e.t("Mostra um workspace e quem o usa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			w, err := e.client().Workspace(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(w)
			}
			fmt.Fprintf(e.out, "%s (%s)\n", w.Name, w.Slug)
			fmt.Fprintf(e.out, e.t("Caminho: %s\n"), dash(w.Path))
			fmt.Fprintf(e.out, e.t("Repositório: %s\n"), dash(w.RepoURL))
			fmt.Fprintf(e.out, e.t("Branch padrão: %s\n"), dash(w.DefaultBranch))
			if w.Usage != nil {
				fmt.Fprintf(e.out, e.t("Em uso por: %d persona(s), %d projeto(s), %d task(s) aberta(s)\n"), w.Usage.Personas, w.Usage.Projects, w.Usage.OpenTasks)
				for _, p := range w.Usage.PersonaItems {
					fmt.Fprintf(e.out, e.t("  persona %s\n"), p.Slug)
				}
				for _, p := range w.Usage.ProjectItems {
					fmt.Fprintf(e.out, e.t("  projeto %s\n"), p.Slug)
				}
			}
			return nil
		},
	}
}

func (e *env) workspaceUpdateCmd() *cobra.Command {
	var name, path, repo, branch string
	c := &cobra.Command{
		Use: "update " + e.t("<workspace>"), Short: e.t("Altera nome, caminho, repositório ou branch padrão de um workspace"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var req client.UpdateWorkspaceReq
			f := cmd.Flags()
			if f.Changed("name") {
				req.Name = &name
			}
			if f.Changed("path") {
				req.Path = &path
			}
			if f.Changed("repo") {
				req.RepoURL = &repo
			}
			if f.Changed("branch") {
				req.Branch = &branch
			}
			if req == (client.UpdateWorkspaceReq{}) {
				return usageError{errors.New(e.t("informe o que alterar: --name, --path, --repo ou --branch"))}
			}
			w, err := e.client().UpdateWorkspace(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(w)
			}
			fmt.Fprintf(e.out, e.t("Workspace atualizado: %s (%s)\n"), w.Name, w.Slug)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&name, "name", "", e.t("novo nome"))
	f.StringVar(&path, "path", "", e.t("novo diretório de trabalho (vazio limpa)"))
	f.StringVar(&repo, "repo", "", e.t("nova URL do repositório (vazio limpa)"))
	f.StringVar(&branch, "branch", "", e.t("nova branch padrão (vazio limpa)"))
	return c
}

func (e *env) workspaceDeleteCmd() *cobra.Command {
	var unlink bool
	c := &cobra.Command{
		Use: "delete " + e.t("<workspace>"), Aliases: []string{"rm"}, Short: e.t("Exclui um workspace (em uso, só com --unlink)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteWorkspace(cmd.Context(), co, args[0], unlink); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Workspace %s excluído.\n"), args[0])
			return nil
		},
	}
	c.Flags().BoolVar(&unlink, "unlink", false, e.t("desvincula personas, projetos e tasks que usam o workspace antes de excluir"))
	return c
}

func (e *env) projectShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show " + e.t("<projeto>"), Short: e.t("Mostra um projeto: progresso, custo do mês e personas"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().Project(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.printProject(p)
			return nil
		},
	}
}

func (e *env) printProject(p api.Project) {
	fmt.Fprintf(e.out, "%s (%s) — %s\n", p.Name, p.Slug, e.projectStatus(p.Status))
	if p.Description != "" {
		fmt.Fprintln(e.out, p.Description)
	}
	st := p.Stats
	if st == nil {
		return
	}
	ws := dash(st.WorkspaceSlug)
	fmt.Fprintf(e.out, e.t("Workspace: %s\n"), ws)
	parts := make([]string, 0, len(domain.TaskStatuses))
	for _, s := range domain.TaskStatuses {
		if n := st.Tasks[string(s)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s, n))
		}
	}
	fmt.Fprintf(e.out, e.t("Tasks: %d (%s) — progresso %.0f%%\n"), st.TasksTotal, dash(strings.Join(parts, ", ")), st.ProgressPercent)
	fmt.Fprintf(e.out, e.t("Orçamento do mês: %s\n"), e.budgetLine(st.Budget))
	if len(st.Personas) > 0 {
		rows := make([][]string, 0, len(st.Personas))
		for _, pe := range st.Personas {
			rows = append(rows, []string{pe.Slug, pe.Name, fmt.Sprint(pe.OpenTasks), fmt.Sprint(pe.Tasks)})
		}
		fmt.Fprintln(e.out)
		e.table([]string{e.t("PERSONA"), e.t("NOME"), e.t("ABERTAS"), e.t("TOTAL")}, rows)
	}
}

func (e *env) projectStatus(s string) string {
	if s == string(domain.ProjectArchived) {
		return e.t("arquivado")
	}
	return e.t("ativo")
}

func (e *env) projectUpdateCmd() *cobra.Command {
	var name, desc, ws, status string
	var clearWS, archive, unarchive bool
	var budget int64
	var clearBudget bool
	c := &cobra.Command{
		Use: "update " + e.t("<projeto>"), Short: e.t("Altera nome, descrição, workspace, status ou orçamento de um projeto"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := client.UpdateProjectReq{Workspace: ws, ClearWorkspace: clearWS, ClearBudget: clearBudget}
			if f.Changed("name") {
				req.Name = &name
			}
			if f.Changed("description") {
				req.Description = &desc
			}
			if archive && unarchive {
				return usageError{errors.New(e.t("use --archive ou --unarchive, não os dois"))}
			}
			switch {
			case archive:
				status = string(domain.ProjectArchived)
			case unarchive:
				status = string(domain.ProjectActive)
			}
			if status != "" {
				req.Status = &status
			}
			if f.Changed("budget-cents") {
				req.BudgetMonthlyCents = &budget
			}
			if req.Name == nil && req.Description == nil && req.Workspace == "" && !req.ClearWorkspace && req.Status == nil &&
				req.BudgetMonthlyCents == nil && !req.ClearBudget {
				return usageError{errors.New(e.t("informe o que alterar (--name, --description, --workspace, --archive, --budget-cents...)"))}
			}
			p, err := e.client().UpdateProject(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Projeto atualizado: %s (%s) — %s\n"), p.Name, p.Slug, e.projectStatus(p.Status))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&name, "name", "", e.t("novo nome"))
	f.StringVarP(&desc, "description", "d", "", e.t("nova descrição"))
	f.StringVar(&ws, "workspace", "", e.t("workspace do projeto (id ou slug)"))
	f.BoolVar(&clearWS, "clear-workspace", false, e.t("remove o workspace do projeto"))
	f.BoolVar(&archive, "archive", false, e.t("arquiva o projeto (não recebe tasks novas)"))
	f.BoolVar(&unarchive, "unarchive", false, e.t("reativa o projeto"))
	f.Int64Var(&budget, "budget-cents", 0, e.t("orçamento mensal em centavos de dólar"))
	f.BoolVar(&clearBudget, "clear-budget", false, e.t("remove o orçamento do projeto"))
	return c
}
