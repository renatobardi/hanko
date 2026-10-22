package evals

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// o roteiro certo de cada cenário passa; o errado (controle negativo) reprova: prova que o verificador pega o erro.
func TestFakeAprovaEControleNegativoReprova(t *testing.T) {
	for _, sc := range Scenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			rep, err := RunSuite(ctxT(t), Config{Scenario: sc.Name}, Scenarios())
			if err != nil || !rep.OK || rep.Scenarios[0].Passed != 1 {
				t.Fatalf("roteiro certo reprovou: %v %+v", err, rep.Scenarios)
			}
			bad, err := RunSuite(ctxT(t), Config{Scenario: sc.Name, Bad: true}, Scenarios())
			if err != nil {
				t.Fatal(err)
			}
			if bad.OK || len(bad.Scenarios[0].Failures) == 0 {
				t.Fatalf("o controle negativo passou: %+v", bad.Scenarios[0])
			}
		})
	}
}

func TestSuiteSelecao(t *testing.T) {
	if n := len(Select(Scenarios(), SuiteChaos, "")); n != 2 {
		t.Fatalf("chaos: %d", n)
	}
	if n := len(Select(Scenarios(), "all", "")); n != len(Scenarios()) {
		t.Fatalf("all: %d", n)
	}
	if _, err := RunSuite(ctxT(t), Config{Scenario: "nao-existe"}, Scenarios()); err == nil {
		t.Fatal("esperava erro de cenário inexistente")
	}
}

func TestWorldRegrasDasTools(t *testing.T) {
	w := NewWorld(equipe, WTask{Title: "x"})
	ctx := ctxT(t)
	call := func(name string, a map[string]any) (string, bool) {
		out, isErr, err := w.Call(ctx, ToolCall{Name: name, Args: a})
		if err != nil {
			t.Fatal(err)
		}
		return out, isErr
	}
	if _, e := call("hanko_task_create", map[string]any{"title": "a", "assignee": "fantasma"}); !e {
		t.Error("aceitou persona inexistente")
	}
	if _, e := call("hanko_task_create", map[string]any{"title": "a", "depends_on": []any{"HNK-99"}}); !e {
		t.Error("aceitou dependência inexistente")
	}
	out, e := call("hanko_task_create", map[string]any{"title": "a"})
	if e || keyOf(out) != "HNK-2" {
		t.Fatalf("criar: %q %v", out, e)
	}
	if _, e := call("hanko_task_dependency", map[string]any{"action": "add", "key": "HNK-2", "depends_on": "HNK-2"}); !e {
		t.Error("aceitou auto-dependência")
	}
	if _, e := call("hanko_task_dependency", map[string]any{"action": "add", "key": "HNK-2", "depends_on": "HNK-1"}); e {
		t.Error("recusou dependência válida")
	}
	if w.Tasks["HNK-2"].Status != "blocked" {
		t.Errorf("status %q", w.Tasks["HNK-2"].Status)
	}
	if _, e := call("hanko_gate_request", map[string]any{}); !e {
		t.Error("gate sem título")
	}
	if out, e := call("hanko_gate_request", map[string]any{"title": "ok"}); e || !strings.Contains(out, "pending") {
		t.Errorf("gate: %q", out)
	}
	if _, e := call("tool_que_nao_existe", nil); !e {
		t.Error("tool desconhecida deveria ser erro")
	}
}

func TestFaultyInjetaPorOrdem(t *testing.T) {
	w := NewWorld(equipe, WTask{})
	f := &Faulty{Inner: w, Faults: []Fault{{From: 2, To: 3, Message: "fora"}}}
	ctx := ctxT(t)
	var got []bool
	for i := 0; i < 4; i++ {
		_, isErr, _ := f.Call(ctx, ToolCall{Name: "hanko_persona_list"})
		got = append(got, isErr && f.WasInjected())
	}
	want := []bool{false, true, true, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chamada %d: %v, esperado %v", i+1, got, want)
		}
	}
}

