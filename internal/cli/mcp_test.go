package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpEcoIn struct {
	Text string `json:"text"`
}

// stdin com o handshake completo e EOF logo depois: a ponte tem de esperar as respostas antes de sair.
func TestMcpCmdRepassaStdinParaORemotoComToken(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "0"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "eco", Description: "devolve o texto"}, func(_ context.Context, _ *sdk.CallToolRequest, in mcpEcoIn) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "eco:" + in.Text}}}, nil, nil
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	var mu sync.Mutex
	var auths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()

	stdin := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"ide","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"eco","arguments":{"text":"oi"}}}`,
	}, "\n") + "\n"
	code, out, errb := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL, "HANKO_TOKEN": "hk_abc"}, stdin, "mcp")
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errb)
	}
	if !strings.Contains(out, "eco:oi") || !strings.Contains(out, `"id":1`) {
		t.Fatalf("saída sem as respostas: %q", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 {
		t.Fatal("sem chamadas ao remoto")
	}
	for _, a := range auths {
		if a != "Bearer hk_abc" {
			t.Fatalf("Authorization %q", a)
		}
	}
}
