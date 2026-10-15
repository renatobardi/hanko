package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type memFake struct {
	*fake
	ws map[domain.ID]domain.Workspace
	// consolidação governada (ADR-0030)
	access    map[string]domain.MemoryAccess
	proposals []service.RecordProposalInput
	recordErr error
}

func (m *memFake) MemoryAccessTo(_ context.Context, _, _ domain.ID, slug string) (domain.MemoryAccess, error) {
	return m.access[slug], nil
}

func (m *memFake) RecordMemoryProposal(_ context.Context, _ domain.ID, in service.RecordProposalInput) (service.MemoryProposalView, bool, error) {
	if m.recordErr != nil {
		return service.MemoryProposalView{}, false, m.recordErr
	}
	m.proposals = append(m.proposals, in)
	v := service.MemoryProposalView{}
	v.ID = domain.NewID()
	v.Kind = domain.ProposalDelete
	return v, true, nil
}

func (m *memFake) GetWorkspace(_ context.Context, _ domain.ID, ref string) (domain.Workspace, error) {
	for id, w := range m.ws {
		if id.String() == ref {
			return w, nil
		}
	}
	return domain.Workspace{}, domain.ErrNotFound
}

type upstreamCall struct {
	path, rawQuery string
	hdr            http.Header
	body           string
}

type memEnv struct {
	*env
	proxy    *httptest.Server
	upstream *httptest.Server
	mu       sync.Mutex
	calls    []upstreamCall
	backend  *memFake
	pages    *fakePages
}

func (m *memEnv) last(t *testing.T) upstreamCall {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		t.Fatal("upstream não foi chamado")
	}
	return m.calls[len(m.calls)-1]
}

func setupMem(t *testing.T, memToken string, upstream http.HandlerFunc) *memEnv {
	t.Helper()
	e := setup(t, false)
	m := &memEnv{env: e, backend: &memFake{fake: e.f, ws: map[domain.ID]domain.Workspace{}}, pages: &fakePages{pages: map[string]string{}}}
	if upstream == nil {
		upstream = func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			m.mu.Lock()
			m.calls = append(m.calls, upstreamCall{path: r.URL.Path, rawQuery: r.URL.RawQuery, hdr: r.Header.Clone(), body: string(b)})
			m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		}
	}
	m.upstream = httptest.NewServer(upstream)
	t.Cleanup(m.upstream.Close)
	h, err := MemoryProxy(MemoryProxyDeps{Backend: m.backend, Tokens: e.tokens, URL: m.upstream.URL + "/", Token: memToken, Pages: m.pages})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(MemoryPath, h)
	m.proxy = httptest.NewServer(mux)
	t.Cleanup(m.proxy.Close)
	return m
}

