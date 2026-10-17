package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

func (e *env) companyCmd() *cobra.Command {
	c := &cobra.Command{Use: "company", Short: e.t("Gerencia empresas")}
	var slug, prefix, tz string
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria uma empresa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.client().CreateCompany(cmd.Context(), client.CreateCompanyReq{Name: args[0], Slug: slug, TaskPrefix: prefix, Timezone: tz})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(co)
			}
			fmt.Fprintf(e.out, e.t("Empresa criada: %s (%s) — prefixo %s\nUse: export HANKO_COMPANY=%s\n"), co.Name, co.ID, co.TaskPrefix, co.Slug)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", e.t("slug (derivado do nome se omitido)"))
	create.Flags().StringVar(&prefix, "prefix", "", e.t("prefixo das chaves de task (padrão HNK)"))
	create.Flags().StringVar(&tz, "timezone", "", e.t("fuso IANA (padrão America/Sao_Paulo)"))
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista empresas"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cs, err := e.client().Companies(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma empresa. Crie uma com: hanko company create <nome>"))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, co := range cs {
				rows = append(rows, []string{co.Slug, co.Name, co.TaskPrefix, co.Timezone, co.ID})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("PREFIXO"), e.t("FUSO"), e.t("ID")}, rows)
			return nil
		},
	}
	c.AddCommand(create, ls)
	return c
}

func (e *env) workspaceCmd() *cobra.Command {
	c := &cobra.Command{Use: "workspace", Short: e.t("Gerencia workspaces (diretórios onde as personas trabalham)")}
	var slug, path, repo, branch string
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um workspace"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			w, err := e.client().CreateWorkspace(cmd.Context(), co, client.CreateWorkspaceReq{Name: args[0], Slug: slug, Path: path, RepoURL: repo, Branch: branch})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(w)
			}
			fmt.Fprintf(e.out, e.t("Workspace criado: %s (%s) em %s\n"), w.Name, w.Slug, w.Path)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", e.t("slug (derivado do nome se omitido)"))
	create.Flags().StringVar(&path, "path", "", e.t("diretório de trabalho (obrigatório)"))
	create.Flags().StringVar(&repo, "repo", "", e.t("URL do repositório"))
	create.Flags().StringVar(&branch, "branch", "", e.t("branch padrão (metadado)"))
	_ = create.MarkFlagRequired("path")
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista workspaces"), Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, e.t("Nenhum workspace."))
				return nil
			}
			rows := make([][]string, 0, len(ws))
			for _, w := range ws {
				uso := "-"
				if w.Usage != nil {
					uso = fmt.Sprintf("%d/%d/%d", w.Usage.Personas, w.Usage.Projects, w.Usage.OpenTasks)
				}
				rows = append(rows, []string{w.Slug, w.Name, w.Path, dash(w.RepoURL), uso})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("CAMINHO"), e.t("REPOSITÓRIO"), e.t("USO (PERSONAS/PROJETOS/TASKS)")}, rows)
			return nil
		},
	}
	c.AddCommand(create, ls, e.workspaceShowCmd(), e.workspaceUpdateCmd(), e.workspaceDeleteCmd(), e.workspaceGitCmd())
	return c
}

func (e *env) projectCmd() *cobra.Command {
	c := &cobra.Command{Use: "project", Short: e.t("Gerencia projetos")}
	var slug, desc, ws string
	var budget int64
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um projeto"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.CreateProjectReq{Name: args[0], Slug: slug, Description: desc, Workspace: ws}
			if cmd.Flags().Changed("budget-cents") {
				req.BudgetMonthlyCents = &budget
			}
			p, err := e.client().CreateProject(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Projeto criado: %s (%s)\n"), p.Name, p.Slug)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", e.t("slug (derivado do nome se omitido)"))
	create.Flags().StringVarP(&desc, "description", "d", "", e.t("descrição"))
	create.Flags().StringVar(&ws, "workspace", "", e.t("workspace padrão (id ou slug)"))
	create.Flags().Int64Var(&budget, "budget-cents", 0, e.t("orçamento mensal em centavos de dólar"))
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista projetos"), Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, e.t("Nenhum projeto."))
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				prog, spent := "-", "-"
				if p.Stats != nil {
					prog = fmt.Sprintf("%.0f%% (%d)", p.Stats.ProgressPercent, p.Stats.TasksTotal)
					spent = e.usd(p.Stats.Budget.SpentUSD)
				}
				rows = append(rows, []string{p.Slug, p.Name, e.projectStatus(p.Status), prog, spent, oneLine(dash(p.Description), 40)})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("STATUS"), e.t("PROGRESSO"), e.t("GASTO NO MÊS"), e.t("DESCRIÇÃO")}, rows)
			return nil
		},
	}
	c.AddCommand(create, ls, e.projectShowCmd(), e.projectUpdateCmd(), e.projectReviewPolicyCmd(), e.projectDefaultsCmd(), e.projectGitCmd())
	return c
}

