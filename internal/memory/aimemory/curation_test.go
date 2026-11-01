package aimemory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/memory"
)

type pageIn struct {
	Path      string `json:"path"`
	Workspace string `json:"workspace,omitempty"`
	Project   string `json:"project,omitempty"`
	MaxChars  int    `json:"max_chars,omitempty"`
}

// curationServer é um ai-memory falso com uma tool de remoção e uma de leitura de página.
type curationServer struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls []pageIn
	fail  bool
	pages map[string]string
}

func newCurationServer(t *testing.T) *curationServer {
	t.Helper()
	cs := &curationServer{pages: map[string]string{"wiki/a": "conteúdo da página"}}
	s := sdk.NewServer(&sdk.Implementation{Name: "fake-ai-memory", Version: "1"}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: "memory_delete_page", Description: "delete"}, func(_ context.Context, _ *sdk.CallToolRequest, in pageIn) (*sdk.CallToolResult, any, error) {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		cs.calls = append(cs.calls, in)
		if cs.fail {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "indisponível"}}}, nil, nil
		}
		delete(cs.pages, in.Path)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "deleted"}}}, nil, nil
	})
	sdk.AddTool(s, &sdk.Tool{Name: "memory_read_page", Description: "read"}, func(_ context.Context, _ *sdk.CallToolRequest, in pageIn) (*sdk.CallToolResult, any, error) {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		cs.calls = append(cs.calls, in)
		txt, ok := cs.pages[in.Path]
		if !ok {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "não existe"}}}, nil, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: txt}}}, nil, nil
	})
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	cs.srv = httptest.NewServer(mux)
	t.Cleanup(cs.srv.Close)
	return cs
}

func (c *curationServer) last(t *testing.T) pageIn {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		t.Fatal("servidor não foi chamado")
	}
	return c.calls[len(c.calls)-1]
}

func TestCallToolEscopoSobrepoeArgumentos(t *testing.T) {
	cs := newCurationServer(t)
	p := New(Options{URL: cs.srv.URL, Token: testToken, Timeout: 3 * time.Second}).(memory.ToolCaller)
	out, err := p.CallTool(context.Background(), memory.Scope{Workspace: "acme", Project: "api"}, "memory_delete_page",
		map[string]any{"path": "wiki/a", "workspace": "outra-empresa", "project": "alheio"})
	if err != nil || out != "deleted" {
		t.Fatalf("CallTool = %q, %v", out, err)
	}
	if got := cs.last(t); got.Workspace != "acme" || got.Project != "api" || got.Path != "wiki/a" {
		t.Errorf("chamada = %+v", got)
	}
	cs.mu.Lock()
	_, still := cs.pages["wiki/a"]
	cs.mu.Unlock()
	if still {
		t.Error("a página não foi removida")
	}
}

func TestCallToolFalhaDeToolEFalhaDeRede(t *testing.T) {
	cs := newCurationServer(t)
	p := New(Options{URL: cs.srv.URL, Token: testToken, Timeout: 2 * time.Second}).(memory.ToolCaller)
	cs.mu.Lock()
	cs.fail = true
	cs.mu.Unlock()
	if _, err := p.CallTool(context.Background(), memory.Scope{Workspace: "acme"}, "memory_delete_page", map[string]any{"path": "wiki/a"}); err == nil {
		t.Error("erro de tool deveria voltar como erro")
	}
	cs.srv.Close()
	if _, err := p.CallTool(context.Background(), memory.Scope{Workspace: "acme"}, "memory_delete_page", map[string]any{"path": "wiki/a"}); err == nil {
		t.Error("servidor fora do ar deveria voltar como erro")
	}
}

func TestReadPageSemToolConfiguradaEUnsupported(t *testing.T) {
	cs := newCurationServer(t)
	p := New(Options{URL: cs.srv.URL, Token: testToken}).(memory.PageReader)
	if _, err := p.ReadPage(context.Background(), memory.Scope{Workspace: "acme"}, "wiki/a"); !errors.Is(err, memory.ErrUnsupported) {
		t.Errorf("err = %v; esperava ErrUnsupported", err)
	}
}

func TestReadPageDevolveOCorpoEPedeOTetoDoServidor(t *testing.T) {
	var args map[string]any
	s := sdk.NewServer(&sdk.Implementation{Name: "ai-memory", Version: "3.3.1"}, nil)
	resposta := `{"path":"wiki/a","body":"linha 1\nlinha 2","truncated":false,"total_chars":15}`
	sdk.AddTool(s, &sdk.Tool{Name: "memory_read_page", Description: "read"}, func(_ context.Context, _ *sdk.CallToolRequest, in map[string]any) (*sdk.CallToolResult, any, error) {
		args = in
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: resposta}}}, nil, nil
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New(Options{URL: srv.URL, ReadPageTool: "memory_read_page", Timeout: 3 * time.Second}).(memory.PageReader)
	scope := memory.Scope{Workspace: "acme", Project: "api"}

	txt, err := p.ReadPage(context.Background(), scope, "wiki/a")
	if err != nil || txt != "linha 1\nlinha 2" {
		t.Fatalf("ReadPage = %q, %v", txt, err)
	}
	if got, _ := args["max_chars"].(float64); got != readPageMaxChars {
		t.Errorf("max_chars = %v; a 3.3 corta em 12.000 sem esse pedido", args["max_chars"])
	}

	resposta = `{"path":"wiki/a","body":"início","truncated":true,"total_chars":90000}`
	txt, err = p.ReadPage(context.Background(), scope, "wiki/a")
	if err != nil || !strings.HasPrefix(txt, "início") || !strings.Contains(txt, "90000") {
		t.Fatalf("página cortada precisa avisar no diff: %q, %v", txt, err)
	}
}

func TestReadPageComToolConfigurada(t *testing.T) {
	cs := newCurationServer(t)
	p := New(Options{URL: cs.srv.URL, Token: testToken, ReadPageTool: "memory_read_page", Timeout: 3 * time.Second}).(memory.PageReader)
	txt, err := p.ReadPage(context.Background(), memory.Scope{Workspace: "acme", Project: "api"}, "wiki/a")
	if err != nil || txt != "conteúdo da página" {
		t.Fatalf("ReadPage = %q, %v", txt, err)
	}
	if got := cs.last(t); got.Workspace != "acme" || got.Project != "api" {
		t.Errorf("escopo = %+v", got)
	}
	if _, err := p.ReadPage(context.Background(), memory.Scope{Workspace: "acme", Project: "api"}, "wiki/ausente"); err == nil {
		t.Error("página inexistente deveria dar erro")
	}
}

// Uma proposta aprovada cuja tool não traz workspace/project é repetida como foi proposta: o Hanko não acrescenta
// argumentos que a tool não declara (o escopo vai nos cabeçalhos).
func TestCallToolNaoAcrescentaEscopoAusente(t *testing.T) {
	cs := newCurationServer(t)
	p := New(Options{URL: cs.srv.URL, Token: testToken, Timeout: 3 * time.Second}).(memory.ToolCaller)
	if _, err := p.CallTool(context.Background(), memory.Scope{Workspace: "acme", Project: "api"}, "memory_delete_page",
		map[string]any{"path": "wiki/a"}); err != nil {
		t.Fatal(err)
	}
	if got := cs.last(t); got.Workspace != "" || got.Project != "" || got.Path != "wiki/a" {
		t.Errorf("chamada = %+v; esperava só o path", got)
	}
}
