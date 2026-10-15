// Package fake is a deterministic in-process adapter for demos and tests: no process,
// no network, no cost.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/adapter"
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

// Config is read from persona.adapter_config.
type Config struct {
	DelayMS *int   `json:"delay_ms"` // overrides Options.Delay
	Fail    bool   `json:"fail"`     // makes the run report an error
	Summary string `json:"summary"`  // overrides the final answer (e.g. a reviewer verdict)
}

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
	if cfg.Fail {
		steps[len(steps)-1] = adapter.Event{Kind: domain.EventAssistant, Text: "Falha simulada."}
		res.Summary, res.IsError, res.Error, res.ExitCode = "", true, "falha simulada pelo adapter fake", 1
	}

	for i, e := range steps {
		if i > 0 && delay > 0 {
			select {
			case <-ctx.Done():
				return a.cancelled(res), nil
			case <-time.After(delay):
			}
		}
		if ctx.Err() != nil {
			return a.cancelled(res), nil
		}
		e.OccurredAt = time.Now().UTC()
		if err := sink.Emit(ctx, e); err != nil {
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
