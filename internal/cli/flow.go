package cli

import (
	"fmt"
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
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um fluxo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.CreateFlowReq{Name: args[0], Slug: slug, Description: desc, Cron: cronExpr, Timezone: tz, Persona: persona,
				Project: project, Priority: priority, TaskTitle: title, TaskDescription: taskDesc}
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
			fmt.Fprintf(e.out, e.t("Fluxo criado: %s (%s) — %s em %s → %s\nPróxima execução: %s\n"), f.Name, f.Slug, f.Cron, f.Timezone, f.PersonaName, e.fmtTimePtr(f.NextFireAt))
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&slug, "slug", "", e.t("slug (derivado do nome se omitido)"))
	cf.StringVarP(&desc, "description", "d", "", e.t("descrição"))
	cf.StringVar(&cronExpr, "cron", "", e.t("cron de 5 campos, ex.: \"0 9 * * 1-5\" (obrigatório)"))
	cf.StringVar(&tz, "timezone", "", e.t("fuso IANA (padrão: o da empresa)"))
	cf.StringVarP(&persona, "persona", "p", "", e.t("persona que recebe a task (obrigatório)"))
	cf.StringVar(&project, "project", "", e.t("projeto das tasks (id ou slug)"))
	cf.StringVar(&priority, "priority", "", e.t("critical, high, medium ou low"))
	cf.StringVar(&title, "title", "", e.t("modelo do título da task; aceita {{data}} e {{n}}"))
	cf.StringVar(&taskDesc, "task-description", "", e.t("modelo da descrição da task"))
	cf.BoolVar(&disabled, "disabled", false, e.t("cria pausado"))
	_ = create.MarkFlagRequired("cron")
	_ = create.MarkFlagRequired("persona")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista fluxos"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			fs, err := e.client().Flows(cmd.Context(), co)
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
				rows = append(rows, []string{f.Slug, e.onOff(f.Enabled), f.Cron, f.PersonaName, next})
			}
			e.table([]string{e.t("SLUG"), e.t("ESTADO"), e.t("CRON"), e.t("PERSONA"), e.t("PRÓXIMA EXECUÇÃO")}, rows)
			return nil
		},
	}

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
			if len(fires) > 0 {
				fmt.Fprintln(e.out, e.t("\nDisparos recentes:"))
				rows := make([][]string, 0, len(fires))
				for _, x := range fires {
					rows = append(rows, []string{e.fmtTime(x.ScheduledFor), dash(x.TaskKey), dash(x.TaskStatus), oneLine(dash(x.TaskTitle), 60)})
				}
				e.table([]string{e.t("AGENDADO"), e.t("TASK"), e.t("STATUS"), e.t("TÍTULO")}, rows)
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

	run := &cobra.Command{
		Use: "run " + e.t("<fluxo>"), Short: e.t("Dispara o fluxo agora (cria a task)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().RunFlow(cmd.Context(), co, args[0])
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
	c.AddCommand(create, ls, show, toggle("enable", e.t("Ativa um fluxo"), true), toggle("disable", e.t("Pausa um fluxo"), false), run)
	return c
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
