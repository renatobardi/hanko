package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

const maxToolResultBytes = 4 << 10

// parser turns `codex exec --json` JSONL into normalized events and accumulates the
// Result. Unknown event types and fields are ignored. Not safe for concurrent use.
type parser struct {
	now func() time.Time

	threadID    string
	lastMessage string
	usage       domain.Usage
	sawUsage    bool
	failed      bool
	errMsg      string
	started     map[string]bool // item ids already announced as tool_use
	completed   bool            // saw turn.completed
}

func newParser(now func() time.Time) *parser {
	if now == nil {
		now = time.Now
	}
	return &parser{now: now, started: map[string]bool{}}
}

type line struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     *item           `json:"item"`
	Usage    *usage          `json:"usage"`
	Error    json.RawMessage `json:"error"`
	Message  string          `json:"message"`
}

type usage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
}

type item struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Changes          []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
	Query string `json:"query"`
}

// Feed parses one stdout line.
func (p *parser) Feed(raw []byte) []adapter.Event {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var l line
	if raw[0] != '{' || json.Unmarshal(raw, &l) != nil || l.Type == "" {
		return []adapter.Event{p.ev(domain.EventStdout, string(raw), nil)}
	}
	payload := json.RawMessage(append([]byte(nil), raw...))
	switch l.Type {
	case "thread.started":
		p.threadID = l.ThreadID
		return []adapter.Event{p.ev(domain.EventSystem, "sessão iniciada "+l.ThreadID, payload)}
	case "item.started", "item.updated":
		if l.Item == nil || l.Type == "item.updated" {
			return nil
		}
		return p.itemStarted(l.Item, payload)
	case "item.completed":
		if l.Item == nil {
			return nil
		}
		return p.itemCompleted(l.Item, payload)
	case "turn.completed":
		p.completed = true
		if l.Usage != nil {
			p.sawUsage = true
			p.usage.InputTokens += l.Usage.InputTokens
			p.usage.CacheReadTokens += l.Usage.CachedInputTokens
			p.usage.OutputTokens += l.Usage.OutputTokens
		}
		return nil
	case "turn.failed", "error":
		msg := l.Message
		if m := errorMessage(l.Error); m != "" {
			msg = m
		}
		if msg == "" {
			msg = l.Type
		}
		p.failed = true
		p.errMsg = msg
		return []adapter.Event{p.ev(domain.EventSystem, "erro: "+msg, payload)}
	}
	return nil
}

func (p *parser) itemStarted(it *item, payload json.RawMessage) []adapter.Event {
	txt, ok := toolText(it)
	if !ok || p.started[it.ID] {
		return nil
	}
	p.started[it.ID] = true
	return []adapter.Event{p.ev(domain.EventToolUse, txt, payload)}
}

func (p *parser) itemCompleted(it *item, payload json.RawMessage) []adapter.Event {
	switch it.Type {
	case "agent_message":
		if strings.TrimSpace(it.Text) == "" {
			return nil
		}
		p.lastMessage = it.Text
		return []adapter.Event{p.ev(domain.EventAssistant, it.Text, nil)}
	case "reasoning":
		if strings.TrimSpace(it.Text) == "" {
			return nil
		}
		return []adapter.Event{p.ev(domain.EventThinking, it.Text, nil)}
	case "error":
		if it.Text != "" {
			return []adapter.Event{p.ev(domain.EventSystem, "erro: "+it.Text, payload)}
		}
		return nil
	}
	use, ok := toolText(it)
	if !ok {
		return nil
	}
	var out []adapter.Event
	if !p.started[it.ID] {
		out = append(out, p.ev(domain.EventToolUse, use, payload))
	}
	delete(p.started, it.ID)
	res := truncate(it.AggregatedOutput, maxToolResultBytes)
	if it.ExitCode != nil {
		res = fmt.Sprintf("[exit %d] %s", *it.ExitCode, res)
	}
	if it.Type != "command_execution" && res == "" {
		res = it.Status
	}
	pl, _ := json.Marshal(map[string]any{"item_id": it.ID, "type": it.Type, "exit_code": it.ExitCode, "status": it.Status})
	return append(out, p.ev(domain.EventToolResult, strings.TrimSpace(res), pl))
}

// toolText describes an item that represents a tool invocation.
func toolText(it *item) (string, bool) {
	switch it.Type {
	case "command_execution":
		return it.Command, true
	case "mcp_tool_call":
		t := it.Server + "." + it.Tool
		if len(it.Arguments) > 0 && string(it.Arguments) != "null" {
			t += " " + truncate(string(it.Arguments), 200)
		}
		return t, true
	case "file_change":
		var parts []string
		for _, c := range it.Changes {
			parts = append(parts, c.Kind+" "+c.Path)
		}
		return "file_change: " + strings.Join(parts, ", "), true
	case "web_search":
		return "web_search: " + it.Query, true
	}
	return "", false
}

func errorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Message
	}
	return string(raw)
}

func (p *parser) ev(k domain.RunEventKind, text string, payload json.RawMessage) adapter.Event {
	return adapter.Event{Kind: k, Text: text, Payload: payload, OccurredAt: p.now().UTC()}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncado]"
}
