package plugin_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/sdk/plugin"
)

// host é o lado do Hanko nos testes: manda requisições ao plugin e responde às que ele faz.
type host struct {
	t      *testing.T
	toP    *io.PipeWriter
	fromP  *bufio.Reader
	nextID int
	onCall func(method string, params json.RawMessage) (any, *plugin.Error)
}

func start(t *testing.T, p *plugin.Plugin) (*host, <-chan error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := p.Serve(context.Background(), inR, outW)
		_ = outW.Close()
		done <- err
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return &host{t: t, toP: inW, fromP: bufio.NewReader(outR)}, done
}

func (h *host) send(v any) {
	h.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := h.toP.Write(append(b, '\n')); err != nil {
		h.t.Fatalf("escrever: %v", err)
	}
}

// call faz uma requisição ao plugin e atende, no meio dela, as chamadas que o plugin fizer ao host.
func (h *host) call(method string, params any) (json.RawMessage, *plugin.Error) {
	h.t.Helper()
	h.nextID++
	id := h.nextID
	h.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		line, err := h.fromP.ReadBytes('\n')
		if err != nil {
			h.t.Fatalf("ler do plugin: %v", err)
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *plugin.Error   `json:"error"`
		}
		if json.Unmarshal(line, &m) != nil {
			h.t.Fatalf("linha inválida: %s", line)
		}
		if m.Method != "" { // chamada do plugin ao host
			res, rerr := h.onCall(m.Method, m.Params)
			if rerr != nil {
				h.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": rerr})
			} else {
				h.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res})
			}
			continue
		}
		return m.Result, m.Error
	}
}

func demo() *plugin.Plugin {
	p := plugin.New("demo")
	p.Tool("ping", func(_ context.Context, _ *plugin.Host, c plugin.ToolCall) (plugin.ToolResult, error) {
		return plugin.ToolResult{Text: "pong " + c.Persona.Slug}, nil
	})
	p.Tool("quebra", func(context.Context, *plugin.Host, plugin.ToolCall) (plugin.ToolResult, error) {
		return plugin.ToolResult{}, plugin.Errorf(plugin.CodeInvalid, "recusado")
	})
	p.Tool("falha", func(context.Context, *plugin.Host, plugin.ToolCall) (plugin.ToolResult, error) {
		return plugin.ToolResult{}, errors.New("deu ruim")
	})
	p.Widget("w", func(context.Context, *plugin.Host, plugin.WidgetRequest) (plugin.Widget, error) {
		return plugin.Widget{Markdown: "**oi**", Table: &plugin.Table{Columns: []string{"a"}, Rows: [][]string{{"1"}}}}, nil
	})
	p.Job("j", func(ctx context.Context, h *plugin.Host, r plugin.JobRequest) error {
		return h.StateSet(ctx, r.Company, "ran", true)
	})
	p.Page("painel", func(ctx context.Context, h *plugin.Host, r plugin.PageRequest) (string, error) {
		var n int
		_, _ = h.StateGet(ctx, r.Company, "n", &n)
		return "<b>" + r.User.Name + "</b>", nil
	})
	p.Bridge(func(ctx context.Context, h *plugin.Host, c plugin.BridgeCall) (any, error) {
		fired, err := h.FireTrigger(ctx, c.Company, "t")
		return map[string]any{"fired": fired, "method": c.Method}, err
	})
	p.OnEvent(func(ctx context.Context, h *plugin.Host, co plugin.Company, e plugin.Event) error {
		return h.CommentTask(ctx, co, e.Subject.ID, "visto", "evt-"+e.ID)
	})
	p.SkillSource("cat", func(context.Context, *plugin.Host, plugin.Company) (plugin.SkillSnapshot, error) {
		return plugin.SkillSnapshot{Revision: "r1", Skills: []plugin.SkillFile{{Path: "a", SkillMD: "x"}}}, nil
	})
	p.ValidateConfig(func(_ context.Context, _ plugin.Company, cfg map[string]any) map[string]string {
		if cfg["modo"] == "ruim" {
			return map[string]string{"modo": "inválido"}
		}
		return nil
	})
	p.Environment("nuvem", fakeEnv{})
	return p
}

