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
}

// Adapter implements adapter.Adapter for the claude harness.
type Adapter struct {
	bin   string
	log   *slog.Logger
	grace time.Duration
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
	return &Adapter{bin: bin, log: log, grace: o.Grace}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (a *Adapter) Name() string { return "claude" }

// Config is the claude-specific part of persona.adapter_config.
type Config struct {
	PermissionMode string   `json:"permission_mode"`
	ExtraArgs      []string `json:"extra_args"`
}

// BuildArgs returns the CLI arguments for spec; mcpFile is the temp MCP config path ("" = none).
func BuildArgs(spec adapter.Spec, cfg Config, mcpFile string) []string {
	mode := cfg.PermissionMode
	if mode == "" {
		mode = "acceptEdits"
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", mode}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.System != "" {
		args = append(args, "--append-system-prompt", spec.System)
	}
	if mcpFile != "" {
		args = append(args, "--mcp-config", mcpFile)
	}
	if spec.SessionRef != "" {
		args = append(args, "--resume", spec.SessionRef)
	}
	return append(args, cfg.ExtraArgs...)
}

// Run executes the harness. Cancelling ctx terminates the whole process group; the
// partial Result is returned with a nil error (the caller inspects ctx).
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
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

	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

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
		Bin: a.bin, Args: BuildArgs(spec, cfg, mcpFile), Dir: dir, Env: env, Stdin: spec.Prompt, Grace: grace,
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
