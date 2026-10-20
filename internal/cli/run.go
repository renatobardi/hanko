package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/domain"
)

func (e *env) runHeader() []string {
	return []string{e.t("NOME"), e.t("ID"), e.t("TASK"), e.t("PERSONA"), e.t("STATUS"), e.t("GATILHO"), e.t("INÍCIO"), e.t("CUSTO")}
}

func (e *env) runRows(rs []api.Run) [][]string {
	rows := make([][]string, 0, len(rs))
	for _, r := range rs {
		rows = append(rows, []string{dash(r.Name), r.ID, dash(r.TaskKey), dash(r.PersonaName), r.Status, r.Trigger, e.fmtTimePtr(r.StartedAt), e.cost(r.CostUSD)})
	}
	return rows
}

func (e *env) modelSourceLabel(s string) string {
	switch s {
	case "task":
		return e.t("escolha manual na task")
	case "persona":
		return e.t("modelo da persona")
	case "harness_default":
		return e.t("padrão do harness")
	}
	return "-"
}

func (e *env) costSourceLabel(s string) string {
	switch s {
	case "reported":
		return e.t("real, informado pelo harness")
	case "estimated":
		return e.t("estimado pela tabela de preços")
	}
	return e.t("desconhecido")
}

func (e *env) cost(c *float64) string {
	if c == nil {
		return "-"
	}
	return e.money("%.4f", *c)
}

func (e *env) runCmd() *cobra.Command {
	c := &cobra.Command{Use: "run", Short: e.t("Acompanha execuções (runs)")}

	var q client.RunQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista execuções"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rs, err := e.client().Runs(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(rs)
			}
			if len(rs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma execução encontrada."))
				return nil
			}
			e.table(e.runHeader(), e.runRows(rs))
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Task, "task", "", e.t("filtra por task (id ou chave)"))
	lf.StringVarP(&q.Persona, "persona", "p", "", e.t("filtra por persona (id ou slug)"))
	lf.StringVar(&q.Status, "status", "", e.t("status separados por vírgula (ex.: running,failed)"))
	lf.IntVar(&q.Limit, "limit", 50, e.t("máximo de itens"))
	lf.IntVar(&q.Offset, "offset", 0, e.t("itens a pular"))

	show := &cobra.Command{
		Use: "show " + e.t("<run>"), Short: e.t("Mostra uma execução (id ou nome, ex.: Veloz_Raposa)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveRun(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			r, err := e.client().Run(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			e.kv([2]string{e.t("Execução"), runTitle(r)}, [2]string{e.t("Task"), fmt.Sprintf("%s %s", dash(r.TaskKey), r.TaskTitle)},
				[2]string{e.t("Persona"), dash(r.PersonaName)}, [2]string{e.t("Status"), r.Status}, [2]string{e.t("Gatilho"), r.Trigger},
				[2]string{e.t("Agente"), r.Agent + modelSuffix(r.Model)}, [2]string{e.t("Criada em"), e.fmtTime(r.CreatedAt)},
				[2]string{e.t("Início"), e.fmtTimePtr(r.StartedAt)}, [2]string{e.t("Fim"), e.fmtTimePtr(r.FinishedAt)},
				[2]string{e.t("Origem do modelo"), e.modelSourceLabel(r.ModelSource)},
				[2]string{e.t("Custo"), e.cost(r.CostUSD) + " (" + e.costSourceLabel(r.CostSource) + ")"}, [2]string{e.t("Custo de lista"), e.cost(r.ListCostUSD)},
				[2]string{e.t("Assinatura"), dash(r.SubscriptionID)},
				[2]string{e.t("Tokens"), fmt.Sprintf("entrada %d · saída %d · cache %d/%d", r.Usage.InputTokens, r.Usage.OutputTokens,
					r.Usage.CacheReadTokens, r.Usage.CacheCreationTokens)})
			if r.Error != "" {
				fmt.Fprintf(e.out, e.t("\nErro:\n%s\n"), strings.TrimSpace(r.Error))
			}
			if r.Summary != "" {
				fmt.Fprintf(e.out, e.t("\nResumo:\n%s\n"), strings.TrimSpace(r.Summary))
			}
			e.printRunResilience(r)
			return nil
		},
	}

	var follow bool
	var after int64
	logs := &cobra.Command{
		Use: "logs " + e.t("<run>"), Short: e.t("Mostra o log de uma execução, por id ou nome (--follow acompanha ao vivo)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveRun(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if follow {
				return e.followRun(cmd.Context(), co, id, after)
			}
			es, err := e.client().AllRunEvents(cmd.Context(), co, id, after)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(es)
			}
			if len(es) == 0 {
				fmt.Fprintln(e.out, e.t("Sem eventos."))
			}
			for _, ev := range es {
				e.printEvent(ev)
			}
			return nil
		},
	}
	logs.Flags().BoolVarP(&follow, "follow", "f", false, e.t("acompanha ao vivo (SSE) até a execução terminar"))
	logs.Flags().Int64Var(&after, "after", 0, e.t("só eventos com seq maior que este valor"))

	cancel := &cobra.Command{
		Use: "cancel " + e.t("<run>"), Short: e.t("Cancela uma execução (id ou nome)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveRun(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			r, err := e.client().CancelRun(cmd.Context(), co, id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("Execução %s: %s\n"), runLabel(r), r.Status)
			return nil
		},
	}
	c.AddCommand(ls, show, logs, cancel)
	c.AddCommand(e.runResilienceCmds()...)
	return c
}

