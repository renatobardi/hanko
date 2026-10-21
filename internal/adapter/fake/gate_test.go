package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// O fake pede um gate como um agente de verdade: tools/call hanko_gate_request no MCP do Hanko, com o token da run.
func TestRunGateCallsHankoMCP(t *testing.T) {
	var gotAuth, gotAccept string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"id\":\"g1\"}"}]}}`)
	}))
	defer srv.Close()

	runID := domain.NewID()
	spec := adapter.Spec{RunID: runID, TaskKey: "ACM-3",
		MCPConfig: json.RawMessage(fmt.Sprintf(`{"mcpServers":{"hanko":{"type":"http","url":%q,"headers":{"Authorization":"Bearer tok-da-run"}}}}`, srv.URL)),
		Config:    json.RawMessage(`{"gate":{"title":"Publicar o relatório?","body":"Impacto: nenhum."}}`)}
	r := &rec{}
	res, err := New(Options{Delay: -1}).Run(context.Background(), spec, r)
	if err != nil || res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotAuth != "Bearer tok-da-run" || gotAccept == "" {
		t.Errorf("headers: auth=%q accept=%q", gotAuth, gotAccept)
	}
	params, _ := gotBody["params"].(map[string]any)
	args, _ := params["arguments"].(map[string]any)
	if gotBody["method"] != "tools/call" || params["name"] != "hanko_gate_request" || args["title"] != "Publicar o relatório?" ||
		args["event_id"] != "fake-gate-"+runID.String() {
		t.Errorf("corpo = %+v", gotBody)
	}
	var sawResult bool
	for _, e := range r.ev {
		if e.Kind == domain.EventToolResult && e.Text == "gate pedido: Publicar o relatório?" {
			sawResult = true
		}
	}
	if !sawResult {
		t.Errorf("eventos sem o resultado do gate: %+v", r.ev)
	}
}

func TestRunGateErrors(t *testing.T) {
	cases := map[string]struct {
		mcp    string
		status int
		body   string
	}{
		"sem MCP":          {mcp: ``},
		"sem hanko":        {mcp: `{"mcpServers":{"outro":{"url":"http://x"}}}`},
		"http 500":         {status: 500, body: `{}`},
		"erro JSON-RPC":    {status: 200, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"sem permissão"}}`},
		"tool com isError": {status: 200, body: `{"jsonrpc":"2.0","id":1,"result":{"isError":true,"content":[{"type":"text","text":"task fechada"}]}}`},
		"resposta lixo":    {status: 200, body: `não é json`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			mcp := c.mcp
			if c.status != 0 {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(c.status)
					_, _ = io.WriteString(w, c.body)
				}))
				defer srv.Close()
				mcp = fmt.Sprintf(`{"mcpServers":{"hanko":{"url":%q}}}`, srv.URL)
			}
			spec := adapter.Spec{RunID: domain.NewID(), TaskKey: "ACM-1", MCPConfig: json.RawMessage(mcp), Config: json.RawMessage(`{"gate":{}}`)}
			res, err := New(Options{Delay: -1}).Run(context.Background(), spec, &rec{})
			if err != nil || !res.IsError || res.ExitCode != 1 || res.Error == "" {
				t.Fatalf("a falha do gate deve virar run com erro: res=%+v err=%v", res, err)
			}
		})
	}
}

func TestRunGateIgnoredWhenFailing(t *testing.T) {
	// fail=true vence: nenhuma chamada de rede
	res, err := New(Options{Delay: -1}).Run(context.Background(), adapter.Spec{TaskKey: "X-1", Config: json.RawMessage(`{"fail":true,"gate":{}}`)}, &rec{})
	if err != nil || !res.IsError || res.Error != "falha simulada pelo adapter fake" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
