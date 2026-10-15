package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/domain"
)

// AuthAdmin é o que os comandos `hanko auth` precisam: acesso direto ao banco (sem passar
// pelo servidor), de modo que o primeiro dono e a primeira API key existam antes de qualquer login.
// Implementado em internal/app sobre o service.
type AuthAdmin interface {
	CreateOwner(ctx context.Context, email, name, password string) (domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (domain.User, error)
	CreateAPIKey(ctx context.Context, userID domain.ID, name string) (string, domain.APIKey, error)
	ListAPIKeys(ctx context.Context, userID domain.ID) ([]domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, userID, keyID domain.ID) (domain.APIKey, error)
	// Administração de contas pelo dono da instalação (users.role = owner). ownerEmail identifica
	// quem opera; vazio = o único dono da instalação. O service confere o papel no banco.
	AdminListUsers(ctx context.Context, ownerEmail string) ([]domain.User, error)
	AdminResetPassword(ctx context.Context, ownerEmail, email, newPassword string) (string, error)
	AdminRemoveUser(ctx context.Context, ownerEmail, email string) error
	// AdminListAudit lista a auditoria das operações acima (admin_audit), da mais recente para a mais antiga.
	AdminListAudit(ctx context.Context, ownerEmail string, limit int) ([]domain.AdminAuditEntry, error)
	Close()
}

// ownerPasswordEnv é a variável lida para a senha do dono (alternativa à stdin).
const ownerPasswordEnv = "HANKO_OWNER_PASSWORD"

// userPasswordEnv é a variável lida para a nova senha em `auth user reset-password`; sem ela, o
// comando gera uma senha temporária e a imprime uma única vez.
const userPasswordEnv = "HANKO_USER_PASSWORD" //nolint:gosec // nome da variável de ambiente, não uma senha

func (e *env) openAuth(ctx context.Context) (AuthAdmin, error) {
	if e.rt.Auth == nil {
		return nil, e.errNotWired()
	}
	a, err := e.rt.Auth(ctx)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (e *env) authCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: e.t("Usuários e API keys (acesso direto ao banco; use no host do servidor)"),
		Long:  e.t("Comandos administrativos de autenticação. Falam direto com o banco configurado (HANKO_DATABASE_URL), sem o servidor no ar; rode `hanko migrate` antes."),
	}
	c.AddCommand(e.authCreateOwnerCmd(), e.authTokenCmd(), e.authUserCmd())
	return c
}

func (e *env) authCreateOwnerCmd() *cobra.Command {
	var email, name string
	cmd := &cobra.Command{
		Use:   "create-owner --email E --name N",
		Short: e.t("Cria o primeiro usuário (dono) com e-mail e senha (provider local)"),
		Long:  e.tf("Cria o dono da instalação. A senha vem de %s ou da primeira linha da entrada padrão\n(nunca de flag, para não ir ao histórico do shell). Exemplo:\n  read -rs HANKO_OWNER_PASSWORD && export HANKO_OWNER_PASSWORD\n  hanko auth create-owner --email voce@exemplo.com --name \"Seu Nome\"", ownerPasswordEnv),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(email) == "" || strings.TrimSpace(name) == "" {
				return usageError{errors.New(e.t("--email e --name são obrigatórios"))}
			}
			pw := e.getenv(ownerPasswordEnv)
			if pw == "" {
				line, err := bufio.NewReader(e.in).ReadString('\n')
				if err != nil && line == "" {
					return usageError{fmt.Errorf(e.t("senha não informada: defina %s ou envie pela entrada padrão"), ownerPasswordEnv)}
				}
				pw = strings.TrimRight(line, "\r\n")
			}
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			u, err := a.CreateOwner(cmd.Context(), email, name, pw)
			if err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(u)
			}
			_, err = fmt.Fprintf(e.out, e.t("Dono criado: %s <%s>\nEntre em /entrar com este e-mail e a senha informada.\n"), u.Name, u.Email)
			return err
		},
	}
	cmd.Flags().StringVar(&email, "email", "", e.t("e-mail do dono"))
	cmd.Flags().StringVar(&name, "name", "", e.t("nome do dono"))
	return cmd
}

