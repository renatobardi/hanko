package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Ciclo de vida da empresa, import com prévia, login pelo navegador e pedidos de entrada (ADR-0057).

// ---------- empresa: editar, arquivar, restaurar, excluir ----------

func (e *env) companyLifecycleCmds() []*cobra.Command {
	var name, tz, desc, prefix string
	edit := &cobra.Command{
		Use: "edit", Short: e.t("Edita nome, fuso, descrição e (com zero tasks) o prefixo da empresa (-c)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			in := client.UpdateCompanyReq{}
			f := cmd.Flags()
			if f.Changed("name") {
				in.Name = &name
			}
			if f.Changed("timezone") {
				in.Timezone = &tz
			}
			if f.Changed("description") {
				in.Description = &desc
			}
			if f.Changed("prefix") {
				in.TaskPrefix = &prefix
			}
			if in == (client.UpdateCompanyReq{}) {
				return usageError{errors.New(e.t("nada para alterar: use --name, --timezone, --description ou --prefix"))}
			}
			c, err := e.client().UpdateCompany(cmd.Context(), co, in)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(c)
			}
			fmt.Fprintf(e.out, e.t("Empresa atualizada: %s (%s) — prefixo %s, fuso %s\n"), c.Name, c.Slug, c.TaskPrefix, c.Timezone)
			return nil
		},
	}
	edit.Flags().StringVar(&name, "name", "", e.t("novo nome"))
	edit.Flags().StringVar(&tz, "timezone", "", e.t("novo fuso IANA"))
	edit.Flags().StringVar(&desc, "description", "", e.t("nova descrição (vazio limpa)"))
	edit.Flags().StringVar(&prefix, "prefix", "", e.t("novo prefixo das chaves (só sem tasks)"))

	archive := &cobra.Command{
		Use: "archive", Short: e.t("Arquiva a empresa (-c): pausa as personas, cancela execuções, desliga fluxos; fica somente leitura"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			c, err := e.client().ArchiveCompany(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(c)
			}
			fmt.Fprintf(e.out, e.t("Empresa %s arquivada (somente leitura). Restaure com: hanko company restore\n"), c.Slug)
			return nil
		},
	}

	var resume bool
	restore := &cobra.Command{
		Use: "restore", Short: e.t("Restaura a empresa arquivada (-c); personas e fluxos seguem pausados, a menos que --resume"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			c, err := e.client().RestoreCompany(cmd.Context(), co, resume)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(c)
			}
			fmt.Fprintf(e.out, e.t("Empresa %s restaurada\n"), c.Slug)
			return nil
		},
	}
	restore.Flags().BoolVar(&resume, "resume", false, e.t("reativa as personas e os fluxos que o arquivamento pausou"))

	var confirm, outFile string
	del := &cobra.Command{
		Use: "delete", Short: e.t("Exclui a empresa arquivada (-c; só owner): guarda o export antes e exige o nome exato em --confirm"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if confirm == "" {
				return usageError{errors.New(e.t("informe o nome exato da empresa em --confirm"))}
			}
			d, err := e.client().DeleteCompany(cmd.Context(), co, confirm)
			if err != nil {
				return err
			}
			if outFile != "" {
				raw, err := e.client().DownloadCompanyDeletion(cmd.Context(), d.ID)
				if err != nil {
					return fmt.Errorf(e.t("a empresa foi excluída, mas o download do export falhou: %w"), err)
				}
				if err := writeFileAtomic(outFile, raw); err != nil {
					return fmt.Errorf(e.t("gravar %s: %w"), outFile, err)
				}
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			fmt.Fprintf(e.out, e.t("Empresa %s excluída. Export guardado (%d bytes, sha256 %s): hanko company deletions\n"), d.Slug, d.ExportBytes, short(d.SHA256))
			if outFile != "" {
				fmt.Fprintf(e.out, e.t("exportado para %s\n"), outFile)
			}
			return nil
		},
	}
	del.Flags().StringVar(&confirm, "confirm", "", e.t("nome exato da empresa"))
	del.Flags().StringVarP(&outFile, "output", "o", "", e.t("baixa o export guardado para este arquivo"))

	deletions := &cobra.Command{
		Use: "deletions", Short: e.t("Lista as empresas excluídas e os exports guardados (dono da instalação)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ds, err := e.client().CompanyDeletions(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ds)
			}
			rows := make([][]string, len(ds))
			for i, d := range ds {
				rows[i] = []string{d.ID, d.Slug, d.Name, e.fmtTime(d.DeletedAt), fmt.Sprint(d.ExportBytes)}
			}
			e.table([]string{e.t("ID"), e.t("SLUG"), e.t("NOME"), e.t("EXCLUÍDA EM"), e.t("BYTES")}, rows)
			return nil
		},
	}
	var dlOut string
	download := &cobra.Command{
		Use: "download-deletion " + e.t("<id>"), Short: e.t("Baixa o export guardado de uma empresa excluída"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := e.client().DownloadCompanyDeletion(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if dlOut == "" {
				_, err = fmt.Fprintln(e.out, string(raw))
				return err
			}
			if err := writeFileAtomic(dlOut, raw); err != nil {
				return fmt.Errorf(e.t("gravar %s: %w"), dlOut, err)
			}
			fmt.Fprintf(e.out, e.t("exportado para %s\n"), dlOut)
			return nil
		},
	}
	download.Flags().StringVarP(&dlOut, "output", "o", "", e.t("grava neste arquivo em vez da saída padrão"))
	return []*cobra.Command{edit, archive, restore, del, deletions, download}
}

