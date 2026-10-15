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

type searchScope struct {
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
}

type searchIn struct {
	Workspace string        `json:"workspace,omitempty"`
	Project   string        `json:"project,omitempty"`
	Query     string        `json:"query"`
	Limit     int           `json:"limit,omitempty"`
	Scopes    []searchScope `json:"scopes,omitempty"`
}

// searchFake é um servidor MCP falso só com a tool memory_query (contrato confirmado num servidor real em 2026-10-15).
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
	hits, err := p.Search(context.Background(), []memory.Scope{{Workspace: "acme", Project: "api"}}, "deploy", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "hanko/tasks/HNK-3.md" || hits[0].Snippet != "o deploy falhou" || hits[0].Score != 0.9 {
		t.Fatalf("hits = %+v", hits)
	}
	if len(f.in) != 1 || f.in[0].Workspace != "acme" || f.in[0].Project != "api" || f.in[0].Query != `"deploy"` || f.in[0].Limit != 5 || len(f.in[0].Scopes) != 0 {
		t.Errorf("args da tool = %+v (um escopo: workspace+project, sem scopes)", f.in)
	}
}

func TestSearchTextoLivreEListaJSON(t *testing.T) {
	f := newSearchFake(t, "# Deploy de maio\nfalhou no passo 3\n\n## Rollback\nfeito em 5 min")
	hits, err := New(Options{URL: f.srv.URL}).Search(context.Background(), []memory.Scope{{Workspace: "acme"}}, "deploy", 10)
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
	hits, err := p.Search(context.Background(), []memory.Scope{{Workspace: "acme"}}, "x", 2)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if _, err := p.Search(context.Background(), []memory.Scope{{Workspace: "acme"}}, "x", 2); err == nil {
		t.Error("erro de tool deveria virar erro")
	}
}

// Vários escopos vão em scopes=[{workspace,project}]; global nunca é usado; a consulta é sanitizada para o FTS5.
func TestSearchVariosEscoposESanitiza(t *testing.T) {
	f := newSearchFake(t, `{"results":[{"path":"a.md","title":"<mark>Deploy</mark> &amp; rollback","snippet":"o <mark>deploy</mark> falhou"}]}`)
	p := New(Options{URL: f.srv.URL})
	hits, err := p.Search(context.Background(), []memory.Scope{{Workspace: "acme", Project: "default"}, {Workspace: "acme", Project: "api"}}, `deploy -x "y" a:b*`, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Title != "Deploy & rollback" || hits[0].Snippet != "o deploy falhou" {
		t.Fatalf("HTML do snippet deveria virar texto: %+v", hits)
	}
	in := f.in[0]
	if in.Workspace != "" || len(in.Scopes) != 2 || in.Scopes[1] != (searchScope{"acme", "api"}) {
		t.Errorf("scopes = %+v", in)
	}
	if in.Query != `"deploy" "x" "y" "a" "b"` {
		t.Errorf("query = %q", in.Query)
	}
}

func TestFTSQuery(t *testing.T) {
	for in, want := range map[string]string{"": "", "  ": "", `"`: "", "a-b": `"a" "b"`, "ação São": `"ação" "São"`, "x_y 1": `"x_y" "1"`} {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q; quer %q", in, got, want)
		}
	}
}
