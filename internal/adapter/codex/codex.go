// Package codex runs a persona with the Codex CLI (`codex exec --json`), ADR-0004.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Options configures the adapter.
type Options struct {
	Bin    string // default "codex"
	Logger *slog.Logger
	Grace  time.Duration // SIGTERM → SIGKILL; 0 = 5 s
}

// Adapter implements adapter.Adapter for the codex harness.
type Adapter struct {
	bin   string
	log   *slog.Logger
	grace time.Duration
}

// New returns the codex adapter.
func New(o Options) *Adapter {
	bin := o.Bin
	if bin == "" {
		bin = "codex"
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Adapter{bin: bin, log: log, grace: o.Grace}
}

func (a *Adapter) Name() string { return "codex" }

// Config is the codex-specific part of persona.adapter_config.
type Config struct {
	Sandbox   string   `json:"sandbox"` // read-only | workspace-write | danger-full-access; default workspace-write
	ExtraArgs []string `json:"extra_args"`
}

var nonEnv = regexp.MustCompile(`[^A-Za-z0-9]+`)

type mcpServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// mcpOverrides translates http MCP servers into `-c` config overrides. Bearer tokens
// are passed through environment variables, never on the command line.
func mcpOverrides(raw json.RawMessage) (args []string, env map[string]string) {
	if len(raw) == 0 {
		return nil, nil
	}
	var cfg struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return nil, nil
	}
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	env = map[string]string{}
	for _, n := range names {
		var s mcpServer
		if json.Unmarshal(cfg.Servers[n], &s) != nil || s.URL == "" {
			continue
		}
		key := "mcp_servers." + nonEnv.ReplaceAllString(n, "_")
		args = append(args, "-c", fmt.Sprintf("%s.url=%q", key, s.URL))
		hnames := make([]string, 0, len(s.Headers))
		for h := range s.Headers {
			hnames = append(hnames, h)
		}
		sort.Strings(hnames)
		for _, h := range hnames {
			v := s.Headers[h]
			if strings.EqualFold(h, "authorization") && strings.HasPrefix(v, "Bearer ") {
				ev := "HANKO_MCP_TOKEN_" + strings.ToUpper(nonEnv.ReplaceAllString(n, "_"))
				env[ev] = strings.TrimPrefix(v, "Bearer ")
				args = append(args, "-c", fmt.Sprintf("%s.bearer_token_env_var=%q", key, ev))
				continue
			}
			ev := "HANKO_MCP_HDR_" + strings.ToUpper(nonEnv.ReplaceAllString(n+"_"+h, "_"))
			env[ev] = v
			args = append(args, "-c", fmt.Sprintf("%s.env_http_headers.%q=%q", key, h, ev))
		}
	}
	return args, env
}

// BuildArgs returns the CLI arguments for spec.
func BuildArgs(spec adapter.Spec, cfg Config, dir string, mcpArgs []string) []string {
	sandbox := cfg.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	args := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", sandbox}
	if spec.Model != "" {
		args = append(args, "-m", spec.Model)
	}
	if dir != "" {
		args = append(args, "-C", dir)
	}
	args = append(args, mcpArgs...)
	args = append(args, cfg.ExtraArgs...)
	return append(args, "-")
}

// Prompt joins the system instructions and the prompt (codex has no separate system channel).
func Prompt(spec adapter.Spec) string {
	if strings.TrimSpace(spec.System) == "" {
		return spec.Prompt
	}
	return spec.System + "\n\n---\n\n" + spec.Prompt
}

// Run executes the harness. Cancelling ctx terminates the whole process group; the
// partial Result is returned with a nil error (the caller inspects ctx).
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	var cfg Config
	if len(spec.Config) > 0 {
		if err := json.Unmarshal(spec.Config, &cfg); err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("codex: adapter_config: %w", err)
		}
	}
	dir := spec.Dir
	if dir == "" {
		d, err := os.MkdirTemp("", "hanko-run-*")
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("codex: tempdir: %w", err)
		}
		defer os.RemoveAll(d)
		dir = d
	}
	mcpArgs, mcpEnv := mcpOverrides(spec.MCPConfig)
	env := os.Environ()
	for k, v := range mcpEnv {
		env = append(env, k+"="+v)
	}
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	p := newParser(nil)
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
			a.log.Warn("codex: sink falhou", "run_id", spec.RunID, "err", err)
		}
	}

	pr, err := procrun.Run(ctx, procrun.Options{
		Bin: a.bin, Args: BuildArgs(spec, cfg, dir, mcpArgs), Dir: dir, Env: env, Stdin: Prompt(spec), Grace: a.grace,
		OnStdout: func(l []byte) {
			for _, e := range p.Feed(l) {
				emit(e)
			}
		},
		OnStderr: func(l []byte) {
			s := string(l)
			if strings.TrimSpace(s) == "" {
				return
			}
			tail = append(tail, s)
			if len(tail) > 5 {
				tail = tail[1:]
			}
			emit(adapter.Event{Kind: domain.EventStderr, Text: s, OccurredAt: time.Now().UTC()})
		},
	})

	res := adapter.Result{ExitCode: pr.ExitCode, SessionRef: p.threadID, Summary: p.lastMessage, Usage: p.usage}
	if err != nil {
		res.IsError, res.Error = true, err.Error()
		return res, fmt.Errorf("codex: %w", err)
	}
	switch {
	case pr.Cancelled:
		res.IsError = true
		res.Error = "execução interrompida: " + ctx.Err().Error()
	case p.failed:
		res.IsError, res.Error = true, p.errMsg
	case pr.ExitCode != 0:
		res.IsError = true
		res.Error = fmt.Sprintf("codex terminou com código %d", pr.ExitCode)
		if len(tail) > 0 {
			res.Error += ": " + strings.Join(tail, " | ")
		}
	case !p.completed && p.lastMessage == "":
		res.IsError, res.Error = true, "codex terminou sem resposta"
	}
	if res.Summary != "" || res.IsError {
		kind := res.Summary
		if kind == "" {
			kind = res.Error
		}
		emit(adapter.Event{Kind: domain.EventResult, Text: kind, OccurredAt: time.Now().UTC()})
	}
	if sinkErr != nil && ctx.Err() == nil {
		return res, fmt.Errorf("codex: sink: %w", sinkErr)
	}
	return res, nil
}
