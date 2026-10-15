package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// resolveProposal aceita o id completo ou um prefixo único (como mostrado em `memory proposals`).
func (e *env) resolveProposal(ctx context.Context, company, ref string) (string, error) {
	if len(ref) >= 36 {
		return ref, nil
	}
	ps, err := e.client().MemoryProposals(ctx, company, "", 200)
	if err != nil {
		return "", err
	}
	var found []string
	for _, p := range ps {
		if strings.HasPrefix(p.ID, ref) {
			found = append(found, p.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf(e.t("proposta %q não encontrada"), ref)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf(e.t("prefixo %q é ambíguo: use mais caracteres do id"), ref)
}

func (e *env) printMemoryStatus(st api.MemoryStatusOut) {
	set := st.Settings
	cur := dash(set.CuratorName)
	if set.CuratorSlug != "" {
		cur = fmt.Sprintf("%s (%s)", set.CuratorName, set.CuratorSlug)
	}
	state := e.t("desligada")
	if set.Enabled {
		state = e.t("ligada")
	}
	e.kv([2]string{e.t("Provedor de memória"), st.Provider}, [2]string{e.t("Consolidação"), state},
		[2]string{e.t("Intervalo"), fmt.Sprintf("%dh", set.IntervalHours)}, [2]string{e.t("Curador"), cur},
		[2]string{e.t("Última rodada"), e.fmtTimePtr(set.LastFiredAt)}, [2]string{e.t("Próxima rodada"), e.fmtTimePtr(set.NextFireAt)},
		[2]string{e.t("Workspaces"), fmt.Sprint(st.Workspaces)}, [2]string{e.t("Propostas pendentes"), fmt.Sprint(st.PendingProposals)})
	if !st.Available {
		fmt.Fprintln(e.out, "\n"+e.t("A memória está desligada (HANKO_MEMORY_PROVIDER=none): não há o que consolidar. Ligue o ai-memory para usar a consolidação."))
	}
}

func (e *env) memoryCmd() *cobra.Command {
	c := &cobra.Command{Use: "memory", Short: e.t("Consolidação de memória governada (curadoria, propostas e permissões)")}

	status := &cobra.Command{
		Use: "status", Short: e.t("Mostra a configuração e o estado da consolidação"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			st, err := e.client().MemoryStatus(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(st)
			}
			e.printMemoryStatus(st)
			return nil
		},
	}

	var enable, disable, clearCurator bool
	var interval int
	var curator string
	config := &cobra.Command{
		Use: "config", Short: e.t("Liga/desliga a consolidação e define intervalo e curador"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if enable && disable {
				return fmt.Errorf("%s", e.t("use --enable ou --disable, não os dois"))
			}
			req := client.UpdateMemoryReq{Curator: curator, ClearCurator: clearCurator}
			if enable || disable {
				on := enable
				req.Enabled = &on
			}
			if cmd.Flags().Changed("interval") {
				req.IntervalHours = &interval
			}
			st, err := e.client().UpdateMemory(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(st)
			}
			e.printMemoryStatus(st)
			return nil
		},
	}
	cf := config.Flags()
	cf.BoolVar(&enable, "enable", false, e.t("liga a consolidação periódica"))
	cf.BoolVar(&disable, "disable", false, e.t("desliga a consolidação periódica"))
	cf.IntVar(&interval, "interval", 0, e.t("horas entre as rodadas (1 a 720)"))
	cf.StringVar(&curator, "curator", "", e.t("persona curadora (id ou slug); de preferência com um modelo barato"))
	cf.BoolVar(&clearCurator, "clear-curator", false, e.t("remove o curador"))

	consolidate := &cobra.Command{
		Use: "consolidate [" + e.t("workspace") + "]", Short: e.t("Roda a consolidação agora (um workspace ou todos)"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ws := ""
			if len(args) == 1 {
				ws = args[0]
			}
			cs, err := e.client().ConsolidateMemory(cmd.Context(), co, ws)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			for _, x := range cs {
				fmt.Fprintf(e.out, e.t("Consolidação aberta: workspace %s (task %s)\n"), x.WorkspaceSlug, dash(x.TaskKey))
			}
			if len(cs) > 0 {
				fmt.Fprintln(e.out, e.t("A persona curadora trabalha como em qualquer run; acompanhe com: hanko memory history"))
			}
			return nil
		},
	}

	var histLimit int
	history := &cobra.Command{
		Use: "history", Short: e.t("Histórico de consolidações"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cs, err := e.client().MemoryConsolidations(cmd.Context(), co, histLimit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma consolidação ainda."))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, x := range cs {
				cost := "-"
				if x.CostUSD != nil {
					cost = fmt.Sprintf("US$ %.4f", *x.CostUSD)
				}
				rows = append(rows, []string{e.fmtTime(x.CreatedAt), x.WorkspaceSlug, x.Origin, x.Status, dash(x.TaskKey), fmt.Sprint(x.Proposals), cost})
			}
			e.table([]string{e.t("QUANDO"), e.t("WORKSPACE"), e.t("ORIGEM"), e.t("STATUS"), e.t("TASK"), e.t("PROPOSTAS"), e.t("CUSTO")}, rows)
			return nil
		},
	}
	history.Flags().IntVar(&histLimit, "limit", 30, e.t("máximo de linhas"))

	var propStatus string
	proposals := &cobra.Command{
		Use: "proposals [" + e.t("id") + "]", Aliases: []string{"proposal"},
		Short: e.t("Lista as propostas de curadoria (ou mostra uma, com o diff)"), Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				id, err := e.resolveProposal(cmd.Context(), co, args[0])
				if err != nil {
					return err
				}
				p, err := e.client().MemoryProposal(cmd.Context(), co, id)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				e.printProposal(p)
				return nil
			}
			ps, err := e.client().MemoryProposals(cmd.Context(), co, propStatus, 100)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			if len(ps) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma proposta."))
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				target := "-"
				if len(p.Targets) > 0 {
					target = p.Targets[0]
					if len(p.Targets) > 1 {
						target += fmt.Sprintf(" (+%d)", len(p.Targets)-1)
					}
				}
				rows = append(rows, []string{shortID(p.ID), p.Status, p.Kind, p.WorkspaceSlug, oneLine(target, 50), e.fmtTime(p.CreatedAt)})
			}
			e.table([]string{e.t("ID"), e.t("STATUS"), e.t("TIPO"), e.t("WORKSPACE"), e.t("ALVO"), e.t("CRIADA")}, rows)
			return nil
		},
	}
	proposals.Flags().StringVar(&propStatus, "status", "", e.t("filtra por estado: pending,approved,rejected,applied,failed"))

	var note string
	decide := func(use, short, action string) *cobra.Command {
		cmd := &cobra.Command{
			Use: use + " " + e.t("<id>"), Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				id, err := e.resolveProposal(cmd.Context(), co, args[0])
				if err != nil {
					return err
				}
				p, err := e.client().DecideMemoryProposal(cmd.Context(), co, id, action, note)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				fmt.Fprintf(e.out, e.t("Proposta %s: %s\n"), shortID(p.ID), p.Status)
				if p.Status == "failed" {
					fmt.Fprintf(e.out, e.t("A aplicação falhou (%s); nova tentativa: %s\n"), dash(p.LastError), e.fmtTimePtr(p.NextAttemptAt))
				}
				return nil
			},
		}
		if action != "retry" {
			cmd.Flags().StringVarP(&note, "note", "m", "", e.t("comentário da decisão (vai para a trilha de auditoria)"))
		}
		return cmd
	}
	approve := decide("approve", e.t("Aprova uma proposta: o Hanko aplica a escrita no ai-memory"), "approve")
	reject := decide("reject", e.t("Rejeita uma proposta: a escrita é descartada"), "reject")
	retry := decide("retry", e.t("Recoloca na fila a aplicação de uma proposta que falhou"), "retry")

	grants := &cobra.Command{
		Use: "grants", Short: e.t("Lista as permissões de memória por workspace"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			gs, err := e.client().MemoryGrants(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(gs)
			}
			if len(gs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma concessão: cada persona alcança só a memória do workspace da própria run."))
				return nil
			}
			rows := make([][]string, 0, len(gs))
			for _, g := range gs {
				rows = append(rows, []string{g.PersonaName, g.WorkspaceSlug, g.Access})
			}
			e.table([]string{e.t("PERSONA"), e.t("WORKSPACE"), e.t("ACESSO")}, rows)
			return nil
		},
	}

	var access string
	grant := &cobra.Command{
		Use: "grant " + e.t("<persona> <workspace>"), Short: e.t("Libera uma persona a ler (ou escrever) na memória de outro workspace"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			g, err := e.client().GrantMemory(cmd.Context(), co, args[0], args[1], access)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("%s agora tem acesso %s à memória de %s.\n"), g.PersonaName, g.Access, g.WorkspaceSlug)
			return nil
		},
	}
	grant.Flags().StringVar(&access, "access", "read", e.t("read ou write"))

	revoke := &cobra.Command{
		Use: "revoke " + e.t("<persona> <workspace>"), Short: e.t("Retira a permissão de memória de uma persona sobre um workspace"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().RevokeMemory(cmd.Context(), co, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Permissão de %s sobre %s retirada.\n"), args[0], args[1])
			return nil
		},
	}

	c.AddCommand(status, config, consolidate, history, proposals, approve, reject, retry, grants, grant, revoke)
	return c
}

