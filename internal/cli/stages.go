package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
)

// Pipelines sobre tasks (ADR-0048): `hanko stage` configura as etapas de um projeto e leva as tasks por elas.

// parseStageSpec lê "chave:nome:status[:persona[:gate]]" (o nome não pode ter ":").
func parseStageSpec(s string) (api.ProjectStageSpec, error) {
	p := strings.Split(s, ":")
	if len(p) < 3 || len(p) > 5 {
		return api.ProjectStageSpec{}, fmt.Errorf("etapa %q: use chave:nome:status[:persona[:gate]]", s)
	}
	sp := api.ProjectStageSpec{Key: strings.TrimSpace(p[0]), Name: strings.TrimSpace(p[1]), Status: strings.TrimSpace(p[2])}
	if len(p) > 3 {
		sp.Persona = strings.TrimSpace(p[3])
	}
	if len(p) > 4 {
		sp.ExitGate = strings.TrimSpace(p[4])
	}
	return sp, nil
}

func (e *env) printStages(v api.ProjectStages) {
	rows := make([][]string, 0, len(v.Stages))
	for _, s := range v.Stages {
		rows = append(rows, []string{strconv.Itoa(s.Position), s.Key, s.Name, s.Status, dash(s.PersonaSlug), s.ExitGate, strconv.Itoa(s.Tasks)})
	}
	e.table([]string{"#", e.t("CHAVE"), e.t("NOME"), "STATUS", "PERSONA", e.t("GATE"), "TASKS"}, rows)
}

func (e *env) printTaskStage(x api.TaskStage) {
	e.kv([2]string{e.t("Task"), x.TaskID}, [2]string{e.t("Etapa"), fmt.Sprintf("%s (%d/%d)", x.Stage.Name, x.Position, x.Total)},
		[2]string{e.t("Situação"), x.State}, [2]string{"Status", x.Stage.Status}, [2]string{e.t("Persona"), dash(x.Stage.PersonaSlug)},
		[2]string{e.t("Gate de saída"), x.Stage.ExitGate}, [2]string{e.t("Próxima"), dash(x.NextStage)})
	if len(x.Learnings) > 0 {
		fmt.Fprintln(e.out, "\n"+e.t("Aprendizados desta etapa:"))
		for _, l := range x.Learnings {
			fmt.Fprintf(e.out, "- %s %s: %s\n", e.fmtTime(l.CreatedAt), l.Decision, oneLine(l.Note, 120))
		}
	}
}

