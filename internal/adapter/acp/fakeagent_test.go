package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A fake ACP agent: the test binary re-executes itself (TestMain) with ACPFAKE=<scenario> and
// speaks ACP over stdio. It reports what it observed in its final message, which the tests
// read back through Result.Summary.

type fakeAgent struct {
	in      *bufio.Scanner
	outMu   sync.Mutex
	out     *os.File
	nextID  int
	pending []map[string]any // messages read while waiting for a response
	cwd     string
	sid     string
	// o que o cliente enviou no handshake (cenário "contract")
	initParams, newParams map[string]any
}

func fakeMain(scenario string) {
	f := &fakeAgent{in: bufio.NewScanner(os.Stdin), out: os.Stdout}
	f.in.Buffer(make([]byte, 0, 1<<20), 32<<20)
	f.run(scenario)
}

func (f *fakeAgent) send(v any) {
	b, _ := json.Marshal(v)
	f.outMu.Lock()
	defer f.outMu.Unlock()
	_, _ = f.out.Write(append(b, '\n'))
}

func (f *fakeAgent) raw(line string) {
	f.outMu.Lock()
	defer f.outMu.Unlock()
	_, _ = f.out.WriteString(line + "\n")
}

// read returns the next message from the client, nil at EOF.
func (f *fakeAgent) read() map[string]any {
	if len(f.pending) > 0 {
		m := f.pending[0]
		f.pending = f.pending[1:]
		return m
	}
	return f.readWire()
}

func (f *fakeAgent) readWire() map[string]any {
	for f.in.Scan() {
		var m map[string]any
		if json.Unmarshal(f.in.Bytes(), &m) == nil {
			return m
		}
	}
	return nil
}

func (f *fakeAgent) reply(id any, result any) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fakeAgent) replyErr(id any, code int, msg string) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (f *fakeAgent) notify(update map[string]any) {
	f.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": f.sid, "update": update}})
}

func (f *fakeAgent) chunk(kind, msgID, text string) {
	u := map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}}
	if msgID != "" {
		u["messageId"] = msgID
	}
	f.notify(u)
}

// call sends a request to the client and waits for its response (other messages are kept).
func (f *fakeAgent) call(method string, params any) (result map[string]any, errObj map[string]any) {
	f.nextID++
	id := 1000 + f.nextID
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		m := f.readWire()
		if m == nil {
			os.Exit(0)
		}
		if mid, ok := m["id"].(float64); ok && int(mid) == id && m["method"] == nil {
			if e, ok := m["error"].(map[string]any); ok {
				return nil, e
			}
			r, _ := m["result"].(map[string]any)
			return r, nil
		}
		f.pending = append(f.pending, m)
	}
}

// waitFor reads until a message with the method arrives (true) or EOF (false).
func (f *fakeAgent) waitFor(method string) bool {
	for {
		m := f.read()
		if m == nil {
			return false
		}
		if m["method"] == method {
			return true
		}
	}
}

// drain reads until the client closes stdin.
func (f *fakeAgent) drain() {
	for f.read() != nil {
		continue
	}
}

func env(k string) string { return os.Getenv(k) }

