// Package claude runs a persona with the Claude Code CLI in print mode
// (`claude -p --output-format stream-json --verbose`), ADR-0004.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Options configures the adapter.
type Options struct {
	Bin    string // default "claude"
	Logger *slog.Logger
	// Grace between SIGTERM and SIGKILL on cancel; 0 = 5 s.
	Grace time.Duration
	// EnvAllow lista variáveis extras do ambiente do servidor repassadas ao harness
	// (nomes exatos ou prefixos terminados em "*"). Ver procrun.HarnessEnv.
	EnvAllow []string
}

// Adapter implements adapter.Adapter for the claude harness.
type Adapter struct {
	bin      string
	log      *slog.Logger
	grace    time.Duration
	envAllow []string
}

// New returns the claude adapter.
func New(o Options) *Adapter {
	bin := o.Bin
	if bin == "" {
		bin = "claude"
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Adapter{bin: bin, log: log, grace: o.Grace, envAllow: o.EnvAllow}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (a *Adapter) Name() string { return "claude" }

// ResumesSessions: the claude harness resumes a session with --resume.
func (a *Adapter) ResumesSessions() bool { return true }

// Config is the claude-specific part of persona.adapter_config.
type Config struct {
	PermissionMode string   `json:"permission_mode"`
	ExtraArgs      []string `json:"extra_args"`
}

// BuildArgs returns the CLI arguments for spec; mcpFile is the temp MCP config path ("" = none) and
// skillsDir the temp directory whose .claude/skills the harness loads through --add-dir ("" = none).
func BuildArgs(spec adapter.Spec, cfg Config, mcpFile, skillsDir string) []string {
	mode := cfg.PermissionMode
	if mode == "" {
		mode = "acceptEdits"
	}
	if spec.ReadOnly {
		mode = "plan"
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", mode}
	if spec.ReadOnly {
		args = append(args, "--disallowedTools", "Edit", "Write", "NotebookEdit")
	}
	if m := cliModel(spec.Model); m != "" {
		args = append(args, "--model", m)
	}
	if spec.System != "" {
		args = append(args, "--append-system-prompt", spec.System)
	}
	if mcpFile != "" {
		args = append(args, "--mcp-config", mcpFile)
		if !spec.ReadOnly {
			// Headless (-p) nega tool MCP sem permissão explícita: libera só o servidor do próprio Hanko
			// (o token da run já limita o escopo). Revisão somente leitura não recebe a liberação.
			args = append(args, "--allowedTools", "mcp__hanko")
		}
	}
	if skillsDir != "" {
		args = append(args, "--add-dir", skillsDir)
	}
	if spec.SessionRef != "" {
		args = append(args, "--resume", spec.SessionRef)
	}
	if spec.ReadOnly {
		return args // persona extra_args could re-enable writes
	}
	return append(args, cfg.ExtraArgs...)
}

// Run executes the harness. Cancelling ctx terminates the whole process group; the
// partial Result is returned with a nil error (the caller inspects ctx).
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	secrets := adapter.SpecSecrets(spec)
	res, err := a.run(ctx, spec, sink, secrets)
	return adapter.RedactResult(res, secrets...), err
}

func (a *Adapter) run(ctx context.Context, spec adapter.Spec, sink adapter.Sink, secrets []string) (adapter.Result, error) {
	var cfg Config
	if len(spec.Config) > 0 {
		if err := json.Unmarshal(spec.Config, &cfg); err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: adapter_config: %w", err)
		}
	}

	dir := spec.Dir
	if dir == "" {
		d, err := os.MkdirTemp("", "hanko-run-*")
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: tempdir: %w", err)
		}
		defer os.RemoveAll(d)
		dir = d
	}

	var mcpFile string
	if len(spec.MCPConfig) > 0 && strings.TrimSpace(string(spec.MCPConfig)) != "" {
		f, err := os.CreateTemp("", "hanko-mcp-*.json")
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: mcp config: %w", err)
		}
		mcpFile = f.Name()
		defer os.Remove(mcpFile)
		_ = f.Chmod(0o600)
		_, werr := f.Write(spec.MCPConfig)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: mcp config: %w", werr)
		}
	}

	var skillsDir string
	if len(spec.Skills) > 0 && !spec.ReadOnly {
		d, err := WriteSkills(spec.Skills)
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: skills: %w", err)
		}
		skillsDir = d
		defer os.RemoveAll(d)
	}

	env := procrun.HarnessEnv(os.Environ(), a.envAllow, spec.Env)
	sink = adapter.RedactingSink(sink, secrets...)

	p := newParser(nil)
	var stderrTail []string
	var sinkErr error
	emit := func(e adapter.Event) {
		if sinkErr != nil {
			return
		}
		// A dying ctx must not lose the tail of the log: emit with a detached context.
		ectx := ctx
		if ctx.Err() != nil {
			var c context.CancelFunc
			ectx, c = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer c()
		}
		if err := sink.Emit(ectx, e); err != nil {
			sinkErr = err
			a.log.Warn("claude: sink falhou", "run_id", spec.RunID, "err", err)
		}
	}

	grace := a.grace
	pr, err := procrun.Run(ctx, procrun.Options{
		Bin: a.bin, Args: BuildArgs(spec, cfg, mcpFile, skillsDir), Dir: dir, Env: env, Stdin: spec.Prompt, Grace: grace,
		OnStdout: func(line []byte) {
			for _, e := range p.Feed(line) {
				emit(e)
			}
		},
		OnStderr: func(line []byte) {
			s := string(line)
			if strings.TrimSpace(s) == "" {
				return
			}
			stderrTail = append(stderrTail, s)
			if len(stderrTail) > 5 {
				stderrTail = stderrTail[1:]
			}
			emit(adapter.Event{Kind: domain.EventStderr, Text: s, OccurredAt: time.Now().UTC()})
		},
	})
	res := p.result
	res.ExitCode = pr.ExitCode
	if res.SessionRef == "" {
		res.SessionRef = p.sessionID
	}
	if res.Model == "" {
		res.Model = p.model
	}
	if err != nil {
		res.IsError, res.Error = true, err.Error()
		return res, fmt.Errorf("claude: %w", err)
	}
	switch {
	case pr.Cancelled:
		res.IsError = true
		if res.Error == "" {
			res.Error = "execução interrompida: " + ctx.Err().Error()
		}
	case pr.ExitCode != 0:
		res.IsError = true
		if res.Error == "" {
			res.Error = fmt.Sprintf("claude terminou com código %d", pr.ExitCode)
			if len(stderrTail) > 0 {
				res.Error += ": " + strings.Join(stderrTail, " | ")
			}
		}
	case !p.gotResult:
		res.IsError = true
		res.Error = "claude terminou sem emitir resultado"
	}
	if sinkErr != nil && ctx.Err() == nil {
		return res, fmt.Errorf("claude: sink: %w", sinkErr)
	}
	return res, nil
}

// cliModel traduz o nome de família usado no Hanko (claude-haiku, claude-sonnet,
// claude-opus — também a chave da tabela de preços) para o alias que o CLI aceita.
// IDs completos (claude-sonnet-4-5, …) e aliases passam intactos.
func cliModel(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "claude-haiku":
		return "haiku"
	case "claude-sonnet":
		return "sonnet"
	case "claude-opus":
		return "opus"
	}
	return strings.TrimSpace(m)
}