// printImportPreview mostra a prévia (ou o resultado) de um import: itens por ação, contagens e avisos.
func (e *env) printImportPreview(res api.ImportedCompany) {
	switch {
	case res.DryRun && res.Merged:
		fmt.Fprintf(e.out, e.t("Prévia (nada foi gravado): mesclar a configuração em %s (%s)\n"), res.Company.Name, res.Company.Slug)
	case res.DryRun:
		fmt.Fprintf(e.out, e.t("Prévia (nada foi gravado): criar a empresa %s (%s)\n"), res.Company.Name, res.Company.Slug)
	case res.Merged:
		fmt.Fprintf(e.out, e.t("Configuração mesclada em %s (%s)\n"), res.Company.Name, res.Company.Slug)
	default:
		fmt.Fprintf(e.out, e.t("empresa %s (%s) criada\n"), res.Company.Name, res.Company.Slug)
	}
	if res.Merged || res.DryRun {
		rows := make([][]string, 0, len(res.Items))
		for _, it := range res.Items {
			if !res.Merged && it.Action == "create" { // empresa nova: tudo é create; as contagens bastam
				continue
			}
			target := ""
			if it.Target != it.Key {
				target = "→ " + it.Target
			}
			rows = append(rows, []string{it.Kind, it.Key, e.actionLabel(it.Action), target})
		}
		if len(rows) > 0 {
			e.table([]string{e.t("TIPO"), e.t("ITEM"), e.t("AÇÃO"), e.t("DESTINO")}, rows)
			fmt.Fprintln(e.out)
		}
	}
	keys := make([]string, 0, len(res.Counts))
	for k := range res.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{k, fmt.Sprint(res.Counts[k])})
	}
	e.table([]string{e.t("ITEM"), e.t("QTD")}, rows)
	for _, w := range res.Warnings {
		msg := e.t(w.Message) // i18n:dyn (os formatos estão marcados com i18n.N em internal/ui/lifecycle.go)
		fmt.Fprintf(e.out, e.t("aviso: ")+msg+"\n", w.Count)
	}
}

func (e *env) actionLabel(a string) string {
	switch a {
	case "create":
		return e.t("criar")
	case "skip":
		return e.t("pular")
	case "rename":
		return e.t("renomear")
	case "replace":
		return e.t("substituir")
	}
	return a
}

// ---------- login pelo navegador ----------

// cliContext é uma conexão salva por `hanko login` (e lida por `hanko context`, #68).
type cliContext struct {
	URL    string `json:"url"`
	Token  string `json:"token"`
	Device string `json:"device,omitempty"`
	User   string `json:"user,omitempty"`
}