func (e *env) personaCmd() *cobra.Command {
	c := &cobra.Command{Use: "persona", Short: e.t("Gerencia personas (papéis contratados pela empresa)")}

	var (
		slug, title, icon, instr, instrFile, agent, model, reports, ws, subscription string
		interval                                                                     int
		budget                                                                       int64
		cfg                                                                          map[string]string
		cfgJSON                                                                      string
		review                                                                       bool
		revPersona, revAgent, revModel                                               string
	)
	hire := &cobra.Command{
		Use: "hire " + e.t("<nome>"), Short: e.t("Contrata uma persona"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if instrFile != "" {
				b, err := os.ReadFile(instrFile)
				if err != nil {
					return fmt.Errorf(e.t("ler %s: %w"), instrFile, err)
				}
				instr = string(b)
			}
			req := client.HirePersonaReq{Name: args[0], Slug: slug, Title: title, Icon: icon, Instructions: instr, Agent: agent, Model: model,
				ReportsTo: reports, Workspace: ws, HeartbeatIntervalSec: interval, RequireReview: review, Subscription: subscription,
				Reviewer: revPersona, ReviewerAgent: revAgent, ReviewerModel: revModel}
			if cmd.Flags().Changed("budget-cents") {
				req.BudgetMonthlyCents = &budget
			}
			if cfgJSON != "" {
				if err := json.Unmarshal([]byte(cfgJSON), &req.AdapterConfig); err != nil {
					return fmt.Errorf(e.t("--config-json não é um objeto JSON válido: %w"), err)
				}
			}
			if len(cfg) > 0 {
				if req.AdapterConfig == nil {
					req.AdapterConfig = map[string]any{}
				}
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
			fmt.Fprintf(e.out, e.t("Persona contratada: %s (%s) — agente %s\n"), p.Name, p.Slug, p.Agent)
			return nil
		},
	}
	hf := hire.Flags()
	hf.StringVar(&slug, "slug", "", e.t("slug (derivado do nome se omitido)"))
	hf.StringVar(&title, "title", "", e.t("cargo (ex.: CTO)"))
	hf.StringVar(&icon, "icon", "", e.t("ícone da persona (bot, code, search, pen-line, shield-check…; padrão bot)"))
	hf.StringVarP(&instr, "instructions", "i", "", e.t("instruções da persona"))
	hf.StringVar(&instrFile, "instructions-file", "", e.t("arquivo com as instruções"))
	hf.StringVar(&agent, "agent", "", e.t("harness: claude, codex, acp ou fake (obrigatório)"))
	hf.StringVar(&model, "model", "", e.t("modelo"))
	hf.StringVar(&subscription, "subscription", "", e.t("assinatura que cobre a persona (id ou nome)"))
	hf.StringVar(&reports, "reports-to", "", e.t("persona superior (id ou slug)"))
	hf.StringVar(&ws, "workspace", "", e.t("workspace (id ou slug)"))
	hf.IntVar(&interval, "interval", 0, e.t("intervalo do heartbeat em segundos (0 desliga)"))
	hf.Int64Var(&budget, "budget-cents", 0, e.t("orçamento mensal em centavos de dólar"))
	hf.StringToStringVar(&cfg, "config", nil, e.t("adapter_config chave=valor (repetível)"))
	hf.StringVar(&cfgJSON, "config-json", "", e.t("adapter_config em JSON, para listas e objetos (ex.: acp: '{\"command\":\"meu-agente\",\"args\":[\"--stdio\"]}'); --config sobrepõe as chaves"))
	hf.BoolVar(&review, "require-review", false, e.t("concluir uma task passa por um gate de revisão"))
	hf.StringVar(&revPersona, "reviewer", "", e.t("persona que revisa as entregas antes do gate (id ou slug)"))
	hf.StringVar(&revAgent, "reviewer-agent", "", e.t("harness alternativo revisor: claude, codex ou fake"))
	hf.StringVar(&revModel, "reviewer-model", "", e.t("modelo do harness revisor"))
	_ = hire.MarkFlagRequired("agent")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista personas"), Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, e.t("Nenhuma persona. Contrate uma com: hanko persona hire <nome> --agent claude"))
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, p.Name, dash(p.Title), p.Agent, dash(p.Model), p.Status})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("CARGO"), e.t("AGENTE"), e.t("MODELO"), e.t("STATUS")}, rows)
			return nil
		},
	}
	show := &cobra.Command{
		Use: "show " + e.t("<persona>"), Short: e.t("Mostra uma persona"), Args: cobra.ExactArgs(1),
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
			e.kv([2]string{e.t("Persona"), fmt.Sprintf("%s (%s)", p.Name, p.Slug)}, [2]string{e.t("ID"), p.ID},
				[2]string{e.t("Cargo"), dash(p.Title)}, [2]string{e.t("Agente"), p.Agent}, [2]string{e.t("Modelo"), dash(p.Model)}, [2]string{e.t("Assinatura"), dash(p.SubscriptionID)},
				[2]string{e.t("Status"), p.Status}, [2]string{e.t("Heartbeat"), e.heartbeat(p.HeartbeatIntervalSec)},
				[2]string{e.t("Reporta a"), dash(p.ReportsTo)}, [2]string{e.t("Exige revisão"), e.yesNo(p.RequireReview)}, [2]string{e.t("Revisor"), e.reviewerOf(p)},
				[2]string{e.t("Orçamento"), e.budgetValue(p.BudgetMonthlyCents)}, [2]string{e.t("Contratada em"), e.fmtTime(p.CreatedAt)})
			if p.PauseReason != "" {
				e.kv([2]string{e.t("Pausada porque"), p.PauseReason})
			}
			if p.Instructions != "" {
				fmt.Fprintf(e.out, e.t("\nInstruções:\n%s\n"), strings.TrimSpace(p.Instructions))
			}
			return nil
		},
	}

	var (
		upReports                      string
		upTop                          bool
		upReview                       string
		upSub                          string
		upNoSub                        bool
		upRev, upRevAgent, upRevModel  string
		upNoRev                        bool
		upEnv                          string
		upInstr, upInstrFile, upReason string
	)
	update := &cobra.Command{
		Use: "update " + e.t("<persona>"), Short: e.t("Muda a quem a persona reporta, a revisão obrigatória e a assinatura"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			instr, err := e.readInstructions(cmd, upInstr, upInstrFile)
			if err != nil {
				return err
			}
			req := client.UpdatePersonaReq{Instructions: instr, InstructionsReason: upReason, ReportsTo: upReports, ClearReportsTo: upTop, Subscription: upSub, ClearSubscription: upNoSub, Environment: upEnv}
			switch upReview {
			case "":
			case "true", "false":
				v := upReview == "true"
				req.RequireReview = &v
			default:
				return usageError{errors.New(e.t("--require-review aceita true ou false"))}
			}
			req.Reviewer, req.ClearReviewer = upRev, upNoRev
			if cmd.Flags().Changed("reviewer-agent") {
				req.ReviewerAgent = &upRevAgent
			}
			if cmd.Flags().Changed("reviewer-model") {
				req.ReviewerModel = &upRevModel
			}
			if upSub != "" && upNoSub {
				return usageError{errors.New(e.t("--subscription e --no-subscription não combinam"))}
			}
			if upReports == "" && !upTop && req.RequireReview == nil && upSub == "" && !upNoSub && upRev == "" && !upNoRev &&
				req.ReviewerAgent == nil && req.ReviewerModel == nil && upEnv == "" && instr == nil {
				return usageError{errors.New(e.t("nada a alterar: use --reports-to, --top, --require-review, --subscription, --no-subscription ou --reviewer/--reviewer-agent/--no-reviewer"))}
			}
			p, err := e.client().UpdatePersona(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Persona atualizada: %s (%s) — reporta a %s, revisão obrigatória: %s\n"), p.Name, p.Slug, dash(p.ReportsTo), e.yesNo(p.RequireReview))
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&upReports, "reports-to", "", e.t("persona superior (id ou slug)"))
	uf.BoolVar(&upTop, "top", false, e.t("põe a persona no topo do organograma"))
	uf.StringVar(&upReview, "require-review", "", e.t("true ou false"))
	uf.StringVar(&upRev, "reviewer", "", e.t("persona revisora (id ou slug)"))
	uf.StringVar(&upRevAgent, "reviewer-agent", "", e.t("harness alternativo revisor"))
	uf.StringVar(&upRevModel, "reviewer-model", "", e.t("modelo do harness revisor"))
	uf.BoolVar(&upNoRev, "no-reviewer", false, e.t("remove o revisor automático"))
	uf.StringVar(&upSub, "subscription", "", e.t("vincula a persona a uma assinatura (id ou nome)"))
	uf.BoolVar(&upNoSub, "no-subscription", false, e.t("desvincula a persona da assinatura"))
	uf.StringVar(&upInstr, "instructions", "", e.t("novas instruções da persona (vira uma revisão)"))
	uf.StringVar(&upInstrFile, "instructions-file", "", e.t("arquivo com as novas instruções"))
	uf.StringVar(&upReason, "reason", "", e.t("motivo da mudança de instruções (fica na revisão)"))
	uf.StringVar(&upEnv, "environment", "", e.t("ambiente de execução (id ou slug; local = o padrão da empresa)"))
	c.AddCommand(hire, ls, show, update)
	c.AddCommand(e.instructionCmds()...)
	return c
}

// reviewerOf descreve o revisor automático configurado na persona.
func (e *env) reviewerOf(p api.Persona) string {
	switch {
	case p.ReviewerPersonaID != "":
		return p.ReviewerPersonaID
	case p.ReviewerAgent != "":
		return strings.TrimSpace(p.ReviewerAgent + " " + p.ReviewerModel)
	}
	return "—"
}

func (e *env) yesNo(b bool) string {
	if b {
		return e.t("sim")
	}
	return e.t("não")
}

func (e *env) heartbeat(sec int) string {
	if sec <= 0 {
		return e.t("desligado")
	}
	return e.tf("a cada %ds", sec)
}
