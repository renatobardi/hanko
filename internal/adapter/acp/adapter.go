// Package acp runs a persona with any agent that speaks the Agent Client Protocol (ACP, version 1)
// over stdio, as one more harness next to claude, codex and fake (ADR-0023).
//
// Hanko is the ACP client: it starts the agent command from persona.adapter_config as a child
// process, performs initialize, session/new (or session/load to resume), sends one session/prompt
// and follows session/update notifications until the stop reason. The agent may call back with
// session/request_permission and fs/read_text_file / fs/write_text_file; both are answered by
// Hanko under the persona's permission policy and confined to the run directory.
//
// How the prompt is delivered: ACP has no system-prompt field, so the prompt is two text blocks in
// order: the persona instructions plus Hanko protocol (Spec.System), then the task briefing
// (Spec.Prompt). The model (Spec.Model, else adapter_config.model) is applied best effort through
// the agent's advertised model selection (a "model" config option, or session/set_model); when the
// agent offers no such choice, or does not list the model, a warning is logged and the agent keeps
// its default.
package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// ProtocolVersion is the only ACP version this client speaks.
const ProtocolVersion = 1

// Options configures the adapter.
type Options struct {
	Logger *slog.Logger
	// Version is announced to the agent in clientInfo (default "dev").
	Version string
	// Grace between SIGTERM and SIGKILL when the agent is killed; 0 = procrun.DefaultGrace.
	Grace time.Duration
	// CancelWait is how long a cancelled prompt may take to answer with stopReason "cancelled"
	// before the agent is killed; 0 = 3 s.
	CancelWait time.Duration
	// ExitWait is how long the agent gets to exit by itself after its stdin is closed at the end
	// of the run, before it is killed; 0 = 2 s.
	ExitWait time.Duration
	// EnvAllow lists extra server environment variables passed to the agent (see procrun.HarnessEnv).
	EnvAllow []string
	// Policy is the operator's gate (HANKO_ACP_ALLOW / HANKO_ACP_COMMANDS); the zero value refuses to run.
	Policy Policy
	// PolicyFunc, when set, is consulted at every run instead of Policy (instance settings can change without a restart).
	PolicyFunc func() Policy
}

// Adapter implements adapter.Adapter for generic ACP agents.
type Adapter struct {
	log        *slog.Logger
	version    string
	grace      time.Duration
	cancelWait time.Duration
	exitWait   time.Duration
	envAllow   []string
	policy     Policy
	policyFunc func() Policy
}

func (a *Adapter) currentPolicy() Policy {
	if a.policyFunc != nil {
		return a.policyFunc()
	}
	return a.policy
}

// New returns the acp adapter.
func New(o Options) *Adapter {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	a := &Adapter{log: log, version: o.Version, grace: o.Grace, cancelWait: o.CancelWait, exitWait: o.ExitWait, envAllow: o.EnvAllow, policy: o.Policy, policyFunc: o.PolicyFunc}
	if a.version == "" {
		a.version = "dev"
	}
	if a.cancelWait <= 0 {
		a.cancelWait = 3 * time.Second
	}
	if a.exitWait <= 0 {
		a.exitWait = 2 * time.Second
	}
	return a
}

// Name implements adapter.Adapter.
func (a *Adapter) Name() string { return "acp" }

// Run executes the agent. Cancelling ctx asks the agent to stop (session/cancel), answers pending
// permission requests with "cancelled", waits briefly and then kills the process group; the
// partial Result is returned with a nil error (the caller inspects ctx).
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	cfg, err := ParseConfig(spec.Config)
	if err != nil {
		return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("acp: adapter_config: %w", err)
	}
	if err := a.currentPolicy().Check(cfg.Command); err != nil {
		return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("acp: %w", err)
	}
	// The persona's env goes first; the run's own variables (run token, OTel) always win.
	env := map[string]string{}
	for k, v := range cfg.Env {
		env[k] = v
	}
	for k, v := range spec.Env {
		env[k] = v
	}
	sspec := spec
	sspec.Env = env
	secrets := adapter.SpecSecrets(sspec)
	res, err := a.run(ctx, spec, cfg, env, sink, secrets)
	return adapter.RedactResult(res, secrets...), err
}