func (f *fakeAgent) run(sc string) {
	if v := env("ACPFAKE_LEAK_ENV"); v != "" {
		fmt.Fprintln(os.Stderr, "leak:"+os.Getenv(v))
	}
	switch sc {
	case "mute":
		time.Sleep(time.Minute)
		return
	case "notacp":
		fmt.Println("usage: notacp [options]")
		fmt.Fprintln(os.Stderr, "fatal: this is not an ACP agent")
		os.Exit(2)
	}
	// initialize
	m := f.read()
	if m == nil || m["method"] != "initialize" {
		os.Exit(1)
	}
	f.initParams, _ = m["params"].(map[string]any)
	if sc == "badline" {
		f.raw("this is not json")
		f.raw(`{"foo":1}`)
		f.raw(`[1,2,3]`)
	}
	version := 1
	if sc == "badversion" {
		version = 2
	}
	caps := map[string]any{"loadSession": env("ACPFAKE_LOAD") == "1",
		"mcpCapabilities": map[string]any{"http": env("ACPFAKE_HTTP") == "1"}}
	if sc == "noversion" {
		f.reply(m["id"], map[string]any{"hello": "world"})
		time.Sleep(time.Minute)
	}
	f.reply(m["id"], map[string]any{"protocolVersion": version, "agentCapabilities": caps,
		"agentInfo":   map[string]any{"name": "fake-agent", "version": "0.0.1", "futureField": []int{1}},
		"authMethods": []any{map[string]any{"id": "oauth", "name": "OAuth"}}})
	if sc == "badversion" {
		time.Sleep(time.Minute)
	}

	// session/new or session/load
	var servers any
	loaded := false
	for {
		m = f.read()
		if m == nil {
			return
		}
		method, _ := m["method"].(string)
		p, _ := m["params"].(map[string]any)
		switch method {
		case "session/new":
			if sc == "auth" {
				f.replyErr(m["id"], -32000, "Authentication required")
				return
			}
			f.cwd, _ = p["cwd"].(string)
			servers = p["mcpServers"]
			f.newParams = p
			f.sid = "sess-1"
			res := map[string]any{"sessionId": f.sid}
			if sc == "model" {
				res["configOptions"] = []any{map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select",
					"currentValue": "default", "options": []any{
						map[string]any{"value": "default", "name": "Default"},
						map[string]any{"group": "g", "name": "Group", "options": []any{map[string]any{"value": "fast-1", "name": "Fast"}}}}}}
			}
			f.reply(m["id"], res)
		case "session/load":
			f.cwd, _ = p["cwd"].(string)
			servers = p["mcpServers"]
			f.sid, _ = p["sessionId"].(string)
			f.chunk("user_message_chunk", "", "REPLAY-USER")
			f.chunk("agent_message_chunk", "old", "REPLAY-AGENT")
			f.notify(map[string]any{"sessionUpdate": "usage_update", "used": 5, "size": 10, "cost": map[string]any{"amount": 1.0, "currency": "USD"}})
			f.reply(m["id"], map[string]any{})
			loaded = true
		case "session/set_config_option":
			f.reply(m["id"], map[string]any{})
			f.chunk("agent_message_chunk", "", fmt.Sprintf("set_config_option=%v/%v", p["configId"], p["value"]))
			continue
		case "session/prompt":
			f.prompt(sc, m, p, servers, loaded)
			// after the turn, wait for EOF like a real agent
			f.drain()
			return
		default:
			if m["id"] != nil {
				f.replyErr(m["id"], -32601, "unknown method")
			}
			continue
		}
	}
}

