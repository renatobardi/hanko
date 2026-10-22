package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/sandbox"
)

// Contextos da CLI (#68, ADR-0060): `hanko context add|use|list|current|rm`. O arquivo é o mesmo que `hanko login` grava
// (contexts.json); cada contexto é um servidor e, quando há, a API key dele. Mais `hanko sandbox`: a instância
// descartável de test-drive por branch ou worktree.

const sandboxContextPrefix = "sandbox-"

// validContextName: nomes simples, para caber em flag, variável de ambiente e chave do JSON.
func validContextName(n string) bool {
	if n == "" || len(n) > 60 {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// validServerURL aceita só http(s)://host[:porta][/prefixo], sem credenciais, query nem fragmento.
func validServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("URL inválida")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (e *env) contextCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "context", Aliases: []string{"ctx"},
		Short: e.t("Servidores conhecidos pela CLI: adiciona, troca e lista (o `hanko login` também grava aqui)"),
	}
	c.AddCommand(e.contextListCmd(), e.contextCurrentCmd(), e.contextUseCmd(), e.contextAddCmd(), e.contextRmCmd())
	return c
}

func (e *env) contextListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os contextos salvos (o ativo leva *)"), Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cs := e.loadContexts()
			names := make([]string, 0, len(cs.Contexts))
			for n := range cs.Contexts {
				names = append(names, n)
			}
			sort.Strings(names)
			if e.asJSON {
				return e.printJSON(map[string]any{"current": cs.Current, "contexts": ctxView(cs, names)})
			}
			if len(names) == 0 {
				fmt.Fprintln(e.out, e.t("nenhum contexto salvo: use `hanko login` ou `hanko context add`"))
				return nil
			}
			rows := make([][]string, 0, len(names))
			for _, n := range names {
				c := cs.Contexts[n]
				mark := ""
				if n == cs.Current {
					mark = "*"
				}
				cred := e.t("sim")
				if c.Token == "" {
					cred = e.t("não")
				}
				rows = append(rows, []string{mark, n, dash(c.URL), dash(c.User), dash(c.Device), cred})
			}
			e.table([]string{"", e.t("NOME"), "URL", e.t("USUÁRIO"), e.t("DISPOSITIVO"), e.t("CHAVE")}, rows)
			return nil
		},
	}
}

// ctxView é o contexto sem o segredo, para --json.
func ctxView(cs cliContexts, names []string) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		c := cs.Contexts[n]
		out = append(out, map[string]any{"name": n, "url": c.URL, "user": c.User, "device": c.Device, "has_token": c.Token != "", "current": n == cs.Current})
	}
	return out
}

func (e *env) contextCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use: "current", Short: e.t("Mostra o contexto ativo"), Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cs := e.loadContexts()
			name := e.activeContextName(cs)
			if _, ok := cs.Contexts[name]; !ok || name == "" {
				if e.asJSON {
					return e.printJSON(map[string]any{"current": "", "url": e.url})
				}
				fmt.Fprintf(e.out, e.t("nenhum contexto ativo (servidor padrão: %s)\n"), e.url)
				return nil
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"current": name, "url": cs.Contexts[name].URL})
			}
			fmt.Fprintf(e.out, "%s\t%s\n", name, cs.Contexts[name].URL)
			return nil
		},
	}
}

func (e *env) contextUseCmd() *cobra.Command {
	return &cobra.Command{
		Use: "use " + e.t("<nome>"), Short: e.t("Passa a usar um contexto salvo"), Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cs := e.loadContexts()
			if _, ok := cs.Contexts[args[0]]; !ok {
				return usageError{fmt.Errorf(e.t("contexto %q não existe: veja `hanko context list`"), args[0])}
			}
			cs.Current = args[0]
			if err := e.saveContexts(cs); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("contexto ativo: %s (%s)\n"), args[0], cs.Contexts[args[0]].URL)
			return nil
		},
	}
}

