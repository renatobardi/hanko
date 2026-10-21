package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Workspace de execução com Git, padrões de projeto e "Nova sessão" (ADR-0054).

func (e *env) printMirror(g api.WorkspaceGit) {
	e.kv(
		[2]string{e.t("Espelho"), g.MirrorState},
		[2]string{e.t("Branch base"), dash(g.BaseBranch)},
		[2]string{e.t("Commit"), dash(short(g.HeadSHA))},
		[2]string{e.t("Última busca"), e.fmtTimePtr(g.LastFetchAt)},
		[2]string{e.t("Credencial"), dash(g.CredentialRef)},
		[2]string{e.t("Erro"), dash(g.LastError)},
	)
	if !g.Available {
		fmt.Fprintln(e.out, e.t("Aviso: o operador não ligou HANKO_GIT_WORKSPACES; nada é clonado neste servidor."))
	}
}

func (e *env) worktreeRows(ws []api.Worktree) [][]string {
	rows := make([][]string, 0, len(ws))
	for _, w := range ws {
		rows = append(rows, []string{dash(w.TaskKey), w.Branch, w.State, dash(w.TaskStatus), dash(short(w.HeadSHA)), oneLine(dash(w.Error), 50)})
	}
	return rows
}

func (e *env) worktreeTable(ws []api.Worktree) {
	e.table([]string{e.t("TASK"), e.t("BRANCH"), e.t("ESTADO"), e.t("STATUS DA TASK"), e.t("COMMIT"), e.t("ERRO")}, e.worktreeRows(ws))
}

func (e *env) workspaceGitCmd() *cobra.Command {
	c := &cobra.Command{Use: "git", Short: e.t("Git do workspace: clone espelho do repositório e worktree por task")}
	show := &cobra.Command{
		Use: "show " + e.t("<workspace>"), Short: e.t("Mostra o espelho, a credencial (só a referência) e os worktrees recentes"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			g, err := e.client().WorkspaceGit(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			e.printMirror(g)
			if len(g.Worktrees) > 0 {
				fmt.Fprintln(e.out)
				e.worktreeTable(g.Worktrees)
			}
			return nil
		},
	}
	var cred string
	var syncNow bool
	enable := &cobra.Command{
		Use: "enable " + e.t("<workspace>"), Short: e.t("Liga o Git do workspace (admin); o repositório vem do --repo do workspace"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			g, err := e.client().EnableWorkspaceGit(cmd.Context(), co, args[0], client.EnableWorkspaceGitReq{CredentialRef: cred, Sync: syncNow})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Git ligado no workspace %s.\n"), args[0])
			e.printMirror(g)
			return nil
		},
	}
	enable.Flags().StringVar(&cred, "credential", "", e.t("referência do segredo com a credencial, no formato ${secret:NOME} (token https ou chave ssh); vazio = repositório público"))
	enable.Flags().BoolVar(&syncNow, "sync", false, e.t("já clona/atualiza o espelho"))
	sync := &cobra.Command{
		Use: "sync " + e.t("<workspace>"), Short: e.t("Clona ou atualiza o espelho do repositório"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			g, err := e.client().SyncWorkspaceGit(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			e.printMirror(g)
			return nil
		},
	}
	disable := &cobra.Command{
		Use: "disable " + e.t("<workspace>"), Short: e.t("Desliga o Git do workspace (recusa se houver worktree em uso)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DisableWorkspaceGit(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Git desligado no workspace %s.\n"), args[0])
			return nil
		},
	}
	c.AddCommand(show, enable, sync, disable)
	return c
}

func (e *env) projectGitCmd() *cobra.Command {
	return &cobra.Command{
		Use: "git " + e.t("<projeto>"), Short: e.t("Lista os worktrees das tasks do projeto"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ws, err := e.client().ProjectWorktrees(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ws)
			}
			if len(ws) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum worktree."))
				return nil
			}
			e.worktreeTable(ws)
			return nil
		},
	}
}

