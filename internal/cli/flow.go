package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

func (e *env) onOff(b bool) string {
	if b {
		return e.t("ativo")
	}
	return e.t("pausado")
}

func (e *env) flowCmd() *cobra.Command {
	c := &cobra.Command{Use: "flow", Short: e.t("Gerencia fluxos (tasks recorrentes por cron)")}

	var slug, desc, cronExpr, tz, persona, project, priority, title, taskDesc string
	var disabled bool
	var varDefs []string
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um fluxo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			vars, err := parseVarDefs(varDefs)
			if err != nil {
				return err
			}
			req := client.CreateFlowReq{Name: args[0], Slug: slug, Description: desc, Cron: cronExpr, Timezone: tz, Persona: persona,
				Project: project, Priority: priority, TaskTitle: title, TaskDescription: taskDesc, Variables: vars}
			if disabled {
				off := false
				req.Enabled = &off
			}
			f, err := e.client().CreateFlow(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Fluxo criado: %s (%s) — %s em %s → %s\nPróxima execução: %s\n"), f.Name, f.Slug, dash(f.Cron), f.Timezone, f.PersonaName, e.fmtTimePtr(f.NextFireAt))
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&slug, "slug", "", e.t("slug (derivado do nome se omitido)"))
	cf.StringVarP(&desc, "description", "d", "", e.t("descrição"))
	cf.StringVar(&cronExpr, "cron", "", e.t("cron de 5 campos, ex.: \"0 9 * * 1-5\" (omitido = sem agenda: só gatilhos e `flow run`)"))
	cf.StringVar(&tz, "timezone", "", e.t("fuso IANA (padrão: o da empresa)"))
	cf.StringVarP(&persona, "persona", "p", "", e.t("persona que recebe a task (obrigatório)"))
	cf.StringVar(&project, "project", "", e.t("projeto das tasks (id ou slug)"))
	cf.StringVar(&priority, "priority", "", e.t("critical, high, medium ou low"))
	cf.StringVar(&title, "title", "", e.t("modelo do título da task; aceita {{data}}, {{n}}, {{semana}}, {{disparo}} e as variáveis"))
	cf.StringArrayVar(&varDefs, "var", nil, e.t("declara uma variável: nome, nome! (obrigatória), nome=padrão ou nome!=padrão (repetível)"))
	cf.StringVar(&taskDesc, "task-description", "", e.t("modelo da descrição da task"))
	cf.BoolVar(&disabled, "disabled", false, e.t("cria pausado"))
	_ = create.MarkFlagRequired("persona")

	var archivedList bool
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista fluxos"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var fs []api.Flow
			if archivedList {
				fs, err = e.client().FlowsArchived(cmd.Context(), co)
			} else {
				fs, err = e.client().Flows(cmd.Context(), co)
			}
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(fs)
			}
			if len(fs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum fluxo. Crie um com: hanko flow create <nome> --cron \"0 9 * * *\" --persona <persona>"))
				return nil
			}
			rows := make([][]string, 0, len(fs))
			for _, f := range fs {
				next := "-"
				if f.Enabled {
					next = e.fmtTimePtr(f.NextFireAt)
				}
				rows = append(rows, []string{f.Slug, e.onOff(f.Enabled), dash(f.Cron), f.PersonaName, next})
			}
			e.table([]string{e.t("SLUG"), e.t("ESTADO"), e.t("CRON"), e.t("PERSONA"), e.t("PRÓXIMA EXECUÇÃO")}, rows)
			return nil
		},
	}

	ls.Flags().BoolVar(&archivedList, "archived", false, e.t("lista os fluxos arquivados"))

	show := &cobra.Command{
		Use: "show " + e.t("<fluxo>"), Short: e.t("Mostra um fluxo e seus últimos disparos"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f, err := e.client().Flow(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			fires, err := e.client().FlowFires(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"flow": f, "fires": fires})
			}
			e.kv([2]string{e.t("Fluxo"), fmt.Sprintf("%s (%s)", f.Name, f.Slug)}, [2]string{e.t("ID"), f.ID}, [2]string{e.t("Estado"), e.onOff(f.Enabled)},
				[2]string{e.t("Cron"), fmt.Sprintf("%s (%s)", f.Cron, f.Timezone)}, [2]string{e.t("Persona"), f.PersonaName},
				[2]string{e.t("Projeto"), dash(f.ProjectName)}, [2]string{e.t("Prioridade"), f.Priority},
				[2]string{e.t("Título da task"), dash(f.TaskTitle)}, [2]string{e.t("Último disparo"), e.fmtTimePtr(f.LastFiredAt)},
				[2]string{e.t("Próxima execução"), e.fmtTimePtr(f.NextFireAt)})
			if strings.TrimSpace(f.Description) != "" {
				fmt.Fprintf(e.out, "\n%s\n", strings.TrimSpace(f.Description))
			}
			if f.ArchivedAt != nil {
				fmt.Fprintf(e.out, e.t("\nArquivado em %s.\n"), e.fmtTime(*f.ArchivedAt))
			}
			if len(f.Variables) > 0 {
				fmt.Fprintln(e.out, e.t("\nVariáveis:"))
				rows := make([][]string, 0, len(f.Variables))
				for _, v := range f.Variables {
					req := ""
					if v.Required {
						req = e.t("sim")
					}
					rows = append(rows, []string{v.Name, req, dash(v.Default)})
				}
				e.table([]string{e.t("NOME"), e.t("OBRIGATÓRIA"), e.t("PADRÃO")}, rows)
			}
			if len(f.Triggers) > 0 {
				fmt.Fprintln(e.out, e.t("\nGatilhos:"))
				e.printTriggers(f.Triggers)
			}
			if len(fires) > 0 {
				fmt.Fprintln(e.out, e.t("\nDisparos recentes:"))
				rows := make([][]string, 0, len(fires))
				for _, x := range fires {
					rows = append(rows, []string{e.fmtTime(x.ScheduledFor), dash(x.Source), dash(x.TaskKey), dash(x.TaskStatus), oneLine(dash(x.TaskTitle), 60)})
				}
				e.table([]string{e.t("AGENDADO"), e.t("ORIGEM"), e.t("TASK"), e.t("STATUS"), e.t("TÍTULO")}, rows)
			}
			return nil
		},
	}

	toggle := func(use, short string, enable bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<fluxo>"), Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				f, err := e.client().UpdateFlow(cmd.Context(), co, args[0], client.UpdateFlowReq{Enabled: &enable})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(f)
				}
				if f.Enabled {
					fmt.Fprintf(e.out, e.t("Fluxo %s ativo. Próxima execução: %s\n"), f.Slug, e.fmtTimePtr(f.NextFireAt))
				} else {
					fmt.Fprintf(e.out, e.t("Fluxo %s pausado.\n"), f.Slug)
				}
				return nil
			},
		}
	}

	var runVars []string
	run := &cobra.Command{
		Use: "run " + e.t("<fluxo>"), Short: e.t("Dispara o fluxo agora (cria a task)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			vals, err := parseVarValues(runVars)
			if err != nil {
				return err
			}
			var r api.FlowRun
			if len(vals) > 0 {
				r, err = e.client().RunFlowWith(cmd.Context(), co, args[0], vals)
			} else {
				r, err = e.client().RunFlow(cmd.Context(), co, args[0])
			}
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			printFlowRun(e, r)
			return nil
		},
	}
	run.Flags().StringArrayVar(&runVars, "var", nil, e.t("valor de uma variável: nome=valor (repetível)"))

	var editCron, editTitle, editTaskDesc string
	var editVars []string
	edit := &cobra.Command{
		Use: "edit " + e.t("<fluxo>"), Short: e.t("Muda a agenda, os modelos ou as variáveis de um fluxo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var req client.UpdateFlowReq
			fl := cmd.Flags()
			if fl.Changed("cron") {
				req.Cron = &editCron
			}
			if fl.Changed("title") {
				req.TaskTitle = &editTitle
			}
			if fl.Changed("task-description") {
				req.TaskDescription = &editTaskDesc
			}
			if fl.Changed("var") {
				vars, err := parseVarDefs(editVars)
				if err != nil {
					return err
				}
				req.Variables = &vars
			}
			f, err := e.client().UpdateFlow(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Fluxo %s atualizado (revisão %d).\n"), f.Slug, f.Revision)
			return nil
		},
	}
	ef := edit.Flags()
	ef.StringVar(&editCron, "cron", "", e.t("novo cron de 5 campos (vazio remove a agenda)"))
	ef.StringVar(&editTitle, "title", "", e.t("novo modelo do título da task"))
	ef.StringVar(&editTaskDesc, "task-description", "", e.t("novo modelo da descrição da task"))
	ef.StringArrayVar(&editVars, "var", nil, e.t("substitui as variáveis: nome, nome!, nome=padrão ou nome!=padrão (repetível; sem valor, remove todas)"))

	archive := func(use, short string, on bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<fluxo>"), Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				f, err := e.client().ArchiveFlow(cmd.Context(), co, args[0], on)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(f)
				}
				if on {
					fmt.Fprintf(e.out, e.t("Fluxo %s arquivado.\n"), f.Slug)
				} else {
					fmt.Fprintf(e.out, e.t("Fluxo %s desarquivado (volta pausado).\n"), f.Slug)
				}
				return nil
			},
		}
	}

	revisions := &cobra.Command{
		Use: "revisions " + e.t("<fluxo>"), Short: e.t("Lista as revisões da definição do fluxo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rs, err := e.client().FlowRevisions(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(rs)
			}
			rows := make([][]string, 0, len(rs))
			for _, r := range rs {
				rows = append(rows, []string{strconv.Itoa(r.Revision), e.fmtTime(r.CreatedAt), r.Origin, r.Actor.Type, oneLine(dash(r.Summary), 60)})
			}
			e.table([]string{e.t("REVISÃO"), e.t("QUANDO"), e.t("ORIGEM"), e.t("AUTOR"), e.t("MUDOU")}, rows)
			return nil
		},
	}
	restore := &cobra.Command{
		Use: "restore " + e.t("<fluxo> <revisão>"), Short: e.t("Restaura uma revisão (vira uma edição nova; os gatilhos não voltam)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				return fmt.Errorf("%s", e.t("a revisão deve ser um número a partir de 1"))
			}
			f, err := e.client().RestoreFlowRevision(cmd.Context(), co, args[0], n)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Fluxo %s restaurado a partir da revisão %d (agora na revisão %d).\n"), f.Slug, n, f.Revision)
			return nil
		},
	}

	c.AddCommand(create, ls, show, edit, toggle("enable", e.t("Ativa um fluxo"), true), toggle("disable", e.t("Pausa um fluxo"), false), run,
		archive("archive", e.t("Arquiva um fluxo (some da lista; não dispara)"), true), archive("unarchive", e.t("Desarquiva um fluxo"), false),
		revisions, restore, e.flowTriggerCmd())
	return c
}

