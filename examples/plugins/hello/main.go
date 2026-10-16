// Command hello é um plugin de exemplo do Hanko (docs/PLUGINS.md), só com a biblioteca padrão.
//
// Protocolo: JSON-RPC 2.0, uma mensagem por linha, em stdin/stdout. O Hanko chama initialize, health, shutdown,
// validate_config, on_event, execute_tool, get_widget e run_job; o plugin chama o Hanko (tasks.*, state.*, log,
// config.get…) pelo mesmo canal, limitado às capacidades do manifesto. Tudo que vai para stderr aparece no log do
// plugin na tela de Configurações › Plugins.
//
// Para instalar: go build -o hello . dentro deste diretório e copie o diretório para HANKO_PLUGINS_DIR/hello.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

type company struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// peer é o lado do plugin na conexão: escreve uma linha por mensagem e casa as respostas do host.
type peer struct {
	mu      sync.Mutex
	out     *os.File
	next    atomic.Int64
	pmu     sync.Mutex
	pending map[string]chan message
}

func (p *peer) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hello: codificar:", err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.out.Write(append(b, '\n'))
}

func (p *peer) reply(id json.RawMessage, result any) {
	p.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (p *peer) fail(id json.RawMessage, code int, msg string) {
	p.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: msg}})
}

// call chama um método do host e espera a resposta (até 30 s).
func (p *peer) call(method string, params any, out any) error {
	id := p.next.Add(1)
	key := fmt.Sprint(id)
	ch := make(chan message, 1)
	p.pmu.Lock()
	p.pending[key] = ch
	p.pmu.Unlock()
	p.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-time.After(30 * time.Second):
		p.pmu.Lock()
		delete(p.pending, key)
		p.pmu.Unlock()
		return fmt.Errorf("%s: sem resposta do host", method)
	}
}

func (p *peer) answer(m message) {
	key := strings.Trim(string(m.ID), `"`)
	p.pmu.Lock()
	ch := p.pending[key]
	delete(p.pending, key)
	p.pmu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func main() {
	p := &peer{out: os.Stdout, pending: map[string]chan message{}}
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var m message
			if json.Unmarshal(line, &m) != nil {
				fmt.Fprintln(os.Stderr, "hello: linha inválida ignorada")
			} else if m.Method == "" {
				p.answer(m)
			} else {
				// cada pedido numa goroutine: o handler pode chamar o host e esperar a resposta
				go handle(p, m)
			}
		}
		if err != nil {
			return
		}
	}
}

