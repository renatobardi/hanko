package aimemory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/memory"
)

const testToken = "s3cr3t-token"

type rec struct {
	Tool string
	Args map[string]any
	Auth string
}

// fakeServer simula o ai-memory: MCP (go-sdk, com sessões) + /health + /hook/batch.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	h          http.Handler
	calls      []rec
	hookBodies []string
	hookAuth   string
	healthCode int
	briefing   string
	handoff    string
	strict     bool // write_page não aceita kind/tag
	failWrite  bool // write_page devolve erro de tool
	statusOK   bool
	delay      time.Duration
	mcpPosts   int
}

type scopeIn struct {
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
}
type briefIn struct {
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
	Limit     int    `json:"recent_pages_limit,omitempty"`
}
type writeIn struct {
	Path      string   `json:"path"`
	Body      string   `json:"body"`
	Title     string   `json:"title"`
	Kind      string   `json:"kind,omitempty"`
	Tag       []string `json:"tag,omitempty"`
	Workspace string   `json:"workspace"`
	Project   string   `json:"project"`
}
type writeStrictIn struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Project   string `json:"project"`
}
type empty struct{}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, healthCode: 200, briefing: "# Briefing\nolá", handoff: "handoff pendente", statusOK: true}
	f.h = f.newMCP()
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		h, d := f.h, f.delay
		f.mcpPosts++
		f.mu.Unlock()
		if d > 0 {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}
		h.ServeHTTP(w, r)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		code := f.healthCode
		f.mu.Unlock()
		w.WriteHeader(code)
	})
	mux.HandleFunc("/hook/batch", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.hookBodies = append(f.hookBodies, string(b))
		f.hookAuth = r.Header.Get("Authorization")
		f.mu.Unlock()
		w.WriteHeader(202)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) record(tool string, req *sdk.CallToolRequest, in any) {
	b, _ := json.Marshal(in)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, rec{Tool: tool, Args: m, Auth: req.GetExtra().Header.Get("Authorization")})
}

func (f *fakeServer) newMCP() http.Handler {
	s := sdk.NewServer(&sdk.Implementation{Name: "fake-ai-memory", Version: "1"}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: "memory_briefing", Description: "briefing"}, func(_ context.Context, r *sdk.CallToolRequest, in briefIn) (*sdk.CallToolResult, any, error) {
		f.record("memory_briefing", r, in)
		f.mu.Lock()
		defer f.mu.Unlock()
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: f.briefing}}}, nil, nil
	})
	sdk.AddTool(s, &sdk.Tool{Name: "memory_handoff_accept", Description: "handoff"}, func(_ context.Context, r *sdk.CallToolRequest, in scopeIn) (*sdk.CallToolResult, any, error) {
		f.record("memory_handoff_accept", r, in)
		f.mu.Lock()
		defer f.mu.Unlock()
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: f.handoff}}}, nil, nil
	})
	sdk.AddTool(s, &sdk.Tool{Name: "memory_status", Description: "status"}, func(_ context.Context, r *sdk.CallToolRequest, in empty) (*sdk.CallToolResult, any, error) {
		f.record("memory_status", r, in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.statusOK {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "down"}}}, nil, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil, nil
	})
	f.mu.Lock()
	strict := f.strict
	f.mu.Unlock()
	writeHandler := func(tool string, r *sdk.CallToolRequest, in any) (*sdk.CallToolResult, any, error) {
		f.record(tool, r, in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failWrite {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "disco cheio"}}}, nil, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "written"}}}, nil, nil
	}
	if strict {
		sdk.AddTool(s, &sdk.Tool{Name: "memory_write_page", Description: "write"}, func(_ context.Context, r *sdk.CallToolRequest, in writeStrictIn) (*sdk.CallToolResult, any, error) {
			return writeHandler("memory_write_page", r, in)
		})
	} else {
		sdk.AddTool(s, &sdk.Tool{Name: "memory_write_page", Description: "write"}, func(_ context.Context, r *sdk.CallToolRequest, in writeIn) (*sdk.CallToolResult, any, error) {
			return writeHandler("memory_write_page", r, in)
		})
	}
	// exige o token no header Authorization
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// restart troca o servidor MCP: as sessões antigas deixam de existir.
func (f *fakeServer) restart() {
	h := f.newMCP()
	f.mu.Lock()
	f.h = h
	f.mu.Unlock()
}