// parseVarDefs lê as declarações nome, nome!, nome=padrão e nome!=padrão.
func parseVarDefs(defs []string) ([]api.FlowVariable, error) {
	out := make([]api.FlowVariable, 0, len(defs))
	for _, d := range defs {
		name, def, hasDef := strings.Cut(d, "=")
		v := api.FlowVariable{Name: strings.TrimSpace(name)}
		if n, ok := strings.CutSuffix(v.Name, "!"); ok {
			v.Name, v.Required = n, true
		}
		if hasDef {
			v.Default = def
		}
		if v.Name == "" {
			return nil, fmt.Errorf("declaração de variável inválida: %q", d)
		}
		out = append(out, v)
	}
	return out, nil
}

// parseVarValues lê os valores nome=valor.
func parseVarValues(vals []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vals {
		name, val, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("use nome=valor, recebi %q", v)
		}
		out[strings.TrimSpace(name)] = val
	}
	return out, nil
}

func (e *env) printTriggers(ts []api.FlowTrigger) {
	rows := make([][]string, 0, len(ts))
	for _, t := range ts {
		what := t.Path
		if t.Kind == "event" {
			what = t.EventVerb
		}
		rows = append(rows, []string{t.ID, t.Name, t.Kind, e.onOff(t.Enabled), strconv.Itoa(t.RatePerMin), what})
	}
	e.table([]string{e.t("ID"), e.t("NOME"), e.t("TIPO"), e.t("ESTADO"), e.t("POR MIN"), e.t("ALVO")}, rows)
}

