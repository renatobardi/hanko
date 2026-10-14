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
	// (key = server name). Nil when the provider has nothing to inject.
	AgentMCP(s Scope, runID string) map[string]json.RawMessage
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
func (Noop) AgentMCP(Scope, string) map[string]json.RawMessage            { return nil }
func (Noop) Ping(context.Context) error                                   { return nil }