type procExit struct {
	res procrun.Result
	err error
}

// exitCause is why the connection ended when the process is gone.
type exitCause struct {
	code int
	err  error
}

func (e *exitCause) Error() string {
	if e.err != nil {
		return "acp agent process failed: " + e.err.Error()
	}
	return fmt.Sprintf("acp agent exited with code %d", e.code)
}

func (a *Adapter) run(ctx context.Context, spec adapter.Spec, cfg Config, env map[string]string, sink adapter.Sink, secrets []string) (adapter.Result, error) {
	dir := spec.Dir
	if dir == "" || spec.Exec != nil && !isDir(dir) {
		d, err := os.MkdirTemp("", "hanko-run-*")
		if err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("acp: tempdir: %w", err)
		}
		defer os.RemoveAll(d) //nolint:errcheck // best-effort cleanup of a scratch dir
		dir = d
	}
	files, err := newFileRoot(dir)
	if err != nil {
		return adapter.Result{ExitCode: -1}, fmt.Errorf("acp: %w", err)
	}
	defer files.Close()

	sink = adapter.RedactingSink(sink, secrets...)
	policy := cfg.Policy(spec.ReadOnly)
	s := newSession(ctx, a.log, sink, policy, spec.ReadOnly, files)
	s.guard = spec.ToolGuard
	s.remote = spec.Exec != nil
	lw := newLateWriter()
	c := newConn(lw, s)

	// The process outlives ctx on purpose: cancelling is a conversation (session/cancel), and only
	// when that fails does killProc end the process group.
	procCtx, killProc := context.WithCancel(context.WithoutCancel(ctx))
	defer killProc()
	exited := make(chan procExit, 1)
	s.emit(domain.EventSystem, "iniciando agente ACP: "+cfg.Command, map[string]any{"command": cfg.Command, "args": len(cfg.Args), "permission": string(policy)})
	go func() {
		opts := procrun.Options{
			Bin: cfg.Command, Args: cfg.Args, Dir: files.abs,
			Env:      procrun.HarnessEnv(os.Environ(), a.envAllow, env),
			Grace:    a.grace,
			OnStdin:  lw.set,
			OnStdout: c.Feed,
			OnStderr: func(line []byte) { s.stderr(string(line)) },
		}
		if spec.Exec != nil {
			// ambiente remoto (ADR-0037): só as variáveis da persona e da run vão para o alvo
			opts.Target, opts.TargetEnv, opts.Dir = spec.Exec, env, ""
		}
		pr, perr := procrun.Run(procCtx, opts)
		if perr != nil {
			lw.abort(perr)
		}
		c.Close(&exitCause{code: pr.ExitCode, err: perr})
		exited <- procExit{pr, perr}
	}()

	d := &driver{a: a, cfg: cfg, spec: spec, s: s, c: c, files: files}
	out := d.protocol(ctx)

	// Close stdin (EOF is how an ACP agent learns the client is done) and wait for the exit.
	closed := make(chan struct{})
	go func() { c.Close(nil); close(closed) }()
	killedByUs := false
	var pe procExit
	t := time.NewTimer(a.exitWait)
	select {
	case pe = <-exited:
	case <-t.C:
		killedByUs = true
		killProc()
		pe = <-exited
	}
	t.Stop()
	<-closed
	s.finish()

	if pe.err != nil && pe.res.ExitCode == -1 { // procrun: the process never started
		return adapter.Result{ExitCode: -1, IsError: true, Error: fmt.Sprintf("não foi possível iniciar o agente ACP %q: %v", cfg.Command, pe.err)}, fmt.Errorf("acp: %w", pe.err)
	}

	res := adapter.Result{ExitCode: pe.res.ExitCode, SessionRef: out.sessionID, Summary: s.summary(), CostUSD: s.costUSD(), Usage: out.usage, Model: out.model}
	if killedByUs && out.stop != "" {
		res.ExitCode = 0 // the turn completed; the agent just did not exit on EOF
	}
	switch {
	case ctx.Err() != nil:
		res.IsError = true
		res.Error = "execução interrompida: " + ctx.Err().Error()
	case out.err != nil:
		res.IsError, res.Error = true, out.err.Error()
	default:
		if msg := stopMessage(out.stop); msg != "" {
			res.IsError, res.Error = true, msg
		}
	}
	text := res.Summary
	if res.IsError && text == "" {
		text = res.Error
	}
	s.emit(domain.EventResult, text, map[string]any{
		"stop_reason": out.stop, "session_id": out.sessionID, "context_used": s.usedTokens, "context_size": s.sizeTokens, "is_error": res.IsError,
	})
	if err := s.sinkError(); err != nil && ctx.Err() == nil {
		return res, fmt.Errorf("acp: sink: %w", err)
	}
	return res, nil
}

