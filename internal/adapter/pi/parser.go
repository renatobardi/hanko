package pi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/cliharness"
	"github.com/renatobardi/hanko/internal/domain"
)

const maxToolResultBytes = 4 << 10

// parser traduz o stream de `pi --mode json -p` (um evento JSON por linha) em eventos normalizados e acumula o
// resultado. Eventos e campos desconhecidos são ignorados; linhas que não são JSON viram stdout. Os tipos tratados:
//
//	session              {id}                       → SessionRef
//	message_end          {message{role,content[],usage,model,stopReason,errorMessage}}
//	tool_execution_start {toolCallId,toolName,args}  → tool_use
//	tool_execution_end   {toolCallId,toolName,result,isError} → tool_result
//	agent_end            fim normal do turno
//
// Não é seguro para uso concorrente.
type parser struct {
	now func() time.Time

	sessionID string
	last      string
	model     string
	usage     domain.Usage
	cost      float64
	sawCost   bool
	failed    bool
	errMsg    string
	completed bool
	toolNames map[string]string // toolCallId → nome, para o tool_result
}

func newParser() *parser { return &parser{now: time.Now, toolNames: map[string]string{}} }

// NewParser é o construtor usado pelo harness.
func NewParser() cliharness.Parser { return newParser() }

type piLine struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Message    *piMessage      `json:"message"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"isError"`
	Error      string          `json:"error"`
}

type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"` // string ou lista de blocos
	Model        string          `json:"model"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        *struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
		Cost       *struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

type piBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Arguments json.RawMessage `json:"arguments"`
}

func (p *parser) ev(kind domain.RunEventKind, text string, payload json.RawMessage) adapter.Event {
	return adapter.Event{Kind: kind, Text: text, Payload: payload, OccurredAt: p.now().UTC()}
}

// Feed processa uma linha de stdout.
func (p *parser) Feed(raw []byte) []adapter.Event {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var l piLine
	if raw[0] != '{' || json.Unmarshal(raw, &l) != nil || l.Type == "" {
		return []adapter.Event{p.ev(domain.EventStdout, string(raw), nil)}
	}
	payload := json.RawMessage(append([]byte(nil), raw...))
	switch l.Type {
	case "session":
		p.sessionID = l.ID
		return []adapter.Event{p.ev(domain.EventSystem, "sessão iniciada "+l.ID, payload)}
	case "message_end":
		if l.Message == nil || l.Message.Role != "assistant" {
			return nil
		}
		return p.assistantMessage(l.Message, payload)
	case "tool_execution_start":
		p.toolNames[l.ToolCallID] = l.ToolName
		return []adapter.Event{p.ev(domain.EventToolUse, toolLabel(l.ToolName, l.Args), payload)}
	case "tool_execution_end":
		name := l.ToolName
		if name == "" {
			name = p.toolNames[l.ToolCallID]
		}
		text := clipUTF8(resultText(l.Result), maxToolResultBytes)
		if l.IsError {
			text = "erro: " + text
		}
		return []adapter.Event{p.ev(domain.EventToolResult, name+": "+text, payload)}
	case "agent_end":
		p.completed = true
		return nil
	case "error":
		msg := l.Error
		if msg == "" {
			msg = "erro do pi"
		}
		p.failed, p.errMsg = true, msg
		return []adapter.Event{p.ev(domain.EventSystem, "erro: "+msg, payload)}
	}
	return nil
}

func (p *parser) assistantMessage(m *piMessage, payload json.RawMessage) []adapter.Event {
	var evs []adapter.Event
	if m.Model != "" {
		p.model = m.Model
	}
	if u := m.Usage; u != nil {
		p.usage.InputTokens += u.Input
		p.usage.OutputTokens += u.Output
		p.usage.CacheReadTokens += u.CacheRead
		p.usage.CacheCreationTokens += u.CacheWrite
		if u.Cost != nil {
			p.cost += u.Cost.Total
			p.sawCost = true
		}
	}
	var text strings.Builder
	var blocks []piBlock
	var plain string
	switch {
	case json.Unmarshal(m.Content, &blocks) == nil:
	case json.Unmarshal(m.Content, &plain) == nil && plain != "":
		blocks = []piBlock{{Type: "text", Text: plain}}
	}
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				evs = append(evs, p.ev(domain.EventThinking, b.Thinking, nil))
			}
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				text.WriteString(b.Text)
			}
		}
	}
	if t := strings.TrimSpace(text.String()); t != "" {
		p.last = t
		evs = append(evs, p.ev(domain.EventAssistant, t, payload))
	}
	if m.StopReason == "error" || m.StopReason == "aborted" {
		msg := m.ErrorMessage
		if msg == "" {
			msg = "o pi parou com stopReason " + m.StopReason
		}
		p.failed, p.errMsg = true, msg
		evs = append(evs, p.ev(domain.EventSystem, "erro: "+msg, nil))
	}
	return evs
}

// Outcome implementa cliharness.Parser.
func (p *parser) Outcome() cliharness.Outcome {
	o := cliharness.Outcome{SessionRef: p.sessionID, Summary: p.last, Model: p.model, Usage: p.usage,
		Completed: p.completed, Failed: p.failed, Error: p.errMsg}
	if p.sawCost && p.cost > 0 {
		c := p.cost
		o.CostUSD = &c
	}
	return o
}

func toolLabel(name string, args json.RawMessage) string {
	a := strings.TrimSpace(string(args))
	if a == "" || a == "null" {
		return name
	}
	return name + " " + clipUTF8(a, 300)
}

// resultText extrai o texto de um resultado de tool: {content:[{type:text,text}]}, string ou JSON qualquer.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var r struct {
		Content []piBlock `json:"content"`
	}
	if json.Unmarshal(raw, &r) == nil && len(r.Content) > 0 {
		var parts []string
		for _, b := range r.Content {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("…[truncado, %d bytes]", len(s))
}