type fakeEnv struct{}

func (fakeEnv) Ping(_ context.Context, key string) error {
	if key == "" {
		return errors.New("sem chave")
	}
	return nil
}
func (fakeEnv) Create(_ context.Context, _ string, s plugin.EnvSpec) (string, error) {
	return "box-" + s.Name, nil
}
func (fakeEnv) SSH(_ context.Context, _, id string, ttl time.Duration) (plugin.SSHAccess, error) {
	return plugin.SSHAccess{Host: "gw", Port: 22, User: id + ttl.String()}, nil
}
func (fakeEnv) Delete(context.Context, string, string) error { return nil }

func TestServeProtocol(t *testing.T) {
	h, done := start(t, demo())
	co := map[string]any{"id": "c1", "slug": "acme", "name": "Acme"}
	var calls []string
	h.onCall = func(method string, params json.RawMessage) (any, *plugin.Error) {
		calls = append(calls, method)
		switch method {
		case "state.get":
			return map[string]any{"found": true, "value": 3}, nil
		case "flows.fire":
			return map[string]any{"fired": 2}, nil
		case "state.set", "tasks.comment":
			return map[string]any{"ok": true}, nil
		}
		return nil, plugin.Errorf(plugin.CodeMethodNotFound, "x")
	}
	must := func(method string, params any) json.RawMessage {
		t.Helper()
		res, err := h.call(method, params)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		return res
	}

	if r := string(must("initialize", map[string]any{"api_version": 1})); !strings.Contains(r, `"api_version":1`) || !strings.Contains(r, `"demo"`) {
		t.Errorf("initialize = %s", r)
	}
	if r := string(must("health", nil)); !strings.Contains(r, `"ok":true`) {
		t.Errorf("health = %s", r)
	}
	if r := string(must("execute_tool", map[string]any{"company": co, "tool": "ping", "persona": map[string]any{"slug": "ana"}})); !strings.Contains(r, "pong ana") {
		t.Errorf("tool = %s", r)
	}
	if _, err := h.call("execute_tool", map[string]any{"company": co, "tool": "quebra"}); err == nil || err.Code != plugin.CodeInvalid {
		t.Errorf("erro com código = %+v", err)
	}
	if _, err := h.call("execute_tool", map[string]any{"company": co, "tool": "falha"}); err == nil || err.Code != -32000 || err.Message != "deu ruim" {
		t.Errorf("erro genérico = %+v", err)
	}
	if _, err := h.call("execute_tool", map[string]any{"company": co, "tool": "nao-existe"}); err == nil || err.Code != plugin.CodeInvalidParams {
		t.Errorf("tool desconhecida = %+v", err)
	}
	if r := string(must("get_widget", map[string]any{"company": co, "key": "w"})); !strings.Contains(r, "**oi**") {
		t.Errorf("widget = %s", r)
	}
	must("run_job", map[string]any{"company": co, "key": "j"})
	if r := string(must("get_page", map[string]any{"company": co, "page": "painel", "user": map[string]any{"name": "Rita"}})); !strings.Contains(r, "Rita") {
		t.Errorf("página = %s", r)
	}
	if r := string(must("page_call", map[string]any{"company": co, "page": "painel", "method": "registrar", "params": nil})); !strings.Contains(r, `"fired":2`) || !strings.Contains(r, "registrar") {
		t.Errorf("ponte = %s", r)
	}
	must("on_event", map[string]any{"company": co, "event": map[string]any{"id": "e1", "verb": "task.created", "subject": map[string]any{"type": "task", "id": "T-1"}}})
	if r := string(must("skills.snapshot", map[string]any{"company": co, "source": "cat"})); !strings.Contains(r, `"revision":"r1"`) {
		t.Errorf("skills = %s", r)
	}
	if r := string(must("validate_config", map[string]any{"company": co, "config": map[string]any{"modo": "ruim"}})); !strings.Contains(r, `"ok":false`) || !strings.Contains(r, "inválido") {
		t.Errorf("validate (ruim) = %s", r)
	}
	if r := string(must("validate_config", map[string]any{"company": co, "config": map[string]any{"modo": "bom"}})); !strings.Contains(r, `"ok":true`) {
		t.Errorf("validate (bom) = %s", r)
	}
	// ambiente
	if r := string(must("env.create", map[string]any{"provider": "nuvem", "api_key": "k", "spec": map[string]any{"name": "n"}})); !strings.Contains(r, `"box-n"`) {
		t.Errorf("env.create = %s", r)
	}
	if r := string(must("env.ssh", map[string]any{"provider": "nuvem", "api_key": "k", "id": "b", "ttl_sec": 60})); !strings.Contains(r, `"host":"gw"`) || !strings.Contains(r, "b1m0s") {
		t.Errorf("env.ssh = %s", r)
	}
	must("env.delete", map[string]any{"provider": "nuvem", "api_key": "k", "id": "b"})
	must("env.ping", map[string]any{"provider": "nuvem", "api_key": "k"})
	if _, err := h.call("env.ping", map[string]any{"provider": "nuvem", "api_key": ""}); err == nil {
		t.Error("ping sem chave deveria falhar")
	}
	if _, err := h.call("env.ping", map[string]any{"provider": "outro", "api_key": "k"}); err == nil || err.Code != plugin.CodeInvalidParams {
		t.Errorf("provider desconhecido = %+v", err)
	}
	if _, err := h.call("nao_existe", nil); err == nil || err.Code != plugin.CodeMethodNotFound {
		t.Errorf("método desconhecido = %+v", err)
	}
	if _, err := h.call("get_page", map[string]any{"company": co, "page": "outra"}); err == nil {
		t.Error("página desconhecida deveria falhar")
	}
	if got := strings.Join(calls, ","); !strings.Contains(got, "state.set") || !strings.Contains(got, "flows.fire") || !strings.Contains(got, "tasks.comment") || !strings.Contains(got, "state.get") {
		t.Errorf("chamadas ao host = %s", got)
	}

	must("shutdown", nil)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve depois do shutdown = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve não terminou depois do shutdown")
	}
}