func (f *fakeServer) callsTo(tool string) []rec {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rec
	for _, c := range f.calls {
		if c.Tool == tool {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeServer) provider(timeout time.Duration) memory.Provider {
	return New(Options{URL: f.srv.URL + "/", Token: testToken, Timeout: timeout})
}

var scope = memory.Scope{Workspace: "acme", Project: "site", Cwd: "/work/site"}

func TestName(t *testing.T) {
	if n := New(Options{URL: "http://x"}).Name(); n != "ai-memory" {
		t.Errorf("name = %q", n)
	}
}

func TestBriefingScopeAndAuth(t *testing.T) {
	f := newFake(t)
	p := f.provider(0)
	got, err := p.Briefing(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if got != "# Briefing\nolá" {
		t.Errorf("briefing = %q", got)
	}
	c := f.callsTo("memory_briefing")
	if len(c) != 1 {
		t.Fatalf("chamadas = %d", len(c))
	}
	want := map[string]any{"workspace": "acme", "project": "site", "recent_pages_limit": float64(10)}
	if d := cmp.Diff(want, c[0].Args); d != "" {
		t.Errorf("args (-want +got):\n%s", d)
	}
	if c[0].Auth != "Bearer "+testToken {
		t.Errorf("auth = %q", c[0].Auth)
	}
}

func TestDefaultProject(t *testing.T) {
	f := newFake(t)
	p := f.provider(0)
	if _, err := p.AcceptHandoff(context.Background(), memory.Scope{Workspace: "acme"}); err != nil {
		t.Fatal(err)
	}
	c := f.callsTo("memory_handoff_accept")
	if len(c) != 1 || c[0].Args["workspace"] != "acme" || c[0].Args["project"] != "default" {
		t.Errorf("calls = %+v", c)
	}
}

func TestAcceptHandoff(t *testing.T) {
	f := newFake(t)
	got, err := f.provider(0).AcceptHandoff(context.Background(), scope)
	if err != nil || got != "handoff pendente" {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestBriefingTruncated(t *testing.T) {
	f := newFake(t)
	f.briefing = strings.Repeat("ação 日本語 ", 3000) // multibyte, > 8 KB
	got, err := f.provider(0).Briefing(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxBriefingBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, "[…truncado]") {
		t.Errorf("len=%d valid=%v tail=%q", len(got), utf8.ValidString(got), got[len(got)-20:])
	}
	short := truncate("  curto \n", 100)
	if short != "curto" {
		t.Errorf("short = %q", short)
	}
}

func TestRecordFullArgs(t *testing.T) {
	f := newFake(t)
	err := f.provider(0).Record(context.Background(), scope, memory.Fact{
		Kind: "task.created", Path: "hanko/tasks/HNK-1.md", Title: "HNK-1", Body: "corpo", Tags: []string{"hanko", "task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := f.callsTo("memory_write_page")
	if len(c) != 1 {
		t.Fatalf("chamadas = %d", len(c))
	}
	want := map[string]any{"path": "hanko/tasks/HNK-1.md", "body": "corpo", "title": "HNK-1", "kind": "hanko",
		"tag": []any{"hanko", "task"}, "workspace": "acme", "project": "site"}
	if d := cmp.Diff(want, c[0].Args); d != "" {
		t.Errorf("args (-want +got):\n%s", d)
	}
	if c[0].Auth != "Bearer "+testToken {
		t.Errorf("auth = %q", c[0].Auth)
	}
}

func TestRecordFallbackWithoutKindTag(t *testing.T) {
	f := newFake(t)
	f.strict = true
	f.restart()
	err := f.provider(0).Record(context.Background(), scope, memory.Fact{Path: "p.md", Title: "T", Body: "B", Tags: []string{"x"}})
	if err != nil {
		t.Fatalf("fallback deveria passar: %v", err)
	}
	c := f.callsTo("memory_write_page")
	if len(c) != 1 { // a 1ª tentativa nem chega ao handler (validação de schema)
		t.Fatalf("chamadas ao handler = %d", len(c))
	}
	if _, ok := c[0].Args["kind"]; ok {
		t.Errorf("kind não deveria ser enviado: %v", c[0].Args)
	}
	if _, ok := c[0].Args["tag"]; ok {
		t.Errorf("tag não deveria ser enviado: %v", c[0].Args)
	}
}

func TestRecordFallbackOnToolError(t *testing.T) {
	f := newFake(t)
	f.failWrite = true
	err := f.provider(0).Record(context.Background(), scope, memory.Fact{Path: "p.md", Body: "B", Tags: []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "disco cheio") || !strings.Contains(err.Error(), "p.md") {
		t.Fatalf("err = %v", err)
	}
	c := f.callsTo("memory_write_page")
	if len(c) != 2 {
		t.Fatalf("esperava 2 tentativas (com e sem kind/tag), veio %d", len(c))
	}
	if _, ok := c[1].Args["kind"]; ok {
		t.Error("segunda tentativa não deveria ter kind")
	}
}

func TestSessionReused(t *testing.T) {
	f := newFake(t)
	p := f.provider(0)
	for i := 0; i < 3; i++ {
		if _, err := p.Briefing(context.Background(), scope); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	three := f.mcpPosts
	f.mu.Unlock()
	if _, err := p.Briefing(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// sessão reaproveitada: cada chamada extra custa exatamente 1 POST (sem novo initialize)
	if f.mcpPosts != three+1 {
		t.Errorf("POSTs ao /mcp: %d -> %d, esperava +1", three, f.mcpPosts)
	}
}

func TestReconnectAfterServerRestart(t *testing.T) {
	f := newFake(t)
	p := f.provider(2 * time.Second)
	if _, err := p.Briefing(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	f.restart() // sessão antiga some → 404
	got, err := p.Briefing(context.Background(), scope)
	if err != nil || got == "" {
		t.Fatalf("deveria reconectar: %q %v", got, err)
	}
	if n := len(f.callsTo("memory_briefing")); n != 2 {
		t.Errorf("briefings = %d", n)
	}
}

func TestServerDownFast(t *testing.T) {
	f := newFake(t)
	url := f.srv.URL
	f.srv.Close()
	p := New(Options{URL: url, Token: testToken, Timeout: 2 * time.Second})
	start := time.Now()
	_, err := p.Briefing(context.Background(), scope)
	if err == nil {
		t.Fatal("esperava erro")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("demorou %v", time.Since(start))
	}
	if strings.Contains(err.Error(), testToken) {
		t.Error("erro vaza o token")
	}
	if err := p.Record(context.Background(), scope, memory.Fact{Path: "a"}); err == nil {
		t.Error("Record deveria falhar")
	}
	if err := p.FinalizeSession(context.Background(), scope, "claude", "s"); err == nil {
		t.Error("FinalizeSession deveria falhar")
	}
	if err := p.Ping(context.Background()); err == nil {
		t.Error("Ping deveria falhar")
	}
}

func TestRecoversAfterDown(t *testing.T) {
	f := newFake(t)
	f.mu.Lock()
	f.delay = 0
	f.mu.Unlock()
	p := f.provider(300 * time.Millisecond)
	f.mu.Lock()
	f.healthCode = 200
	f.mu.Unlock()
	// 1ª chamada falha por servidor hung; depois normaliza.
	f.mu.Lock()
	f.delay = 2 * time.Second
	f.mu.Unlock()
	start := time.Now()
	if _, err := p.Briefing(context.Background(), scope); err == nil {
		t.Fatal("esperava timeout")
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Errorf("timeout não respeitado: %v", time.Since(start))
	}
	f.mu.Lock()
	f.delay = 0
	f.mu.Unlock()
	if _, err := p.Briefing(context.Background(), scope); err != nil {
		t.Fatalf("deveria recuperar: %v", err)
	}
}

func TestContextCancelled(t *testing.T) {
	f := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.provider(0).Briefing(ctx, scope); err == nil {
		t.Error("esperava erro com ctx cancelado")
	}
}

func TestUnauthorized(t *testing.T) {
	f := newFake(t)
	p := New(Options{URL: f.srv.URL, Token: "errado", Timeout: time.Second})
	if _, err := p.Briefing(context.Background(), scope); err == nil {
		t.Error("token errado deveria falhar")
	}
}

func TestFinalizeSession(t *testing.T) {
	f := newFake(t)
	if err := f.provider(0).FinalizeSession(context.Background(), scope, "claude", "run-123"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hookBodies) != 1 {
		t.Fatalf("hooks = %d", len(f.hookBodies))
	}
	var items []struct {
		URL  string         `json:"url"`
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(f.hookBodies[0]), &items); err != nil || len(items) != 1 {
		t.Fatalf("body %q: %v", f.hookBodies[0], err)
	}
	u, err := url.Parse(items[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/hook" {
		t.Errorf("path = %q", u.Path)
	}
	wantQ := url.Values{"event": {"session-end"}, "agent": {"claude"}, "cwd": {"/work/site"}, "workspace": {"acme"}, "project": {"site"}}
	if d := cmp.Diff(wantQ, u.Query()); d != "" {
		t.Errorf("query (-want +got):\n%s", d)
	}
	if !strings.HasPrefix(items[0].URL, f.srv.URL+"/hook?") {
		t.Errorf("url = %q", items[0].URL)
	}
	if items[0].Body["session_id"] != "run-123" || items[0].Body["cwd"] != "/work/site" {
		t.Errorf("body = %v", items[0].Body)
	}
	if f.hookAuth != "Bearer "+testToken {
		t.Errorf("auth = %q", f.hookAuth)
	}
}

func TestFinalizeSessionHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "quebrou", 500) }))
	defer srv.Close()
	err := New(Options{URL: srv.URL}).FinalizeSession(context.Background(), scope, "codex", "s")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}

func TestAgentMCP(t *testing.T) {
	p := New(Options{URL: "http://mem:8080/", Token: "mem-secret", PublicURL: "http://127.0.0.1:7420/"})
	m := p.AgentMCP(memory.Scope{Workspace: "acme", Project: "site"}, "run-1", "run-token-xyz")
	raw, ok := m["ai-memory"]
	if !ok || len(m) != 1 {
		t.Fatalf("map = %v", m)
	}
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(`{"type":"http","url":"http://127.0.0.1:7420/memory-mcp","headers":{"Authorization":"Bearer run-token-xyz"}}`), &want)
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("(-want +got):\n%s", d)
	}
	if strings.Contains(string(raw), "mem-secret") || strings.Contains(string(raw), "X-Memory") || strings.Contains(string(raw), "mem:8080") {
		t.Errorf("credencial/escopo/URL do ai-memory vazou ao agente: %s", raw)
	}
	// sem run token ou sem PublicURL: nada a injetar (nunca "Bearer " vazio)
	if m := p.AgentMCP(memory.Scope{Workspace: "acme"}, "r", ""); m != nil {
		t.Errorf("sem run token: %v", m)
	}
	if m := New(Options{URL: "http://mem:8080", Token: "t"}).AgentMCP(memory.Scope{}, "r", "tok"); m != nil {
		t.Errorf("sem PublicURL: %v", m)
	}
}

func TestPing(t *testing.T) {
	f := newFake(t)
	p := f.provider(time.Second)
	if err := p.Ping(context.Background()); err != nil {
		t.Fatalf("health 200: %v", err)
	}
	if len(f.callsTo("memory_status")) != 0 {
		t.Error("não deveria usar fallback com /health ok")
	}
	f.mu.Lock()
	f.healthCode = 503
	f.mu.Unlock()
	if err := p.Ping(context.Background()); err != nil {
		t.Fatalf("fallback memory_status: %v", err)
	}
	if len(f.callsTo("memory_status")) != 1 {
		t.Error("esperava fallback via memory_status")
	}
	f.mu.Lock()
	f.statusOK = false
	f.mu.Unlock()
	if err := p.Ping(context.Background()); err == nil {
		t.Error("health 503 + status com erro deveria falhar")
	}
}

func TestCustomHTTPClientKept(t *testing.T) {
	f := newFake(t)
	called := false
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return http.DefaultTransport.RoundTrip(r)
	})}
	p := New(Options{URL: f.srv.URL, Token: testToken, HTTP: hc})
	if _, err := p.Briefing(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("transport do cliente customizado não foi usado")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClip(t *testing.T) {
	if got := clip("ação", 2); !utf8.ValidString(got) {
		t.Errorf("clip inválido: %q", got)
	}
	if clip("abc", 10) != "abc" {
		t.Error("clip curto")
	}
}