func (f *fakeAgent) prompt(sc string, m map[string]any, p map[string]any, servers any, loaded bool) {
	id := m["id"]
	blocks, _ := p["prompt"].([]any)
	stop := "end_turn"
	var final []string

	switch sc {
	case "happy":
		f.chunk("agent_thought_chunk", "t1", "pensando")
		f.chunk("agent_thought_chunk", "t1", " mais")
		f.chunk("agent_message_chunk", "m1", "Vou ler ")
		f.chunk("agent_message_chunk", "m1", "o arquivo.")
		f.notify(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "Reading a.txt", "kind": "read", "status": "pending",
			"locations": []any{map[string]any{"path": "/x/a.txt", "line": 1}}, "rawInput": map[string]any{"path": "/x/a.txt"}, "futureField": true})
		f.notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "in_progress"})
		f.notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "conteúdo do arquivo"}}}})
		f.notify(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c2", "title": "Editing b.txt", "kind": "edit", "status": "pending"})
		res, _ := f.call("session/request_permission", map[string]any{"sessionId": f.sid,
			"toolCall": map[string]any{"toolCallId": "c2", "title": "Editing b.txt", "kind": "edit"},
			"options":  []any{map[string]any{"optionId": "yes", "name": "Yes", "kind": "allow_once"}, map[string]any{"optionId": "no", "name": "No", "kind": "reject_once"}}})
		out, _ := res["outcome"].(map[string]any)
		f.notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c2", "status": "failed",
			"content": []any{map[string]any{"type": "diff", "path": "/x/b.txt", "oldText": "a\n", "newText": "a\nb\n"}}})
		f.notify(map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "passo 1", "priority": "high", "status": "pending"}}})
		f.notify(map[string]any{"sessionUpdate": "something_new", "x": 1})
		f.notify(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{}})
		f.notify(map[string]any{"sessionUpdate": "notice", "severity": "warning", "title": "Atenção", "description": "detalhe"})
		_, uerr := f.call("foo/bar", map[string]any{})
		f.notify(map[string]any{"sessionUpdate": "usage_update", "used": 100, "size": 1000, "cost": map[string]any{"amount": 0.25, "currency": "USD"}})
		f.chunk("agent_message_chunk", "m2", "Feito: ")
		f.chunk("agent_message_chunk", "m2", fmt.Sprintf("perm=%v unk=%v blocks=%d", out["optionId"], uerr["code"], len(blocks)))
	case "perm":
		for _, kind := range strings.Split(env("ACPFAKE_KINDS"), ",") {
			opts := []any{map[string]any{"optionId": "ya", "name": "Always", "kind": "allow_always"}, map[string]any{"optionId": "y1", "name": "Once", "kind": "allow_once"},
				map[string]any{"optionId": "na", "name": "Never", "kind": "reject_always"}, map[string]any{"optionId": "n1", "name": "No", "kind": "reject_once"}}
			if env("ACPFAKE_ONLY") == "allow" {
				opts = opts[:2]
			}
			if env("ACPFAKE_ONLY") == "reject" {
				opts = opts[2:]
			}
			tc := map[string]any{"toolCallId": "t-" + kind, "title": "do " + kind}
			if kind != "none" {
				tc["kind"] = kind
			}
			res, _ := f.call("session/request_permission", map[string]any{"sessionId": f.sid, "toolCall": tc, "options": opts})
			out, _ := res["outcome"].(map[string]any)
			final = append(final, fmt.Sprintf("%s=%v/%v", kind, out["outcome"], out["optionId"]))
		}
	case "fs":
		for _, op := range strings.Split(env("ACPFAKE_OPS"), ";") {
			kind, arg, _ := strings.Cut(op, ":")
			path := arg
			if !strings.HasPrefix(arg, "/") && !strings.HasPrefix(arg, "@") {
				path = filepath.Join(f.cwd, arg)
			}
			path = strings.TrimPrefix(path, "@")
			var res map[string]any
			var e map[string]any
			switch kind {
			case "read":
				res, e = f.call("fs/read_text_file", map[string]any{"sessionId": f.sid, "path": path})
			case "readlines":
				res, e = f.call("fs/read_text_file", map[string]any{"sessionId": f.sid, "path": path, "line": 2, "limit": 1})
			case "write":
				res, e = f.call("fs/write_text_file", map[string]any{"sessionId": f.sid, "path": path, "content": "novo\n"})
			case "writebig":
				res, e = f.call("fs/write_text_file", map[string]any{"sessionId": f.sid, "path": path, "content": strings.Repeat("x", 9<<20)})
			}
			if e != nil {
				final = append(final, fmt.Sprintf("%s:%s=ERR%v", kind, arg, e["code"]))
			} else if kind == "write" {
				final = append(final, fmt.Sprintf("%s:%s=OK", kind, arg))
			} else {
				final = append(final, fmt.Sprintf("%s:%s=OK%q", kind, arg, res["content"]))
			}
		}
	case "load":
		final = append(final, fmt.Sprintf("loaded=%v", loaded))
		f.notify(map[string]any{"sessionUpdate": "usage_update", "used": 7, "size": 10, "cost": map[string]any{"amount": 1.5, "currency": "USD"}})
	case "mcp":
		b, _ := json.Marshal(servers)
		final = append(final, "servers="+string(b))
	case "cost_brl":
		f.notify(map[string]any{"sessionUpdate": "usage_update", "used": 7, "size": 10, "cost": map[string]any{"amount": 9.0, "currency": "BRL"}})
		f.notify(map[string]any{"sessionUpdate": "usage_update", "used": 8, "size": 10, "cost": map[string]any{"amount": 9.5, "currency": "BRL"}})
	case "stop_max":
		stop = "max_tokens"
	case "stop_refusal":
		stop = "refusal"
	case "crash":
		f.chunk("agent_message_chunk", "m1", "começando")
		fmt.Fprintln(os.Stderr, "panic: boom")
		os.Exit(3)
	case "cancel":
		f.chunk("agent_message_chunk", "m1", "trabalhando")
		f.send(map[string]any{"jsonrpc": "2.0", "method": "hanko/test-ready"}) // unknown notification, ignored
		if !f.waitFor("session/cancel") {
			return
		}
		res, _ := f.call("session/request_permission", map[string]any{"sessionId": f.sid,
			"toolCall": map[string]any{"toolCallId": "late", "title": "late", "kind": "read"},
			"options":  []any{map[string]any{"optionId": "y1", "name": "Once", "kind": "allow_once"}}})
		out, _ := res["outcome"].(map[string]any)
		f.chunk("agent_message_chunk", "m2", fmt.Sprintf("after-cancel=%v", out["outcome"]))
		stop = "cancelled"
	case "cancel_ignore":
		signal.Ignore(syscall.SIGTERM)
		f.chunk("agent_message_chunk", "m1", "teimoso")
		time.Sleep(time.Minute)
	case "model":
		final = append(final, "prompted")
	case "contract":
		final = append(final, "violations=["+strings.Join(f.contractViolations(blocks), ";")+"]")
	}
	if len(final) > 0 {
		f.chunk("agent_message_chunk", "m3", strings.Join(final, " "))
	}
	res := map[string]any{"stopReason": stop}
	if sc == "happy" {
		res["usage"] = map[string]any{"inputTokens": 11, "outputTokens": 22, "cachedReadTokens": 3, "cachedWriteTokens": 4}
	}
	f.reply(id, res)
}