func TestRunAgentLimiteDePassos(t *testing.T) {
	loop := ScriptedModel{Policy: func(Request) Reply {
		return call("c", "hanko_persona_list", map[string]any{})
	}}
	run, err := RunAgent(ctxT(t), loop, NewWorld(equipe, WTask{}), "", "oi", 3)
	if err != ErrStepLimit || len(run.Trace) != 3 {
		t.Fatalf("%v %d", err, len(run.Trace))
	}
}

func TestLiveSemChaveERecusadoNoPR(t *testing.T) {
	var out, errb bytes.Buffer
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c := Main(ctxT(t), []string{"--live"}, env(nil), &out, &errb); c != ExitUsage || !strings.Contains(errb.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("live sem chave: %d %q", c, errb.String())
	}
	errb.Reset()
	if c := Main(ctxT(t), nil, env(map[string]string{"HANKO_EVALS_LIVE": "1", "ANTHROPIC_API_KEY": "k", "GITHUB_EVENT_NAME": "pull_request"}), &out, &errb); c != ExitUsage || !strings.Contains(errb.String(), "pull request") {
		t.Fatalf("live em PR: %d %q", c, errb.String())
	}
}

func TestMainFakePorPadraoGeraRelatorio(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	getenv := func(k string) string {
		if k == "GITHUB_STEP_SUMMARY" {
			return filepath.Join(dir, "sum.md")
		}
		return "" // sem HANKO_EVALS_LIVE nem chave: nada pago
	}
	code := Main(ctxT(t), []string{"--report", filepath.Join(dir, "r.json")}, getenv, &out, &errb)
	if code != ExitOK {
		t.Fatalf("code %d\n%s%s", code, out.String(), errb.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "r.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "fake" || rep.Cost.Kind != "test" || rep.Cost.USD != 0 || !rep.OK || len(rep.Scenarios) != len(Scenarios()) {
		t.Fatalf("relatório: %+v", rep)
	}
	if s, _ := os.ReadFile(filepath.Join(dir, "sum.md")); !strings.Contains(string(s), "evals (fake): verde") {
		t.Fatalf("resumo: %q", s)
	}
	// controle negativo pelo comando: tem de sair vermelho
	if code := Main(ctxT(t), []string{"--bad"}, getenv, &out, &errb); code != ExitFailed {
		t.Fatalf("--bad deveria falhar, code %d", code)
	}
}

// ---------- modelo real contra um servidor de mentira (nenhuma chamada paga) ----------

func fakeAnthropic(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "k-teste" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, `{"error":{"type":"auth","message":"x"}}`, http.StatusUnauthorized)
			return
		}
		var req aRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls.Add(1)
		w.Header().Set("content-type", "application/json")
		if len(req.Messages) == 1 {
			if len(req.Tools) == 0 || req.System == "" || req.Model != "haiku-teste" {
				t.Errorf("pedido sem tools/system/model: %+v", req)
			}
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"vou pedir"},{"type":"tool_use","id":"tu1","name":"hanko_gate_request","input":{"title":"t","body":"b"}}],"usage":{"input_tokens":100,"output_tokens":20}}`))
			return
		}
		// segunda volta: tem de trazer o tool_result do id certo
		last := req.Messages[len(req.Messages)-1]
		if last.Role != "user" || len(last.Content) == 0 || last.Content[0].Type != "tool_result" || last.Content[0].ToolUseID != "tu1" {
			t.Errorf("sem tool_result: %+v", last)
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"aguardo"}],"usage":{"input_tokens":150,"output_tokens":5}}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestAnthropicLaçoCompletoContraServidorDeMentira(t *testing.T) {
	var n atomic.Int32
	ts := fakeAnthropic(t, &n)
	m := &Anthropic{APIKey: "k-teste", Model: "haiku-teste", BaseURL: ts.URL}
	run, err := RunAgent(ctxT(t), m, NewWorld(equipe, WTask{}), personaSystem, "faça", 5)
	if err != nil || n.Load() != 2 {
		t.Fatalf("%v chamadas=%d", err, n.Load())
	}
	if run.Final != "aguardo" || len(run.Calls("hanko_gate_request")) != 1 || run.Usage.InputTokens != 250 || run.Usage.OutputTokens != 25 {
		t.Fatalf("run: %+v", run)
	}
}

func TestAnthropicErroDaAPI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"devagar"}}`))
	}))
	defer ts.Close()
	_, err := (&Anthropic{APIKey: "k", BaseURL: ts.URL}).Next(ctxT(t), Request{Messages: []Message{{Role: "user", Text: "oi"}}})
	if err == nil || !strings.Contains(err.Error(), "rate_limit_error") {
		t.Fatalf("%v", err)
	}
	if _, err := (&Anthropic{}).Next(ctxT(t), Request{}); err == nil {
		t.Fatal("sem chave deveria falhar antes de qualquer rede")
	}
}

