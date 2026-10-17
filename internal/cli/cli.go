// Package cli é a linha de comando do Hanko: servidor (serve, migrate), utilitários
// (version, openapi) e um cliente da API REST do próprio servidor.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/storage"
)

// Runtime injeta o que depende de infraestrutura (banco, config). O main de produção
// conecta internal/app; testes passam fakes.
type Runtime struct {
	Serve   func(ctx context.Context) error
	Migrate func(ctx context.Context) error
	// OpenAPI gera a spec; nil usa api.Spec.
	OpenAPI func() ([]byte, error)
	// Auth abre o acesso direto ao banco para `hanko auth …` (usuários e API keys).
	Auth func(ctx context.Context) (AuthAdmin, error)
	// BlobsMigrate move o conteúdo dos artefatos entre drivers (`hanko blobs migrate`): usa o banco e o env do processo.
	BlobsMigrate func(ctx context.Context, a BlobsMigrateArgs) (storage.MigrateReport, error)
}

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
	exitSig   = 130
)

// usageError marca erros de uso (flags/argumentos) para o código de saída 2.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// Execute roda a CLI com os args e o ambiente do processo e devolve o código de saída.
func Execute(ctx context.Context, version string, rt Runtime) int {
	return run(ctx, version, rt, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
}

// env é o estado compartilhado pelos comandos (flags globais e streams).
type env struct {
	in          io.Reader
	out, errOut io.Writer
	getenv      func(string) string
	url, token  string
	company     string
	asJSON      bool
	version     string
	rt          Runtime
	sleep       func(time.Duration)
	followRetry int
	followWait  time.Duration
	// chatPoll is how often `hanko chat` asks whether the persona has answered.
	chatPoll time.Duration
	// ctx carrega o idioma da CLI, resolvido uma vez (HANKO_LANG > LC_ALL > LC_MESSAGES > LANG > pt-BR).
	ctx context.Context
}

// t traduz um texto pt-BR para o idioma da CLI; texto sem entrada no catálogo volta como está.
func (e *env) t(s string) string { return i18n.T(e.ctx, s) }

// tf é t para um formato printf: traduz o formato e depois preenche.
func (e *env) tf(format string, args ...any) string { return i18n.Tf(e.ctx, format, args...) }

// lang é o idioma resolvido da CLI.
func (e *env) lang() i18n.Lang { return i18n.FromContext(e.ctx) }

func run(ctx context.Context, version string, rt Runtime, args []string, in io.Reader, out, errOut io.Writer, getenv func(string) string) int {
	e := &env{in: in, out: out, errOut: errOut, getenv: getenv, version: version, rt: rt,
		sleep: time.Sleep, followRetry: 5, followWait: time.Second, chatPoll: chatPollEvery}
	root := e.root() // resolve o idioma antes de montar os comandos (Short/Long/flags já saem traduzidos)
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return exitSig
	}
	fmt.Fprintln(errOut, e.t("erro:"), err)
	var ue usageError
	if errors.As(err, &ue) || isCobraUsageError(err) {
		fmt.Fprintln(errOut, e.tf("Use '%s --help' para ver as opções.", root.Name()))
		return exitUsage
	}
	return exitError
}

