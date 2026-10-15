package aimemory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/memory"
)

type searchIn struct {
	Workspace string `json:"workspace"`
	Query     string `json:"query"`
	Limit     int    `json:"limit"`
}

// searchFake é um servidor MCP falso só com a tool memory_search (o contrato real não está confirmado: ADR-0024).
type searchFake struct {
	mu   sync.Mutex
	in   []searchIn
	text string
	fail bool
	srv  *httptest.Server
}

func newSearchFake(t *testing.T, text string) *searchFake {
	f := &searchFake{text: text}
	s := sdk.NewServer(&sdk.Implementation{Name: "fake-ai-memory", Version: "1"}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: SearchTool, Description: "search"}, func(_ context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.in = append(f.in, in)
		if f.fail {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "índice indisponível"}}}, nil, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: f.text}}}, nil, nil
	})
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", inner)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestSearchJSON(t *testing.T) {
	f := newSearchFake(t, `{"results":[{"path":"hanko/tasks/HNK-3.md","title":"HNK-3 — Deploy","snippet":"o deploy falhou","score":0.9},{"path":"","title":"","snippet":""}]}`)
	p := New(Options{URL: f.srv.URL})
	hits, err := p.Search(context.Background(), memory.Scope{Workspace: "acme", Project: "ignorado"}, "deploy", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "hanko/tasks/HNK-3.md" || hits[0].Snippet != "o deploy falhou" || hits[0].Score != 0.9 {
		t.Fatalf("hits = %+v", hits)
	}
	if len(f.in) != 1 || f.in[0] != (searchIn{Workspace: "acme", Query: "deploy", Limit: 5}) {
		t.Errorf("args da tool = %+v (a busca vale para o workspace inteiro, sem project)", f.in)
	}
}

func TestSearchTextoLivreEListaJSON(t *testing.T) {
	f := newSearchFake(t, "# Deploy de maio\nfalhou no passo 3\n\n## Rollback\nfeito em 5 min")
	hits, err := New(Options{URL: f.srv.URL}).Search(context.Background(), memory.Scope{Workspace: "acme"}, "deploy", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Title != "Deploy de maio" || !strings.Contains(hits[0].Snippet, "passo 3") || hits[1].Title != "Rollback" {
		t.Fatalf("hits = %+v", hits)
	}
	if got := parseSearch(`[{"title":"a","content":"b"}]`); len(got) != 1 || got[0].Snippet != "b" {
		t.Errorf("lista JSON: %+v", got)
	}
	if got := parseSearch("  "); got != nil {
		t.Errorf("vazio: %+v", got)
	}
}

func TestSearchLimiteEErro(t *testing.T) {
	f := newSearchFake(t, `[{"title":"a"},{"title":"b"},{"title":"c"}]`)
	p := New(Options{URL: f.srv.URL})
	hits, err := p.Search(context.Background(), memory.Scope{Workspace: "acme"}, "x", 2)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if _, err := p.Search(context.Background(), memory.Scope{Workspace: "acme"}, "x", 2); err == nil {
		t.Error("erro de tool deveria virar erro")
	}
}
