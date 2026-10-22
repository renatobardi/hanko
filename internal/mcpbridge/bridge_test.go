package mcpbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text"`
}

// fakeRemote sobe um servidor MCP http com a tool "eco" e registra o Authorization visto.
func fakeRemote(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "0"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "eco", Description: "devolve o texto"}, func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "eco:" + in.Text}}}, nil, nil
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	var mu sync.Mutex
	var auths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), auths...) }
}

func TestBridgeRepassaToolsEToken(t *testing.T) {
	ts, auths := fakeRemote(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// o "cliente da IDE" fala com a ponte por um par de transportes em memória
	idePipe, bridgeLocal := sdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, bridgeLocal, RemoteTransport(Endpoint(ts.URL+"/"), "hk_segredo", nil)) }()

	cl := sdk.NewClient(&sdk.Implementation{Name: "ide", Version: "0"}, nil)
	sess, err := cl.Connect(ctx, idePipe, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	tools, err := sess.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "eco" {
		t.Fatalf("tools: %v %+v", err, tools)
	}
	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "eco", Arguments: map[string]any{"text": "oi"}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; got != "eco:oi" {
		t.Fatalf("resposta %q", got)
	}
	_ = sess.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("a ponte não encerrou quando o cliente saiu")
	}
	as := auths()
	if len(as) == 0 {
		t.Fatal("o servidor remoto não recebeu chamadas")
	}
	for _, a := range as {
		if a != "Bearer hk_segredo" {
			t.Fatalf("Authorization %q", a)
		}
	}
}

func TestEndpointNormalizaBarra(t *testing.T) {
	if got := Endpoint("http://h:1///"); got != "http://h:1/mcp" {
		t.Fatal(got)
	}
}

func TestBridgeRemotoIndisponivel(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close() // porta fechada: o Connect do remoto ou a primeira mensagem falha
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ide, local := sdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, local, RemoteTransport(Endpoint(url), "x", nil)) }()
	cl := sdk.NewClient(&sdk.Implementation{Name: "ide", Version: "0"}, nil)
	if sess, err := cl.Connect(ctx, ide, nil); err == nil {
		_ = sess.Close()
		t.Fatal("esperava erro com o servidor fora do ar")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("a ponte ficou presa com o servidor fora do ar")
	}
}
