package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

func onOff(b bool) string {
	if b {
		return "ativo"
	}
	return "pausado"
}

func (e *env) flowCmd() *cobra.Command {
	c := &cobra.Command{Use: "flow", Short: "Gerencia fluxos (tasks recorrentes por cron)"}

	var slug, desc, cronExpr, tz, persona, project, priority, title, taskDesc string
	var disabled bool
	create := &cobra.Command{
		Use: "create <nome>", Short: "Cria um fluxo", Args: cobra.ExactArgs(1),
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
			fmt.Fprintf(e.out, "Fluxo criado: %s (%s) — %s em %s → %s\nPróxima execução: %s\n", f.Name, f.Slug, f.Cron, f.Timezone, f.PersonaName, fmtTimePtr(f.NextFireAt))
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&slug, "slug", "", "slug (derivado do nome se omitido)")
	cf.StringVarP(&desc, "description", "d", "", "descrição")
	cf.StringVar(&cronExpr, "cron", "", "cron de 5 campos, ex.: \"0 9 * * 1-5\" (obrigatório)")
	cf.StringVar(&tz, "timezone", "", "fuso IANA (padrão: o da empresa)")
	cf.StringVarP(&persona, "persona", "p", "", "persona que recebe a task (obrigatório)")
	cf.StringVar(&project, "project", "", "projeto das tasks (id ou slug)")
	cf.StringVar(&priority, "priority", "", "critical, high, medium ou low")
	cf.StringVar(&title, "title", "", "modelo do título da task; aceita {{data}} e {{n}}")
	cf.StringVar(&taskDesc, "task-description", "", "modelo da descrição da task")
	cf.BoolVar(&disabled, "disabled", false, "cria pausado")
	_ = create.MarkFlagRequired("cron")
	_ = create.MarkFlagRequired("persona")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "Lista fluxos", Args: cobra.NoArgs,
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
				fmt.Fprintln(e.out, "Nenhum fluxo. Crie um com: hanko flow create <nome> --cron \"0 9 * * *\" --persona <persona>")
				return nil
			}
			rows := make([][]string, 0, len(fs))
			for _, f := range fs {
				next := "-"
				if f.Enabled {
					next = fmtTimePtr(f.NextFireAt)
				}
				rows = append(rows, []string{f.Slug, onOff(f.Enabled), f.Cron, f.PersonaName, next})
			}
			e.table([]string{"SLUG", "ESTADO", "CRON", "PERSONA", "PRÓXIMA EXECUÇÃO"}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show <fluxo>", Short: "Mostra um fluxo e seus últimos disparos", Args: cobra.ExactArgs(1),
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
			e.kv([2]string{"Fluxo", fmt.Sprintf("%s (%s)", f.Name, f.Slug)}, [2]string{"ID", f.ID}, [2]string{"Estado", onOff(f.Enabled)},
				[2]string{"Cron", fmt.Sprintf("%s (%s)", f.Cron, f.Timezone)}, [2]string{"Persona", f.PersonaName},
				[2]string{"Projeto", dash(f.ProjectName)}, [2]string{"Prioridade", f.Priority},
				[2]string{"Título da task", dash(f.TaskTitle)}, [2]string{"Último disparo", fmtTimePtr(f.LastFiredAt)},
				[2]string{"Próxima execução", fmtTimePtr(f.NextFireAt)})
			if strings.TrimSpace(f.Description) != "" {
				fmt.Fprintf(e.out, "\n%s\n", strings.TrimSpace(f.Description))
			}
			if len(fires) > 0 {
				fmt.Fprintln(e.out, "\nDisparos recentes:")
				rows := make([][]string, 0, len(fires))
				for _, x := range fires {
					rows = append(rows, []string{fmtTime(x.ScheduledFor), dash(x.TaskKey), dash(x.TaskStatus), oneLine(dash(x.TaskTitle), 60)})
				}
				e.table([]string{"AGENDADO", "TASK", "STATUS", "TÍTULO"}, rows)
			}
			return nil
		},
	}

	toggle := func(use, short string, enable bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " <fluxo>", Short: short, Args: cobra.ExactArgs(1),
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
					fmt.Fprintf(e.out, "Fluxo %s ativo. Próxima execução: %s\n", f.Slug, fmtTimePtr(f.NextFireAt))
				} else {
					fmt.Fprintf(e.out, "Fluxo %s pausado.\n", f.Slug)
				}
				return nil
			},
		}
	}

	run := &cobra.Command{
		Use: "run <fluxo>", Short: "Dispara o fluxo agora (cria a task)", Args: cobra.ExactArgs(1),
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
	c.AddCommand(create, ls, show, toggle("enable", "Ativa um fluxo", true), toggle("disable", "Pausa um fluxo", false), run)
	return c
}

func printFlowRun(e *env, r api.FlowRun) {
	if r.Task == nil {
		fmt.Fprintln(e.out, "Nada criado.")
		return
	}
	if r.Duplicate {
		fmt.Fprintf(e.out, "Já disparado neste minuto: %s %s\n", r.Task.Key, r.Task.Title)
		return
	}
	fmt.Fprintf(e.out, "Task criada: %s %s → %s\n", r.Task.Key, r.Task.Title, dash(r.Task.PersonaName))
}
