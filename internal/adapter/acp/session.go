package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

const (
	maxToolResultBytes = 4 << 10
	maxPayloadInput    = 4 << 10
	maxBadLineEvents   = 50
	maxBadLineBytes    = 2 << 10
	// flushBytes bounds how much agent text is held before an event is emitted, so a very long
	// message still shows up in the log while it is being produced.
	flushBytes = 16 << 10
)

// session is the Hanko side of one ACP run: it answers what the agent asks (permissions, file
// access), turns session/update notifications into run-log events and keeps what the Result needs.
// All methods run on the connection's dispatcher goroutine except where noted.
type session struct {
	log    *slog.Logger
	sink   adapter.Sink
	runCtx context.Context //nolint:containedctx // the run's context, used only to emit events

	policy   Permission
	readOnly bool
	files    *fileRoot

	emitMu  sync.Mutex // sink is single-goroutine; stderr arrives on another one
	sinkErr error

	mu         sync.Mutex // guards the fields below (the driver goroutine reads them too)
	sessionID  string
	replaying  bool
	cancelling bool

	// text aggregation
	buf       chunkBuf
	agentMsg  strings.Builder
	agentMsgI string // messageId of agentMsg
	agentOpen bool
	lastAgent string

	tools map[string]*toolState

	// usage
	usedTokens, sizeTokens int64
	cost, costBase         *float64
	warnedCurrency         bool

	badLines   int
	badSample  string
	lastPlan   string
	stderrTail []string
}

type chunkBuf struct {
	kind  domain.RunEventKind
	msgID string
	sb    strings.Builder
}

type toolState struct {
	title    string
	kind     string
	status   string
	content  []string
	output   string
	reported bool
}

func newSession(ctx context.Context, log *slog.Logger, sink adapter.Sink, policy Permission, readOnly bool, files *fileRoot) *session {
	return &session{runCtx: ctx, log: log, sink: sink, policy: policy, readOnly: readOnly, files: files, tools: map[string]*toolState{}}
}

// emit sends one event. A dying run context must not lose the tail of the log.
func (s *session) emit(kind domain.RunEventKind, text string, payload any) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if s.sinkErr != nil {
		return
	}
	ctx := s.runCtx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	var raw json.RawMessage
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			raw = b
		}
	}
	if err := s.sink.Emit(ctx, adapter.Event{Kind: kind, Text: text, Payload: raw, OccurredAt: time.Now().UTC()}); err != nil {
		s.sinkErr = err
		s.log.Warn("acp: sink falhou", "err", err)
	}
}

// announceSession logs the session id and hands it to the service right away (adapter.Event.SessionRef), so a run
// killed mid-way can still be resumed with session/load.
func (s *session) announceSession(id string) {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if s.sinkErr != nil {
		return
	}
	if err := s.sink.Emit(s.runCtx, adapter.Event{Kind: domain.EventSystem, Text: "sessão ACP " + id, SessionRef: id, OccurredAt: time.Now().UTC()}); err != nil {
		s.sinkErr = err
		s.log.Warn("acp: sink falhou", "err", err)
	}
}

func (s *session) sinkError() error {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	return s.sinkErr
}

func (s *session) setSessionID(id string) {
	s.mu.Lock()
	s.sessionID = id
	s.mu.Unlock()
}

func (s *session) setReplaying(v bool) {
	s.mu.Lock()
	s.replaying = v
	s.mu.Unlock()
}

func (s *session) setCancelling(v bool) {
	s.mu.Lock()
	s.cancelling = v
	s.mu.Unlock()
}

func (s *session) isCancelling() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelling
}

// stderr records and emits one stderr line of the agent process.
func (s *session) stderr(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	s.mu.Lock()
	s.stderrTail = append(s.stderrTail, line)
	if len(s.stderrTail) > 5 {
		s.stderrTail = s.stderrTail[1:]
	}
	s.mu.Unlock()
	s.emit(domain.EventStderr, line, nil)
}

func (s *session) stderrSummary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.stderrTail, " | ")
}

// BadLine implements handler: stdout that is not JSON-RPC is kept in the log, bounded.
func (s *session) BadLine(line []byte, reason string) {
	s.mu.Lock()
	s.badLines++
	n := s.badLines
	if s.badSample == "" {
		s.badSample = truncate(oneLine(string(line)), 120)
	}
	s.mu.Unlock()
	switch {
	case n <= maxBadLineEvents:
		s.emit(domain.EventStdout, truncate(string(line), maxBadLineBytes), map[string]string{"invalid": reason})
	case n == maxBadLineEvents+1:
		s.emit(domain.EventSystem, "mais linhas inválidas (fora do protocolo ACP) omitidas", nil)
	}
}