func (e *env) friendlyAuthErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrConflict):
		return fmt.Errorf(e.t("já existe um usuário: o dono só pode ser criado em um banco sem usuários (%w)"), err)
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf(e.t("não encontrado: %w"), err)
	case errors.Is(err, domain.ErrForbidden):
		return fmt.Errorf(e.t("só o dono da instalação (users.role = owner) pode fazer isto: %w"), err)
	}
	return err
}

func (e *env) authTokenCmd() *cobra.Command {
	var email string
	c := &cobra.Command{Use: "token", Short: e.t("API keys de usuário (Authorization: Bearer hk_…)")}
	c.PersistentFlags().StringVar(&email, "email", "", e.t("e-mail do usuário dono da chave (omita se houver um único usuário)"))

	owner := func(ctx context.Context, a AuthAdmin) (domain.User, error) {
		if strings.TrimSpace(email) != "" {
			u, err := a.GetUserByEmail(ctx, email)
			if err != nil {
				return domain.User{}, e.friendlyAuthErr(err)
			}
			return u, nil
		}
		us, err := a.ListUsers(ctx)
		if err != nil {
			return domain.User{}, err
		}
		switch len(us) {
		case 0:
			return domain.User{}, errors.New(e.t("não há usuários: crie o dono com `hanko auth create-owner`"))
		case 1:
			return us[0], nil
		}
		return domain.User{}, usageError{errors.New(e.t("há mais de um usuário: informe --email"))}
	}

	create := &cobra.Command{
		Use:   "create --name N",
		Short: e.t("Cria uma API key (exibida uma única vez)"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			u, err := owner(cmd.Context(), a)
			if err != nil {
				return err
			}
			key, k, err := a.CreateAPIKey(cmd.Context(), u.ID, name)
			if err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"key": key, "id": k.ID, "name": k.Name, "prefix": k.Prefix, "user": u.Email})
			}
			fmt.Fprintf(e.errOut, e.t("Chave %q criada para %s. Guarde agora: não será exibida de novo.\n"), k.Name, u.Email)
			_, err = fmt.Fprintln(e.out, key)
			return err
		},
	}
	create.Flags().String("name", "", e.t("nome da chave (ex.: notebook, ci)"))
	_ = create.MarkFlagRequired("name")

	list := &cobra.Command{
		Use:   "list",
		Short: e.t("Lista as API keys do usuário"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			u, err := owner(cmd.Context(), a)
			if err != nil {
				return err
			}
			ks, err := a.ListAPIKeys(cmd.Context(), u.ID)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ks)
			}
			rows := make([][]string, 0, len(ks))
			for _, k := range ks {
				status := "ativa"
				if !k.Active() {
					status = "revogada"
				}
				rows = append(rows, []string{k.ID.String(), k.Name, k.Prefix + "…", e.fmtTime(k.CreatedAt), e.fmtTimePtr(k.LastUsedAt), status})
			}
			e.table([]string{e.t("ID"), e.t("NOME"), e.t("PREFIXO"), e.t("CRIADA"), e.t("ÚLTIMO USO"), e.t("STATUS")}, rows)
			return nil
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke " + e.t("<id>"),
		Short: e.t("Revoga uma API key"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kid, err := domain.ParseID(args[0])
			if err != nil {
				return usageError{fmt.Errorf(e.t("id de chave inválido: %q"), args[0])}
			}
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			u, err := owner(cmd.Context(), a)
			if err != nil {
				return err
			}
			k, err := a.RevokeAPIKey(cmd.Context(), u.ID, kid)
			if err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(k)
			}
			_, err = fmt.Fprintf(e.out, e.t("Chave %q revogada.\n"), k.Name)
			return err
		},
	}
	c.AddCommand(create, list, revoke)
	return c
}