func TestServeEndsOnEOFAndHostErrors(t *testing.T) {
	p := plugin.New("x")
	p.Bridge(func(ctx context.Context, h *plugin.Host, c plugin.BridgeCall) (any, error) {
		_, err := h.FireTrigger(ctx, c.Company, "t")
		return nil, err
	})
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- p.Serve(context.Background(), inR, outW); _ = outW.Close() }()
	rd := bufio.NewReader(outR)
	// uma linha inválida é ignorada; depois, o erro do host chega ao handler
	_, _ = inW.Write([]byte("não é json\n"))
	_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","id":7,"method":"page_call","params":{"company":{"id":"c"},"method":"m"}}` + "\n"))
	line, _ := rd.ReadBytes('\n') // a chamada flows.fire do plugin
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(line, &req)
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": plugin.CodeDenied, "message": "capacidade flows.trigger não concedida"}})
	_, _ = inW.Write(append(b, '\n'))
	line, _ = rd.ReadBytes('\n')
	if !strings.Contains(string(line), "flows.trigger") || !strings.Contains(string(line), `"id":7`) {
		t.Fatalf("resposta = %s", line)
	}
	_ = inW.Close() // EOF encerra sem erro
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve no EOF = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve não terminou no EOF")
	}
}

func TestErrorAndNames(t *testing.T) {
	e := plugin.Errorf(plugin.CodeBusy, "ocupado %d", 3)
	if e.Error() != "-32002: ocupado 3" {
		t.Errorf("Error() = %q", e.Error())
	}
	n := demo().Names()
	if strings.Join(n["tools"], ",") != "falha,ping,quebra" || strings.Join(n["pages"], ",") != "painel" || strings.Join(n["envs"], ",") != "nuvem" ||
		strings.Join(n["jobs"], ",") != "j" || strings.Join(n["widgets"], ",") != "w" || strings.Join(n["sources"], ",") != "cat" {
		t.Errorf("Names = %v", n)
	}
}
