package cli

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/domain"
)

func (e *env) checkRole(role string) error {
	if !domain.MemberRole(role).Valid() {
		return usageError{fmt.Errorf(e.t("papel inválido %q: use owner, admin, member ou viewer"), role)}
	}
	return nil
}

func (e *env) memberCmd() *cobra.Command {
	c := &cobra.Command{Use: "member", Aliases: []string{"members"}, Short: e.t("Membros da empresa: papéis e convites")}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os membros (e, para admin/owner, os convites abertos)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cl := e.client()
			ms, err := cl.Members(cmd.Context(), co)
			if err != nil {
				return err
			}
			invs, ierr := cl.Invites(cmd.Context(), co) // 403 para quem não é admin: só a lista de membros
			var ce *client.Error
			if ierr != nil {
				if !errors.As(ierr, &ce) || ce.Status != http.StatusForbidden {
					return ierr
				}
				invs = nil
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"members": ms, "invites": invs})
			}
			rows := make([][]string, len(ms))
			for i, m := range ms {
				rows[i] = []string{m.Email, dash(m.Name), m.Role, e.fmtTime(m.CreatedAt)}
			}
			e.table([]string{e.t("E-MAIL"), e.t("NOME"), e.t("PAPEL"), e.t("DESDE")}, rows)
			if len(invs) > 0 {
				fmt.Fprintln(e.out, e.t("\nConvites abertos:"))
				ir := make([][]string, len(invs))
				for i, in := range invs {
					state := e.fmtTime(in.ExpiresAt)
					if in.Expired {
						state = "expirado"
					}
					ir[i] = []string{short(in.ID), in.Email, in.Role, state}
				}
				e.table([]string{e.t("ID"), e.t("E-MAIL"), e.t("PAPEL"), e.t("VALIDADE")}, ir)
			}
			return nil
		},
	}

	var role string
	invite := &cobra.Command{
		Use: "invite " + e.t("<e-mail>"), Short: e.t("Convida alguém e imprime o link (o Hanko não envia e-mail)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.checkRole(role); err != nil {
				return err
			}
			inv, err := e.client().InviteMember(cmd.Context(), co, args[0], role)
			if err != nil {
				return err
			}
			link := e.client().BaseURL + inv.Path
			if e.asJSON {
				return e.printJSON(map[string]any{"invite": inv, "link": link})
			}
			fmt.Fprintf(e.out, e.t("Convite criado para %s como %s (vale até %s).\n"), inv.Email, inv.Role, e.fmtTime(inv.ExpiresAt))
			fmt.Fprintf(e.out, e.t("Link (só aparece agora; troque o host pelo endereço público se for outro):\n%s\n"), link)
			return nil
		},
	}
	invite.Flags().StringVar(&role, "role", "member", e.t("papel: owner, admin, member ou viewer"))

	revoke := &cobra.Command{
		Use: "revoke " + e.t("<id-do-convite>"), Short: e.t("Revoga um convite aberto"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveInvite(cmd, co, args[0])
			if err != nil {
				return err
			}
			if err := e.client().RevokeInvite(cmd.Context(), co, id); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Convite revogado."))
			return nil
		},
	}

	setRole := &cobra.Command{
		Use: "role " + e.t("<e-mail|id>") + " " + e.t("<papel>"), Short: e.t("Troca o papel de um membro"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.checkRole(args[1]); err != nil {
				return err
			}
			m, err := e.client().SetMemberRole(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(m)
			}
			fmt.Fprintf(e.out, e.t("%s agora é %s.\n"), m.Email, m.Role)
			return nil
		},
	}

	remove := &cobra.Command{
		Use: "remove " + e.t("<e-mail|id>"), Aliases: []string{"rm"}, Short: e.t("Remove um membro da empresa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().RemoveMember(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Membro removido."))
			return nil
		},
	}

	c.AddCommand(ls, invite, revoke, setRole, remove)
	c.AddCommand(e.memberJoinCmds()...)
	return c
}

// resolveInvite aceita o id inteiro ou o prefixo de 8 caracteres mostrado em `member list`.
func (e *env) resolveInvite(cmd *cobra.Command, company, ref string) (string, error) {
	if len(ref) >= 32 {
		return ref, nil
	}
	invs, err := e.client().Invites(cmd.Context(), company)
	if err != nil {
		return "", err
	}
	var found string
	for _, in := range invs {
		if len(ref) > 0 && len(in.ID) >= len(ref) && in.ID[:len(ref)] == ref {
			if found != "" {
				return "", usageError{errors.New(e.t("prefixo ambíguo: use mais caracteres do id do convite"))}
			}
			found = in.ID
		}
	}
	if found == "" {
		return "", fmt.Errorf(e.t("convite %q não encontrado"), ref)
	}
	return found, nil
}