// runTitle mostra o nome amigável ao lado do id.
func runTitle(r api.Run) string {
	if r.Name == "" {
		return r.ID
	}
	return r.Name + " (" + r.ID + ")"
}

// runLabel é o nome amigável da execução, ou o id curto se ela não tem nome.
func runLabel(r api.Run) string {
	if r.Name != "" {
		return r.Name
	}
	return short(r.ID)
}

// resolveRun aceita o id da execução ou o nome amigável (Veloz_Raposa; sem diferenciar caixa nem
// acento) e devolve o id. Texto que não tem a forma de um nome vai direto para a API como id.
func (e *env) resolveRun(ctx context.Context, company, ref string) (string, error) {
	name, ok := domain.CanonicalRunName(ref)
	if !ok {
		return ref, nil
	}
	rs, err := e.client().Runs(ctx, company, client.RunQuery{Name: name, Limit: 2})
	if err != nil {
		return "", err
	}
	switch len(rs) {
	case 0:
		return "", fmt.Errorf(e.t("execução %q não encontrada nesta empresa"), name)
	case 1:
		return rs[0].ID, nil
	}
	return "", fmt.Errorf(e.t("mais de uma execução com o nome %q; use o id"), name)
}

func modelSuffix(m string) string {
	if m == "" {
		return ""
	}
	return " (" + m + ")"
}

// printEvent escreve uma linha de log; textos multilinha são indentados.
func (e *env) printEvent(ev api.RunEvent) {
	ts := ev.OccurredAt.Local().Format("15:04:05")
	text := strings.TrimRight(ev.Text, "\n")
	lines := strings.Split(text, "\n")
	fmt.Fprintf(e.out, "%s  %-11s %s\n", ts, ev.Kind, lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(e.out, "%*s%s\n", 8+2+11+1, "", l)
	}
}

// followRun consome o SSE, reconectando a partir do último seq se a conexão cair.
func (e *env) followRun(ctx context.Context, company, run string, after int64) error {
	cl := e.client()
	last := after
	var final *api.Run
	handle := func(f client.Frame) error {
		switch f.Event {
		case "run.event":
			var ev api.RunEvent
			if err := json.Unmarshal([]byte(f.Data), &ev); err != nil {
				return fmt.Errorf(e.t("evento inválido do servidor: %w"), err)
			}
			last = max(last, ev.Seq)
			if e.asJSON {
				_, err := fmt.Fprintln(e.out, f.Data)
				return err
			}
			e.printEvent(ev)
		case "run.status":
			var r api.Run
			if err := json.Unmarshal([]byte(f.Data), &r); err != nil {
				return fmt.Errorf(e.t("status inválido do servidor: %w"), err)
			}
			final = &r
			if e.asJSON {
				_, err := fmt.Fprintln(e.out, f.Data)
				return err
			}
			fmt.Fprintf(e.out, e.t("-- execução %s: %s\n"), runLabel(r), r.Status)
		}
		return nil
	}
	attempts := 0
	for {
		before := last
		err := cl.Stream(ctx, company, run, last, handle)
		switch {
		case err == nil:
			if final != nil && (final.Status == string(domain.RunFailed) || final.Status == string(domain.RunExpired) || final.Status == string(domain.RunInterrupted)) {
				return fmt.Errorf(e.t("a execução terminou com status %s"), final.Status)
			}
			return nil
		case errors.Is(err, client.ErrStreamEnded):
			if last > before {
				attempts = 0
			}
			attempts++
			if attempts > e.followRetry || ctx.Err() != nil {
				return errors.New(e.tf("conexão com o servidor encerrada antes do fim da execução; retome com: hanko run logs --follow --after %s %s", strconv.FormatInt(last, 10), run))
			}
			fmt.Fprintf(e.errOut, e.t("conexão perdida; reconectando (%d/%d)…\n"), attempts, e.followRetry)
			e.sleep(time.Duration(attempts) * e.followWait)
		default:
			return err
		}
	}
}