// isCobraUsageError reconhece erros de uso gerados pelo próprio cobra (flags
// obrigatórias, comando desconhecido, número de argumentos).
func isCobraUsageError(err error) bool {
	msg := err.Error()
	for _, p := range []string{"required flag", "unknown command", "unknown flag", "unknown shorthand", "accepts ", "requires at least",
		"invalid argument", "flag needs an argument"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

func (e *env) root() *cobra.Command {
	if e.ctx == nil {
		e.ctx = i18n.WithLang(context.Background(), i18n.FromEnv(e.getenv))
	}
	def := func(key, fallback string) string {
		if v := e.getenv(key); v != "" {
			return v
		}
		return fallback
	}
	root := &cobra.Command{
		Use:           "hanko",
		Short:         e.t("Hanko — control plane para times de agentes de IA"),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.CompletionOptions.DisableDefaultCmd = true
	f := root.PersistentFlags()
	f.StringVar(&e.url, "url", def("HANKO_URL", client.DefaultURL), e.t("URL do servidor Hanko (HANKO_URL)"))
	f.StringVarP(&e.company, "company", "c", def("HANKO_COMPANY", ""), e.t("empresa: id ou slug (HANKO_COMPANY)"))
	f.StringVar(&e.token, "token", def("HANKO_TOKEN", ""), e.t("credencial opcional: API key hk_… (modo authenticated) ou run token (HANKO_TOKEN)"))
	f.BoolVar(&e.asJSON, "json", false, e.t("saída em JSON"))

	root.AddCommand(e.serveCmd(), e.migrateCmd(), e.versionCmd(), e.openapiCmd(),
		e.companyCmd(), e.workspaceCmd(), e.projectCmd(), e.personaCmd(), e.taskCmd(), e.runCmd(), e.gateCmd(), e.chatCmd(), e.orgCmd(),
		e.costCmd(), e.budgetCmd(), e.subscriptionCmd(), e.flowCmd(), e.memoryCmd(), e.memberCmd(), e.authCmd(), e.exportCmd(), e.importCmd(), e.searchCmd(), e.skillCmd(), e.integrationCmd(), e.pluginCmd(),
		e.artifactCmd(), e.goalCmd(), e.statusCardCmd(), e.toolProfileCmd(), e.toolGatewayCmd(),
		e.secretCmd(), e.settingsCmd(), e.blobsCmd(), e.toolPolicyCmd(), e.hookCmd())
	root.AddCommand(e.roomCmd())        // sala (ADR-0033)
	root.AddCommand(e.environmentCmd()) // ambientes de execução (ADR-0037)
	root.AddCommand(e.pipelinesCmds()...)
	e.localizeHelp(root)
	return root
}

func (e *env) client() *client.Client { return client.New(e.url, e.token) }

func (e *env) needCompany() (string, error) {
	if e.company == "" {
		return "", usageError{errors.New(e.t("empresa não informada: use --company ou HANKO_COMPANY"))}
	}
	return e.company, nil
}

func (e *env) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: %w", e.t("codificar JSON"), err)
	}
	_, err = fmt.Fprintln(e.out, string(b))
	return err
}

// table escreve uma tabela alinhada com cabeçalho.
func (e *env) table(header []string, rows [][]string) {
	tw := tabwriter.NewWriter(e.out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, join(header))
	for _, r := range rows {
		fmt.Fprintln(tw, join(r))
	}
	_ = tw.Flush()
}

func join(cols []string) string {
	s := ""
	for i, c := range cols {
		if i > 0 {
			s += "\t"
		}
		s += c
	}
	return s
}

// ---------- utilitários de formatação ----------

// fmtTime formata data e hora locais: dd/mm/aaaa em pt-BR, aaaa-mm-dd em inglês.
func (e *env) fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	layout := "02/01/2006 15:04"
	if e.lang() == i18n.EN {
		layout = "2006-01-02 15:04"
	}
	return t.Local().Format(layout)
}

func (e *env) fmtTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return e.fmtTime(*t)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func oneLine(s string, max int) string {
	r := []rune(s)
	for i, c := range r {
		if c == '\n' || c == '\r' || c == '\t' {
			r[i] = ' '
		}
	}
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return string(r)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// kv imprime pares "rótulo: valor" alinhados, omitindo valores vazios quando skipEmpty.
func (e *env) kv(pairs ...[2]string) {
	w := 0
	for _, p := range pairs {
		w = max(w, len([]rune(p[0])))
	}
	for _, p := range pairs {
		fmt.Fprintf(e.out, "%-*s  %s\n", w+1, p[0]+":", p[1])
	}
}

// localizeHelp traduz o que o cobra escreve por conta própria: o modelo de uso, o comando e a
// flag de ajuda. Roda depois de todos os comandos estarem montados.
func (e *env) localizeHelp(root *cobra.Command) {
	root.SetUsageTemplate(e.usageTemplate())
	help := &cobra.Command{
		Use:   "help [" + e.t("comando") + "]",
		Short: e.t("Ajuda sobre qualquer comando"),
		Long:  e.tf("Mostra a ajuda de qualquer comando da aplicação.\nDigite %s help [caminho do comando] para ver os detalhes.", root.DisplayName()),
		Run: func(c *cobra.Command, args []string) {
			cmd, _, err := c.Root().Find(args)
			if cmd == nil || err != nil {
				c.Println(e.tf("Tópico de ajuda desconhecido %q", args))
				_ = c.Root().Usage()
				return
			}
			cmd.InitDefaultHelpFlag()
			_ = cmd.Help()
		},
	}
	root.SetHelpCommand(help)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		if f := c.Flags().Lookup("help"); f != nil {
			f.Usage = e.tf("ajuda de %s", c.DisplayName())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	walk(help)
}

// usageTemplate é o modelo de uso padrão do cobra (v1.10) com os rótulos traduzidos.
func (e *env) usageTemplate() string {
	return e.t("Uso:") + `{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [` + e.t("comando") + `]{{end}}{{if gt (len .Aliases) 0}}

` + e.t("Apelidos:") + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + e.t("Exemplos:") + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

` + e.t("Comandos disponíveis:") + `{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

` + e.t("Outros comandos:") + `{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + e.t("Opções:") + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + e.t("Opções globais:") + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

` + e.t("Tópicos de ajuda adicionais:") + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

` + e.t("Use \"{{.CommandPath}} [comando] --help\" para mais informações sobre um comando.") + `{{end}}
`
}
