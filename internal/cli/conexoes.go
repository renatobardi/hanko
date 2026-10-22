package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Conexões OAuth (ADR-0043): hanko connection list|add|authorize|probe|refresh|disconnect|rm.

func (e *env) connStatusLabel(st string) string {
	switch st {
	case "connected":
		return e.t("conectada")
	case "pending_auth":
		return e.t("aguardando autorização")
	case "expired":
		return e.t("expirada")
	case "revoked":
		return e.t("revogada")
	case "error":
		return e.t("com erro")
	}
	return e.t("rascunho")
}

func (e *env) connSetupLabel(s string) string {
	switch s {
	case "authorize":
		return e.t("autorizar: hanko connection authorize")
	case "finish":
		return e.t("concluir a autorização no navegador (ou recomeçar com hanko connection authorize)")
	case "reconnect":
		return e.t("reconectar: hanko connection authorize")
	case "probe":
		return e.t("testar de novo: hanko connection probe")
	}
	return ""
}

func (e *env) printConnection(c api.Connection) {
	fmt.Fprintf(e.out, "%s  %s  %s\n", c.Slug, e.connStatusLabel(c.Status), c.URL)
	if c.ExpiresAt != nil {
		fmt.Fprintf(e.out, e.t("  token vale até %s\n"), c.ExpiresAt.Local().Format(time.RFC3339))
	}
	if c.LastError != "" {
		fmt.Fprintf(e.out, e.t("  último erro: %s\n"), c.LastError)
	}
	if s := e.connSetupLabel(c.Setup); s != "" {
		fmt.Fprintf(e.out, e.t("  próximo passo: %s\n"), s)
	}
}

func (e *env) connectionCmd() *cobra.Command {
	c := &cobra.Command{Use: "connection", Aliases: []string{"conn"}, Short: e.t("Conexões OAuth com servidores MCP remotos")}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as conexões"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cs, err := e.client().Connections(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if len(cs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma conexão. Crie com: hanko connection add <nome> <url-do-servidor-mcp>"))
				return nil
			}
			rows := make([][]string, 0, len(cs))
			for _, x := range cs {
				exp := "—"
				if x.ExpiresAt != nil {
					exp = x.ExpiresAt.Local().Format("02/01 15:04")
				}
				id := e.t("empresa")
				if x.Identity == "user" {
					id = e.t("pessoal")
				}
				rows = append(rows, []string{x.Slug, e.connStatusLabel(x.Status), id, exp, x.URL})
			}
			e.table([]string{e.t("SLUG"), e.t("ESTADO"), e.t("IDENTIDADE"), e.t("TOKEN ATÉ"), e.t("URL")}, rows)
			return nil
		},
	}

	var identity, slug, clientID, clientSecret, scopes string
	add := &cobra.Command{
		Use: "add " + e.t("<nome> <url>"), Short: e.t("Cria o rascunho de uma conexão (depois: hanko connection authorize)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().CreateConnection(cmd.Context(), co, client.CreateConnectionReq{Name: a[0], Slug: slug, URL: a[1],
				Identity: identity, Scopes: strings.Fields(scopes), ClientID: clientID, ClientSecretRef: clientSecret})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			fmt.Fprintf(e.out, e.t("Conexão %s criada (rascunho). Autorize com: hanko connection authorize %s\n"), v.Slug, v.Slug)
			return nil
		},
	}
	af := add.Flags()
	af.StringVar(&identity, "identity", "company", e.t("company (as personas usam) ou user (só você)"))
	af.StringVar(&slug, "slug", "", e.t("slug (derivado do nome se vazio)"))
	af.StringVar(&scopes, "scopes", "", e.t("escopos pedidos, separados por espaço"))
	af.StringVar(&clientID, "client-id", "", e.t("client_id, para servidor sem registro dinâmico"))
	af.StringVar(&clientSecret, "client-secret-ref", "", e.t("nome do segredo da empresa com o segredo do cliente"))

	var noOpen bool
	authz := &cobra.Command{
		Use: "authorize " + e.t("<slug>"), Aliases: []string{"auth"}, Short: e.t("Autoriza (ou reconecta) no navegador e espera concluir"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			st, err := e.client().AuthorizeConnection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(st)
			}
			fmt.Fprintf(e.out, e.t("Abra no navegador para autorizar (vale até %s):\n%s\n"), st.ExpiresAt.Local().Format("15:04"), st.AuthorizationURL)
			if !noOpen {
				if err := e.openBrowserFn()(st.AuthorizationURL); err != nil {
					fmt.Fprintln(e.out, e.t("(não consegui abrir o navegador; use o link acima)"))
				}
			}
			// espera a conexão ficar conectada, até a validade do pedido
			deadline := time.Now().Add(time.Until(st.ExpiresAt))
			for time.Now().Before(deadline) {
				e.sleep(2 * time.Second)
				v, err := e.client().Connection(cmd.Context(), co, a[0])
				if err != nil {
					return err
				}
				switch v.Status {
				case "connected":
					fmt.Fprintf(e.out, e.t("Conexão %s autorizada.\n"), v.Slug)
					return nil
				case "draft":
					if v.LastError != "" {
						return fmt.Errorf("%s", v.LastError)
					}
				}
			}
			return fmt.Errorf("%s", e.t("a autorização não terminou a tempo; recomece com: hanko connection authorize "+a[0]))
		},
	}
	authz.Flags().BoolVar(&noOpen, "no-open", false, e.t("só imprime o link, não abre o navegador"))

	probe := &cobra.Command{
		Use: "probe " + e.t("<slug>"), Short: e.t("Testa a autenticação com o servidor"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().ProbeConnection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			e.printConnection(v)
			return nil
		},
	}
	refresh := &cobra.Command{
		Use: "refresh " + e.t("<slug>"), Short: e.t("Renova o token agora"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().RefreshConnection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			e.printConnection(v)
			return nil
		},
	}
	disc := &cobra.Command{
		Use: "disconnect " + e.t("<slug>"), Short: e.t("Apaga os tokens, desliga a integração e volta a rascunho"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().DisconnectConnection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			fmt.Fprintf(e.out, e.t("Conexão %s desconectada.\n"), v.Slug)
			return nil
		},
	}
	rm := &cobra.Command{
		Use: "rm " + e.t("<slug>"), Aliases: []string{"delete"}, Short: e.t("Apaga a conexão, a integração e os tokens"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteConnection(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Conexão %s apagada.\n"), a[0])
			return nil
		},
	}
	show := &cobra.Command{
		Use: "show " + e.t("<slug>"), Short: e.t("Mostra uma conexão e o próximo passo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().Connection(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			e.printConnection(v)
			return nil
		},
	}
	c.AddCommand(ls, show, add, authz, probe, refresh, disc, rm)
	return c
}