func (e *env) authUserCmd() *cobra.Command {
	var as string
	c := &cobra.Command{
		Use:   "user",
		Short: e.t("Contas da instalação (só o dono da instalação)"),
		Long:  e.t("Lista, redefine a senha e remove contas. Quem opera é o dono da instalação (users.role = owner): --as informa o e-mail dele; omitido, vale o único dono. No provider local, o link de convite é a prova de identidade de quem aceita; estes comandos são o remédio para uma conta indevida."),
	}
	c.PersistentFlags().StringVar(&as, "as", "", e.t("e-mail do dono da instalação que opera (omita se houver um só)"))

	list := &cobra.Command{
		Use:   "list",
		Short: e.t("Lista as contas"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			us, err := a.AdminListUsers(cmd.Context(), as)
			if err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(us)
			}
			rows := make([][]string, 0, len(us))
			for _, u := range us {
				rows = append(rows, []string{u.Email, u.Name, string(u.Role), string(u.Provider), e.fmtTime(u.CreatedAt), e.fmtTimePtr(u.LastLoginAt)})
			}
			e.table([]string{e.t("E-MAIL"), e.t("NOME"), e.t("PAPEL"), e.t("PROVIDER"), e.t("CRIADA"), e.t("ÚLTIMO LOGIN")}, rows)
			return nil
		},
	}

	reset := &cobra.Command{
		Use:   "reset-password " + e.t("<e-mail>"),
		Short: e.t("Redefine a senha de uma conta local e encerra as sessões dela"),
		Long:  e.tf("A nova senha vem de %s; sem ela, o comando gera uma senha temporária e a imprime uma única vez (entregue por um canal seguro). Provider local apenas.", userPasswordEnv),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			tmp, err := a.AdminResetPassword(cmd.Context(), as, args[0], e.getenv(userPasswordEnv))
			if err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				out := map[string]any{"email": args[0]}
				if tmp != "" {
					out["temporary_password"] = tmp
				}
				return e.printJSON(out)
			}
			if tmp == "" {
				_, err = fmt.Fprintf(e.out, e.t("Senha de %s redefinida; sessões encerradas.\n"), args[0])
				return err
			}
			fmt.Fprintf(e.errOut, e.t("Senha temporária de %s (exibida uma única vez; sessões encerradas):\n"), args[0])
			_, err = fmt.Fprintln(e.out, tmp)
			return err
		},
	}

	remove := &cobra.Command{
		Use:   "remove " + e.t("<e-mail>"),
		Short: e.t("Remove uma conta (sessões, API keys e vínculos com empresas)"),
		Long:  e.t("Recusa o dono da instalação e quem é o único owner de alguma empresa."),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			if err := a.AdminRemoveUser(cmd.Context(), as, args[0]); err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"removed": args[0]})
			}
			_, err = fmt.Fprintf(e.out, e.t("Conta %s removida.\n"), args[0])
			return err
		},
	}
	var limit int
	audit := &cobra.Command{
		Use:   "audit",
		Short: e.t("Lista a auditoria das operações de conta (quem operou, em quem, o quê e o resultado)"),
		Long:  e.t("Cada reset de senha e remoção de conta, inclusive as negadas e as recusadas, fica gravada no banco. Só o dono da instalação lê."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := e.openAuth(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			es, err := a.AdminListAudit(cmd.Context(), as, limit)
			if err != nil {
				return e.friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(es)
			}
			if len(es) == 0 {
				_, err = fmt.Fprintln(e.out, e.t("Nenhum registro de auditoria."))
				return err
			}
			rows := make([][]string, 0, len(es))
			for _, r := range es {
				rows = append(rows, []string{e.fmtTime(r.At), r.ActorEmail, r.Action, r.TargetEmail, r.Result})
			}
			e.table([]string{e.t("QUANDO"), e.t("OPERADOR"), e.t("AÇÃO"), e.t("ALVO"), e.t("RESULTADO")}, rows)
			return nil
		},
	}
	audit.Flags().IntVar(&limit, "limit", 100, e.t("máximo de registros (1 a 1000)"))
	c.AddCommand(list, reset, remove, audit)
	return c
}
