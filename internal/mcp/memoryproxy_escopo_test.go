package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func upstreamArgs(t *testing.T, body string) map[string]any {
	t.Helper()
	var msg struct {
		Params struct {
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("corpo: %v %s", err, body)
	}
	return msg.Params.Arguments
}

func TestMemoryProxyRecusaForaDoContrato(t *testing.T) {
	m := setupMem(t, "t", nil)
	cases := map[string]string{
		"global":          toolCallBody(1, "memory_query", `{"query":"x","global":true}`),
		"scopes":          toolCallBody(1, "memory_query", `{"query":"x","scopes":[{"workspace":"outra","project":"default"}]}`),
		"escrita global":  toolCallBody(1, "memory_write_page", `{"path":"a","body":"b","scope":"global"}`),
		"leitura global":  toolCallBody(1, "memory_read_page", `{"path":"a","scope":"global"}`),
		"remoção global":  toolCallBody(1, "memory_delete_page", `{"path":"a","scope":"global"}`),
		"origem forjada":  toolCallBody(1, "memory_message_send", `{"body":"b","to_project":"default","from_workspace":"outra"}`),
		"recado externo":  toolCallBody(1, "memory_message_send", `{"body":"b","to_workspace":"outra","to_project":"default"}`),
		"argumento novo":  toolCallBody(1, "memory_status", `{"novo":1}`),
		"tool nova":       toolCallBody(1, "memory_nova", `{}`),
		"lote com global": `[` + toolCallBody(1, "memory_status", `{}`) + `,` + toolCallBody(2, "memory_query", `{"query":"x","global":true}`) + `]`,
		"lote outro alvo": `[` + toolCallBody(1, "memory_status", `{}`) + `,` + toolCallBody(2, "memory_recent", `{"project":"api"}`) + `]`,
	}
	for nome, body := range cases {
		b, _ := io.ReadAll(m.post(t, m.tok, body, nil).Body)
		if !strings.Contains(string(b), `"error"`) {
			t.Errorf("%s: deveria ser recusado: %s", nome, b)
		}
	}
	if n := m.upstreamCalls(); n != 0 {
		t.Errorf("upstream recebeu %d chamadas", n)
	}
}

func TestMemoryProxyLoteDoProprioEscopoSegue(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.post(t, m.tok, `[`+toolCallBody(1, "memory_status", `{}`)+`,`+toolCallBody(2, "memory_recent", `{"project":"default"}`)+`]`, nil)
	if c := m.last(t); strings.Count(c.body, `"workspace":"acme"`) != 2 {
		t.Errorf("lote sem escopo: %s", c.body)
	}
}

func TestMemoryProxyLeituraDoCorpoFalhaFechada(t *testing.T) {
	m := setupMem(t, "t", nil)
	for nome, body := range map[string]string{
		"lixo":             `isso não é json`,
		"objeto quebrado":  `{"method":"tools/call"`,
		"sem params":       `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
		"argumento texto":  `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_status","arguments":"x"}}`,
		"método não texto": `{"jsonrpc":"2.0","id":1,"method":1}`,
		"item de lote":     `[1]`,
	} {
		b, _ := io.ReadAll(m.post(t, m.tok, body, nil).Body)
		if !strings.Contains(string(b), `"error"`) {
			t.Errorf("%s: deveria ser recusado: %s", nome, b)
		}
	}
	if n := m.upstreamCalls(); n != 0 {
		t.Errorf("upstream recebeu %d chamadas", n)
	}
}

// Chave com outra caixa não pode esconder a chamada real: o que segue é o que o proxy leu, com o escopo fixado.
func TestMemoryProxyChaveComOutraCaixaNaoEscondeChamada(t *testing.T) {
	m := setupMem(t, "t", nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","METHOD":"ping","params":{"name":"memory_query","Name":"memory_status",` +
		`"arguments":{"query":"x","global":true},"Arguments":{"query":"x"}}}`
	b, _ := io.ReadAll(m.post(t, m.tok, body, nil).Body)
	if !strings.Contains(string(b), `"error"`) || m.upstreamCalls() != 0 {
		t.Errorf("deveria ser recusado: %s", b)
	}
}

func TestMemoryProxySempreFixaOEscopo(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.post(t, m.tok, toolCallBody(1, "memory_recent", `{}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["workspace"] != "acme" || a["project"] != "default" {
		t.Errorf("sem escopo nos argumentos: %v", a)
	}
	m.post(t, m.tok, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_status"}}`, nil)
	if a := upstreamArgs(t, m.last(t).body); a["workspace"] != "acme" {
		t.Errorf("chamada sem arguments: %v", a)
	}
	m.post(t, m.tok, toolCallBody(3, "memory_consolidate", `{}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["workspace"] != nil {
		t.Errorf("tool sem escopo não pode receber workspace: %v", a)
	}
}

func TestMemoryProxyBuscaUsaUmItemEmScopes(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.backend.access = map[string]domain.MemoryAccess{"api": domain.MemoryRead}
	want := func(project string) {
		t.Helper()
		a := upstreamArgs(t, m.last(t).body)
		list, _ := a["scopes"].([]any)
		if len(list) != 1 || a["workspace"] != nil || a["project"] != nil {
			t.Fatalf("argumentos = %v", a)
		}
		if s, _ := list[0].(map[string]any); s["workspace"] != "acme" || s["project"] != project {
			t.Errorf("escopo = %v", s)
		}
	}
	m.post(t, m.tok, toolCallBody(1, "memory_query", `{"query":"x","workspace":"outra","project":"sem-concessao"}`), nil)
	want("default")
	m.post(t, m.tok, toolCallBody(2, "memory_query", `{"query":"x","project":"api"}`), nil)
	want("api")
}

func TestMemoryProxyBuscaHistoricaFicaSemScopes(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.post(t, m.tok, toolCallBody(1, "memory_query", `{"query":"x","as_of":"2026-01-01T00:00:00Z"}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["scopes"] != nil || a["workspace"] != "acme" || a["project"] != "default" {
		t.Errorf("argumentos = %v", a)
	}
}

func TestMemoryProxyRecado(t *testing.T) {
	m := setupMem(t, "t", nil)
	m.post(t, m.tok, toolCallBody(1, "memory_message_send", `{"body":"oi","to_workspace":"acme","to_project":"default"}`), nil)
	a := upstreamArgs(t, m.last(t).body)
	if a["to_workspace"] != "acme" || a["from_workspace"] != "acme" || a["from_project"] != "default" {
		t.Errorf("recado = %v", a)
	}
	// sem concessão o destino volta ao próprio workspace; com concessão de escrita a origem segue sendo a da run
	m.post(t, m.tok, toolCallBody(2, "memory_message_send", `{"body":"oi","to_project":"api"}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["to_project"] != "default" {
		t.Errorf("sem concessão: %v", a)
	}
	m.backend.access = map[string]domain.MemoryAccess{"api": domain.MemoryWrite}
	m.post(t, m.tok, toolCallBody(3, "memory_message_send", `{"body":"oi","to_project":"api"}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["to_project"] != "api" || a["from_project"] != "default" {
		t.Errorf("com concessão: %v", a)
	}
}

// A memória segue a pasta da run: a task sem workspace usa o do projeto, mesmo com a persona em outro.
func TestMemoryProxyEscopoSegueOWorkspaceDaRun(t *testing.T) {
	m := setupMem(t, "t", nil)
	doProjeto, daPersona := domain.NewID(), domain.NewID()
	m.backend.ws[doProjeto] = domain.Workspace{ID: doProjeto, Slug: "do-projeto"}
	m.backend.ws[daPersona] = domain.Workspace{ID: daPersona, Slug: "da-persona"}
	m.f.personas[1].WorkspaceID = &daPersona
	r := m.f.runs[m.runID]
	r.WorkspaceID, r.WorkspaceSource = &doProjeto, domain.WorkspaceFromProject
	m.f.runs[m.runID] = r
	m.post(t, m.tok, toolCallBody(1, "memory_recent", `{}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["project"] != "do-projeto" {
		t.Errorf("project = %v", a["project"])
	}
	r.WorkspaceID, r.WorkspaceSource = nil, domain.WorkspaceNone
	m.f.runs[m.runID] = r
	m.post(t, m.tok, toolCallBody(2, "memory_recent", `{}`), nil)
	if a := upstreamArgs(t, m.last(t).body); a["project"] != "default" {
		t.Errorf("run sem workspace: project = %v", a["project"])
	}
}

// TestMemoryProxyContraServidorReal é o simulado de ponta a ponta: token de run, proxy e ai-memory real, com uma
// página de outra empresa e uma da área global. Só roda com HANKO_AI_MEMORY_CONTRACT_URL; use um servidor de teste.
func TestMemoryProxyContraServidorReal(t *testing.T) {
	base, token := os.Getenv("HANKO_AI_MEMORY_CONTRACT_URL"), os.Getenv("HANKO_AI_MEMORY_CONTRACT_TOKEN")
	if base == "" {
		t.Skip("HANKO_AI_MEMORY_CONTRACT_URL vazio")
	}
	direct := func(tool, args string) string {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/mcp", strings.NewReader(toolCallBody(1, tool, args)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	direct("memory_write_page", `{"workspace":"outra","project":"default","path":"notas/alheia.md","title":"A","body":"termoalheio"}`)
	direct("memory_write_page", `{"scope":"global","path":"notas/geral.md","title":"G","body":"termogeral"}`)

	m := setupMem(t, token, nil)
	h, err := MemoryProxy(MemoryProxyDeps{Backend: m.backend, Tokens: m.tokens, URL: base, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(MemoryPath, h)
	real := httptest.NewServer(mux)
	t.Cleanup(real.Close)
	m.proxy = real
	hdr := map[string]string{"Accept": "application/json, text/event-stream"}
	ask := func(tool, args string) string {
		t.Helper()
		b, _ := io.ReadAll(m.post(t, m.tok, toolCallBody(1, tool, args), hdr).Body)
		return string(b)
	}

	if out := ask("memory_write_page", `{"path":"notas/propria.md","title":"P","body":"termoproprio"}`); !strings.Contains(out, "page_id") {
		t.Fatalf("escrita no próprio escopo: %s", out)
	}
	if out := ask("memory_query", `{"query":"termoproprio","pin_first":true}`); !strings.Contains(out, "termoproprio") {
		t.Errorf("busca no próprio escopo: %s", out)
	}
	if out := ask("memory_read_page", `{"path":"notas/propria.md"}`); !strings.Contains(out, "termoproprio") {
		t.Errorf("leitura no próprio escopo: %s", out)
	}
	if out := ask("memory_query", `{"query":"termogeral","as_of":"2030-01-01T00:00:00Z"}`); strings.Contains(out, "termogeral") {
		t.Errorf("busca histórica trouxe a área global: %s", out)
	}
	for _, q := range []string{`{"query":"termoalheio"}`, `{"query":"termogeral"}`, `{"query":"termoalheio","global":true}`,
		`{"query":"termoalheio","scopes":[{"workspace":"outra","project":"default"}]}`} {
		if out := ask("memory_query", q); strings.Contains(out, "termoalheio") || strings.Contains(out, "termogeral") {
			t.Errorf("%s vazou: %s", q, out)
		}
	}
	if out := ask("memory_read_page", `{"path":"notas/alheia.md","workspace":"outra","project":"default"}`); strings.Contains(out, "termoalheio") {
		t.Errorf("leitura alheia vazou: %s", out)
	}
	if out := ask("memory_write_page", `{"scope":"global","path":"notas/x.md","body":"termoinjetado"}`); !strings.Contains(out, `"error"`) {
		t.Errorf("escrita global passou: %s", out)
	}
	if out := direct("memory_query", `{"workspace":"outra","project":"default","query":"termoinjetado"}`); strings.Contains(out, "termoinjetado") {
		t.Errorf("escrita global chegou a outra empresa: %s", out)
	}
}
