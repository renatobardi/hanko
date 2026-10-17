package httpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

type collector struct {
	mu sync.Mutex
	ev []adapter.Event
}

func (c *collector) Emit(_ context.Context, e adapter.Event) error {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
	return nil
}

func (c *collector) kinds() (out []domain.RunEventKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.ev {
		out = append(out, e.Kind)
	}
	return
}

func (c *collector) text(kind domain.RunEventKind) (out []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.ev {
		if e.Kind == kind {
			out = append(out, e.Text)
		}
	}
	return
}

// fakeLLM é um servidor OpenAI-compatível que devolve respostas roteirizadas (tool_calls e depois texto).
type fakeLLM struct {
	*httptest.Server
	mu    sync.Mutex
	reqs  []map[string]any
	auths []string
	steps []string // corpo JSON de cada resposta, na ordem
	n     int
}

func newFakeLLM(t *testing.T, steps ...string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{steps: steps}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/v1/models":
			f.auths = append(f.auths, r.Header.Get("Authorization"))
			if r.Header.Get("Authorization") == "Bearer errada" {
				http.Error(w, "bad key", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-tools"}]}`))
		case "/v1/chat/completions":
			f.auths = append(f.auths, r.Header.Get("Authorization"))
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.reqs = append(f.reqs, req)
			i := min(f.n, len(f.steps)-1)
			f.n++
			_, _ = w.Write([]byte(f.steps[i]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func toolCallReply(id, name, args string, tokens ...int) string {
	a, _ := json.Marshal(args)
	return fmt.Sprintf(`{"model":"qwen-tools","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"vou usar %s",
	 "tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]}}],"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`,
		name, id, name, a, tokens[0], tokens[1])
}

func finalReply(text string, cost float64) string {
	return fmt.Sprintf(`{"model":"qwen-tools","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":200,"completion_tokens":30,"cost":%v}}`, text, cost)
}

type commentIn struct {
	Task string `json:"task"`
	Body string `json:"body"`
}

// fakeHanko é um servidor MCP de verdade (SDK, streamable HTTP sem estado como o /mcp do Hanko) com duas tools.
type fakeHanko struct {
	*httptest.Server
	mu       sync.Mutex
	comments []commentIn
	auth     []string
}

func newFakeHanko(t *testing.T) *fakeHanko {
	t.Helper()
	f := &fakeHanko{}
	srv := sdk.NewServer(&sdk.Implementation{Name: "hanko", Version: "t"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "hanko_task_get", Description: "Detalha uma task"},
		func(_ context.Context, _ *sdk.CallToolRequest, in struct {
			Task string `json:"task" jsonschema:"chave da task"`
		}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "task " + in.Task + ": corrigir o login"}}}, nil, nil
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "hanko_task_comment", Description: "Comenta na task"},
		func(_ context.Context, _ *sdk.CallToolRequest, in commentIn) (*sdk.CallToolResult, any, error) {
			f.mu.Lock()
			f.comments = append(f.comments, in)
			f.mu.Unlock()
			if in.Body == "" {
				r := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "body é obrigatório"}}}
				r.IsError = true
				return r, nil, nil
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "comentado"}}}, nil, nil
		})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer run-token-SENTINELA-1234" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeHanko) mcpConfig() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"mcpServers":{"hanko":{"type":"http","url":%q,"headers":{"Authorization":"Bearer run-token-SENTINELA-1234"}}}}`, f.URL))
}

func spec(t *testing.T, llm *fakeLLM, extra map[string]any) adapter.Spec {
	t.Helper()
	cfg := map[string]any{"base_url": llm.URL + "/v1", "api_key_ref": "sk-local-SENTINELA-9999", "model": "qwen-tools"}
	for k, v := range extra {
		cfg[k] = v
	}
	raw, _ := json.Marshal(cfg)
	return adapter.Spec{RunID: domain.NewID(), Agent: "http", System: "SISTEMA", Prompt: "Trabalhe na HNK-7", Config: raw}
}

