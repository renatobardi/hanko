// Package cliharness é o esqueleto comum dos harnesses de CLI que falam um fluxo JSONL no stdout (pi, cursor; ADR-0056):
// monta o ambiente, roda o processo por procrun (local ou ambiente remoto), alimenta um parser linha a linha e
// traduz o desfecho num adapter.Result. O que muda de um CLI para outro é só o argv e o parser.
package cliharness

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Outcome é o que um parser acumulou ao longo do stream.
type Outcome struct {
	SessionRef string
	Summary    string // última mensagem do assistente
	Model      string
	Usage      domain.Usage
	CostUSD    *float64
	// Completed: o CLI sinalizou o fim normal do turno.
	Completed bool
	// Failed: o CLI sinalizou erro (Error é o motivo).
	Failed bool
	Error  string
}

// Parser traduz uma linha de stdout em eventos normalizados.
type Parser interface {
	Feed(line []byte) []adapter.Event
	Outcome() Outcome
}

// Harness descreve um CLI.
type Harness struct {
	// Name é o nome do harness (agent).
	Name string
	// Bin é o executável.
	Bin string
	// NewParser cria o parser de uma run.
	NewParser func() Parser
	// Args monta o argv; dir é o diretório de trabalho (local ou o do alvo remoto).
	Args func(spec adapter.Spec, dir string) ([]string, error)
	// Stdin monta o que vai ao stdin do processo (o prompt).
	Stdin func(spec adapter.Spec) string
	// Resumes diz se Spec.SessionRef retoma uma sessão.
	Resumes bool
	// ProbeArgs é o argv de uma checagem barata (versão); vazio = sem probe.
	ProbeArgs []string
}

// Options configura o adapter.
type Options struct {
	Logger *slog.Logger
	Grace  time.Duration
	// EnvAllow lista variáveis extras do servidor repassadas ao CLI (procrun.HarnessEnv).
	EnvAllow []string
}

// Adapter implementa adapter.Adapter para um Harness.
type Adapter struct {
	h        Harness
	log      *slog.Logger
	grace    time.Duration
	envAllow []string
}

// New devolve o adapter do harness h.
func New(h Harness, o Options) *Adapter {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Adapter{h: h, log: log, grace: o.Grace, envAllow: o.EnvAllow}
}

// Name implementa adapter.Adapter.
func (a *Adapter) Name() string { return a.h.Name }

// ResumesSessions implementa adapter.SessionResumer.
func (a *Adapter) ResumesSessions() bool { return a.h.Resumes }

// Run executa o CLI. Cancelar ctx termina o grupo de processos; o Result parcial volta com erro nil (quem chama
// inspeciona ctx).
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	secrets := adapter.SpecSecrets(spec)
	res, err := a.run(ctx, spec, sink, secrets)
	return adapter.RedactResult(res, secrets...), err
}

