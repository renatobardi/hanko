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
	Close()
}

// ownerPasswordEnv é a variável lida para a senha do dono (alternativa à stdin).
const ownerPasswordEnv = "HANKO_OWNER_PASSWORD"

func (e *env) openAuth(ctx context.Context) (AuthAdmin, error) {
	if e.rt.Auth == nil {
		return nil, errNotWired
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
		Short: "Usuários e API keys (acesso direto ao banco; use no host do servidor)",
		Long: "Comandos administrativos de autenticação. Falam direto com o banco configurado " +
			"(HANKO_DATABASE_URL), sem o servidor no ar; rode `hanko migrate` antes.",
	}
	c.AddCommand(e.authCreateOwnerCmd(), e.authTokenCmd())
	return c
}

func (e *env) authCreateOwnerCmd() *cobra.Command {
	var email, name string
	cmd := &cobra.Command{
		Use:   "create-owner --email E --name N",
		Short: "Cria o primeiro usuário (dono) com e-mail e senha (provider local)",
		Long: "Cria o dono da instalação. A senha vem de " + ownerPasswordEnv + " ou da primeira linha da entrada padrão\n" +
			"(nunca de flag, para não ir ao histórico do shell). Exemplo:\n" +
			"  read -rs HANKO_OWNER_PASSWORD && export HANKO_OWNER_PASSWORD\n" +
			"  hanko auth create-owner --email voce@exemplo.com --name \"Seu Nome\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(email) == "" || strings.TrimSpace(name) == "" {
				return usageError{errors.New("--email e --name são obrigatórios")}
			}
			pw := e.getenv(ownerPasswordEnv)
			if pw == "" {
				line, err := bufio.NewReader(e.in).ReadString('\n')
				if err != nil && line == "" {
					return usageError{fmt.Errorf("senha não informada: defina %s ou envie pela entrada padrão", ownerPasswordEnv)}
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
				return friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(u)
			}
			_, err = fmt.Fprintf(e.out, "Dono criado: %s <%s>\nEntre em /entrar com este e-mail e a senha informada.\n", u.Name, u.Email)
			return err
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "e-mail do dono")
	cmd.Flags().StringVar(&name, "name", "", "nome do dono")
	return cmd
}

func friendlyAuthErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrConflict):
		return fmt.Errorf("já existe um usuário: o dono só pode ser criado em um banco sem usuários (%w)", err)
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("não encontrado: %w", err)
	}
	return err
}

func (e *env) authTokenCmd() *cobra.Command {
	var email string
	c := &cobra.Command{Use: "token", Short: "API keys de usuário (Authorization: Bearer hk_…)"}
	c.PersistentFlags().StringVar(&email, "email", "", "e-mail do usuário dono da chave (omita se houver um único usuário)")

	owner := func(ctx context.Context, a AuthAdmin) (domain.User, error) {
		if strings.TrimSpace(email) != "" {
			u, err := a.GetUserByEmail(ctx, email)
			if err != nil {
				return domain.User{}, friendlyAuthErr(err)
			}
			return u, nil
		}
		us, err := a.ListUsers(ctx)
		if err != nil {
			return domain.User{}, err
		}
		switch len(us) {
		case 0:
			return domain.User{}, errors.New("não há usuários: crie o dono com `hanko auth create-owner`")
		case 1:
			return us[0], nil
		}
		return domain.User{}, usageError{errors.New("há mais de um usuário: informe --email")}
	}

	create := &cobra.Command{
		Use:   "create --name N",
		Short: "Cria uma API key (exibida uma única vez)",
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
				return friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"key": key, "id": k.ID, "name": k.Name, "prefix": k.Prefix, "user": u.Email})
			}
			fmt.Fprintf(e.errOut, "Chave %q criada para %s. Guarde agora: não será exibida de novo.\n", k.Name, u.Email)
			_, err = fmt.Fprintln(e.out, key)
			return err
		},
	}
	create.Flags().String("name", "", "nome da chave (ex.: notebook, ci)")
	_ = create.MarkFlagRequired("name")

	list := &cobra.Command{
		Use:   "list",
		Short: "Lista as API keys do usuário",
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
				rows = append(rows, []string{k.ID.String(), k.Name, k.Prefix + "…", fmtTime(k.CreatedAt), fmtTimePtr(k.LastUsedAt), status})
			}
			e.table([]string{"ID", "NOME", "PREFIXO", "CRIADA", "ÚLTIMO USO", "STATUS"}, rows)
			return nil
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoga uma API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kid, err := domain.ParseID(args[0])
			if err != nil {
				return usageError{fmt.Errorf("id de chave inválido: %q", args[0])}
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
				return friendlyAuthErr(err)
			}
			if e.asJSON {
				return e.printJSON(k)
			}
			_, err = fmt.Fprintf(e.out, "Chave %q revogada.\n", k.Name)
			return err
		},
	}
	c.AddCommand(create, list, revoke)
	return c
}