// Demo do M12: uma persona http roda a task com um modelo que pede tools; o Hanko as executa e devolve.
func TestToolUseLoopAgainstFakeEndpointAndRealMCPServer(t *testing.T) {
	llm := newFakeLLM(t,
		toolCallReply("c1", "hanko_task_get", `{"task":"HNK-7"}`, 100, 20),
		toolCallReply("c2", "hanko_task_comment", `{"task":"HNK-7","body":"login corrigido"}`, 150, 25),
		finalReply("Task lida e comentada.", 0.0007))
	hk := newFakeHanko(t)
	sp := spec(t, llm, nil)
	sp.MCPConfig = hk.mcpConfig()
	sp.Env = map[string]string{"HANKO_RUN_TOKEN": "run-token-SENTINELA-1234"}
	col := &collector{}
	res, err := New(Options{}).Run(context.Background(), sp, col)
	if err != nil || res.IsError {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if res.Summary != "Task lida e comentada." || res.Model != "qwen-tools" {
		t.Fatalf("res = %+v", res)
	}
	if res.Usage.InputTokens != 450 || res.Usage.OutputTokens != 75 {
		t.Fatalf("usage = %+v", res.Usage)
	}
	if res.CostUSD == nil || *res.CostUSD != 0.0007 {
		t.Fatalf("cost = %v", res.CostUSD)
	}
	hk.mu.Lock()
	if len(hk.comments) != 1 || hk.comments[0].Body != "login corrigido" {
		t.Fatalf("o comentário não chegou ao Hanko: %+v", hk.comments)
	}
	hk.mu.Unlock()
	if got := col.text(domain.EventToolResult); len(got) != 2 || !strings.Contains(got[0], "task HNK-7: corrigir o login") || !strings.Contains(got[1], "comentado") {
		t.Fatalf("tool results = %q", got)
	}
	// 3 chamadas ao modelo: a 2ª já leva a resposta da tool; todas levam a chave do endpoint e as tools do Hanko
	if len(llm.reqs) != 3 {
		t.Fatalf("chamadas ao modelo = %d", len(llm.reqs))
	}
	msgs := llm.reqs[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "c1" || !strings.Contains(last["content"].(string), "corrigir o login") {
		t.Fatalf("a resposta da tool não voltou ao modelo: %v", last)
	}
	if first := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "SISTEMA" {
		t.Fatalf("system prompt: %v", first)
	}
	if tools := llm.reqs[0]["tools"].([]any); len(tools) != 2 {
		t.Fatalf("tools oferecidas = %d", len(tools))
	}
	for _, a := range llm.auths {
		if a != "Bearer sk-local-SENTINELA-9999" {
			t.Fatalf("Authorization do endpoint = %q", a)
		}
	}
	for _, a := range hk.auth {
		if a != "Bearer run-token-SENTINELA-1234" {
			t.Fatalf("o MCP do Hanko recebeu %q", a)
		}
	}
	// a chave e o run token nunca aparecem nos eventos
	for _, e := range col.ev {
		if strings.Contains(e.Text, "SENTINELA") || strings.Contains(string(e.Payload), "SENTINELA") {
			t.Fatalf("segredo vazou em %s: %q", e.Kind, e.Text)
		}
	}
	if k := col.kinds(); k[len(k)-1] != domain.EventResult {
		t.Fatalf("kinds = %v", k)
	}
}

func TestToolAllowlistUnknownToolAndBadArguments(t *testing.T) {
	llm := newFakeLLM(t,
		toolCallReply("a", "hanko_task_comment", `{"task":"HNK-7","body":"x"}`, 10, 1), // fora da lista
		toolCallReply("b", "ferramenta_inventada", `{}`, 10, 1),
		toolCallReply("c", "hanko_task_get", `não é json`, 10, 1),
		finalReply("encerrei", 0))
	hk := newFakeHanko(t)
	sp := spec(t, llm, map[string]any{"tools": []string{"hanko_task_get"}})
	sp.MCPConfig = hk.mcpConfig()
	col := &collector{}
	res, err := New(Options{}).Run(context.Background(), sp, col)
	if err != nil || res.IsError || res.Summary != "encerrei" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if tools := llm.reqs[0]["tools"].([]any); len(tools) != 1 {
		t.Fatalf("só uma tool deveria ser oferecida: %d", len(tools))
	}
	hk.mu.Lock()
	defer hk.mu.Unlock()
	if len(hk.comments) != 0 {
		t.Fatalf("a tool fora da lista foi executada: %+v", hk.comments)
	}
	got := strings.Join(col.text(domain.EventToolResult), "|")
	for _, w := range []string{"não existe ou não está liberada", "argumentos não são um objeto JSON"} {
		if !strings.Contains(got, w) {
			t.Errorf("sem %q em %s", w, got)
		}
	}
}

func TestStepLimitAndModelErrors(t *testing.T) {
	llm := newFakeLLM(t, toolCallReply("x", "hanko_task_get", `{"task":"HNK-7"}`, 10, 1)) // repete para sempre
	hk := newFakeHanko(t)
	sp := spec(t, llm, map[string]any{"max_steps": 3})
	sp.MCPConfig = hk.mcpConfig()
	res, err := New(Options{}).Run(context.Background(), sp, &collector{})
	if err != nil || !res.IsError || !strings.Contains(res.Error, "limite de 3 passos") || len(llm.reqs) != 3 {
		t.Fatalf("res = %+v err = %v reqs = %d", res, err, len(llm.reqs))
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "overloaded", 503) }))
	defer bad.Close()
	raw, _ := json.Marshal(map[string]any{"base_url": bad.URL, "model": "m"})
	res, err = New(Options{}).Run(context.Background(), adapter.Spec{Prompt: "x", Config: raw}, &collector{})
	if err != nil || !res.IsError || !strings.Contains(res.Error, "503") {
		t.Fatalf("res = %+v err = %v", res, err)
	}

	empty := newFakeLLM(t, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`)
	res, _ = New(Options{}).Run(context.Background(), spec(t, empty, nil), &collector{})
	if !res.IsError || !strings.Contains(res.Error, "sem resposta") {
		t.Fatalf("res = %+v", res)
	}
}

func TestNoToolsForReviewOrWithoutCredential(t *testing.T) {
	llm := newFakeLLM(t, finalReply("VEREDITO: APROVADO", 0))
	hk := newFakeHanko(t)
	sp := spec(t, llm, nil)
	sp.MCPConfig, sp.ReadOnly = hk.mcpConfig(), true
	if res, err := New(Options{}).Run(context.Background(), sp, &collector{}); err != nil || res.IsError {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, ok := llm.reqs[0]["tools"]; ok {
		t.Fatal("revisão é somente leitura: nenhuma tool deve ser oferecida")
	}
	if len(hk.auth) != 0 {
		t.Fatal("revisão não deve nem conectar ao MCP")
	}

	// MCP fora do ar: a run segue sem tools, com aviso
	llm2 := newFakeLLM(t, finalReply("ok sem tools", 0))
	sp = spec(t, llm2, nil)
	sp.MCPConfig = json.RawMessage(`{"mcpServers":{"hanko":{"type":"http","url":"http://127.0.0.1:1/mcp"}}}`)
	col := &collector{}
	res, err := New(Options{}).Run(context.Background(), sp, col)
	if err != nil || res.IsError || res.Summary != "ok sem tools" || !strings.Contains(strings.Join(col.text(domain.EventSystem), "|"), "indisponíveis") {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestCancelStopsTheLoop(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lido o corpo, o servidor passa a notar o cliente que se foi
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer slow.Close()
	raw, _ := json.Marshal(map[string]any{"base_url": slow.URL, "model": "m"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	res, err := New(Options{}).Run(ctx, adapter.Spec{Prompt: "x", Config: raw}, &collector{})
	if err != nil || !res.IsError || !strings.Contains(res.Error, "interrompida") || time.Since(t0) > 10*time.Second {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestConfig(t *testing.T) {
	for in, want := range map[string]string{
		``:                                       "base_url",
		`{"base_url":"ftp://x"}`:                 "base_url",
		`{"base_url":"http://u:p@h/v1"}`:         "base_url",
		`{"base_url":"http://h","max_steps":99}`: "max_steps",
		`{"base_url":"http://h","temperature":3}`:    "temperature",
		`{"base_url":"http://h","timeout_sec":9999}`: "timeout_sec",
		`{"base_url":"http://h","tolls":[]}`:         "http accepts",
		`{"base_url":"http://h","tools":[""]}`:       "tools",
	} {
		if _, err := ParseConfig(json.RawMessage(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v (queria %q)", in, err, want)
		}
	}
	c, err := ParseConfig(json.RawMessage(`{"base_url":" http://h/v1 ","api_key_ref":"${secret:k}"}`))
	if err != nil || c.BaseURL != "http://h/v1" || c.maxSteps() != defaultMaxSteps || c.timeout() != defaultTimeoutSec*time.Second {
		t.Fatalf("%+v %v", c, err)
	}
	isRef := func(s string) bool { return strings.HasPrefix(s, "${secret:") }
	if err := c.ValidateHire(isRef); err != nil {
		t.Fatal(err)
	}
	c.APIKeyRef = "sk-em-claro"
	if err := c.ValidateHire(isRef); err == nil {
		t.Fatal("chave em claro deve ser recusada na contratação")
	}
	if _, err := New(Options{}).Run(context.Background(), adapter.Spec{Config: json.RawMessage(`{"base_url":"http://h"}`)}, &collector{}); err == nil {
		t.Fatal("sem modelo deve falhar")
	}
}

func TestProbe(t *testing.T) {
	llm := newFakeLLM(t)
	a := New(Options{})
	step := func(r adapter.ProbeResult, name string) *adapter.ProbeStep {
		for i := range r.Steps {
			if r.Steps[i].Name == name {
				return &r.Steps[i]
			}
		}
		return nil
	}
	if r := a.Probe(context.Background(), spec(t, llm, nil)); !r.OK || step(r, "modelo") == nil {
		t.Fatalf("pronto: %+v", r)
	}
	if r := a.Probe(context.Background(), spec(t, llm, map[string]any{"model": "outro"})); r.OK || step(r, "modelo").OK {
		t.Fatalf("modelo fora do catálogo: %+v", r)
	}
	if r := a.Probe(context.Background(), spec(t, llm, map[string]any{"api_key_ref": "errada"})); r.OK || step(r, "autenticação").OK {
		t.Fatalf("chave errada: %+v", r)
	}
	if r := a.Probe(context.Background(), adapter.Spec{Config: json.RawMessage(`{"base_url":"http://127.0.0.1:1","model":"m"}`)}); r.OK || step(r, "endpoint").OK {
		t.Fatalf("endpoint fora do ar: %+v", r)
	}
	if r := a.Probe(context.Background(), adapter.Spec{Config: json.RawMessage(`{`)}); r.OK || r.Steps[0].Name != "configuração" {
		t.Fatalf("config ruim: %+v", r)
	}
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	raw, _ := json.Marshal(map[string]any{"base_url": notFound.URL, "model": "m"})
	if r := a.Probe(context.Background(), adapter.Spec{Config: raw}); !r.OK {
		t.Fatalf("servidor local sem /models não reprova: %+v", r)
	}
}