func (e *env) printProposal(p api.MemoryProposalOut) {
	e.kv([2]string{e.t("Proposta"), p.ID}, [2]string{e.t("Estado"), p.Status}, [2]string{e.t("Tipo"), p.Kind},
		[2]string{e.t("Workspace"), p.WorkspaceSlug}, [2]string{e.t("Alvos"), dash(strings.Join(p.Targets, ", "))},
		[2]string{e.t("Tool"), p.Tool}, [2]string{e.t("Persona"), dash(p.PersonaName)}, [2]string{"SHA-256", p.SHA256},
		[2]string{e.t("Tentativas"), fmt.Sprint(p.Attempts)}, [2]string{e.t("Último erro"), dash(p.LastError)},
		[2]string{e.t("Próxima tentativa"), e.fmtTimePtr(p.NextAttemptAt)}, [2]string{e.t("Criada"), e.fmtTime(p.CreatedAt)})
	switch {
	case strings.TrimSpace(p.Diff) != "":
		fmt.Fprintf(e.out, "\n%s\n%s", e.t("Diff:"), p.Diff)
	case strings.TrimSpace(p.Proposed) != "":
		fmt.Fprintf(e.out, "\n%s\n%s\n", e.t("Conteúdo proposto:"), p.Proposed)
	}
}