// contractViolations confere o que um cliente ACP v1 deve enviar no handshake e no prompt (teste de contrato).
func (f *fakeAgent) contractViolations(blocks []any) []string {
	var v []string
	check := func(ok bool, msg string) {
		if !ok {
			v = append(v, msg)
		}
	}
	check(f.initParams["protocolVersion"] == float64(1), "initialize.protocolVersion != 1")
	ci, _ := f.initParams["clientInfo"].(map[string]any)
	check(ci["name"] == "hanko" && ci["version"] != nil, "initialize.clientInfo sem name=hanko/version")
	caps, _ := f.initParams["clientCapabilities"].(map[string]any)
	fsCaps, _ := caps["fs"].(map[string]any)
	check(fsCaps["readTextFile"] == true && fsCaps["writeTextFile"] == true, "clientCapabilities.fs incompleto")
	check(caps["terminal"] == false, "clientCapabilities.terminal deveria ser false")
	check(filepath.IsAbs(f.cwd), "session/new.cwd não é absoluto")
	_, isList := f.newParams["mcpServers"].([]any)
	check(isList, "session/new.mcpServers não é lista")
	check(len(blocks) == 2, fmt.Sprintf("session/prompt com %d blocos (esperado 2)", len(blocks)))
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		check(bm["type"] == "text" && bm["text"] != "", "bloco de prompt que não é texto")
	}
	return v
}