func handle(p *peer, m message) {
	switch m.Method {
	case "initialize":
		p.reply(m.ID, map[string]any{"api_version": 1, "name": "hello"})
	case "health":
		p.reply(m.ID, map[string]any{"ok": true})
	case "shutdown":
		p.reply(m.ID, map[string]any{})
		time.Sleep(50 * time.Millisecond)
		os.Exit(0)
	case "validate_config":
		var in struct {
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(m.Params, &in)
		g, _ := in.Config["greeting"].(string)
		if strings.Contains(strings.ToLower(g), "proibido") {
			p.reply(m.ID, map[string]any{"ok": false, "errors": map[string]string{"greeting": "essa saudação não é aceita"}})
			return
		}
		p.reply(m.ID, map[string]any{"ok": true})
	case "on_event":
		onEvent(p, m)
	case "execute_tool":
		executeTool(p, m)
	case "get_widget":
		getWidget(p, m)
	case "run_job":
		runJob(p, m)
	default:
		p.fail(m.ID, -32601, "método desconhecido: "+m.Method)
	}
}

func config(p *peer, co company) map[string]any {
	var out struct {
		Config map[string]any `json:"config"`
	}
	if err := p.call("config.get", map[string]any{"company": co.ID}, &out); err != nil {
		fmt.Fprintln(os.Stderr, "hello: config.get:", err)
		return map[string]any{"greeting": "Olá", "comment_new_tasks": true}
	}
	return out.Config
}

func onEvent(p *peer, m message) {
	var in struct {
		Company company `json:"company"`
		Event   struct {
			ID      string `json:"id"`
			Verb    string `json:"verb"`
			Subject struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"subject"`
		} `json:"event"`
	}
	if err := json.Unmarshal(m.Params, &in); err != nil {
		p.fail(m.ID, -32602, "params inválidos")
		return
	}
	cfg := config(p, in.Company)
	if in.Event.Verb == "task.created" && cfg["comment_new_tasks"] != false {
		greeting, _ := cfg["greeting"].(string)
		// event_id = id do evento: uma entrega repetida não duplica o comentário
		err := p.call("tasks.comment", map[string]any{"company": in.Company.ID, "key": in.Event.Subject.ID,
			"body": greeting + "! Task recebida pelo plugin hello.", "event_id": "evt-" + in.Event.ID}, nil)
		if err != nil {
			p.fail(m.ID, -32000, "comentar: "+err.Error())
			return
		}
	}
	var n struct {
		Found bool `json:"found"`
		Value int  `json:"value"`
	}
	_ = p.call("state.get", map[string]any{"company": in.Company.ID, "key": "events"}, &n)
	_ = p.call("state.set", map[string]any{"company": in.Company.ID, "key": "events", "value": n.Value + 1}, nil)
	p.reply(m.ID, map[string]any{"ok": true})
}

func executeTool(p *peer, m message) {
	var in struct {
		Company   company `json:"company"`
		Persona   struct{ Slug string } `json:"persona"`
		Tool      string `json:"tool"`
		Arguments struct {
			Text string `json:"text"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(m.Params, &in); err != nil || in.Tool != "hello_echo" {
		p.fail(m.ID, -32602, "tool desconhecida")
		return
	}
	if strings.TrimSpace(in.Arguments.Text) == "" {
		p.reply(m.ID, map[string]any{"text": "informe text", "is_error": true})
		return
	}
	greeting, _ := config(p, in.Company)["greeting"].(string)
	who := in.Persona.Slug
	if who == "" {
		who = "você"
	}
	p.reply(m.ID, map[string]any{"text": fmt.Sprintf("%s, %s: %s", greeting, who, in.Arguments.Text)})
}

func getWidget(p *peer, m message) {
	var in struct {
		Company company        `json:"company"`
		Key     string         `json:"key"`
		Context map[string]any `json:"context"`
	}
	_ = json.Unmarshal(m.Params, &in)
	var n struct {
		Value int `json:"value"`
	}
	_ = p.call("state.get", map[string]any{"company": in.Company.ID, "key": "events"}, &n)
	var tick struct {
		Value string `json:"value"`
	}
	_ = p.call("state.get", map[string]any{"company": in.Company.ID, "key": "last_tick"}, &tick)
	if in.Key == "task" {
		p.reply(m.ID, map[string]any{"markdown": fmt.Sprintf("O plugin hello acompanha a task **%v**.", in.Context["key"])})
		return
	}
	last := tick.Value
	if last == "" {
		last = "ainda não rodou"
	}
	p.reply(m.ID, map[string]any{
		"markdown": fmt.Sprintf("**%d** evento(s) recebido(s) em %s.", n.Value, in.Company.Name),
		"table":    map[string]any{"columns": []string{"Job", "Última execução"}, "rows": [][]string{{"tick", last}}},
	})
}

func runJob(p *peer, m message) {
	var in struct {
		Company company `json:"company"`
		Key     string  `json:"key"`
	}
	_ = json.Unmarshal(m.Params, &in)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := p.call("state.set", map[string]any{"company": in.Company.ID, "key": "last_tick", "value": now}, nil); err != nil {
		p.fail(m.ID, -32000, err.Error())
		return
	}
	_ = p.call("log", map[string]any{"company": in.Company.ID, "level": "info", "message": "job " + in.Key + " rodou em " + now}, nil)
	p.reply(m.ID, map[string]any{"ok": true})
}