func TestRunSuiteLiveRespeitaTetoDeGasto(t *testing.T) {
	var n atomic.Int32
	ts := fakeAnthropic(t, &n)
	// cada tentativa gasta 250 in + 25 out tokens; com preço absurdo estoura o teto na primeira
	cfg := Config{Live: true, Trials: 5, MaxUSD: 0.5, Pricing: Pricing{InPerM: 1e6, OutPerM: 1e6},
		NewModel: func() Model { return &Anthropic{APIKey: "k-teste", Model: "haiku-teste", BaseURL: ts.URL} }, Scenario: "gate-antes-de-producao"}
	rep, err := RunSuite(ctxT(t), cfg, Scenarios())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Aborted == "" || rep.OK || rep.Scenarios[0].Trials != 1 || rep.Cost.Kind != "test" || rep.Cost.USD <= 0.5 {
		t.Fatalf("teto não respeitado: %+v", rep)
	}
	if _, err := RunSuite(ctxT(t), Config{Live: true}, Scenarios()); err == nil {
		t.Fatal("live sem modelo deveria ser recusado")
	}
	if _, err := RunSuite(ctxT(t), Config{Live: true, Bad: true, NewModel: func() Model { return nil }}, Scenarios()); err == nil {
		t.Fatal("--bad no live deveria ser recusado")
	}
}

// ---------- MCPHarness contra um servidor MCP de mentira ----------

func TestMCPHarnessListaEChamaTools(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "0"}, nil)
	type in struct {
		Title string `json:"title"`
	}
	sdk.AddTool(srv, &sdk.Tool{Name: "hanko_gate_request", Description: "pede gate"}, func(_ context.Context, _ *sdk.CallToolRequest, a in) (*sdk.CallToolResult, any, error) {
		if a.Title == "" {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "title é obrigatório"}}}, nil, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "gate " + a.Title}}}, nil, nil
	})
	ctx := ctxT(t)
	a, b := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	h, err := NewMCPHarness(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if ts := h.Tools(); len(ts) != 1 || ts[0].Name != "hanko_gate_request" || ts[0].InputSchema["type"] != "object" {
		t.Fatalf("tools: %+v", ts)
	}
	out, isErr, err := h.Call(ctx, ToolCall{Name: "hanko_gate_request", Args: map[string]any{"title": "x"}})
	if err != nil || isErr || out != "gate x" {
		t.Fatalf("%q %v %v", out, isErr, err)
	}
	if out, isErr, _ := h.Call(ctx, ToolCall{Name: "hanko_gate_request", Args: map[string]any{"title": ""}}); !isErr || !strings.Contains(out, "obrigatório") {
		t.Fatalf("erro da tool: %q %v", out, isErr)
	}
}

type fakeInjector struct{ injected, restored int }

func (f *fakeInjector) Name() string                  { return "fake" }
func (f *fakeInjector) Inject(context.Context) error  { f.injected++; return nil }
func (f *fakeInjector) Restore(context.Context) error { f.restored++; return nil }

func TestRunWithInjectorSempreRestaura(t *testing.T) {
	inj := &fakeInjector{}
	err := RunWithInjector(ctxT(t), inj, func(context.Context) error { return os.ErrInvalid })
	if err != os.ErrInvalid || inj.injected != 1 || inj.restored != 1 {
		t.Fatalf("%v %+v", err, inj)
	}
}
