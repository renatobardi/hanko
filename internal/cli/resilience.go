package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
)

// Resiliência de run (ADR-0039): hanko run retry|continue|close|steer e hanko followup.

func (e *env) causeLabel(c string) string {
	switch c {
	case "process_restart":
		return e.t("o processo do Hanko caiu")
	case "server_shutdown":
		return e.t("o Hanko foi encerrado")
	case "agent_error":
		return e.t("erro do agente")
	case "timeout":
		return e.t("tempo limite")
	case "cancelled":
		return e.t("cancelada")
	}
	return c
}

func (e *env) nextActionLabel(a string) string {
	switch a {
	case "auto_continue":
		return e.t("continuação automática")
	case "human_decision":
		return e.t("espera a sua decisão: Tentar de novo, Continuar ou Encerrar")
	case "continued":
		return e.t("já continuada")
	}
	return e.t("nenhuma")
}

func (e *env) steerModeLabel(m string) string {
	switch m {
	case "inject":
		return e.t("injeta ao vivo")
	case "restart":
		return e.t("interrompe e continua")
	}
	return e.t("não aceita")
}

func (e *env) waitReasonLabel(f api.Followup) string {
	var s string
	switch f.WaitReason {
	case "run_active":
		s = e.t("espera a execução terminar")
	case "steer_pending":
		s = e.t("será injetado na execução ativa")
	case "restart_pending":
		s = e.t("a execução será interrompida e continuada")
	case "decision_pending":
		s = e.t("espera a sua decisão sobre uma execução parada")
	case "persona_unavailable":
		s = e.t("a persona não pode trabalhar agora")
	case "unassigned":
		s = e.t("a task não tem persona")
	default:
		s = e.t("a próxima execução está abrindo")
	}
	if f.WaitDetail != "" {
		s += " (" + f.WaitDetail + ")"
	}
	return s
}

// printRunResilience mostra, em `run show`, a ligação com a cadeia de continuações e o card de diagnóstico.
func (e *env) printRunResilience(r api.Run) {
	if r.ContinuationOfRunID != "" {
		fmt.Fprintf(e.out, e.t("\nContinuação de: %s (%s, tentativa %d)\n"), r.ContinuationOfRunID, r.ContinuationReason, r.ContinuationAttempt)
	}
	if r.ContinuedByRunID != "" {
		fmt.Fprintf(e.out, e.t("Continuada por: %s\n"), r.ContinuedByRunID)
	}
	if r.SteerMode != "" && (r.Status == "queued" || r.Status == "running") {
		fmt.Fprintf(e.out, e.t("Steer: %s\n"), e.steerModeLabel(r.SteerMode))
	}
	d := r.Diagnosis
	if d == nil {
		return
	}
	fmt.Fprintln(e.out, e.t("\nDiagnóstico:"))
	cause := e.causeLabel(d.Cause)
	if d.CauseDetail != "" {
		cause += " — " + d.CauseDetail
	}
	e.kv([2]string{e.t("  Causa"), cause},
		[2]string{e.t("  Tentativa"), e.tf("continuação %d de %d", d.Attempt, d.MaxAttempts)},
		[2]string{e.t("  Próxima ação"), e.nextActionLabel(d.NextAction)},
		[2]string{e.t("  Dono"), map[string]string{"system": e.t("sistema"), "human": e.t("operador")}[d.Owner]})
	if d.Uncertain != nil {
		fmt.Fprintf(e.out, e.t("  Desfecho incerto: a tool %s estava em andamento (%s); verifique o efeito antes de continuar.\n"), d.Uncertain.Name, d.Uncertain.Input)
	}
	if r.Decision != "" {
		fmt.Fprintf(e.out, e.t("  Decisão: %s %s\n"), r.Decision, r.DecisionNote)
	}
}

