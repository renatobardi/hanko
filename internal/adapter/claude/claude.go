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
	// Env são variáveis só desta persona (ex.: ANTHROPIC_BASE_URL/ANTHROPIC_AUTH_TOKEN/ANTHROPIC_MODEL para apontar
	// o harness a um servidor compatível com a API Anthropic). As variáveis da própria run (run token, OTel) vencem.
	Env map[string]string `json:"env"`
}

// BuildArgs returns the CLI arguments for spec; mcpFile is the temp MCP config path ("" = none) and
// skillsDir the temp directory whose .claude/skills the harness loads through --add-dir ("" = none).
func BuildArgs(spec adapter.Spec, cfg Config, mcpFile, skillsDir string) []string {
	return BuildArgsWith(spec, cfg, mcpFile, skillsDir, "")
}

// BuildArgsWith é BuildArgs com o arquivo de settings do hook PreToolUse da política de tools (ADR-0053; "" = sem hook).
func BuildArgsWith(spec adapter.Spec, cfg Config, mcpFile, skillsDir, settingsFile string) []string {
	mode := cfg.PermissionMode
	if mode == "" {
		mode = "acceptEdits"
	}
	if spec.ReadOnly || spec.PlanOnly {
		mode = "plan"
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", mode}
	if spec.ReadOnly || spec.PlanOnly {
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
	if settingsFile != "" {
		args = append(args, "--settings", settingsFile)
	}
	if spec.SessionRef != "" {
		args = append(args, "--resume", spec.SessionRef)
	}
	if spec.ReadOnly || spec.PlanOnly {
		return args // persona extra_args could re-enable writes
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
	// A env da persona entra primeiro; as variáveis da run sempre vencem. Tudo o que parece credencial
	// (inclusive o da persona) entra na redação de segredos.
	env := map[string]string{}
	for k, v := range cfg.Env {
		env[k] = v
	}
	for k, v := range spec.Env {
		env[k] = v
	}
	spec.Env = env
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
	// Num ambiente remoto (ADR-0037) os arquivos auxiliares vão para o diretório que chega ao alvo e os
	// argumentos levam o caminho de lá.
	tmpBase, remote := "", func(p string) string { return p }
	if spec.Exec != nil {
		tmpBase, _ = spec.Exec.AuxDir()
		remote = func(p string) string { return adapter.RemotePath(spec.Exec, p) }
	}
	if dir == "" && spec.Exec == nil {
		d, err := os.MkdirTemp("", "hanko-run-*")
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: tempdir: %w", err)
		}
		defer os.RemoveAll(d)
		dir = d
	}

	var mcpFile string
	if len(spec.MCPConfig) > 0 && strings.TrimSpace(string(spec.MCPConfig)) != "" {
		f, err := os.CreateTemp(tmpBase, "hanko-mcp-*.json")
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
		d, err := WriteSkillsIn(tmpBase, spec.Skills)
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: skills: %w", err)
		}
		skillsDir = d
		defer os.RemoveAll(d)
	}

	// Hook PreToolUse da política de tools (ADR-0053): o claude chama `hanko hook pretooluse` antes de cada tool nativa e
	// o Hanko decide (liberar, recusar ou esperar um gate). Só local: num ambiente remoto o executável não está no alvo.
	var settingsFile string
	if spec.ToolHookBin != "" && !spec.ReadOnly {
		if spec.Exec != nil {
			emitSystem(ctx, sink, "política de tools: o hook do claude não vale em ambiente remoto; as tools nativas seguem o modo de permissão da persona")
		} else {
			f, err := writeHookSettings(tmpBase, spec.ToolHookBin)
			if err != nil {
				return adapter.Result{ExitCode: -1}, fmt.Errorf("claude: hook settings: %w", err)
			}
			settingsFile = f
			defer os.Remove(settingsFile)
		}
	}

	env := procrun.HarnessEnv(os.Environ(), a.envAllow, spec.Env)
	sink = adapter.RedactingSink(sink, secrets...)
	opts := procrun.Options{Bin: a.bin, Dir: dir, Env: env, Stdin: spec.Prompt, Grace: a.grace}
	if spec.Exec != nil {
		// no alvo valem só as variáveis da run e da persona; o ambiente deste servidor não vai junto
		opts.Target, opts.TargetEnv, opts.Dir = spec.Exec, spec.Env, ""
	}
	argMCP, argSkills := mcpFile, skillsDir
	if mcpFile != "" {
		argMCP = remote(mcpFile)
	}
	if skillsDir != "" {
		argSkills = remote(skillsDir)
	}
	opts.Args = BuildArgsWith(spec, cfg, argMCP, argSkills, settingsFile)

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

	opts.OnStdout = func(line []byte) {
		for _, e := range p.Feed(line) {
			emit(e)
		}
	}
	opts.OnStderr = func(line []byte) {
		s := string(line)
		if strings.TrimSpace(s) == "" {
			return
		}
		stderrTail = append(stderrTail, s)
		if len(stderrTail) > 5 {
			stderrTail = stderrTail[1:]
		}
		emit(adapter.Event{Kind: domain.EventStderr, Text: s, OccurredAt: time.Now().UTC()})
	}
	pr, err := procrun.Run(ctx, opts)
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

// hookTimeoutSec é o prazo do hook: cobre a espera de um gate de chamada nativa (10 min) com folga.
const hookTimeoutSec = 660

// hookSettings monta o settings do claude com o hook PreToolUse da política de tools.
func hookSettings(bin string) ([]byte, error) {
	cmd := shellQuote(bin) + " hook pretooluse"
	return json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "*",
		"hooks":   []any{map[string]any{"type": "command", "command": cmd, "timeout": hookTimeoutSec}},
	}}}})
}

func writeHookSettings(dir, bin string) (string, error) {
	raw, err := hookSettings(bin)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "hanko-hooks-*.json")
	if err != nil {
		return "", err
	}
	_ = f.Chmod(0o600)
	_, werr := f.Write(raw)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return f.Name(), nil
}

// shellQuote protege o caminho do executável para o shell que roda o hook.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func emitSystem(ctx context.Context, sink adapter.Sink, text string) {
	_ = sink.Emit(ctx, adapter.Event{Kind: domain.EventSystem, Text: text, OccurredAt: time.Now().UTC()})
}
