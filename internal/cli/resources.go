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

func (e *env) companyCmd() *cobra.Command {
	c := &cobra.Command{Use: "company", Short: "Gerencia empresas"}
	var slug, prefix, tz string
	create := &cobra.Command{
		Use: "create <nome>", Short: "Cria uma empresa", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.client().CreateCompany(cmd.Context(), client.CreateCompanyReq{Name: args[0], Slug: slug, TaskPrefix: prefix, Timezone: tz})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(co)
			}
			fmt.Fprintf(e.out, "Empresa criada: %s (%s) — prefixo %s\nUse: export HANKO_COMPANY=%s\n", co.Name, co.ID, co.TaskPrefix, co.Slug)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "slug (derivado do nome se omitido)")
	create.Flags().StringVar(&prefix, "prefix", "", "prefixo das chaves de task (padrão HNK)")
	create.Flags().StringVar(&tz, "timezone", "", "fuso IANA (padrão America/Sao_Paulo)")
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista empresas", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cs, err := e.client().Companies(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, "Nenhuma empresa. Crie uma com: hanko company create <nome>")
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, co := range cs {
				rows = append(rows, []string{co.Slug, co.Name, co.TaskPrefix, co.Timezone, co.ID})
			}
			e.table([]string{"SLUG", "NOME", "PREFIXO", "FUSO", "ID"}, rows)
			return nil
		},
	}
	c.AddCommand(create, ls)
	return c
}

func (e *env) workspaceCmd() *cobra.Command {
	c := &cobra.Command{Use: "workspace", Short: "Gerencia workspaces (diretórios onde as personas trabalham)"}
	var slug, path, repo string
	create := &cobra.Command{
		Use: "create <nome>", Short: "Cria um workspace", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			w, err := e.client().CreateWorkspace(cmd.Context(), co, client.CreateWorkspaceReq{Name: args[0], Slug: slug, Path: path, RepoURL: repo})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(w)
			}
			fmt.Fprintf(e.out, "Workspace criado: %s (%s) em %s\n", w.Name, w.Slug, w.Path)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "slug (derivado do nome se omitido)")
	create.Flags().StringVar(&path, "path", "", "diretório de trabalho (obrigatório)")
	create.Flags().StringVar(&repo, "repo", "", "URL do repositório")
	_ = create.MarkFlagRequired("path")
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista workspaces", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ws, err := e.client().Workspaces(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ws)
			}
			if len(ws) == 0 {
				fmt.Fprintln(e.out, "Nenhum workspace.")
				return nil
			}
			rows := make([][]string, 0, len(ws))
			for _, w := range ws {
				rows = append(rows, []string{w.Slug, w.Name, w.Path, dash(w.RepoURL)})
			}
			e.table([]string{"SLUG", "NOME", "CAMINHO", "REPOSITÓRIO"}, rows)
			return nil
		},
	}
	c.AddCommand(create, ls)
	return c
}

func (e *env) projectCmd() *cobra.Command {
	c := &cobra.Command{Use: "project", Short: "Gerencia projetos"}
	var slug, desc, ws string
	create := &cobra.Command{
		Use: "create <nome>", Short: "Cria um projeto", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().CreateProject(cmd.Context(), co, client.CreateProjectReq{Name: args[0], Slug: slug, Description: desc, Workspace: ws})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, "Projeto criado: %s (%s)\n", p.Name, p.Slug)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "slug (derivado do nome se omitido)")
	create.Flags().StringVarP(&desc, "description", "d", "", "descrição")
	create.Flags().StringVar(&ws, "workspace", "", "workspace padrão (id ou slug)")
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista projetos", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().Projects(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			if len(ps) == 0 {
				fmt.Fprintln(e.out, "Nenhum projeto.")
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, p.Name, oneLine(dash(p.Description), 60)})
			}
			e.table([]string{"SLUG", "NOME", "DESCRIÇÃO"}, rows)
			return nil
		},
	}
	c.AddCommand(create, ls)
	return c
}