func (e *env) taskGitCmd() *cobra.Command {
	c := &cobra.Command{Use: "git", Short: e.t("Aba Git da task: status, diff, log e arquivos (somente leitura)")}
	status := &cobra.Command{
		Use: "status " + e.t("<task>"), Short: e.t("Worktree, branch, alterações soltas e quanto a branch está à frente da base"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().TaskGit(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			if v.Worktree != nil {
				e.kv(
					[2]string{e.t("Branch"), v.Worktree.Branch},
					[2]string{e.t("Estado"), v.Worktree.State},
					[2]string{e.t("Base"), dash(v.Worktree.BaseBranch) + " " + short(v.Worktree.BaseSHA)},
					[2]string{e.t("Commit"), dash(short(v.HeadSHA))},
					[2]string{e.t("À frente / atrás"), fmt.Sprintf("%d / %d", v.Ahead, v.Behind)},
					[2]string{e.t("Integrada"), fmt.Sprintf("%v", v.Merged)},
					[2]string{e.t("Erro"), dash(v.Worktree.Error)},
				)
			}
			if v.Status != nil {
				if len(v.Status.Entries) == 0 {
					fmt.Fprintln(e.out, e.t("Sem alterações soltas."))
				}
				for _, en := range v.Status.Entries {
					fmt.Fprintf(e.out, " %s %s\n", en.XY, en.Path)
				}
			} else if !v.Live {
				fmt.Fprintln(e.out, e.t("A pasta do worktree já foi limpa; a branch segue no espelho."))
			}
			return nil
		},
	}
	diff := &cobra.Command{
		Use: "diff " + e.t("<task>"), Short: e.t("Diff da branch contra a base"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().TaskGitDiff(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintln(e.out, p.Text)
			if p.Truncated {
				fmt.Fprintln(e.out, e.t("(diff cortado no teto de 512 KiB)"))
			}
			return nil
		},
	}
	var limit int
	logCmd := &cobra.Command{
		Use: "log " + e.t("<task>"), Short: e.t("Commits da branch desde a base"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cs, err := e.client().TaskGitLog(cmd.Context(), co, args[0], limit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum commit na branch."))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, c := range cs {
				rows = append(rows, []string{short(c.SHA), e.fmtTime(c.At), c.Author, oneLine(c.Subject, 80)})
			}
			e.table([]string{e.t("COMMIT"), e.t("QUANDO"), e.t("AUTOR"), e.t("ASSUNTO")}, rows)
			return nil
		},
	}
	logCmd.Flags().IntVar(&limit, "limit", 100, e.t("máximo de commits"))
	ls := &cobra.Command{
		Use: "ls " + e.t("<task> [caminho]"), Short: e.t("Lista uma pasta do worktree (somente leitura)"), Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			path := ""
			if len(args) > 1 {
				path = args[1]
			}
			t, err := e.client().TaskGitTree(cmd.Context(), co, args[0], path)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			for _, en := range t.Entries {
				name := en.Name
				if en.Dir {
					name += "/"
				}
				fmt.Fprintf(e.out, "%10d  %s\n", en.Size, name)
			}
			return nil
		},
	}
	cat := &cobra.Command{
		Use: "cat " + e.t("<task> <caminho>"), Short: e.t("Mostra um arquivo do worktree (somente leitura, até 256 KiB)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f, err := e.client().TaskGitFile(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			if f.Binary {
				fmt.Fprintf(e.out, e.t("(arquivo binário, %d bytes)\n"), f.Size)
				return nil
			}
			fmt.Fprint(e.out, f.Content)
			if f.Truncated {
				fmt.Fprintln(e.out, e.t("\n(arquivo cortado no teto de 256 KiB)"))
			}
			return nil
		},
	}
	clean := &cobra.Command{
		Use: "clean " + e.t("<task>"), Short: e.t("Limpa o worktree da task (admin); a branch e o trabalho solto ficam no espelho"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().RemoveTaskWorktree(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Worktree de %s limpo.\n"), args[0])
			return nil
		},
	}
	c.AddCommand(status, diff, logCmd, ls, cat, clean)
	return c
}

func (e *env) taskNewSessionCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use: "new-session " + e.t("<task>"), Short: e.t("Nova sessão: o próximo run ignora a sessão anterior e usa só o briefing e a memória"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().TaskNewSession(cmd.Context(), co, args[0], reason)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("Nova sessão pedida para %s: o próximo run começa do briefing e da memória.\n"), args[0])
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", e.t("por que recomeçar (fica na atividade e no briefing do próximo run)"))
	return c
}

func (e *env) projectDefaultsCmd() *cobra.Command {
	var instr, instrFile, goal, skillsPath string
	var clearGoal, importSkills bool
	c := &cobra.Command{
		Use: "defaults " + e.t("<projeto>"), Short: e.t("Mostra ou altera os padrões do projeto: instruções, meta e skills do repositório"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			var d api.ProjectDefaults
			if f.NFlag() > 0 {
				var req client.ProjectDefaultsReq
				if f.Changed("instructions") && f.Changed("instructions-file") {
					return usageError{errors.New(e.t("use --instructions ou --instructions-file, não os dois"))}
				}
				switch {
				case f.Changed("instructions"):
					req.Instructions = &instr
				case f.Changed("instructions-file"):
					b, err := os.ReadFile(instrFile) //nolint:gosec // o arquivo é o que o usuário apontou na linha de comando
					if err != nil {
						return fmt.Errorf("%s: %w", e.t("ler as instruções"), err)
					}
					s := strings.TrimSpace(string(b))
					req.Instructions = &s
				}
				req.Goal, req.ClearGoal = goal, clearGoal
				if f.Changed("import-skills") {
					req.ImportRepoSkills = &importSkills
				}
				if f.Changed("skills-path") {
					req.SkillsPath = &skillsPath
				}
				if d, err = e.client().SetProjectDefaults(cmd.Context(), co, args[0], req); err != nil {
					return err
				}
			} else if d, err = e.client().ProjectDefaults(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			e.kv(
				[2]string{e.t("Meta padrão"), dash(d.Goal)},
				[2]string{e.t("Importar skills do repositório"), fmt.Sprintf("%v", d.ImportRepoSkills)},
				[2]string{e.t("Caminho das skills"), dash(d.SkillsPath)},
			)
			if d.Instructions == "" {
				fmt.Fprintln(e.out, e.t("Sem instruções do projeto."))
			} else {
				fmt.Fprintf(e.out, "%s\n%s\n", e.t("Instruções do projeto:"), d.Instructions)
			}
			fmt.Fprintln(e.out, e.t("A política de revisão do projeto fica em: review-policy."))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&instr, "instructions", "", e.t("briefing do projeto (entra no prompt depois das instruções da persona; vazio limpa)"))
	f.StringVar(&instrFile, "instructions-file", "", e.t("lê o briefing de um arquivo"))
	f.StringVar(&goal, "goal", "", e.t("meta padrão das tasks novas (slug ou id)"))
	f.BoolVar(&clearGoal, "clear-goal", false, e.t("remove a meta padrão"))
	f.BoolVar(&importSkills, "import-skills", false, e.t("importa as skills do repositório clonado do workspace (--import-skills=false desliga)"))
	f.StringVar(&skillsPath, "skills-path", "", e.t("caminho do repositório onde procurar skills (vazio = tudo)"))
	return c
}
