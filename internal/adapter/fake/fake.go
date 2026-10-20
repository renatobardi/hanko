// Package fake is a deterministic in-process adapter for demos and tests: no process,
// no network, no cost.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// DefaultDelay is the pause between emitted events.
const DefaultDelay = 50 * time.Millisecond

// Options configures the adapter.
type Options struct {
	Delay time.Duration // between events; 0 = DefaultDelay; negative = none
}

// Adapter implements adapter.Adapter without spawning anything.
type Adapter struct{ delay time.Duration }

// New returns the fake adapter.
func New(o Options) *Adapter {
	d := o.Delay
	switch {
	case d == 0:
		d = DefaultDelay
	case d < 0:
		d = 0
	}
	return &Adapter{delay: d}
}

func (*Adapter) Name() string { return "fake" }

// ResumesSessions: the fake pretends to resume sessions so chat flows can be exercised end to end.
func (*Adapter) ResumesSessions() bool { return true }

// SteerMode: the fake takes an operator message while it runs, so the injection path can be proved end to end.
func (*Adapter) SteerMode() domain.SteerMode { return domain.SteerInject }

// Config is read from persona.adapter_config.
type Config struct {
	DelayMS *int   `json:"delay_ms"` // overrides Options.Delay
	Fail    bool   `json:"fail"`     // makes the run report an error
	Summary string `json:"summary"`  // overrides the final answer (e.g. a reviewer verdict)
	// HoldForSteerMS: before the last step, wait up to this long for an operator message (steer) when none arrived yet.
	HoldForSteerMS int `json:"hold_for_steer_ms"`
	// Touch grava um arquivo com este nome (só o nome, sem diretório) no diretório de trabalho da run; no
	// ambiente remoto (ADR-0037) ele é gravado lá e volta pela sincronização.
	Touch string `json:"touch"`
	// Commit, com Touch, faz um commit com esta mensagem no diretório de trabalho (um worktree Git, ADR-0054). Só
	// local; o autor vem de GIT_AUTHOR_* do ambiente da run.
	Commit string `json:"commit"`
	// RemoteSleepSec segura o comando no ambiente remoto por estes segundos (0 a 60), para exercitar o
	// cancelamento; não vale localmente.
	RemoteSleepSec int `json:"remote_sleep_sec"`
	// Gate pede uma decisão humana pela tool MCP hanko_gate_request antes de terminar (jornadas e2e, ADR-0058).
	Gate *GateConfig `json:"gate"`
}

// maxRemoteSleep é o teto de Config.RemoteSleepSec.
const maxRemoteSleep = 60

