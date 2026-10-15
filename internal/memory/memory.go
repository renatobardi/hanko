// Package memory integrates the memory provider (ADR-0015). ai-memory is the
// default provider; "none" disables memory. The core never depends on it being up.
package memory

import (
	"context"
	"encoding/json"
)

// Scope identifies a memory project. Workspace = the Hanko company slug,
// Project = the Hanko workspace slug (or "default" when the task has none).
type Scope struct {
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
	Cwd       string `json:"cwd,omitempty"`
}

// Fact is something the control plane records in memory (via memory_outbox).
type Fact struct {
	Kind  string   `json:"kind"` // task.created, task.closed, run.finished, comment.human, ...
	Path  string   `json:"path"` // wiki page path, e.g. hanko/runs/2026-10-14-HNK-3-<run>.md
	Title string   `json:"title"`
	Body  string   `json:"body"` // markdown
	Tags  []string `json:"tags,omitempty"`
}

// Hit is one memory search result (ADR-0024).
type Hit struct {
	Path    string  `json:"path"`    // wiki page path, when the provider has one
	Title   string  `json:"title"`   // page title (may be empty)
	Snippet string  `json:"snippet"` // matching text, plain (never HTML)
	Score   float64 `json:"score,omitempty"`
}

// Provider is implemented by ai-memory and by Noop.
type Provider interface {
	Name() string
	// Briefing returns a bounded markdown context block for a new run (may be empty).
	Briefing(ctx context.Context, s Scope) (string, error)
	// AcceptHandoff fetches and acknowledges the pending handoff for the scope (may be empty).
	AcceptHandoff(ctx context.Context, s Scope) (string, error)
	// Record writes a control-plane fact. Must be idempotent on Fact.Path.
	Record(ctx context.Context, s Scope, f Fact) error
	// FinalizeSession closes an agent session so the provider builds summary + handoff.
	FinalizeSession(ctx context.Context, s Scope, agent, sessionID string) error
	// AgentMCP returns the MCP server entry to inject into the agent config for this run
	// (key = server name). Nil when the provider has nothing to inject. The entry must
	// never carry the provider's own credentials: agents authenticate with their run token
	// against Hanko's memory proxy, which injects the provider credentials server-side.
	AgentMCP(s Scope, runID, runToken string) map[string]json.RawMessage
	// Search looks the query up in the company's memory (Scope.Workspace = company slug; Scope.Project is
	// ignored: the search spans every project of the company). Noop returns nothing.
	Search(ctx context.Context, s Scope, query string, limit int) ([]Hit, error)
	// Ping checks availability.
	Ping(ctx context.Context) error
}

// Noop is the "none" provider.
type Noop struct{}

func (Noop) Name() string                                                 { return "none" }
func (Noop) Briefing(context.Context, Scope) (string, error)              { return "", nil }
func (Noop) AcceptHandoff(context.Context, Scope) (string, error)         { return "", nil }
func (Noop) Record(context.Context, Scope, Fact) error                    { return nil }
func (Noop) FinalizeSession(context.Context, Scope, string, string) error { return nil }
func (Noop) AgentMCP(Scope, string, string) map[string]json.RawMessage    { return nil }
func (Noop) Search(context.Context, Scope, string, int) ([]Hit, error)    { return nil, nil }
func (Noop) Ping(context.Context) error                                   { return nil }