// stopMessage explains a stop reason that is not a normal end of turn ("" = fine).
func stopMessage(stop string) string {
	switch stop {
	case "end_turn":
		return ""
	case "max_tokens":
		return "o agente parou por limite de tokens (stopReason max_tokens) antes de concluir"
	case "max_turn_requests":
		return "o agente parou por limite de requisições do turno (stopReason max_turn_requests) antes de concluir"
	case "refusal":
		return "o agente recusou-se a continuar (stopReason refusal)"
	case "cancelled":
		return "o agente cancelou o turno (stopReason cancelled)"
	case "":
		return "o agente terminou sem informar stopReason"
	}
	return fmt.Sprintf("o agente parou com stopReason desconhecido %q", stop)
}

// ---- protocol driver ------------------------------------------------------------------------

type driver struct {
	a     *Adapter
	cfg   Config
	spec  adapter.Spec
	s     *session
	c     *conn
	files *fileRoot
}

type outcome struct {
	stop      string
	sessionID string
	model     string
	usage     domain.Usage
	err       error
}

type initResult struct {
	ProtocolVersion   *json.Number `json:"protocolVersion"`
	AgentCapabilities struct {
		LoadSession     bool    `json:"loadSession"`
		McpCapabilities mcpCaps `json:"mcpCapabilities"`
	} `json:"agentCapabilities"`
	AgentInfo struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"agentInfo"`
	AuthMethods []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"authMethods"`
}

type configValue struct {
	Value   string        `json:"value"`
	Name    string        `json:"name"`
	Options []configValue `json:"options"` // a group of values
}

type configOption struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Category string        `json:"category"`
	Options  []configValue `json:"options"`
}

