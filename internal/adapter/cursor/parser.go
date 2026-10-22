package cursor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/cliharness"
	"github.com/renatobardi/hanko/internal/domain"
)

const maxToolResultBytes = 4 << 10

// parser traduz o stream de `cursor-agent -p --output-format stream-json` em eventos normalizados. Tipos tratados:
//
//	system/init          {session_id,model}                  → SessionRef, modelo
//	assistant            {message{content[{type:text}]}}     → assistant
//	tool_call/started    {call_id,tool_call{<x>ToolCall{args}}} → tool_use
//	tool_call/completed  {call_id,tool_call{<x>ToolCall{result}}} → tool_result
//	result               {subtype,is_error,result,session_id} → resumo e fim
//
// O CLI não informa tokens nem custo: o Result sai sem eles. Campos e tipos desconhecidos são ignorados. Não é seguro
// para uso concorrente.
type parser struct {
	now func() time.Time

	sessionID string
	model     string
	last      string
	result    string
	completed bool
	failed    bool
	errMsg    string
}

func newParser() *parser { return &parser{now: time.Now} }

// NewParser é o construtor usado pelo harness.
func NewParser() cliharness.Parser { return newParser() }

type curLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	CallID    string          `json:"call_id"`
	IsError   bool            `json:"is_error"`
	Result    string          `json:"result"`
	Message   *curMessage     `json:"message"`
	ToolCall  json.RawMessage `json:"tool_call"`
	Error     string          `json:"error"`
}

type curMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
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
	var l curLine
	if raw[0] != '{' || json.Unmarshal(raw, &l) != nil || l.Type == "" {
		return []adapter.Event{p.ev(domain.EventStdout, string(raw), nil)}
	}
	if l.SessionID != "" && p.sessionID == "" {
		p.sessionID = l.SessionID
	}
	payload := json.RawMessage(append([]byte(nil), raw...))
	switch l.Type {
	case "system":
		if l.Subtype != "init" {
			return nil
		}
		p.model = l.Model
		return []adapter.Event{p.ev(domain.EventSystem, strings.TrimSpace("sessão iniciada "+l.SessionID+" "+l.Model), payload)}
	case "assistant":
		if l.Message == nil {
			return nil
		}
		if t := messageText(l.Message.Content); t != "" {
			p.last = t
			return []adapter.Event{p.ev(domain.EventAssistant, t, payload)}
		}
	case "tool_call":
		name, body := toolOf(l.ToolCall)
		switch l.Subtype {
		case "started":
			return []adapter.Event{p.ev(domain.EventToolUse, name+argsLabel(body), payload)}
		case "completed":
			return []adapter.Event{p.ev(domain.EventToolResult, name+": "+clipUTF8(resultOf(body), maxToolResultBytes), payload)}
		}
	case "result":
		p.completed = true
		p.result = strings.TrimSpace(l.Result)
		if l.IsError || (l.Subtype != "" && l.Subtype != "success") {
			p.failed = true
			p.errMsg = firstNonEmpty(p.result, l.Error, "o cursor-agent terminou com erro ("+l.Subtype+")")
		}
	case "error":
		p.failed, p.errMsg = true, firstNonEmpty(l.Error, "erro do cursor-agent")
		return []adapter.Event{p.ev(domain.EventSystem, "erro: "+p.errMsg, payload)}
	}
	return nil
}

// Outcome implementa cliharness.Parser. O resumo é o texto final do evento result; sem ele, a última mensagem.
func (p *parser) Outcome() cliharness.Outcome {
	sum := p.result
	if sum == "" || p.failed {
		sum = p.last
	}
	return cliharness.Outcome{SessionRef: p.sessionID, Summary: sum, Model: p.model, Completed: p.completed, Failed: p.failed, Error: p.errMsg}
}

func messageText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, ""))
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

// toolOf devolve o nome (a chave do objeto, sem o sufixo ToolCall) e o corpo de um tool_call.
func toolOf(raw json.RawMessage) (string, map[string]json.RawMessage) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return "tool", nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	k := keys[0]
	var body map[string]json.RawMessage
	_ = json.Unmarshal(m[k], &body)
	name := strings.TrimSuffix(k, "ToolCall")
	if k == "function" {
		var f struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(m[k], &f) == nil && f.Name != "" {
			name = f.Name
		}
	}
	return name, body
}

func argsLabel(body map[string]json.RawMessage) string {
	a := strings.TrimSpace(string(body["args"]))
	if a == "" || a == "null" {
		return ""
	}
	return " " + clipUTF8(a, 300)
}

func resultOf(body map[string]json.RawMessage) string {
	r, ok := body["result"]
	if !ok {
		return ""
	}
	var wrap map[string]json.RawMessage
	if json.Unmarshal(r, &wrap) == nil {
		if rej, ok := wrap["error"]; ok {
			return "erro: " + string(rej)
		}
		if s, ok := wrap["success"]; ok {
			return string(s)
		}
	}
	return string(r)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
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