type cliContexts struct {
	Current  string                `json:"current"`
	Contexts map[string]cliContext `json:"contexts"`
}

// configPath é o arquivo de contextos: $HANKO_CONFIG_DIR, senão $XDG_CONFIG_HOME/hanko, senão ~/.config/hanko.
func (e *env) configPath() string {
	dir := e.getenv("HANKO_CONFIG_DIR")
	if dir == "" {
		if x := e.getenv("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "hanko")
		} else if h := e.getenv("HOME"); h != "" {
			dir = filepath.Join(h, ".config", "hanko")
		}
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "contexts.json")
}

func (e *env) loadContexts() cliContexts {
	var cs cliContexts
	p := e.configPath()
	if p == "" {
		return cs
	}
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &cs) != nil {
		return cliContexts{}
	}
	return cs
}

func (e *env) saveContexts(cs cliContexts) error {
	p := e.configPath()
	if p == "" {
		return errors.New(e.t("não achei onde gravar a configuração: defina HANKO_CONFIG_DIR ou HOME"))
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p, append(b, '\n')) // 0600
}

// currentContext devolve o contexto ativo, se houver.
func (e *env) currentContext() (cliContext, bool) {
	cs := e.loadContexts()
	c, ok := cs.Contexts[cs.Current]
	return c, ok && c.Token != ""
}

func (e *env) loginCmd() *cobra.Command {
	var ctxName, device, scope string
	var noBrowser bool
	c := &cobra.Command{
		Use: "login", Short: e.t("Entra pelo navegador: autoriza este dispositivo e guarda uma API key local"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if scope != "full" && scope != "read" {
				return usageError{errors.New(e.t("escopo inválido: use full ou read"))}
			}
			if device == "" {
				device = "cli"
				if h, err := os.Hostname(); err == nil && h != "" {
					device = h
				}
			}
			cl := client.New(e.url, "") // o pedido é público: nunca manda a credencial antiga
			st, err := cl.StartCLIAuth(cmd.Context(), device, scope)
			if err != nil {
				return err
			}
			link := strings.TrimRight(e.url, "/") + st.VerifyPath
			fmt.Fprintf(e.out, e.t("Abra no navegador para autorizar \"%s\" (escopo %s):\n  %s\n"), device, scope, link)
			if !noBrowser {
				if err := e.openBrowserFn()(link); err != nil {
					fmt.Fprintln(e.errOut, e.t("não consegui abrir o navegador; abra o link acima"))
				}
			}
			fmt.Fprintln(e.out, e.t("Aguardando a autorização..."))
			return e.waitLogin(cmd.Context(), cl, st, ctxName, device)
		},
	}
	c.Flags().StringVar(&ctxName, "name", "default", e.t("nome do contexto onde guardar a conexão"))
	c.Flags().StringVar(&device, "device", "", e.t("nome deste dispositivo (padrão: o hostname)"))
	c.Flags().StringVar(&scope, "scope", "full", e.t("escopo da API key: full ou read (só leitura)"))
	c.Flags().BoolVar(&noBrowser, "no-browser", false, e.t("não tenta abrir o navegador; só imprime o link"))
	return c
}