func (e *env) flowTriggerCmd() *cobra.Command {
	c := &cobra.Command{Use: "trigger", Short: e.t("Gatilhos do fluxo: webhook (HMAC) e evento interno")}
	var kind, secretRef, verb string
	var rate int
	var filters, maps []string
	add := &cobra.Command{
		Use: "add " + e.t("<fluxo> <nome>"), Short: e.t("Cria um gatilho"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			filter, err := parseKV(filters)
			if err != nil {
				return err
			}
			mapping, err := parseKV(maps)
			if err != nil {
				return err
			}
			t, err := e.client().CreateFlowTrigger(cmd.Context(), co, args[0], client.CreateFlowTriggerReq{Kind: kind, Name: args[1],
				SecretRef: secretRef, RatePerMin: rate, EventVerb: verb, EventFilter: filter, Mapping: mapping})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("Gatilho criado: %s (%s)\n"), t.Name, t.ID)
			if t.Kind == "webhook" {
				fmt.Fprintf(e.out, e.t("POST <url pública>%s com o cabeçalho X-Hanko-Signature (t=<unix>,v1=<hmac-sha256 de \"<t>.<corpo>\">)\n"), t.Path)
				if t.SecretValue != "" {
					fmt.Fprintf(e.out, e.t("Segredo HMAC (guarde agora, não será mostrado de novo): %s\n"), t.SecretValue)
				}
			}
			return nil
		},
	}
	af := add.Flags()
	af.StringVar(&kind, "kind", "webhook", e.t("webhook ou event"))
	af.StringVar(&secretRef, "secret", "", e.t("webhook: referência ${secret:NOME} do HMAC (omitida, o servidor gera um segredo)"))
	af.StringVar(&verb, "event", "", e.t("evento: verbo de atividade (task.created) ou prefixo (gate.*)"))
	af.IntVar(&rate, "rate", 0, e.t("entregas por minuto (padrão 30)"))
	af.StringArrayVar(&filters, "filter", nil, e.t("evento: condição caminho=valor (data.status=done), repetível"))
	af.StringArrayVar(&maps, "map", nil, e.t("variável=caminho.na.carga, repetível"))

	ls := &cobra.Command{
		Use: "list " + e.t("<fluxo>"), Aliases: []string{"ls"}, Short: e.t("Lista os gatilhos"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ts, err := e.client().FlowTriggers(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ts)
			}
			if len(ts) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum gatilho."))
				return nil
			}
			e.printTriggers(ts)
			return nil
		},
	}
	set := func(use, short string, on bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<fluxo> <gatilho>"), Short: short, Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				t, err := e.client().UpdateFlowTrigger(cmd.Context(), co, args[0], args[1], client.UpdateFlowTriggerReq{Enabled: &on})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(t)
				}
				fmt.Fprintf(e.out, "%s: %s\n", t.Name, e.onOff(t.Enabled))
				return nil
			},
		}
	}
	rm := &cobra.Command{
		Use: "rm " + e.t("<fluxo> <gatilho>"), Short: e.t("Remove um gatilho"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteFlowTrigger(cmd.Context(), co, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Gatilho removido."))
			return nil
		},
	}
	c.AddCommand(add, ls, set("enable", e.t("Liga um gatilho"), true), set("disable", e.t("Desliga um gatilho"), false), rm)
	return c
}

// parseKV lê pares chave=valor.
func parseKV(in []string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range in {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("use chave=valor, recebi %q", kv)
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

func printFlowRun(e *env, r api.FlowRun) {
	if r.Task == nil {
		fmt.Fprintln(e.out, e.t("Nada criado."))
		return
	}
	if r.Duplicate {
		fmt.Fprintf(e.out, e.t("Já disparado neste minuto: %s %s\n"), r.Task.Key, r.Task.Title)
		return
	}
	fmt.Fprintf(e.out, e.t("Task criada: %s %s → %s\n"), r.Task.Key, r.Task.Title, dash(r.Task.PersonaName))
}