func (s *session) badLineHint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.badLines == 0 {
		return ""
	}
	return fmt.Sprintf("o agente escreveu %d linha(s) na saída que não são JSON-RPC (ex.: %q)", s.badLines, s.badSample)
}

// HandleRequest implements handler for agent-to-client requests.
func (s *session) HandleRequest(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "session/request_permission":
		return s.requestPermission(params)
	case "fs/read_text_file":
		return s.readTextFile(params)
	case "fs/write_text_file":
		return s.writeTextFile(params)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not supported by this client: " + method}
}

// HandleNotification implements handler.
func (s *session) HandleNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return // $/cancel_request and anything unknown: nothing to do for a fire-and-forget peer
	}
	s.sessionUpdate(params)
}

// ---- permissions ----------------------------------------------------------------------------

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionParams struct {
	SessionID string `json:"sessionId"`
	ToolCall  struct {
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Kind       string          `json:"kind"`
		RawInput   json.RawMessage `json:"rawInput"`
	} `json:"toolCall"`
	Options []permissionOption `json:"options"`
}

func (s *session) requestPermission(raw json.RawMessage) (any, *rpcError) {
	var p permissionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid session/request_permission params: " + err.Error()}
	}
	s.flushText()
	title := p.ToolCall.Title
	if title == "" {
		title = p.ToolCall.ToolCallID
	}
	if s.isCancelling() {
		s.emit(domain.EventSystem, "permissão cancelada (execução interrompida): "+title, map[string]any{"tool_call_id": p.ToolCall.ToolCallID, "outcome": "cancelled"})
		return cancelledOutcome(), nil
	}
	allow := decide(s.policy, p.ToolCall.Kind)
	opt, ok := pickOption(p.Options, allow)
	verdict := "negada"
	if allow {
		verdict = "concedida"
	}
	pl := map[string]any{"tool_call_id": p.ToolCall.ToolCallID, "kind": p.ToolCall.Kind, "policy": string(s.policy), "allow": allow}
	if !ok {
		pl["outcome"] = "cancelled"
		s.emit(domain.EventSystem, fmt.Sprintf("permissão %s sem opção compatível, respondida como cancelada: %s [%s]", verdict, title, kindLabel(p.ToolCall.Kind)), pl)
		return cancelledOutcome(), nil
	}
	pl["option_id"] = opt.OptionID
	s.emit(domain.EventSystem, fmt.Sprintf("permissão %s (política %s): %s [%s]", verdict, s.policy, title, kindLabel(p.ToolCall.Kind)), pl)
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": opt.OptionID}}, nil
}

