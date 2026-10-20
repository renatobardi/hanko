package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatSendsKeyAndParsesToolCallsAndCost(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"model":"vendor/m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
		 "tool_calls":[{"id":"c1","type":"function","function":{"name":"echo","arguments":"{\"x\":1}"}}]}}],
		 "usage":{"prompt_tokens":10,"completion_tokens":5,"cost":0.0042}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/v1/", APIKey: "sk-test", HTTP: srv.Client()}
	resp, err := c.Chat(context.Background(), ChatRequest{Model: "vendor/m", JSONObject: true,
		Messages: []Message{{Role: "user", Content: "oi"}},
		Tools:    []Tool{{Type: "function", Function: FuncDef{Name: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" {
		t.Fatalf("auth = %q", auth)
	}
	if got["response_format"] == nil || got["tools"] == nil {
		t.Fatalf("pedido sem response_format/tools: %v", got)
	}
	if u, _ := got["usage"].(map[string]any); u["include"] != true {
		t.Fatalf("pedido deve pedir usage.include: %v", got["usage"])
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Function.Name != "echo" || resp.FinishReason != "tool_calls" {
		t.Fatalf("resposta: %+v", resp)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 || resp.Usage.CostUSD == nil || *resp.Usage.CostUSD != 0.0042 {
		t.Fatalf("uso: %+v", resp.Usage)
	}
}

func TestChatErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/401/chat/completions":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`bad key sk-secret`))
		case "/empty/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[]}`))
		case "/apierr/chat/completions":
			_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
		default:
			_, _ = w.Write([]byte(`not json`))
		}
	}))
	defer srv.Close()
	chat := func(p string) error {
		c := &Client{BaseURL: srv.URL + p, APIKey: "sk-secret", HTTP: srv.Client()}
		_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
		return err
	}
	if err := chat("/401"); err == nil || strings.Contains(err.Error(), "sk-secret") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 deve citar o status e mascarar a chave: %v", err)
	}
	for p, want := range map[string]string{"/empty": "sem choices", "/apierr": "quota", "/junk": "JSON"} {
		if err := chat(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v (queria %q)", p, err, want)
		}
	}
	c := &Client{BaseURL: "ftp://x"}
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m"}); err == nil {
		t.Fatal("base_url inválida deve falhar")
	}
	if _, err := (&Client{BaseURL: srv.URL}).Chat(context.Background(), ChatRequest{}); err == nil {
		t.Fatal("modelo vazio deve falhar")
	}
}

func TestCatalogParsesPricesAndSorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
		 {"id":"z/last","name":"Z","context_length":1000,"pricing":{"prompt":"0.000002","completion":"0.000004"}},
		 {"id":"","name":"sem id"},
		 {"id":"a/first","name":"A","pricing":{"prompt":"x","completion":"-1"}}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/api/v1", HTTP: srv.Client()}
	ms, err := c.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "a/first" || ms[1].ID != "z/last" {
		t.Fatalf("catálogo: %+v", ms)
	}
	if ms[0].PromptUSD != 0 || ms[0].CompletionUSD != 0 || ms[1].PromptUSD != 0.000002 || ms[1].CompletionUSD != 0.000004 {
		t.Fatalf("preços: %+v", ms)
	}
	if got := EstimateCost(Usage{PromptTokens: 1000, CompletionTokens: 500}, ms[1].PromptUSD, ms[1].CompletionUSD); got < 0.0039 || got > 0.0041 {
		t.Fatalf("estimativa = %v", got)
	}
	if _, err := (&Client{BaseURL: srv.URL + "/nope", HTTP: srv.Client()}).Catalog(context.Background()); err == nil {
		t.Fatal("404 deve falhar")
	}
}