func (m *memEnv) post(t *testing.T, tok, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, m.proxy.URL+MemoryPath+"?x=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestMemoryProxyHeadersEEscopoVemDoToken(t *testing.T) {
	m := setupMem(t, "mem-secret-token", nil)
	forged := map[string]string{
		"X-Memory-Workspace": "outra-empresa", "x-memory-project": "alheio", "X-Memory-Extra": "1", "Cookie": "a=b",
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_briefing","arguments":{"workspace":"outra-empresa","project":"alheio","q":"x"}}}`
	resp := m.post(t, m.tok, body, forged)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d %s", resp.StatusCode, b)
	}
	c := m.last(t)
	if c.path != "/mcp" || c.rawQuery != "" {
		t.Errorf("upstream path = %q?%q", c.path, c.rawQuery)
	}
	if got := c.hdr.Get("Authorization"); got != "Bearer mem-secret-token" {
		t.Errorf("Authorization upstream = %q", got)
	}
	if strings.Contains(c.hdr.Get("Authorization"), m.tok) {
		t.Error("run token vazou ao upstream")
	}
	if c.hdr.Get("X-Memory-Workspace") != "acme" || c.hdr.Get("X-Memory-Project") != "default" {
		t.Errorf("escopo = %q/%q", c.hdr.Get("X-Memory-Workspace"), c.hdr.Get("X-Memory-Project"))
	}
	if c.hdr.Get("X-Memory-Extra") != "" || c.hdr.Get("Cookie") != "" {
		t.Errorf("headers do cliente passaram: %v", c.hdr)
	}
	if len(c.hdr.Values("X-Memory-Workspace")) != 1 {
		t.Errorf("X-Memory-Workspace duplicado: %v", c.hdr.Values("X-Memory-Workspace"))
	}
	var msg struct {
		Params struct {
			Arguments map[string]string `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(c.body), &msg); err != nil {
		t.Fatalf("corpo: %v %s", err, c.body)
	}
	if a := msg.Params.Arguments; a["workspace"] != "acme" || a["project"] != "default" || a["q"] != "x" {
		t.Errorf("argumentos = %v", a)
	}
}

func TestMemoryProxyProjetoDoWorkspaceDaTask(t *testing.T) {
	m := setupMem(t, "t", nil)
	wsID := domain.NewID()
	m.backend.ws[wsID] = domain.Workspace{ID: wsID, Slug: "api", Name: "API"}
	m.f.mu.Lock()
	m.f.tasks[0].WorkspaceID = &wsID
	m.f.mu.Unlock()
	m.post(t, m.tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil)
	if c := m.last(t); c.hdr.Get("X-Memory-Project") != "api" || c.hdr.Get("X-Memory-Workspace") != "acme" {
		t.Errorf("escopo = %q/%q", c.hdr.Get("X-Memory-Workspace"), c.hdr.Get("X-Memory-Project"))
	}
}

func TestMemoryProxyProjetoDoWorkspaceDaPersona(t *testing.T) {
	m := setupMem(t, "t", nil)
	wsID := domain.NewID()
	m.backend.ws[wsID] = domain.Workspace{ID: wsID, Slug: "site"}
	m.f.personas[1].WorkspaceID = &wsID
	m.post(t, m.tok, `{}`, nil)
	if c := m.last(t); c.hdr.Get("X-Memory-Project") != "site" {
		t.Errorf("projeto = %q", c.hdr.Get("X-Memory-Project"))
	}
}

func TestMemoryProxyAuth(t *testing.T) {
	m := setupMem(t, "t", nil)
	r := m.f.runs[m.runID]
	cases := []struct {
		name string
		tok  string
		st   domain.RunStatus
	}{
		{"sem token", "", domain.RunRunning},
		{"token lixo", "lixo", domain.RunRunning},
		{"run encerrado", m.tok, domain.RunDone},
		{"run cancelado", m.tok, domain.RunCancelled},
	}
	for _, tc := range cases {
		r.Status = tc.st
		m.f.runs[m.runID] = r
		if resp := m.post(t, tc.tok, `{}`, nil); resp.StatusCode != 401 {
			t.Errorf("%s: %d", tc.name, resp.StatusCode)
		}
	}
	m.mu.Lock()
	n := len(m.calls)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("upstream recebeu %d chamadas não autenticadas", n)
	}
	// token com segredo errado
	forged, _ := auth.RunTokens{Secret: []byte("another-secret-another-secret!!")}.Issue(m.f.company.ID, domain.NewID(), m.runID, domain.NewID())
	if resp := m.post(t, forged, `{}`, nil); resp.StatusCode != 401 {
		t.Errorf("forjado: %d", resp.StatusCode)
	}
	// queued vale
	r.Status = domain.RunQueued
	m.f.runs[m.runID] = r
	if resp := m.post(t, m.tok, `{}`, nil); resp.StatusCode != 200 {
		t.Errorf("queued: %d", resp.StatusCode)
	}
}

func TestMemoryProxySemTokenDeMemoriaNaoEnviaAuthorization(t *testing.T) {
	m := setupMem(t, "", nil)
	m.post(t, m.tok, `{}`, nil)
	if got := m.last(t).hdr.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, deveria estar ausente", got)
	}
}

func TestMemoryProxyStreaming(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	m := setupMem(t, "t", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: primeiro\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: segundo\n\n")
	})
	resp := m.post(t, m.tok, `{}`, nil)
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		if s != "data: primeiro\n" {
			t.Fatalf("linha = %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("primeiro evento não chegou antes do fim do upstream (sem flush)")
	}
	once.Do(func() { close(release) })
}

func TestMemoryProxyURLInvalida(t *testing.T) {
	if _, err := MemoryProxy(MemoryProxyDeps{URL: "ftp://x"}); err == nil {
		t.Error("esperava erro")
	}
}