func (e *env) personaCmd() *cobra.Command {
	c := &cobra.Command{Use: "persona", Short: "Gerencia personas (papéis contratados pela empresa)"}

	var (
		slug, title, instr, instrFile, agent, model, reports, ws string
		interval                                                 int
		budget                                                   int64
		cfg                                                      map[string]string
		review                                                   bool
		revPersona, revAgent, revModel                           string
	)
	hire := &cobra.Command{
		Use: "hire <nome>", Short: "Contrata uma persona", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if instrFile != "" {
				b, err := os.ReadFile(instrFile)
				if err != nil {
					return fmt.Errorf("ler %s: %w", instrFile, err)
				}
				instr = string(b)
			}
			req := client.HirePersonaReq{Name: args[0], Slug: slug, Title: title, Instructions: instr, Agent: agent, Model: model,
				ReportsTo: reports, Workspace: ws, HeartbeatIntervalSec: interval, RequireReview: review,
				Reviewer: revPersona, ReviewerAgent: revAgent, ReviewerModel: revModel}
			if cmd.Flags().Changed("budget-cents") {
				req.BudgetMonthlyCents = &budget
			}
			if len(cfg) > 0 {
				req.AdapterConfig = map[string]any{}
				for k, v := range cfg {
					req.AdapterConfig[k] = v
				}
			}
			p, err := e.client().HirePersona(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, "Persona contratada: %s (%s) — agente %s\n", p.Name, p.Slug, p.Agent)
			return nil
		},
	}
	hf := hire.Flags()
	hf.StringVar(&slug, "slug", "", "slug (derivado do nome se omitido)")
	hf.StringVar(&title, "title", "", "cargo (ex.: CTO)")
	hf.StringVarP(&instr, "instructions", "i", "", "instruções da persona")
	hf.StringVar(&instrFile, "instructions-file", "", "arquivo com as instruções")
	hf.StringVar(&agent, "agent", "", "harness: claude, codex ou fake (obrigatório)")
	hf.StringVar(&model, "model", "", "modelo")
	hf.StringVar(&reports, "reports-to", "", "persona superior (id ou slug)")
	hf.StringVar(&ws, "workspace", "", "workspace (id ou slug)")
	hf.IntVar(&interval, "interval", 0, "intervalo do heartbeat em segundos (0 desliga)")
	hf.Int64Var(&budget, "budget-cents", 0, "orçamento mensal em centavos de dólar")
	hf.StringToStringVar(&cfg, "config", nil, "adapter_config chave=valor (repetível)")
	hf.BoolVar(&review, "require-review", false, "concluir uma task passa por um gate de revisão")
	hf.StringVar(&revPersona, "reviewer", "", "persona que revisa as entregas antes do gate (id ou slug)")
	hf.StringVar(&revAgent, "reviewer-agent", "", "harness alternativo revisor: claude, codex ou fake")
	hf.StringVar(&revModel, "reviewer-model", "", "modelo do harness revisor")
	_ = hire.MarkFlagRequired("agent")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista personas", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().Personas(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			if len(ps) == 0 {
				fmt.Fprintln(e.out, "Nenhuma persona. Contrate uma com: hanko persona hire <nome> --agent claude")
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, p.Name, dash(p.Title), p.Agent, dash(p.Model), p.Status})
			}
			e.table([]string{"SLUG", "NOME", "CARGO", "AGENTE", "MODELO", "STATUS"}, rows)
			return nil
		},
	}
	show := &cobra.Command{
		Use: "show <persona>", Short: "Mostra uma persona", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().Persona(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.kv([2]string{"Persona", fmt.Sprintf("%s (%s)", p.Name, p.Slug)}, [2]string{"ID", p.ID},
				[2]string{"Cargo", dash(p.Title)}, [2]string{"Agente", p.Agent}, [2]string{"Modelo", dash(p.Model)},
				[2]string{"Status", p.Status}, [2]string{"Heartbeat", heartbeat(p.HeartbeatIntervalSec)},
				[2]string{"Reporta a", dash(p.ReportsTo)}, [2]string{"Exige revisão", yesNo(p.RequireReview)}, [2]string{"Revisor", reviewerOf(p)},
				[2]string{"Orçamento", budgetValue(p.BudgetMonthlyCents)}, [2]string{"Contratada em", fmtTime(p.CreatedAt)})
			if p.PauseReason != "" {
				e.kv([2]string{"Pausada porque", p.PauseReason})
			}
			if p.Instructions != "" {
				fmt.Fprintf(e.out, "\nInstruções:\n%s\n", strings.TrimSpace(p.Instructions))
			}
			return nil
		},
	}

	var (
		upReports                     string
		upTop                         bool
		upReview                      string
		upRev, upRevAgent, upRevModel string
		upNoRev                       bool
	)
	update := &cobra.Command{
		Use: "update <persona>", Short: "Muda a quem a persona reporta e a revisão obrigatória", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.UpdatePersonaReq{ReportsTo: upReports, ClearReportsTo: upTop}
			switch upReview {
			case "":
			case "true", "false":
				v := upReview == "true"
				req.RequireReview = &v
			default:
				return usageError{errors.New("--require-review aceita true ou false")}
			}
			req.Reviewer, req.ClearReviewer = upRev, upNoRev
			if cmd.Flags().Changed("reviewer-agent") {
				req.ReviewerAgent = &upRevAgent
			}
			if cmd.Flags().Changed("reviewer-model") {
				req.ReviewerModel = &upRevModel
			}
			if upReports == "" && !upTop && req.RequireReview == nil && upRev == "" && !upNoRev && req.ReviewerAgent == nil && req.ReviewerModel == nil {
				return usageError{errors.New("nada a alterar: use --reports-to, --top, --require-review ou --reviewer/--reviewer-agent/--no-reviewer")}
			}
			p, err := e.client().UpdatePersona(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, "Persona atualizada: %s (%s) — reporta a %s, revisão obrigatória: %s\n", p.Name, p.Slug, dash(p.ReportsTo), yesNo(p.RequireReview))
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&upReports, "reports-to", "", "persona superior (id ou slug)")
	uf.BoolVar(&upTop, "top", false, "põe a persona no topo do organograma")
	uf.StringVar(&upReview, "require-review", "", "true ou false")
	uf.StringVar(&upRev, "reviewer", "", "persona revisora (id ou slug)")
	uf.StringVar(&upRevAgent, "reviewer-agent", "", "harness alternativo revisor")
	uf.StringVar(&upRevModel, "reviewer-model", "", "modelo do harness revisor")
	uf.BoolVar(&upNoRev, "no-reviewer", false, "remove o revisor automático")
	c.AddCommand(hire, ls, show, update)
	return c
}

// reviewerOf descreve o revisor automático configurado na persona.
func reviewerOf(p api.Persona) string {
	switch {
	case p.ReviewerPersonaID != "":
		return p.ReviewerPersonaID
	case p.ReviewerAgent != "":
		return strings.TrimSpace(p.ReviewerAgent + " " + p.ReviewerModel)
	}
	return "—"
}

func yesNo(b bool) string {
	if b {
		return "sim"
	}
	return "não"
}

func heartbeat(sec int) string {
	if sec <= 0 {
		return "desligado"
	}
	return fmt.Sprintf("a cada %ds", sec)
}