func (e *env) contextAddCmd() *cobra.Command {
	var urlFlag, user string
	var tokenStdin, noCheck, use bool
	c := &cobra.Command{
		Use: "add " + e.t("<nome>"), Short: e.t("Salva um servidor (e, se tiver, a API key) sem passar pelo navegador"), Args: cobra.ExactArgs(1),
		Long: e.t("Guarda um servidor para alternar com `hanko context use`. A API key (hk_…) vem da primeira linha da entrada padrão com --token-stdin (nunca de flag, para não ir ao histórico do shell); sem ela o contexto serve a um servidor em modo trusted. Por padrão confere o servidor com um whoami antes de gravar."),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validContextName(name) {
				return usageError{errors.New(e.t("nome de contexto inválido: use letras, números, - _ ."))}
			}
			if urlFlag == "" {
				return usageError{errors.New(e.t("--url é obrigatório"))}
			}
			srv, err := validServerURL(urlFlag)
			if err != nil {
				return usageError{errors.New(e.t("URL inválida: use http(s)://host[:porta]"))}
			}
			tok := ""
			if tokenStdin {
				line, rerr := bufio.NewReader(e.in).ReadString('\n')
				if rerr != nil && !errors.Is(rerr, io.EOF) {
					return rerr
				}
				if tok = strings.TrimSpace(line); tok == "" {
					return usageError{errors.New(e.t("--token-stdin: a entrada padrão está vazia"))}
				}
			}
			who := user
			if !noCheck {
				w, err := client.New(srv, tok).Whoami(cmd.Context())
				if err != nil {
					return fmt.Errorf(e.t("não consegui falar com %s (use --no-check para gravar mesmo assim): %w"), srv, err)
				}
				if tok != "" && !w.Authenticated {
					return errors.New(e.t("o servidor não reconheceu a API key"))
				}
				if who == "" {
					who = w.Email
				}
			}
			cs := e.loadContexts()
			if cs.Contexts == nil {
				cs.Contexts = map[string]cliContext{}
			}
			prev := cs.Contexts[name]
			cs.Contexts[name] = cliContext{URL: srv, Token: tok, Device: prev.Device, User: who}
			if use || cs.Current == "" {
				cs.Current = name
			}
			if err := e.saveContexts(cs); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("contexto %s salvo (%s)\n"), name, srv)
			return nil
		},
	}
	c.Flags().StringVar(&urlFlag, "url", "", e.t("endereço do servidor, ex.: https://hanko.exemplo.com"))
	c.Flags().StringVar(&user, "user", "", e.t("rótulo do usuário (padrão: o e-mail que o servidor devolve)"))
	c.Flags().BoolVar(&tokenStdin, "token-stdin", false, e.t("lê a API key da primeira linha da entrada padrão"))
	c.Flags().BoolVar(&noCheck, "no-check", false, e.t("não confere o servidor antes de gravar"))
	c.Flags().BoolVar(&use, "use", false, e.t("torna este o contexto ativo"))
	return c
}

func (e *env) contextRmCmd() *cobra.Command {
	return &cobra.Command{
		Use: "rm " + e.t("<nome>"), Aliases: []string{"remove"}, Short: e.t("Esquece um contexto (a API key continua válida no servidor até ser revogada)"), Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cs := e.loadContexts()
			if _, ok := cs.Contexts[args[0]]; !ok {
				return usageError{fmt.Errorf(e.t("contexto %q não existe: veja `hanko context list`"), args[0])}
			}
			delete(cs.Contexts, args[0])
			if cs.Current == args[0] {
				cs.Current = ""
			}
			if err := e.saveContexts(cs); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("contexto %s removido\n"), args[0])
			return nil
		},
	}
}

// activeContextName é o contexto em uso: --context/HANKO_CONTEXT vence o salvo.
func (e *env) activeContextName(cs cliContexts) string {
	if e.ctxName != "" {
		return e.ctxName
	}
	return cs.Current
}

// ---------- sandbox (test-drive por branch/worktree) ----------

func (e *env) sandboxCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sandbox",
		Short: e.t("Instância descartável de test-drive por branch ou worktree (banco próprio, porta própria, contexto próprio)"),
		Long:  e.t("Cria um banco `hanko_sbx_<branch>` no Postgres que você já usa (HANKO_SANDBOX_DATABASE_URL ou HANKO_DATABASE_URL), sobe o servidor num porto determinístico em modo trusted e registra o contexto `sandbox-<branch>`. Serve para testar uma branch sem tocar nos dados da instalação de verdade; `hanko sandbox down` apaga tudo."),
	}
	c.AddCommand(e.sandboxUpCmd(), e.sandboxDownCmd(), e.sandboxListCmd())
	return c
}