type sessionSetup struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []configOption `json:"configOptions"`
	Models        *struct {
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
	} `json:"models"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		InputTokens      int64 `json:"inputTokens"`
		OutputTokens     int64 `json:"outputTokens"`
		CachedReadTokens int64 `json:"cachedReadTokens"`
		CachedWrite      int64 `json:"cachedWriteTokens"`
	} `json:"usage"`
}

func (d *driver) protocol(ctx context.Context) (out outcome) {
	s, c := d.s, d.c

	// 1. initialize
	ictx, cancel := context.WithTimeout(ctx, d.cfg.initTimeout())
	raw, err := c.Call(ictx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": !d.s.remote, "writeTextFile": !d.s.remote},
			"terminal": false,
		},
		"clientInfo": map[string]string{"name": "hanko", "title": "Hanko", "version": d.a.version},
	})
	cancel()
	if err != nil {
		out.err = d.phaseError(ctx, "initialize", err)
		return out
	}
	var ir initResult
	if err := json.Unmarshal(raw, &ir); err != nil || ir.ProtocolVersion == nil {
		out.err = fmt.Errorf("a resposta ao initialize não é de um agente ACP (sem protocolVersion válido); confirme que %q fala ACP v%d em stdio%s", d.cfg.Command, ProtocolVersion, d.hint())
		return out
	}
	if v, verr := ir.ProtocolVersion.Int64(); verr != nil || v != ProtocolVersion {
		out.err = fmt.Errorf("versão do ACP incompatível: o agente respondeu protocolVersion %s e o Hanko fala a versão %d; use um agente (ou versão) com ACP v%d", ir.ProtocolVersion.String(), ProtocolVersion, ProtocolVersion)
		return out
	}
	name := firstNonEmpty(ir.AgentInfo.Title, ir.AgentInfo.Name, d.cfg.Command)
	s.emit(domain.EventSystem, strings.TrimSpace(fmt.Sprintf("agente ACP conectado: %s %s (protocolo %d)", name, ir.AgentInfo.Version, ProtocolVersion)), json.RawMessage(raw))

	// 2. session/load or session/new
	servers, warns := convertMCP(d.spec.MCPConfig, ir.AgentCapabilities.McpCapabilities)
	for _, w := range warns {
		s.emit(domain.EventSystem, "aviso: "+w, nil)
	}
	cwd := d.files.abs
	if d.spec.Exec != nil {
		cwd = d.spec.Exec.WorkDir()
	}
	var setupRaw json.RawMessage
	if ref := d.spec.SessionRef; ref != "" {
		if ir.AgentCapabilities.LoadSession {
			s.setSessionID(ref)
			s.setReplaying(true)
			lraw, lerr := c.Call(ctx, "session/load", map[string]any{"sessionId": ref, "cwd": cwd, "mcpServers": servers})
			s.setReplaying(false)
			var rerr *rpcError
			switch {
			case lerr == nil:
				out.sessionID, setupRaw = ref, lraw
				s.emit(domain.EventSystem, "sessão retomada: "+ref, nil)
			case errors.As(lerr, &rerr):
				s.setSessionID("")
				s.emit(domain.EventSystem, fmt.Sprintf("aviso: não foi possível retomar a sessão %s (%s); iniciando uma nova", ref, rerr.Message), nil)
			default:
				out.err = d.phaseError(ctx, "session/load", lerr)
				return out
			}
		} else {
			s.emit(domain.EventSystem, "aviso: o agente não anuncia loadSession; iniciando uma nova sessão em vez de retomar "+ref, nil)
		}
	}
	if out.sessionID == "" {
		nraw, nerr := c.Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": servers})
		if nerr != nil {
			out.err = d.sessionError(ctx, nerr, ir)
			return out
		}
		var ss sessionSetup
		if json.Unmarshal(nraw, &ss) != nil || ss.SessionID == "" {
			out.err = errors.New("o agente respondeu a session/new sem sessionId")
			return out
		}
		out.sessionID, setupRaw = ss.SessionID, nraw
		s.setSessionID(ss.SessionID)
	}

	// 3. model (best effort)
	if model := firstNonEmpty(d.spec.Model, d.cfg.Model); model != "" {
		out.model = d.applyModel(ctx, out.sessionID, model, setupRaw)
	}

	// 4. prompt
	var blocks []map[string]string
	if strings.TrimSpace(d.spec.System) != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": d.spec.System})
	}
	if strings.TrimSpace(d.spec.Prompt) != "" {
		blocks = append(blocks, map[string]string{"type": "text", "text": d.spec.Prompt})
	}
	if len(blocks) == 0 {
		out.err = errors.New("a run não tem prompt para enviar ao agente")
		return out
	}
	praw, perr := d.prompt(ctx, out.sessionID, blocks)
	if perr != nil {
		out.err = d.phaseError(ctx, "session/prompt", perr)
		return out
	}
	var pr promptResult
	if err := json.Unmarshal(praw, &pr); err != nil {
		out.err = errors.New("resposta inválida de session/prompt")
		return out
	}
	out.stop = pr.StopReason
	if pr.Usage != nil {
		out.usage = domain.Usage{InputTokens: pr.Usage.InputTokens, OutputTokens: pr.Usage.OutputTokens,
			CacheReadTokens: pr.Usage.CachedReadTokens, CacheCreationTokens: pr.Usage.CachedWrite}
	}
	return out
}

// prompt sends session/prompt and waits for its answer. If ctx ends first, the agent is asked to
// cancel the turn (session/cancel) and pending permission requests are answered "cancelled"; it then
// gets cancelWait to finish with stopReason "cancelled" before the caller kills it.
func (d *driver) prompt(ctx context.Context, sessionID string, blocks []map[string]string) (json.RawMessage, error) {
	type reply struct {
		raw json.RawMessage
		err error
	}
	wait, err := d.c.CallAsync("session/prompt", map[string]any{"sessionId": sessionID, "prompt": blocks})
	if err != nil {
		return nil, err
	}
	ch := make(chan reply, 1)
	go func() {
		raw, err := wait(context.WithoutCancel(ctx))
		ch <- reply{raw, err}
	}()
	select {
	case r := <-ch:
		return r.raw, r.err
	case <-ctx.Done():
	}
	d.s.setCancelling()
	_ = d.c.Notify("session/cancel", map[string]any{"sessionId": sessionID})
	t := time.NewTimer(d.a.cancelWait)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.raw, r.err
	case <-t.C:
		return nil, ctx.Err()
	}
}

// phaseError turns a failed call into the run's error text.
func (d *driver) phaseError(ctx context.Context, phase string, err error) error {
	var rerr *rpcError
	var xc *exitCause
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("interrompido durante %s: %w", phase, ctx.Err())
	case errors.Is(err, context.DeadlineExceeded) && phase == "initialize":
		return fmt.Errorf("o agente não respondeu ao initialize em %s; confirme que %q fala ACP v%d em stdio (não um CLI comum)%s",
			d.cfg.initTimeout(), d.cfg.Command, ProtocolVersion, d.hint())
	case errors.As(err, &rerr):
		return fmt.Errorf("o agente recusou %s: %s (código %d)", phase, rerr.Message, rerr.Code)
	case errors.As(err, &xc):
		return fmt.Errorf("o agente encerrou (%s) durante %s%s", xc.exitText(), phase, d.hint())
	}
	return fmt.Errorf("%s: %w", phase, err)
}

func (e *exitCause) exitText() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("código %d", e.code)
}

// sessionError explains a failed session/new, with a dedicated message when the agent wants
// authentication (interactive authentication is out of scope for Hanko).
func (d *driver) sessionError(ctx context.Context, err error, ir initResult) error {
	var rerr *rpcError
	if errors.As(err, &rerr) && (rerr.Code == -32000 || strings.Contains(strings.ToLower(rerr.Message), "auth")) {
		ids := make([]string, 0, len(ir.AuthMethods))
		for _, m := range ir.AuthMethods {
			ids = append(ids, firstNonEmpty(m.ID, m.Name))
		}
		hint := ""
		if len(ids) > 0 {
			hint = " (métodos anunciados: " + strings.Join(ids, ", ") + ")"
		}
		return fmt.Errorf("o agente exige autenticação%s: %s. O Hanko não faz autenticação interativa; autentique o agente fora do Hanko (por exemplo, a chave de API em adapter_config.env) e tente de novo", hint, rerr.Message)
	}
	return d.phaseError(ctx, "session/new", err)
}

func (d *driver) hint() string {
	var parts []string
	if h := d.s.badLineHint(); h != "" {
		parts = append(parts, h)
	}
	if e := d.s.stderrSummary(); e != "" {
		parts = append(parts, "stderr: "+e)
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, "; ")
}

// applyModel selects the model through what the agent advertises; it never fails the run.
func (d *driver) applyModel(ctx context.Context, sessionID, model string, setupRaw json.RawMessage) string {
	var ss sessionSetup
	_ = json.Unmarshal(setupRaw, &ss)
	match := func(value, name string) bool {
		return strings.EqualFold(value, model) || (name != "" && strings.EqualFold(name, model))
	}
	var avail []string
	for _, co := range ss.ConfigOptions {
		if co.Category != "model" && co.ID != "model" {
			continue
		}
		for _, v := range flattenValues(co.Options) {
			avail = append(avail, v.Value)
			if match(v.Value, v.Name) {
				if _, err := d.c.Call(ctx, "session/set_config_option", map[string]any{"sessionId": sessionID, "configId": co.ID, "value": v.Value}); err != nil {
					d.s.emit(domain.EventSystem, fmt.Sprintf("aviso: o agente recusou o modelo %q (%v); usando o padrão dele", model, err), nil)
					return ""
				}
				return v.Value
			}
		}
	}
	if ss.Models != nil {
		for _, m := range ss.Models.AvailableModels {
			avail = append(avail, m.ModelID)
			if match(m.ModelID, m.Name) {
				if _, err := d.c.Call(ctx, "session/set_model", map[string]any{"sessionId": sessionID, "modelId": m.ModelID}); err != nil {
					d.s.emit(domain.EventSystem, fmt.Sprintf("aviso: o agente recusou o modelo %q (%v); usando o padrão dele", model, err), nil)
					return ""
				}
				return m.ModelID
			}
		}
	}
	if len(avail) == 0 {
		d.s.emit(domain.EventSystem, fmt.Sprintf("aviso: o agente não anuncia escolha de modelo; %q não aplicado (usando o padrão dele)", model), nil)
	} else {
		d.s.emit(domain.EventSystem, fmt.Sprintf("aviso: o modelo %q não está entre os que o agente oferece (%s); usando o padrão dele", model, strings.Join(avail, ", ")), nil)
	}
	return ""
}

func flattenValues(in []configValue) []configValue {
	var out []configValue
	for _, v := range in {
		if len(v.Options) > 0 {
			out = append(out, flattenValues(v.Options)...)
			continue
		}
		if v.Value != "" {
			out = append(out, v)
		}
	}
	return out
}

// ---- stdin plumbing -------------------------------------------------------------------------

// lateWriter is the conn's writer before the process exists: writes wait until procrun hands over
// the stdin pipe, or fail if the process could not start.
type lateWriter struct {
	mu      sync.Mutex
	ready   chan struct{}
	once    sync.Once
	w       io.WriteCloser
	err     error
	closing bool
}

func newLateWriter() *lateWriter { return &lateWriter{ready: make(chan struct{})} }

func (l *lateWriter) set(w io.WriteCloser) {
	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		_ = w.Close()
	} else {
		l.w = w
		l.mu.Unlock()
	}
	l.once.Do(func() { close(l.ready) })
}

func (l *lateWriter) abort(err error) {
	l.mu.Lock()
	if l.w == nil && l.err == nil {
		l.err = err
	}
	l.mu.Unlock()
	l.once.Do(func() { close(l.ready) })
}

func (l *lateWriter) Write(p []byte) (int, error) {
	<-l.ready
	l.mu.Lock()
	w, err := l.w, l.err
	l.mu.Unlock()
	if w == nil {
		if err == nil {
			err = errConnClosed
		}
		return 0, err
	}
	return w.Write(p)
}

func (l *lateWriter) Close() error {
	l.mu.Lock()
	l.closing = true
	w := l.w
	l.mu.Unlock()
	l.once.Do(func() { close(l.ready) })
	if w != nil {
		return w.Close()
	}
	return nil
}

// isDir diz se p é um diretório existente.
func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