// waitLogin consulta o pedido até ser decidido ou vencer, e grava o contexto.
func (e *env) waitLogin(ctx context.Context, cl *client.Client, st api.CLIAuthStarted, ctxName, device string) error {
	interval := time.Duration(st.IntervalSec) * time.Second
	if e.loginPoll > 0 {
		interval = e.loginPoll
	} else if d, err := time.ParseDuration(e.getenv("HANKO_LOGIN_POLL")); err == nil && d > 0 { // intervalo menor, para testes
		interval = d
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, err := cl.PollCLIAuth(ctx, st.PollToken)
		var ce *client.Error
		switch {
		case errors.As(err, &ce) && ce.Status == 404:
			return errors.New(e.t("o pedido de login expirou; rode hanko login de novo"))
		case err != nil:
			return err
		}
		switch p.Status {
		case "denied":
			return errors.New(e.t("o login foi negado no navegador"))
		case "consumed":
			return errors.New(e.t("este pedido de login já foi usado; rode hanko login de novo"))
		case "approved":
			if p.APIKey == "" {
				return errors.New(e.t("a resposta não trouxe a API key; rode hanko login de novo"))
			}
			cs := e.loadContexts()
			if cs.Contexts == nil {
				cs.Contexts = map[string]cliContext{}
			}
			who := ""
			if p.User != nil {
				who = p.User.Email
			}
			cs.Contexts[ctxName] = cliContext{URL: strings.TrimRight(e.url, "/"), Token: p.APIKey, Device: device, User: who}
			cs.Current = ctxName
			if err := e.saveContexts(cs); err != nil {
				return fmt.Errorf(e.t("a chave foi emitida, mas não deu para gravá-la: %w"), err)
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"context": ctxName, "user": who, "device": device, "url": e.url})
			}
			fmt.Fprintf(e.out, e.t("Login feito como %s (dispositivo %s, contexto %s)\n"), who, device, ctxName)
			return nil
		}
		e.sleep(interval)
	}
}

// openBrowserFn devolve o abridor de navegador (injetável nos testes).
func (e *env) openBrowserFn() func(string) error {
	if e.openBrowser != nil {
		return e.openBrowser
	}
	return func(link string) error {
		if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
			return errors.New("link inválido")
		}
		var cmd *osexec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = osexec.Command("open", link)
		case "windows":
			cmd = osexec.Command("rundll32", "url.dll,FileProtocolHandler", link)
		default:
			cmd = osexec.Command("xdg-open", link)
		}
		return cmd.Start()
	}
}

func (e *env) logoutCmd() *cobra.Command {
	var ctxName string
	return &cobra.Command{
		Use: "logout", Short: e.t("Sai: revoga nada no servidor (use a tela de API keys) e apaga o contexto local"), Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cs := e.loadContexts()
			name := ctxName
			if name == "" {
				name = cs.Current
			}
			if _, ok := cs.Contexts[name]; !ok {
				fmt.Fprintln(e.out, e.t("nenhum login salvo"))
				return nil
			}
			delete(cs.Contexts, name)
			if cs.Current == name {
				cs.Current = ""
			}
			if err := e.saveContexts(cs); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("contexto %s removido (a API key continua válida no servidor até ser revogada)\n"), name)
			return nil
		},
	}
}

func (e *env) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use: "whoami", Short: e.t("Mostra quem você é neste servidor: usuário, papel e a API key em uso"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w, err := e.client().Whoami(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(w)
			}
			if !w.Authenticated {
				fmt.Fprintln(e.out, e.t("sem login (servidor em modo trusted)"))
				return nil
			}
			if w.Via == "run_token" {
				fmt.Fprintln(e.out, e.t("credencial: run token de uma persona"))
				return nil
			}
			pairs := [][2]string{{e.t("usuário"), w.Email}, {e.t("nome"), dash(w.Name)}, {e.t("papel na instalação"), w.Role}, {e.t("via"), w.Via},
				{e.t("servidor"), e.serverURL()}}
			if w.Key != nil {
				pairs = append(pairs, [2]string{e.t("API key"), fmt.Sprintf("%s (%s…, %s)", w.Key.Name, w.Key.Prefix, w.Key.Scope)},
					[2]string{e.t("dispositivo"), dash(w.Key.Device)})
			}
			e.kv(pairs...)
			return nil
		},
	}
}

func (e *env) usersCmd() *cobra.Command {
	return &cobra.Command{
		Use: "users", Short: e.t("Lista as contas da instalação (dono ou admin da instalação)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			us, err := e.client().Users(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(us)
			}
			rows := make([][]string, len(us))
			for i, u := range us {
				last := "-"
				if u.LastLoginAt != nil {
					last = e.fmtTime(*u.LastLoginAt)
				}
				rows[i] = []string{u.Email, dash(u.Name), u.Role, u.Provider, fmt.Sprint(u.Companies), fmt.Sprint(u.ActiveKeys), last}
			}
			e.table([]string{e.t("E-MAIL"), e.t("NOME"), e.t("PAPEL"), e.t("PROVIDER"), e.t("EMPRESAS"), e.t("CHAVES"), e.t("ÚLTIMO LOGIN")}, rows)
			return nil
		},
	}
}