func (a *Adapter) run(ctx context.Context, spec adapter.Spec, sink adapter.Sink, secrets []string) (adapter.Result, error) {
	name := a.h.Name
	dir := spec.Dir
	if dir == "" && spec.Exec == nil {
		d, err := os.MkdirTemp("", "hanko-run-*")
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("%s: tempdir: %w", name, err)
		}
		defer os.RemoveAll(d) //nolint:errcheck // limpeza de diretório de rascunho
		dir = d
	}
	extra := map[string]string{}
	for k, v := range adapter.CLIToolsEnv(spec) {
		extra[k] = v
	}
	for k, v := range spec.Env {
		extra[k] = v
	}
	env := procrun.HarnessEnv(os.Environ(), a.envAllow, extra)
	sink = adapter.RedactingSink(sink, secrets...)

	args, err := a.h.Args(spec, dir)
	if err != nil {
		return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("%s: %w", name, err)
	}
	p := a.h.NewParser()
	var tail []string
	var sinkErr error
	emit := func(e adapter.Event) {
		if sinkErr != nil {
			return
		}
		ectx := ctx
		if ctx.Err() != nil {
			var c context.CancelFunc
			ectx, c = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer c()
		}
		if err := sink.Emit(ectx, e); err != nil {
			sinkErr = err
			a.log.Warn(name+": sink falhou", "run_id", spec.RunID, "err", err)
		}
	}

	opts := procrun.Options{Bin: a.h.Bin, Args: args, Dir: dir, Env: env, Stdin: a.h.Stdin(spec), Grace: a.grace}
	if spec.Exec != nil {
		// ambiente remoto (ADR-0037): o diretório é o do alvo e só as variáveis da run vão junto
		if opts.Args, err = a.h.Args(spec, spec.Exec.WorkDir()); err != nil {
			return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("%s: %w", name, err)
		}
		opts.Target, opts.TargetEnv, opts.Dir = spec.Exec, extra, ""
	}
	opts.OnStdout = func(l []byte) {
		for _, e := range p.Feed(l) {
			emit(e)
		}
	}
	opts.OnStderr = func(l []byte) {
		s := string(l)
		if strings.TrimSpace(s) == "" {
			return
		}
		tail = append(tail, s)
		if len(tail) > 5 {
			tail = tail[1:]
		}
		emit(adapter.Event{Kind: domain.EventStderr, Text: s, OccurredAt: time.Now().UTC()})
	}
	pr, perr := procrun.Run(ctx, opts)

	o := p.Outcome()
	res := adapter.Result{ExitCode: pr.ExitCode, SessionRef: o.SessionRef, Summary: o.Summary, Usage: o.Usage, CostUSD: o.CostUSD, Model: o.Model}
	if perr != nil {
		res.IsError, res.Error = true, fmt.Sprintf("não foi possível iniciar %s (%q): %v", name, a.h.Bin, perr)
		return res, fmt.Errorf("%s: %w", name, perr)
	}
	switch {
	case pr.Cancelled:
		res.IsError = true
		res.Error = "execução interrompida: " + ctx.Err().Error()
	case o.Failed:
		res.IsError, res.Error = true, o.Error
	case pr.ExitCode != 0:
		res.IsError = true
		res.Error = fmt.Sprintf("%s terminou com código %d", name, pr.ExitCode)
		if len(tail) > 0 {
			res.Error += ": " + strings.Join(tail, " | ")
		}
	case !o.Completed && o.Summary == "":
		res.IsError, res.Error = true, name+" terminou sem resposta"
	}
	if res.Summary != "" || res.IsError {
		text := res.Summary
		if text == "" {
			text = res.Error
		}
		emit(adapter.Event{Kind: domain.EventResult, Text: text, OccurredAt: time.Now().UTC()})
	}
	if sinkErr != nil && ctx.Err() == nil {
		return res, fmt.Errorf("%s: sink: %w", name, sinkErr)
	}
	return res, nil
}

// Probe confere se o binário existe e responde (ProbeArgs, por exemplo --version), sem gastar modelo. A
// autenticação do CLI só aparece na primeira run: o probe não a verifica.
func (a *Adapter) Probe(ctx context.Context, spec adapter.Spec) adapter.ProbeResult {
	var out adapter.ProbeResult
	if len(a.h.ProbeArgs) == 0 {
		out.Add("binário", true, "sem verificação para este harness")
		return out
	}
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var lines []string
	pr, err := procrun.Run(pctx, procrun.Options{Bin: a.h.Bin, Args: a.h.ProbeArgs, Grace: a.grace,
		Env:      procrun.HarnessEnv(os.Environ(), a.envAllow, spec.Env),
		OnStdout: func(l []byte) { lines = append(lines, string(l)) }})
	switch {
	case err != nil:
		out.Add("binário", false, fmt.Sprintf("não foi possível iniciar %q: %v", a.h.Bin, err))
	case pr.Cancelled:
		out.Add("binário", false, a.h.Bin+" não respondeu em 20 s")
	case pr.ExitCode != 0:
		out.Add("binário", false, fmt.Sprintf("%s %s terminou com código %d", a.h.Bin, strings.Join(a.h.ProbeArgs, " "), pr.ExitCode))
	default:
		first := ""
		if len(lines) > 0 {
			first = adapter.Redact(strings.TrimSpace(lines[0]), adapter.SpecSecrets(spec)...)
		}
		out.Add("binário", true, first)
	}
	return out
}