func (e *env) stageCmd() *cobra.Command {
	c := &cobra.Command{Use: "stage", Aliases: []string{"stages"}, Short: e.t("Pipeline do projeto: etapas que levam a task de persona em persona")}

	ls := &cobra.Command{
		Use: "list " + e.t("<projeto>"), Aliases: []string{"ls"}, Short: e.t("Mostra as etapas do projeto"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().ProjectStages(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			e.printStages(v)
			return nil
		},
	}

	var specs []string
	var file string
	set := &cobra.Command{
		Use: "set " + e.t("<projeto>"), Short: e.t("Define o pipeline do projeto (admin): --stage chave:nome:status[:persona[:gate]], na ordem, ou --file"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var stages []api.ProjectStageSpec
			if file != "" {
				raw, err := os.ReadFile(file) //nolint:gosec // o arquivo é escolha de quem roda a CLI
				if err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &stages); err != nil {
					return fmt.Errorf(e.t("arquivo de etapas inválido (esperado uma lista JSON): %w"), err)
				}
			}
			for _, s := range specs {
				sp, err := parseStageSpec(s)
				if err != nil {
					return usageError{err}
				}
				stages = append(stages, sp)
			}
			v, err := e.client().SetProjectStages(cmd.Context(), co, args[0], stages)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			e.printStages(v)
			return nil
		},
	}
	set.Flags().StringArrayVar(&specs, "stage", nil, e.t("etapa chave:nome:status[:persona[:gate]]; repita na ordem do pipeline (gate: none, approval ou review)"))
	set.Flags().StringVar(&file, "file", "", e.t("lista JSON de etapas (chave, nome, status, persona, exit_gate, instructions)"))

	clear := &cobra.Command{
		Use: "clear " + e.t("<projeto>"), Short: e.t("Desliga o pipeline do projeto (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().ClearProjectStages(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Pipeline desligado."))
			return nil
		},
	}

	var lStage string
	learnings := &cobra.Command{
		Use: "learnings " + e.t("<projeto>"), Short: e.t("Aprendizados das etapas: decisões de gate com nota"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ls, err := e.client().StageLearnings(cmd.Context(), co, args[0], lStage, 50)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ls)
			}
			rows := make([][]string, 0, len(ls))
			for _, l := range ls {
				rows = append(rows, []string{e.fmtTime(l.CreatedAt), l.Stage, dash(l.TaskKey), l.Decision, oneLine(l.Note, 90)})
			}
			e.table([]string{e.t("QUANDO"), e.t("ETAPA"), "TASK", e.t("DECISÃO"), e.t("NOTA")}, rows)
			return nil
		},
	}
	learnings.Flags().StringVar(&lStage, "stage", "", e.t("só desta etapa (chave)"))

	show := &cobra.Command{
		Use: "show " + e.t("<task>"), Short: e.t("Mostra em que etapa a task está, o histórico e os aprendizados"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			x, err := e.client().TaskStage(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(x)
			}
			e.printTaskStage(x)
			return nil
		},
	}

	var sStage string
	start := &cobra.Command{
		Use: "start " + e.t("<task>"), Short: e.t("Põe a task no pipeline do projeto dela"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			x, err := e.client().StartTaskStage(cmd.Context(), co, args[0], sStage)
			if err != nil {
				return err
			}
			return e.stageDone(x, e.t("Task no pipeline."))
		},
	}
	start.Flags().StringVar(&sStage, "stage", "", e.t("etapa de entrada (padrão: a primeira)"))

	var cNote string
	complete := &cobra.Command{
		Use: "complete " + e.t("<task>"), Short: e.t("Conclui a etapa: a task avança ou espera o gate da etapa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			x, err := e.client().CompleteTaskStage(cmd.Context(), co, args[0], cNote)
			if err != nil {
				return err
			}
			return e.stageDone(x, e.t("Etapa concluída."))
		},
	}
	complete.Flags().StringVar(&cNote, "note", "", e.t("o que foi entregue"))

	var aTo, aNote string
	advance := &cobra.Command{
		Use: "advance " + e.t("<task>"), Short: e.t("Move a task à mão (por cima dos gates) para a etapa seguinte ou para --to"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			x, err := e.client().AdvanceTaskStage(cmd.Context(), co, args[0], aTo, aNote)
			if err != nil {
				return err
			}
			return e.stageDone(x, e.t("Task movida."))
		},
	}
	advance.Flags().StringVar(&aTo, "to", "", e.t("etapa de destino (para a frente ou para trás)"))
	advance.Flags().StringVar(&aNote, "note", "", e.t("motivo"))

	var lNote string
	leave := &cobra.Command{
		Use: "leave " + e.t("<task>"), Short: e.t("Tira a task do pipeline"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().LeaveTaskStage(cmd.Context(), co, args[0], lNote); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Task fora do pipeline."))
			return nil
		},
	}
	leave.Flags().StringVar(&lNote, "note", "", e.t("motivo"))

	c.AddCommand(ls, set, clear, learnings, show, start, complete, advance, leave)
	return c
}

func (e *env) stageDone(x api.TaskStage, msg string) error {
	if e.asJSON {
		return e.printJSON(x)
	}
	fmt.Fprintln(e.out, msg)
	e.printTaskStage(x)
	return nil
}
