package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
)

type fakePages struct{ pages map[string]string }

func (f *fakePages) ReadPage(_ context.Context, _ memory.Scope, path string) (string, error) {
	if t, ok := f.pages[path]; ok {
		return t, nil
	}
	return "", memory.ErrUnsupported
}

func toolCallBody(id int, tool string, args string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
}

func (m *memEnv) upstreamCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *memEnv) asCuration() {
	r := m.f.runs[m.runID]
	r.Trigger = domain.TriggerMemory
	m.f.runs[m.runID] = r
}

func readJSON(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	b, _ := io.ReadAll(body)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("resposta não é JSON-RPC: %v %s", err, b)
	}
	return out
}

func TestMemoryProxyForaDoEscopoSemConcessaoFicaNoProprioProjeto(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.post(t, m.tok, toolCallBody(1, "memory_briefing", `{"project":"financeiro","workspace":"outra"}`), nil)
	c := m.last(t)
	if c.hdr.Get("X-Memory-Project") != "default" || c.hdr.Get("X-Memory-Workspace") != "acme" {
		t.Errorf("escopo = %q/%q", c.hdr.Get("X-Memory-Workspace"), c.hdr.Get("X-Memory-Project"))
	}
	if strings.Contains(c.body, "financeiro") || strings.Contains(c.body, "outra") {
		t.Errorf("argumentos alheios passaram: %s", c.body)
	}
}

func TestMemoryProxyConcessaoDeLeituraAlcancaOutroWorkspace(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.backend.access = map[string]domain.MemoryAccess{"api": domain.MemoryRead}

	m.post(t, m.tok, toolCallBody(1, "memory_briefing", `{"project":"api"}`), nil)
	c := m.last(t)
	if c.hdr.Get("X-Memory-Project") != "api" || c.hdr.Get("X-Memory-Workspace") != "acme" {
		t.Errorf("com concessão: escopo = %q/%q", c.hdr.Get("X-Memory-Workspace"), c.hdr.Get("X-Memory-Project"))
	}

	// escrever com concessão só de leitura: recusado, o upstream não é chamado.
	before := m.upstreamCalls()
	resp := m.post(t, m.tok, toolCallBody(2, "memory_write_page", `{"project":"api","path":"x","body":"y"}`), nil)
	out := readJSON(t, resp.Body)
	if out["error"] == nil {
		t.Fatalf("escrita com concessão de leitura deveria ser recusada: %v", out)
	}
	if m.upstreamCalls() != before {
		t.Error("o upstream recebeu uma escrita sem permissão")
	}

	// concessão de escrita libera.
	m.backend.access["api"] = domain.MemoryWrite
	m.post(t, m.tok, toolCallBody(3, "memory_write_page", `{"project":"api","path":"x","body":"y"}`), nil)
	if c := m.last(t); c.hdr.Get("X-Memory-Project") != "api" {
		t.Errorf("com concessão de escrita: projeto = %q", c.hdr.Get("X-Memory-Project"))
	}
}

func TestMemoryProxyConcessaoNuncaAtravessaEmpresa(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.backend.access = map[string]domain.MemoryAccess{"api": domain.MemoryWrite}
	// o argumento workspace (empresa) é sempre o do token, com ou sem concessão.
	m.post(t, m.tok, toolCallBody(1, "memory_briefing", `{"workspace":"outra-empresa","project":"api"}`), nil)
	c := m.last(t)
	if c.hdr.Get("X-Memory-Workspace") != "acme" || strings.Contains(c.body, "outra-empresa") {
		t.Errorf("empresa do token não prevaleceu: %q %s", c.hdr.Get("X-Memory-Workspace"), c.body)
	}
}

func TestMemoryProxyCuradoriaLeituraEAditivoSeguem(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.asCuration()
	m.post(t, m.tok, toolCallBody(1, "memory_search", `{"q":"x"}`), nil)
	m.post(t, m.tok, toolCallBody(2, "memory_write_page", `{"path":"hanko/curadoria/2026-01-01","body":"relatório"}`), nil)
	if n := m.upstreamCalls(); n != 2 {
		t.Fatalf("upstream recebeu %d chamadas; esperava 2", n)
	}
	if len(m.backend.proposals) != 0 {
		t.Errorf("leitura/aditivo não geram proposta: %v", m.backend.proposals)
	}
}

func TestMemoryProxyCuradoriaEscritaDestrutivaViraProposta(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.asCuration()
	m.pages.pages["wiki/a"] = "linha 1\nlinha 2\n"
	resp := m.post(t, m.tok, toolCallBody(1, "memory_delete_page", `{"path":"wiki/a","workspace":"outra","project":"alheio"}`), nil)
	out := readJSON(t, resp.Body)
	if out["result"] == nil || out["error"] != nil {
		t.Fatalf("resposta sintética esperada: %v", out)
	}
	if m.upstreamCalls() != 0 {
		t.Fatal("a escrita destrutiva não pode chegar ao ai-memory")
	}
	if len(m.backend.proposals) != 1 {
		t.Fatalf("propostas = %d", len(m.backend.proposals))
	}
	p := m.backend.proposals[0]
	if p.Tool != "memory_delete_page" || p.Current != "linha 1\nlinha 2\n" {
		t.Errorf("proposta = %+v", p)
	}
	if p.Arguments["workspace"] != "acme" || p.Arguments["project"] != "default" {
		t.Errorf("escopo da proposta não veio do token: %v", p.Arguments)
	}
}

func TestMemoryProxyCuradoriaRecusaHandoffELote(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.asCuration()
	resp := m.post(t, m.tok, toolCallBody(1, "memory_handoff_accept", `{}`), nil)
	if out := readJSON(t, resp.Body); out["error"] == nil {
		t.Errorf("handoff deveria ser recusado: %v", out)
	}
	batch := `[` + toolCallBody(1, "memory_search", `{}`) + `,` + toolCallBody(2, "memory_delete_page", `{"path":"a"}`) + `]`
	resp = m.post(t, m.tok, batch, nil)
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"error"`) || m.upstreamCalls() != 0 || len(m.backend.proposals) != 0 {
		t.Errorf("lote com escrita retida deveria ser recusado por inteiro: %s", b)
	}
}

func TestMemoryProxyCuradoriaFalhaAoRegistrarNaoEscreve(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.asCuration()
	m.backend.recordErr = context.DeadlineExceeded
	resp := m.post(t, m.tok, toolCallBody(1, "memory_delete_page", `{"path":"a"}`), nil)
	if out := readJSON(t, resp.Body); out["error"] == nil {
		t.Errorf("esperava erro JSON-RPC: %v", out)
	}
	if m.upstreamCalls() != 0 {
		t.Error("nada pode chegar ao ai-memory quando a proposta não foi registrada")
	}
}
