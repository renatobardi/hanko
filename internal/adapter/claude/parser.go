package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

const (
	maxToolResultBytes = 4 << 10
	maxInputSummary    = 200
)

// parser turns the harness' stream-json lines into normalized events and
// accumulates the final Result. It is not safe for concurrent use.
type parser struct {
	now func() time.Time

	sessionID string
	model     string

	gotResult bool
	// results counts the result lines seen (a steered session ends one turn per message).
	results int
	result  adapter.Result
}

func newParser(now func() time.Time) *parser {
	if now == nil {
		now = time.Now
	}
	return &parser{now: now}
}

type streamLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	Message   json.RawMessage `json:"message"`

	// result
	IsError      bool     `json:"is_error"`
	Result       string   `json:"result"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	Usage        *usage   `json:"usage"`
	Errors       []string `json:"errors"`
	// result: tools que o harness negou por falta de permissão (diagnóstico)
	PermissionDenials []struct {
		ToolName string `json:"tool_name"`
	} `json:"permission_denials"`
	// init: estado de conexão de cada servidor MCP
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
}

type usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type block struct {
	ID        string          `json:"id,omitempty"` // id da chamada de tool: casa tool_use com tool_result (desfecho incerto)
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Feed parses one stdout line. Non-JSON lines become stdout events; unknown JSON
// types are ignored.
func (p *parser) Feed(line []byte) []adapter.Event {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	var l streamLine
	if line[0] != '{' || json.Unmarshal(line, &l) != nil || l.Type == "" {
		return []adapter.Event{p.ev(domain.EventStdout, string(line), nil)}
	}
	raw := json.RawMessage(append([]byte(nil), line...))
	switch l.Type {
	case "system":
		if l.Subtype != "init" {
			return nil
		}
		if l.SessionID != "" {
			p.sessionID = l.SessionID
		}
		if l.Model != "" {
			p.model = l.Model
		}
		txt := "sessão iniciada"
		if l.SessionID != "" {
			txt += " " + l.SessionID
		}
		if l.Model != "" {
			txt += " (" + l.Model + ")"
		}
		if len(l.MCPServers) > 0 {
			var ms []string
			for _, m := range l.MCPServers {
				ms = append(ms, m.Name+"="+m.Status)
			}
			txt += " · mcp: " + strings.Join(ms, ", ")
		}
		ev := p.ev(domain.EventSystem, txt, raw)
		ev.SessionRef = l.SessionID
		return []adapter.Event{ev}
	case "assistant":
		return p.assistant(l, raw)
	case "user":
		return p.user(l)
	case "result":
		return p.resultLine(l, raw)
	}
	return nil
}

func (p *parser) assistant(l streamLine, raw json.RawMessage) []adapter.Event {
	var out []adapter.Event
	for _, b := range blocks(l.Message) {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, p.ev(domain.EventAssistant, b.Text, nil))
			}
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				out = append(out, p.ev(domain.EventThinking, b.Thinking, nil))
			}
		case "tool_use":
			pl, _ := json.Marshal(b)
			txt := b.Name
			if s := summarizeInput(b.Input); s != "" {
				txt += ": " + s
			}
			out = append(out, p.ev(domain.EventToolUse, txt, pl))
		}
	}
	return out
}

func (p *parser) user(l streamLine) []adapter.Event {
	var out []adapter.Event
	for _, b := range blocks(l.Message) {
		if b.Type != "tool_result" {
			continue
		}
		txt := truncate(toolResultText(b.Content), maxToolResultBytes)
		if b.IsError {
			txt = "erro: " + txt
		}
		pl, _ := json.Marshal(map[string]any{"tool_use_id": b.ToolUseID, "is_error": b.IsError})
		out = append(out, p.ev(domain.EventToolResult, txt, pl))
	}
	return out
}

func (p *parser) resultLine(l streamLine, raw json.RawMessage) []adapter.Event {
	p.gotResult = true
	p.results++
	r := adapter.Result{Summary: l.Result, IsError: l.IsError, CostUSD: l.TotalCostUSD, Model: p.model}
	if l.Usage != nil {
		r.Usage = domain.Usage{
			InputTokens: l.Usage.InputTokens, OutputTokens: l.Usage.OutputTokens,
			CacheReadTokens: l.Usage.CacheReadTokens, CacheCreationTokens: l.Usage.CacheCreationTokens,
		}
	}
	if l.SessionID != "" {
		p.sessionID = l.SessionID
	}
	r.SessionRef = p.sessionID
	if l.IsError {
		r.Error = l.Result
		if r.Error == "" {
			r.Error = strings.Join(l.Errors, "; ")
		}
		if r.Error == "" {
			r.Error = "agente reportou erro (" + l.Subtype + ")"
		}
	}
	p.result = r
	txt := l.Result
	if txt == "" && l.IsError {
		txt = r.Error
	}
	evs := []adapter.Event{p.ev(domain.EventResult, txt, raw)}
	if len(l.PermissionDenials) > 0 {
		seen := map[string]bool{}
		var names []string
		for _, d := range l.PermissionDenials {
			if d.ToolName != "" && !seen[d.ToolName] {
				seen[d.ToolName] = true
				names = append(names, d.ToolName)
			}
		}
		evs = append([]adapter.Event{p.ev(domain.EventSystem, "tools negadas por permissão: "+strings.Join(names, ", "), nil)}, evs...)
	}
	return evs
}

func (p *parser) ev(k domain.RunEventKind, text string, payload json.RawMessage) adapter.Event {
	return adapter.Event{Kind: k, Text: text, Payload: payload, OccurredAt: p.now().UTC()}
}

// blocks decodes message.content, which is either a string or a list of blocks.
func blocks(msg json.RawMessage) []block {
	if len(msg) == 0 {
		return nil
	}
	var m message
	if json.Unmarshal(msg, &m) != nil || len(m.Content) == 0 {
		return nil
	}
	var bs []block
	if json.Unmarshal(m.Content, &bs) == nil {
		return bs
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil && s != "" {
		return []block{{Type: "text", Text: s}}
	}
	return nil
}

func toolResultText(c json.RawMessage) string {
	if len(c) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(c, &s) == nil {
		return s
	}
	var bs []block
	if json.Unmarshal(c, &bs) == nil {
		var parts []string
		for _, b := range bs {
			if b.Type == "text" || b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(c)
}

// summarizeInput picks the most telling field of a tool input, or compact JSON.
func summarizeInput(in json.RawMessage) string {
	if len(in) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(in, &m) != nil || len(m) == 0 {
		return truncate(string(in), maxInputSummary)
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
		if v, ok := m[k].(string); ok && v != "" {
			return truncate(oneLine(v), maxInputSummary)
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		b, _ := json.Marshal(m[k])
		fmt.Fprintf(&sb, "%s=%s", k, b)
	}
	return truncate(oneLine(sb.String()), maxInputSummary)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncate cuts s to at most max bytes on a rune boundary, appending an ellipsis marker.
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