// runResilienceCmds são os subcomandos de `hanko run` do ADR-0039.
func (e *env) runResilienceCmds() []*cobra.Command {
	var out []*cobra.Command
	for _, a := range []struct{ name, action, short string }{
		{"retry", "retry", e.t("Tentar de novo: abre uma execução nova do zero para a execução que parou")},
		{"continue", "continue", e.t("Continuar: retoma a sessão da execução que parou, com o briefing do que já foi feito")},
		{"close", "close", e.t("Encerrar: fecha o card da execução que parou, sem abrir outra (a task em andamento vai para blocked)")},
	} {
		var note string
		c := &cobra.Command{
			Use: a.name + " " + e.t("<run>"), Short: a.short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				id, err := e.resolveRun(cmd.Context(), co, args[0])
				if err != nil {
					return err
				}
				r, err := e.client().RunAction(cmd.Context(), co, id, a.action, note)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(r)
				}
				fmt.Fprintf(e.out, e.t("Execução %s: decisão registrada (%s).\n"), runLabel(r), r.Decision)
				return nil
			},
		}
		c.Flags().StringVar(&note, "note", "", e.t("nota para a execução seguinte (entra no briefing)"))
		out = append(out, c)
	}

	steer := &cobra.Command{
		Use: "steer " + e.t("<run> <mensagem>"), Short: e.t("Muda o rumo de uma execução ativa (injeta a mensagem ou interrompe e continua, conforme o harness)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			msg := strings.TrimSpace(args[1])
			if msg == "" {
				return usageError{errors.New(e.t("a mensagem está vazia"))}
			}
			id, err := e.resolveRun(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			res, err := e.client().SteerRun(cmd.Context(), co, id, msg)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			fmt.Fprintf(e.out, e.t("Mensagem enviada (%s). Follow-up %s.\n"), e.steerModeLabel(res.Mode), res.Followup.ID)
			return nil
		},
	}
	return append(out, steer)
}

func (e *env) followupRows(fs []api.Followup) [][]string {
	rows := make([][]string, 0, len(fs))
	for _, f := range fs {
		body := strings.Join(strings.Fields(f.Body), " ")
		if len([]rune(body)) > 60 {
			body = string([]rune(body)[:59]) + "…"
		}
		rows = append(rows, []string{fmt.Sprint(f.Position), f.ID, f.Kind, body, e.waitReasonLabel(f)})
	}
	return rows
}

func (e *env) followupCmd() *cobra.Command {
	c := &cobra.Command{Use: "followup", Short: e.t("Fila de follow-ups: o que você mandou enquanto a execução trabalha")}

	var task, conv string
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista a fila (--task ou --conversation): ordem e motivo da espera"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var fs []api.Followup
			switch {
			case task != "" && conv == "":
				fs, err = e.client().TaskFollowups(cmd.Context(), co, task)
			case conv != "" && task == "":
				id, rerr := e.resolveConversation(cmd.Context(), co, conv)
				if rerr != nil {
					return rerr
				}
				fs, err = e.client().ConversationFollowups(cmd.Context(), co, id)
			default:
				return usageError{errors.New(e.t("informe --task ou --conversation"))}
			}
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(fs)
			}
			if len(fs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum follow-up na fila."))
				return nil
			}
			e.table([]string{"#", e.t("ID"), e.t("TIPO"), e.t("TEXTO"), e.t("ESPERA")}, e.followupRows(fs))
			return nil
		},
	}
	ls.Flags().StringVar(&task, "task", "", e.t("task (id ou chave)"))
	ls.Flags().StringVar(&conv, "conversation", "", e.t("conversa (id ou prefixo)"))

	var addTask string
	add := &cobra.Command{
		Use: "add " + e.t("<mensagem>"), Short: e.t("Enfileira uma instrução para a task: a execução seguinte a recebe, na ordem"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if addTask == "" {
				return usageError{errors.New(e.t("informe --task"))}
			}
			f, err := e.client().QueueTaskFollowup(cmd.Context(), co, addTask, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Follow-up %s na fila (posição %d): %s\n"), f.ID, f.Position, e.waitReasonLabel(f))
			return nil
		},
	}
	add.Flags().StringVar(&addTask, "task", "", e.t("task (id ou chave)"))

	edit := &cobra.Command{
		Use: "edit " + e.t("<id> <mensagem>"), Short: e.t("Edita um follow-up que ainda não foi entregue"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f, err := e.client().EditFollowup(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(f)
			}
			fmt.Fprintf(e.out, e.t("Follow-up %s editado.\n"), f.ID)
			return nil
		},
	}

	discard := &cobra.Command{
		Use: "discard " + e.t("<id>"), Short: e.t("Descarta um follow-up que ainda não foi entregue"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DiscardFollowup(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]string{"discarded": args[0]})
			}
			fmt.Fprintf(e.out, e.t("Follow-up %s descartado.\n"), args[0])
			return nil
		},
	}
	c.AddCommand(ls, add, edit, discard)
	return c
}