// ---------- pedidos de entrada ----------

func (e *env) memberJoinCmds() []*cobra.Command {
	link := &cobra.Command{Use: "join-link", Short: e.t("Link público de pedido de entrada na empresa (admin)")}
	linkShow := &cobra.Command{
		Use: "show", Short: e.t("Mostra se o link está ligado"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			l, err := e.client().JoinLink(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if l.Enabled {
				fmt.Fprintln(e.out, e.t("link ligado (o endereço só aparece ao gerar; gere outro para trocar)"))
			} else {
				fmt.Fprintln(e.out, e.t("link desligado ou inexistente"))
			}
			return nil
		},
	}
	linkNew := &cobra.Command{
		Use: "new", Short: e.t("Gera (ou troca) o link e imprime o endereço, uma única vez"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			l, err := e.client().CreateJoinLink(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			fmt.Fprintln(e.out, strings.TrimRight(e.serverURL(), "/")+l.Path)
			return nil
		},
	}
	linkOff := &cobra.Command{
		Use: "off", Short: e.t("Desliga o link"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().RevokeJoinLink(cmd.Context(), co); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("link desligado"))
			return nil
		},
	}
	link.AddCommand(linkShow, linkNew, linkOff)

	var all bool
	reqs := &cobra.Command{
		Use: "requests", Short: e.t("Lista os pedidos de entrada (abertos, ou todos com --all)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rs, err := e.client().JoinRequests(cmd.Context(), co, !all)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(rs)
			}
			rows := make([][]string, len(rs))
			for i, r := range rs {
				rows[i] = []string{short(r.ID), r.Email, r.Status, dash(r.Role), oneLine(r.Message, 40), e.fmtTime(r.CreatedAt)}
			}
			e.table([]string{e.t("ID"), e.t("E-MAIL"), e.t("ESTADO"), e.t("PAPEL"), e.t("MENSAGEM"), e.t("PEDIDO EM")}, rows)
			return nil
		},
	}
	reqs.Flags().BoolVar(&all, "all", false, e.t("inclui os já decididos"))

	var role string
	approve := &cobra.Command{
		Use: "approve " + e.t("<id>"), Short: e.t("Aprova um pedido de entrada com o papel escolhido"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.checkRole(role); err != nil {
				return err
			}
			id, err := e.resolveJoinID(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			r, err := e.client().DecideJoinRequest(cmd.Context(), co, id, true, role)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("%s entrou como %s\n"), r.Email, r.Role)
			return nil
		},
	}
	approve.Flags().StringVar(&role, "role", "member", e.t("papel: owner, admin, member ou viewer"))
	deny := &cobra.Command{
		Use: "deny " + e.t("<id>"), Short: e.t("Nega um pedido de entrada"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveJoinID(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			r, err := e.client().DecideJoinRequest(cmd.Context(), co, id, false, "")
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("pedido de %s negado\n"), r.Email)
			return nil
		},
	}
	return []*cobra.Command{link, reqs, approve, deny}
}

// resolveJoinID aceita o id inteiro ou o prefixo curto exibido na lista.
func (e *env) resolveJoinID(ctx context.Context, company, ref string) (string, error) {
	if len(ref) >= 36 {
		return ref, nil
	}
	rs, err := e.client().JoinRequests(ctx, company, true)
	if err != nil {
		return "", err
	}
	var found string
	for _, r := range rs {
		if strings.HasPrefix(r.ID, ref) {
			if found != "" {
				return "", usageError{fmt.Errorf(e.t("prefixo %q ambíguo"), ref)}
			}
			found = r.ID
		}
	}
	if found == "" {
		return "", usageError{fmt.Errorf(e.t("pedido %q não encontrado entre os abertos"), ref)}
	}
	return found, nil
}
