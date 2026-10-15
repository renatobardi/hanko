// Package adapter runs a persona's agent harness (claude, codex, acp, fake) for one run
// and streams normalized events back to Hanko.
package adapter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Spec is everything an adapter needs to execute one run.
type Spec struct {
	RunID     domain.ID
	CompanyID domain.ID
	PersonaID domain.ID
	TaskID    domain.ID
	TaskKey   string
	// ConversationID is set (and TaskID zero) on a chat run, which belongs to a conversation, not a task.
	ConversationID domain.ID
	Agent          string // claude | codex | acp | fake
	Model          string
	Dir            string // working directory (workspace path); empty = temp dir
	System         string // persona instructions + Hanko protocol (appended system prompt)
	Prompt         string // task + thread + memory briefing
	SessionRef     string // previous agent session id to resume, if any
	Env            map[string]string
	MCPConfig      json.RawMessage // {"mcpServers":{...}} written to a temp file for the harness
	// Skills são as skills habilitadas da persona que o harness carrega nativamente (hoje só o claude, via
	// um diretório temporário da run; ADR-0029). Nos demais harnesses o índice vai em System e o corpo
	// sai pela tool MCP hanko_skill_get, então o campo fica vazio.
	Skills  []domain.Skill
	Config  json.RawMessage // persona.adapter_config
	Timeout time.Duration
	// ReadOnly forces the harness into its read-only mode (review runs), ignoring any
	// permissive adapter_config (permission mode, sandbox, extra args).
	ReadOnly bool
}

// Event is one normalized log entry emitted while the agent runs.
type Event struct {
	Kind       domain.RunEventKind
	Text       string
	Payload    json.RawMessage
	OccurredAt time.Time
}

// Sink receives events in order. Implementations must be safe to call from one goroutine.
type Sink interface {
	Emit(ctx context.Context, e Event) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(ctx context.Context, e Event) error

func (f SinkFunc) Emit(ctx context.Context, e Event) error { return f(ctx, e) }

// Result is what the adapter reports when the agent exits.
type Result struct {
	ExitCode   int
	SessionRef string
	Summary    string   // final assistant message
	CostUSD    *float64 // reported by the harness when available
	Usage      domain.Usage
	Model      string // model actually used, when reported
	IsError    bool
	Error      string
}

// Adapter executes a run. Run blocks until the agent exits or ctx is cancelled.
type Adapter interface {
	Name() string
	Run(ctx context.Context, spec Spec, sink Sink) (Result, error)
}

// SessionResumer is implemented by adapters that can resume the harness session named by Spec.SessionRef
// (a chat run resumes the session of the previous run of its conversation).
type SessionResumer interface{ ResumesSessions() bool }

// ResumesSessions reports whether a can resume Spec.SessionRef.
func ResumesSessions(a Adapter) bool {
	r, ok := a.(SessionResumer)
	return ok && r.ResumesSessions()
}

// Registry resolves adapters by agent name.
type Registry map[string]Adapter

func (r Registry) Get(name string) (Adapter, bool) { a, ok := r[name]; return a, ok }