func cancelledOutcome() map[string]any {
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

func kindLabel(k string) string {
	if k == "" {
		return "other"
	}
	return k
}

// decide applies the policy to a tool-call kind.
func decide(p Permission, kind string) bool {
	switch p {
	case PermAllow:
		return true
	case PermReject:
		return false
	}
	switch kind {
	case "read", "search", "think", "fetch":
		return true
	}
	return false // edit, delete, move, execute, switch_mode, other and anything unknown
}

// pickOption chooses the option to answer with: the once-only variant is preferred over the
// persistent one so a decision never outlives the request.
func pickOption(opts []permissionOption, allow bool) (permissionOption, bool) {
	once, always := "reject_once", "reject_always"
	if allow {
		once, always = "allow_once", "allow_always"
	}
	var fallback *permissionOption
	for i := range opts {
		switch opts[i].Kind {
		case once:
			return opts[i], true
		case always:
			if fallback == nil {
				fallback = &opts[i]
			}
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return permissionOption{}, false
}

// ---- session/update -------------------------------------------------------------------------

type updateEnvelope struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Resource *struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource"`
}

func (b contentBlock) plain() string {
	switch b.Type {
	case "text", "":
		return b.Text
	case "image", "audio":
		return "[" + b.Type + "]"
	case "resource_link":
		return "[link: " + firstNonEmpty(b.Name, b.URI) + "]"
	case "resource":
		if b.Resource != nil {
			if b.Resource.Text != "" {
				return b.Resource.Text
			}
			return "[recurso: " + b.Resource.URI + "]"
		}
	}
	return ""
}

type updateBody struct {
	Kind      string          `json:"sessionUpdate"`
	Content   json.RawMessage `json:"content"` // chunks: a block; tool_call(_update): a list of tool content
	MessageID string          `json:"messageId"`

	ToolCallID string            `json:"toolCallId"`
	Title      string            `json:"title"`
	Name       string            `json:"name"`
	ToolKind   string            `json:"kind"`
	Status     string            `json:"status"`
	Locations  []json.RawMessage `json:"locations"`
	RawInput   json.RawMessage   `json:"rawInput"`
	RawOutput  json.RawMessage   `json:"rawOutput"`

	Entries []struct {
		Content  string `json:"content"`
		Priority string `json:"priority"`
		Status   string `json:"status"`
	} `json:"entries"`

	Used int64 `json:"used"`
	Size int64 `json:"size"`
	Cost *struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"cost"`

	Severity    string `json:"severity"`
	Description string `json:"description"`
}

func (s *session) sessionUpdate(raw json.RawMessage) {
	var env updateEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Update) == 0 {
		return
	}
	s.mu.Lock()
	sid, replay := s.sessionID, s.replaying
	s.mu.Unlock()
	if sid != "" && env.SessionID != "" && env.SessionID != sid {
		return
	}
	var u updateBody
	if err := json.Unmarshal(env.Update, &u); err != nil {
		return
	}
	if replay {
		// History replay of session/load: not part of this run's log. Only the accumulated cost
		// matters (it is the baseline the run's own cost is measured from).
		if u.Kind == "usage_update" {
			s.usage(u, true)
		}
		return
	}
	switch u.Kind {
	case "agent_message_chunk":
		s.chunk(domain.EventAssistant, u)
	case "agent_thought_chunk":
		s.closeAgent()
		s.chunk(domain.EventThinking, u)
	case "tool_call":
		s.toolCall(u)
	case "tool_call_update":
		s.toolUpdate(u)
	case "plan":
		s.plan(u)
	case "usage_update":
		s.usage(u, false)
	case "notice":
		s.flushText()
		s.closeAgent()
		txt := u.Title
		if u.Description != "" {
			txt += " — " + u.Description
		}
		s.emit(domain.EventSystem, fmt.Sprintf("aviso do agente (%s): %s", firstNonEmpty(u.Severity, "info"), txt), nil)
	}
	// user_message_chunk, available_commands_update, current_mode_update, config_option_update,
	// session_info_update and anything unknown are intentionally ignored.
}

// ---- text aggregation -----------------------------------------------------------------------

func (s *session) chunk(kind domain.RunEventKind, u updateBody) {
	var b contentBlock
	if len(u.Content) == 0 || json.Unmarshal(u.Content, &b) != nil {
		return
	}
	text := b.plain()
	if text == "" {
		return
	}
	if s.buf.sb.Len() > 0 && (s.buf.kind != kind || (u.MessageID != "" && s.buf.msgID != "" && u.MessageID != s.buf.msgID)) {
		s.flushText()
	}
	s.buf.kind = kind
	if u.MessageID != "" {
		s.buf.msgID = u.MessageID
	}
	s.buf.sb.WriteString(text)
	if kind == domain.EventAssistant {
		if !s.agentOpen || (u.MessageID != "" && s.agentMsgI != "" && u.MessageID != s.agentMsgI) {
			s.closeAgent()
			s.agentMsg.Reset()
			s.agentOpen = true
		}
		if u.MessageID != "" {
			s.agentMsgI = u.MessageID
		}
		s.agentMsg.WriteString(text)
	}
	if s.buf.sb.Len() >= flushBytes {
		s.flushText()
	}
}

// flushText emits the aggregated text as one event.
func (s *session) flushText() {
	if s.buf.sb.Len() == 0 {
		return
	}
	text := s.buf.sb.String()
	kind := s.buf.kind
	s.buf.sb.Reset()
	s.buf.msgID = ""
	if strings.TrimSpace(text) == "" {
		return
	}
	s.emit(kind, text, nil)
}

// closeAgent ends the current agent message (a tool call, a thought or the end of the turn).
func (s *session) closeAgent() {
	if !s.agentOpen {
		return
	}
	s.agentOpen = false
	s.agentMsgI = ""
	if t := s.agentMsg.String(); strings.TrimSpace(t) != "" {
		s.lastAgent = t
	}
}

// finish flushes the text and closes the last message at the end of the turn.
func (s *session) finish() {
	s.flushText()
	s.closeAgent()
}

// summary is the last message of the agent.
func (s *session) summary() string {
	return strings.TrimSpace(s.lastAgent)
}

// ---- tools ----------------------------------------------------------------------------------

type toolContent struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
	Path    string          `json:"path"`
	OldText string          `json:"oldText"`
	NewText string          `json:"newText"`
	TermID  string          `json:"terminalId"`
}

func toolContentText(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var items []toolContent
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		switch it.Type {
		case "content":
			var b contentBlock
			if json.Unmarshal(it.Content, &b) == nil {
				if t := b.plain(); t != "" {
					out = append(out, t)
				}
			}
		case "diff":
			out = append(out, fmt.Sprintf("diff %s (+%d/-%d linhas)", it.Path, countLines(it.NewText), countLines(it.OldText)))
		case "terminal":
			out = append(out, "[terminal "+it.TermID+"]")
		}
	}
	return out
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func (s *session) toolCall(u updateBody) {
	s.flushText()
	s.closeAgent()
	st := s.tools[u.ToolCallID]
	if st == nil {
		st = &toolState{}
		s.tools[u.ToolCallID] = st
	}
	st.merge(u)
	title := firstNonEmpty(st.title, u.Name, st.kind, u.ToolCallID)
	pl := map[string]any{"id": u.ToolCallID, "name": firstNonEmpty(u.Name, st.title), "kind": st.kind, "status": firstNonEmpty(st.status, "pending")}
	if len(u.Locations) > 0 {
		pl["locations"] = u.Locations
	}
	if in := boundedRaw(u.RawInput); in != nil {
		pl["input"] = in
	}
	s.emit(domain.EventToolUse, title, pl)
	s.maybeReport(u.ToolCallID, st)
}

func (s *session) toolUpdate(u updateBody) {
	st := s.tools[u.ToolCallID]
	if st == nil {
		st = &toolState{}
		s.tools[u.ToolCallID] = st
	}
	st.merge(u)
	s.maybeReport(u.ToolCallID, st)
}

func (st *toolState) merge(u updateBody) {
	if u.Title != "" {
		st.title = u.Title
	}
	if u.ToolKind != "" {
		st.kind = u.ToolKind
	}
	if u.Status != "" {
		st.status = u.Status
	}
	if c := toolContentText(u.Content); len(c) > 0 {
		st.content = c
	}
	if len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
		st.output = rawText(u.RawOutput)
	}
}

// maybeReport emits the tool result once the call reached a final status.
func (s *session) maybeReport(id string, st *toolState) {
	if st.reported || (st.status != "completed" && st.status != "failed") {
		return
	}
	st.reported = true
	failed := st.status == "failed"
	txt := strings.Join(st.content, "\n")
	if txt == "" {
		txt = st.output
	}
	txt = truncate(txt, maxToolResultBytes)
	if failed {
		txt = "erro: " + txt
	}
	s.emit(domain.EventToolResult, txt, map[string]any{"tool_use_id": id, "is_error": failed, "status": st.status})
}

func rawText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	return string(raw)
}

// boundedRaw keeps small tool inputs whole and replaces large ones (a whole file being written)
// by a marker, so the run log does not grow with the agent's payloads.
func boundedRaw(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if len(raw) > maxPayloadInput {
		return map[string]any{"truncated": true, "bytes": len(raw)}
	}
	return raw
}

func (s *session) plan(u updateBody) {
	s.flushText()
	var sb strings.Builder
	sb.WriteString("plano do agente:")
	for _, e := range u.Entries {
		fmt.Fprintf(&sb, "\n- [%s] %s", firstNonEmpty(e.Status, "pending"), e.Content)
	}
	txt := sb.String()
	if txt == s.lastPlan {
		return
	}
	s.lastPlan = txt
	s.emit(domain.EventSystem, txt, map[string]any{"plan": u.Entries})
}

// usage records a usage_update. During replay it only sets the cost baseline.
func (s *session) usage(u updateBody, replay bool) {
	if !replay {
		s.usedTokens, s.sizeTokens = u.Used, u.Size
	}
	if u.Cost == nil {
		return
	}
	if !strings.EqualFold(u.Cost.Currency, "USD") {
		if !s.warnedCurrency && !replay {
			s.warnedCurrency = true
			s.emit(domain.EventSystem, fmt.Sprintf("custo reportado em %q ignorado: o Hanko só contabiliza USD", u.Cost.Currency), nil)
		}
		return
	}
	amt := u.Cost.Amount
	if replay {
		s.costBase = &amt
		return
	}
	s.cost = &amt
}

// costUSD is what this run spent: the agent reports cost accumulated by the session, so on a
// resumed session the replayed baseline is subtracted.
func (s *session) costUSD() *float64 {
	if s.cost == nil {
		return nil
	}
	c := *s.cost
	if s.costBase != nil && c >= *s.costBase {
		c -= *s.costBase
	}
	return &c
}

// ---- helpers --------------------------------------------------------------------------------

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncate cuts s to at most max bytes on a rune boundary, appending a marker.
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