// sandboxAdminURL é o Postgres onde os bancos de sandbox são criados.
func (e *env) sandboxAdminURL(flag string) (string, error) {
	for _, v := range []string{flag, e.getenv("HANKO_SANDBOX_DATABASE_URL"), e.getenv("HANKO_DATABASE_URL")} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	return "", usageError{errors.New(e.t("informe o Postgres: --database-url, HANKO_SANDBOX_DATABASE_URL ou HANKO_DATABASE_URL"))}
}

// sandboxName é o nome pedido ou o da branch do diretório atual.
func (e *env) sandboxName(ctx context.Context, flag string) string {
	if flag != "" {
		return sandbox.Slug(flag)
	}
	return sandbox.Slug(sandbox.Branch(ctx, "."))
}

func (e *env) sandboxDir(name string) string {
	if p := e.configPath(); p != "" {
		return filepath.Join(filepath.Dir(p), "sandboxes", name)
	}
	return filepath.Join(os.TempDir(), "hanko-sandboxes", name)
}

// portFreeFn / healthyFn / serveFn são os pontos de injeção dos testes.
func (e *env) portFree(port int) bool {
	if e.portFreeFn != nil {
		return e.portFreeFn(port)
	}
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func (e *env) healthy(ctx context.Context, base string) bool {
	if e.healthyFn != nil {
		return e.healthyFn(ctx, base)
	}
	cctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (e *env) runServe(ctx context.Context, childEnv []string) error {
	if e.serveFn != nil {
		return e.serveFn(ctx, childEnv)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := osexec.CommandContext(ctx, self, "serve") //nolint:gosec // o próprio binário
	cmd.Env = append(os.Environ(), childEnv...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = e.out, e.errOut, nil
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func (e *env) sandboxUpCmd() *cobra.Command {
	var nameFlag, dbURL string
	var port int
	var noServe, use bool
	c := &cobra.Command{
		Use: "up", Short: e.t("Cria (ou reaproveita) a sandbox da branch atual e sobe o servidor nela"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			admin, err := e.sandboxAdminURL(dbURL)
			if err != nil {
				return err
			}
			name := e.sandboxName(ctx, nameFlag)
			inst, err := sandbox.Create(ctx, admin, name)
			if err != nil {
				return err
			}
			p := port
			if p == 0 {
				p = inst.Port
				if !e.portFree(p) && !e.healthy(ctx, fmt.Sprintf("http://127.0.0.1:%d", p)) {
					for i := 1; i <= 20 && !e.portFree(p); i++ { // porta tomada por outro processo: a próxima livre
						p = inst.Port + i
					}
				}
			}
			base := fmt.Sprintf("http://127.0.0.1:%d", p)
			running := e.healthy(ctx, base)
			if !running && !e.portFree(p) {
				return fmt.Errorf(e.t("a porta %d está ocupada por outro processo: use --port"), p)
			}
			ctxName := sandboxContextPrefix + name
			cs := e.loadContexts()
			if cs.Contexts == nil {
				cs.Contexts = map[string]cliContext{}
			}
			cs.Contexts[ctxName] = cliContext{URL: base, Device: "sandbox"}
			if use {
				cs.Current = ctxName
			}
			if err := e.saveContexts(cs); err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"name": name, "database": inst.Database, "url": base, "context": ctxName, "running": running})
			}
			e.kv([2]string{e.t("sandbox"), name}, [2]string{e.t("banco"), inst.Database}, [2]string{e.t("endereço"), base}, [2]string{e.t("contexto"), ctxName})
			if running {
				fmt.Fprintln(e.out, e.t("já há um servidor respondendo nesse endereço: nada a subir"))
				return nil
			}
			childEnv := []string{
				"HANKO_DATABASE_URL=" + inst.DatabaseURL, "HANKO_ADDR=127.0.0.1:" + strconv.Itoa(p), "HANKO_PUBLIC_URL=" + base,
				"HANKO_MODE=trusted", "HANKO_MIGRATE_ON_BOOT=true", "HANKO_WORKSPACES_ROOT=" + filepath.Join(e.sandboxDir(name), "workspaces"),
			}
			if noServe {
				fmt.Fprintln(e.out, e.t("\nPara subir à mão:"))
				for _, kv := range childEnv {
					fmt.Fprintf(e.out, "  %s\n", kv)
				}
				fmt.Fprintln(e.out, "  hanko serve")
				return nil
			}
			if err := os.MkdirAll(filepath.Join(e.sandboxDir(name), "workspaces"), 0o700); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("\nSubindo o servidor (Ctrl-C para parar). Abra %s para o assistente de primeiros passos.\nPara usar pela CLI: hanko --context %s ...; para apagar tudo: hanko sandbox down --name %s\n\n"), base, ctxName, name)
			return e.runServe(ctx, childEnv)
		},
	}
	c.Flags().StringVar(&nameFlag, "name", "", e.t("nome da sandbox (padrão: a branch git do diretório atual)"))
	c.Flags().StringVar(&dbURL, "database-url", "", e.t("Postgres onde criar o banco (padrão: HANKO_SANDBOX_DATABASE_URL ou HANKO_DATABASE_URL)"))
	c.Flags().IntVar(&port, "port", 0, e.t("porta do servidor (padrão: determinística pelo nome, 18000-18999)"))
	c.Flags().BoolVar(&noServe, "no-serve", false, e.t("só cria o banco e o contexto; imprime como subir à mão"))
	c.Flags().BoolVar(&use, "use", false, e.t("torna o contexto da sandbox o ativo"))
	return c
}

func (e *env) sandboxDownCmd() *cobra.Command {
	var nameFlag, dbURL string
	c := &cobra.Command{
		Use: "down", Short: e.t("Apaga o banco, os arquivos e o contexto da sandbox"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			admin, err := e.sandboxAdminURL(dbURL)
			if err != nil {
				return err
			}
			name := e.sandboxName(ctx, nameFlag)
			inst := sandbox.Instance{Name: name, Port: sandbox.Port(name)}
			if e.healthy(ctx, fmt.Sprintf("http://127.0.0.1:%d", inst.Port)) {
				fmt.Fprintf(e.errOut, e.t("aviso: há um servidor respondendo na porta %d; pare-o antes (o banco será apagado de qualquer forma)\n"), inst.Port)
			}
			if err := sandbox.Drop(ctx, admin, name); err != nil {
				return err
			}
			_ = os.RemoveAll(e.sandboxDir(name))
			cs := e.loadContexts()
			ctxName := sandboxContextPrefix + name
			if _, ok := cs.Contexts[ctxName]; ok {
				delete(cs.Contexts, ctxName)
				if cs.Current == ctxName {
					cs.Current = ""
				}
				if err := e.saveContexts(cs); err != nil {
					return err
				}
			}
			fmt.Fprintf(e.out, e.t("sandbox %s apagada\n"), name)
			return nil
		},
	}
	c.Flags().StringVar(&nameFlag, "name", "", e.t("nome da sandbox (padrão: a branch git do diretório atual)"))
	c.Flags().StringVar(&dbURL, "database-url", "", e.t("Postgres onde está o banco"))
	return c
}

func (e *env) sandboxListCmd() *cobra.Command {
	var dbURL string
	c := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as sandboxes do Postgres (e se o servidor de cada uma está de pé)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			admin, err := e.sandboxAdminURL(dbURL)
			if err != nil {
				return err
			}
			all, err := sandbox.List(cmd.Context(), admin)
			if err != nil {
				return err
			}
			cs := e.loadContexts()
			type row struct {
				Name, Database, URL string
				Context, Running    bool
			}
			rows := make([]row, 0, len(all))
			for _, in := range all {
				ctxName := sandboxContextPrefix + in.Name
				r := row{Name: in.Name, Database: in.Database, URL: fmt.Sprintf("http://127.0.0.1:%d", in.Port)}
				if c, ok := cs.Contexts[ctxName]; ok {
					r.Context, r.URL = true, c.URL
				}
				r.Running = e.healthy(cmd.Context(), r.URL)
				rows = append(rows, r)
			}
			if e.asJSON {
				return e.printJSON(rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(e.out, e.t("nenhuma sandbox: `hanko sandbox up` cria a da branch atual"))
				return nil
			}
			yn := func(b bool) string {
				if b {
					return e.t("sim")
				}
				return e.t("não")
			}
			out := make([][]string, len(rows))
			for i, r := range rows {
				out[i] = []string{r.Name, r.Database, r.URL, yn(r.Context), yn(r.Running)}
			}
			e.table([]string{e.t("NOME"), e.t("BANCO"), "URL", e.t("CONTEXTO"), e.t("DE PÉ")}, out)
			return nil
		},
	}
	c.Flags().StringVar(&dbURL, "database-url", "", e.t("Postgres a consultar"))
	return c
}
