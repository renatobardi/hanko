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
}

func run(ctx context.Context, version string, rt Runtime, args []string, in io.Reader, out, errOut io.Writer, getenv func(string) string) int {
	e := &env{in: in, out: out, errOut: errOut, getenv: getenv, version: version, rt: rt,
		sleep: time.Sleep, followRetry: 5, followWait: time.Second}
	root := e.root()
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
	fmt.Fprintln(errOut, "erro:", err)
	var ue usageError
	if errors.As(err, &ue) || isCobraUsageError(err) {
		fmt.Fprintf(errOut, "Use '%s --help' para ver as opções.\n", root.Name())
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
	def := func(key, fallback string) string {
		if v := e.getenv(key); v != "" {
			return v
		}
		return fallback
	}
	root := &cobra.Command{
		Use:           "hanko",
		Short:         "Hanko — control plane para times de agentes de IA",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.CompletionOptions.DisableDefaultCmd = true
	f := root.PersistentFlags()
	f.StringVar(&e.url, "url", def("HANKO_URL", client.DefaultURL), "URL do servidor Hanko (HANKO_URL)")
	f.StringVarP(&e.company, "company", "c", def("HANKO_COMPANY", ""), "empresa: id ou slug (HANKO_COMPANY)")
	f.StringVar(&e.token, "token", def("HANKO_TOKEN", ""), "credencial opcional: API key hk_… (modo authenticated) ou run token (HANKO_TOKEN)")
	f.BoolVar(&e.asJSON, "json", false, "saída em JSON")

	root.AddCommand(e.serveCmd(), e.migrateCmd(), e.versionCmd(), e.openapiCmd(),
		e.companyCmd(), e.workspaceCmd(), e.projectCmd(), e.personaCmd(), e.taskCmd(), e.runCmd(), e.gateCmd(), e.orgCmd(),
		e.costCmd(), e.budgetCmd(), e.flowCmd(), e.authCmd())
	return root
}

func (e *env) client() *client.Client { return client.New(e.url, e.token) }

func (e *env) needCompany() (string, error) {
	if e.company == "" {
		return "", usageError{errors.New("empresa não informada: use --company ou HANKO_COMPANY")}
	}
	return e.company, nil
}

func (e *env) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("codificar JSON: %w", err)
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

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("02/01/2006 15:04")
}

func fmtTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return fmtTime(*t)
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