// Run emits a fixed script of events and returns a deterministic Result.
func (a *Adapter) Run(ctx context.Context, spec adapter.Spec, sink adapter.Sink) (adapter.Result, error) {
	var cfg Config
	if len(spec.Config) > 0 {
		if err := json.Unmarshal(spec.Config, &cfg); err != nil {
			return adapter.Result{ExitCode: -1}, fmt.Errorf("fake: adapter_config: %w", err)
		}
	}
	delay := a.delay
	if cfg.DelayMS != nil {
		delay = time.Duration(*cfg.DelayMS) * time.Millisecond
	}
	key := spec.TaskKey
	if key == "" {
		key = "task"
	}
	session := "fake-" + spec.RunID.String()
	summary := fmt.Sprintf("Fake concluiu %s: análise registrada. Próximo passo: revisar o resultado.", key)
	chat := spec.ConversationID != (domain.ID{})
	if chat {
		key = "conversa"
		summary = "Fake respondeu na conversa: resposta registrada."
		if spec.SessionRef != "" {
			summary = "Fake respondeu na conversa (sessão retomada): resposta registrada."
		}
	} else if spec.SessionRef != "" {
		summary = fmt.Sprintf("Fake concluiu %s (sessão retomada): análise registrada. Próximo passo: revisar o resultado.", key)
	}
	if cfg.Summary != "" {
		summary = cfg.Summary
	}
	toolInput := json.RawMessage(fmt.Sprintf(`{"name":"hanko_task_get","input":{"task":%q}}`, key))

	steps := []adapter.Event{
		{Kind: domain.EventSystem, Text: "sessão iniciada " + session + " (fake)"},
		{Kind: domain.EventAssistant, Text: fmt.Sprintf("Analisando %s…", key)},
		{Kind: domain.EventToolUse, Text: "hanko_task_get: " + key, Payload: toolInput},
		{Kind: domain.EventToolResult, Text: "ok: " + key},
		{Kind: domain.EventAssistant, Text: summary},
	}
	res := adapter.Result{SessionRef: session, Model: "fake", Summary: summary}
	cost := 0.0
	res.CostUSD = &cost
	if cfg.Touch != "" && (cfg.Touch != filepath.Base(cfg.Touch) || cfg.Touch == "." || cfg.Touch == "..") {
		return adapter.Result{ExitCode: -1, IsError: true, Error: "fake: touch aceita só um nome de arquivo"}, fmt.Errorf("fake: touch inválido: %q", cfg.Touch)
	}
	if spec.Exec != nil {
		// No ambiente remoto o fake roda de verdade um comando no alvo (sem rede nem modelo): mostra onde
		// rodou e, com touch, grava o arquivo lá.
		where, err := a.remote(ctx, spec, cfg)
		if ctx.Err() != nil {
			return a.cancelled(res), nil
		}
		if err != nil {
			return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, err
		}
		steps[0].Text += " no ambiente " + spec.Exec.Describe() + ": " + where
	} else if cfg.Touch != "" && spec.Dir != "" {
		if err := os.WriteFile(filepath.Join(spec.Dir, cfg.Touch), []byte("fake\n"), 0o600); err != nil {
			return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, fmt.Errorf("fake: touch: %w", err)
		}
		if cfg.Commit != "" {
			if err := gitCommit(ctx, spec, cfg.Commit); err != nil {
				return adapter.Result{ExitCode: -1, IsError: true, Error: err.Error()}, err
			}
		}
	}
	if cfg.Fail {
		steps[len(steps)-1] = adapter.Event{Kind: domain.EventAssistant, Text: "Falha simulada."}
		res.Summary, res.IsError, res.Error, res.ExitCode = "", true, "falha simulada pelo adapter fake", 1
	}

	var steered []string
	// take applies one operator message: the log shows it and the final answer carries the new direction.
	take := func(m adapter.Steer) error {
		steered = append(steered, m.Text)
		err := sink.Emit(ctx, adapter.Event{Kind: domain.EventAssistant, Text: "Rumo alterado pelo operador: " + m.Text, OccurredAt: time.Now().UTC()})
		m.Done(err)
		return err
	}
	// pause waits d (or until ctx ends), taking steer messages that arrive meanwhile; it returns false when ctx ended.
	pause := func(d time.Duration) (bool, error) {
		t := time.NewTimer(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return false, nil
			case m := <-spec.Steer:
				if err := take(m); err != nil {
					return true, err
				}
			case <-t.C:
				return true, nil
			}
		}
	}
	for i, e := range steps {
		if i > 0 && delay > 0 {
			ok, err := pause(delay)
			if err != nil {
				return res, fmt.Errorf("fake: sink: %w", err)
			}
			if !ok {
				return a.cancelled(res), nil
			}
		}
		if i == len(steps)-1 && cfg.HoldForSteerMS > 0 && len(steered) == 0 && spec.Steer != nil {
			ok, err := pause(time.Duration(cfg.HoldForSteerMS) * time.Millisecond)
			if err != nil {
				return res, fmt.Errorf("fake: sink: %w", err)
			}
			if !ok {
				return a.cancelled(res), nil
			}
		}
		if ctx.Err() != nil {
			return a.cancelled(res), nil
		}
		if i == len(steps)-1 && len(steered) > 0 && !res.IsError {
			res.Summary += " Rumo alterado pelo operador: " + strings.Join(steered, " | ")
			e.Text = res.Summary
		}
		e.OccurredAt = time.Now().UTC()
		if i == 0 {
			e.SessionRef = session
		}
		if err := sink.Emit(ctx, e); err != nil {
			return res, fmt.Errorf("fake: sink: %w", err)
		}
	}
	if cfg.Gate != nil && !cfg.Fail {
		out, err := requestGate(ctx, spec, *cfg.Gate, http.DefaultClient)
		if err != nil {
			_ = sink.Emit(ctx, adapter.Event{Kind: domain.EventStderr, Text: err.Error(), OccurredAt: time.Now().UTC()})
			return adapter.Result{ExitCode: 1, IsError: true, Error: err.Error(), SessionRef: session, Model: "fake"}, nil
		}
		if err := sink.Emit(ctx, adapter.Event{Kind: domain.EventToolResult, Text: out, OccurredAt: time.Now().UTC()}); err != nil {
			return res, fmt.Errorf("fake: sink: %w", err)
		}
	}
	text := res.Summary
	if res.IsError {
		text = res.Error
	}
	if err := sink.Emit(ctx, adapter.Event{Kind: domain.EventResult, Text: text, OccurredAt: time.Now().UTC()}); err != nil {
		return res, fmt.Errorf("fake: sink: %w", err)
	}
	return res, nil
}

func (*Adapter) cancelled(r adapter.Result) adapter.Result {
	r.Summary, r.IsError, r.ExitCode = "", true, -1
	r.Error = "execução interrompida"
	return r
}

// remote roda o comando do fake no alvo e devolve o diretório onde ele rodou.
func (a *Adapter) remote(ctx context.Context, spec adapter.Spec, cfg Config) (string, error) {
	script := "pwd"
	if cfg.Touch != "" {
		script += ` && printf 'fake\n' > "$1"`
	}
	if n := min(max(cfg.RemoteSleepSec, 0), maxRemoteSleep); n > 0 {
		script += fmt.Sprintf(" && sleep %d", n)
	}
	var out, errOut []string
	pr, err := procrun.Run(ctx, procrun.Options{
		Bin: "sh", Args: []string{"-c", script, "sh", cfg.Touch}, Target: spec.Exec, TargetEnv: spec.Env,
		OnStdout: func(l []byte) { out = append(out, string(l)) },
		OnStderr: func(l []byte) { errOut = append(errOut, string(l)) },
	})
	if err != nil {
		return "", fmt.Errorf("fake: %w", err)
	}
	if pr.ExitCode != 0 && !pr.Cancelled {
		return "", fmt.Errorf("fake: o comando no ambiente terminou com código %d: %s", pr.ExitCode, strings.Join(errOut, " | "))
	}
	if len(out) == 0 {
		return "", nil
	}
	return strings.TrimSpace(out[0]), nil
}

// gitCommit registra tudo o que está solto em spec.Dir num commit (o que uma persona faz no worktree da task).
func gitCommit(ctx context.Context, spec adapter.Spec, message string) error {
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	for k, v := range spec.Env {
		if strings.HasPrefix(k, "GIT_") {
			env = append(env, k+"="+v)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "--no-verify", "-m", message}} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)...) //nolint:gosec // argv fixo; só testes e demos
		cmd.Dir, cmd.Env = spec.Dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("fake: git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
