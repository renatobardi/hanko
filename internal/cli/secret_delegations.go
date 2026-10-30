package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Delegação de segredo pessoal (ADR-0068 §6, #117 fatia 2): o dono concede o uso do segredo a uma task, conversa ou run
// que ele mesmo pediu. Prazo padrão 24 h, máximo 7 dias. Só o dono cria, lista e revoga; a CLI nunca mostra o valor.

// maxDelegationTTL é o prazo máximo de uma delegação (domain.MaxDelegationTTL); a CLI recusa antes de chamar a API.
const maxDelegationTTL = 7 * 24 * time.Hour

func (e *env) secretDelegationCmd() *cobra.Command {
	c := &cobra.Command{Use: "delegation", Aliases: []string{"delegations"},
		Short: e.t("Delegações de segredo pessoal a uma task, conversa ou run sua (só você; nunca mostra valor)")}

	var targetType, target string
	var expires time.Duration
	create := &cobra.Command{
		Use: "create " + e.t("<segredo>"), Short: e.t("Delega um segredo pessoal ao trabalho que você pediu (padrão 24 h, máximo 7 dias)"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if expires < 0 || expires > maxDelegationTTL {
				return usageError{errors.New(e.t("prazo inválido: de 0 a 7 dias (ex.: 24h, 168h)"))}
			}
			d, err := e.client().DelegateSecret(cmd.Context(), co, a[0], delegationReq(targetType, target, expires))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			fmt.Fprintf(e.out, e.t("Delegação criada: %s para %s %s (até %s)\n"), d.Secret, d.TargetType, d.TargetID, e.fmtTime(d.ExpiresAt))
			return nil
		}),
	}
	cf := create.Flags()
	cf.StringVar(&targetType, "target-type", "", e.t("task, conversation ou run"))
	cf.StringVar(&target, "target", "", e.t("id da task, da conversa ou da run (run só se estiver em curso)"))
	cf.DurationVar(&expires, "expires", 0, e.t("prazo, como 24h ou 168h (padrão 24h, máximo 168h)"))
	_ = create.MarkFlagRequired("target-type")
	_ = create.MarkFlagRequired("target")

	ls := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: e.t("Lista as suas delegações (segredo, alvo, prazo e estado)"), Args: cobra.NoArgs,
		RunE: e.inCompany(func(cmd *cobra.Command, co string, _ []string) error {
			l, err := e.client().ListMyDelegations(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma delegação."))
				return nil
			}
			rows := make([][]string, len(l))
			for i, d := range l {
				rows[i] = []string{d.ID, d.Secret, d.TargetType + " " + d.TargetID, e.fmtTime(d.ExpiresAt), delegationState(e, d)}
			}
			e.table([]string{e.t("ID"), e.t("SEGREDO"), e.t("ALVO"), e.t("EXPIRA EM"), e.t("ESTADO")}, rows)
			return nil
		}),
	}

	revoke := &cobra.Command{
		Use: "revoke " + e.t("<id>"), Short: e.t("Revoga uma delegação sua; a run deixa de resolver o segredo"), Args: cobra.ExactArgs(1),
		RunE: e.inCompany(func(cmd *cobra.Command, co string, a []string) error {
			if err := e.client().RevokeDelegation(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Delegação %s revogada.\n"), a[0])
			return nil
		}),
	}

	c.AddCommand(create, ls, revoke)
	return c
}

// delegationReq monta o pedido da API; prazo zero deixa o padrão de 24 h para o servidor.
func delegationReq(targetType, target string, expires time.Duration) client.CreateDelegationReq {
	return client.CreateDelegationReq{TargetType: targetType, TargetID: target, ExpiresInSeconds: int(expires / time.Second)}
}

// delegationState diz em uma palavra se a delegação vale agora: revogada, ativa ou inativa (vencida ou trabalho fechado).
func delegationState(e *env, d api.SecretDelegation) string {
	switch {
	case d.RevokedAt != nil:
		return e.t("revogada")
	case d.Active:
		return e.t("ativa")
	default:
		return e.t("inativa")
	}
}
