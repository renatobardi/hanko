package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/renatobardi/hanko/internal/api"
)

func init() { time.Local = time.UTC }

// hit registra uma requisição recebida pelo servidor falso.
type hit struct {
	Method, Path, Query, Auth, Body string
}

type fakeAPI struct {
	mu   sync.Mutex
	hits []hit
	h    map[string]http.HandlerFunc // "METHOD /path" -> handler
}

func newFakeAPI(t *testing.T, h map[string]http.HandlerFunc) (*httptest.Server, *fakeAPI) {
	t.Helper()
	f := &fakeAPI{h: h}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.hits = append(f.hits, hit{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(b)})
		f.mu.Unlock()
		if fn, ok := f.h[r.Method+" "+r.URL.Path]; ok {
			fn(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"title":"Não encontrado","status":404,"detail":"rota desconhecida"}`))
	}))
	t.Cleanup(ts.Close)
	return ts, f
}

func (f *fakeAPI) last() hit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[len(f.hits)-1]
}

func jsonResp(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
}

func list[T any](items ...T) api.ListBody[T] { return api.ListBody[T]{Items: items, Limit: 50} }

// exec roda a CLI e devolve (código, stdout, stderr).
func exec(t *testing.T, rt Runtime, getenv map[string]string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := run(ctx, "v-test", rt, args, strings.NewReader(stdin), &out, &errb, func(k string) string { return getenv[k] })
	return code, out.String(), errb.String()
}

var task1 = api.Task{ID: "0198f2a0-0000-7000-8000-000000000001", Key: "HNK-1", Title: "Implementar login", Status: "todo",
	Priority: "high", PersonaName: "Marina", CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 13, 9, 5, 0, 0, time.UTC)}

func TestVersion(t *testing.T) {
	code, out, _ := exec(t, Runtime{}, nil, "", "version")
	if code != 0 || out != "hanko v-test\n" {
		t.Fatalf("%d %q", code, out)
	}
	_, out, _ = exec(t, Runtime{}, nil, "", "version", "--json")
	if !strings.Contains(out, `"version": "v-test"`) {
		t.Fatalf("%q", out)
	}
}

func TestOpenAPIComando(t *testing.T) {
	code, out, _ := exec(t, Runtime{OpenAPI: func() ([]byte, error) { return []byte("{}\n"), nil }}, nil, "", "openapi")
	if code != 0 || out != "{}\n" {
		t.Fatalf("%d %q", code, out)
	}
	// padrão: spec real da API, sem banco
	code, out, _ = exec(t, Runtime{}, nil, "", "openapi")
	if code != 0 || !strings.Contains(out, `"openapi"`) || !strings.Contains(out, "/api/v1/companies") {
		t.Fatalf("%d %.80q", code, out)
	}
}

func TestServeEMigrate(t *testing.T) {
	var served, migrated bool
	rt := Runtime{
		Serve:   func(context.Context) error { served = true; return nil },
		Migrate: func(context.Context) error { migrated = true; return errors.New("falhou") },
	}
	if code, _, _ := exec(t, rt, nil, "", "serve"); code != 0 || !served {
		t.Fatal("serve não chamado")
	}
	code, _, errOut := exec(t, rt, nil, "", "migrate")
	if code != 1 || !migrated || !strings.Contains(errOut, "erro: falhou") {
		t.Fatalf("%d %q", code, errOut)
	}
	code, _, errOut = exec(t, Runtime{}, nil, "", "serve")
	if code != 1 || !strings.Contains(errOut, "não conectado") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestEmpresaObrigatoria(t *testing.T) {
	code, _, errOut := exec(t, Runtime{}, nil, "", "task", "list")
	if code != 2 || !strings.Contains(errOut, "empresa não informada") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestFlagInvalidaDaCodigoDeUso(t *testing.T) {
	code, _, errOut := exec(t, Runtime{}, nil, "", "task", "list", "--nao-existe")
	if code != 2 || !strings.Contains(errOut, "erro:") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestCompanyList(t *testing.T) {
	c := api.Company{ID: "id-1", Slug: "acme", Name: "Acme Ltda", TaskPrefix: "HNK", Timezone: "America/Sao_Paulo"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies": jsonResp(200, list(c))})

	code, out, _ := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL}, "", "company", "list")
	if code != 0 {
		t.Fatal(code)
	}
	want := "SLUG  NOME       PREFIXO  FUSO               ID\nacme  Acme Ltda  HNK      America/Sao_Paulo  id-1\n"
	if diff := cmp.Diff(want, out); diff != "" {
		t.Fatalf("tabela:\n%s", diff)
	}
	if f.last().Query != "limit=500" {
		t.Fatalf("query = %q", f.last().Query)
	}

	_, out, _ = exec(t, Runtime{}, nil, "", "--url", ts.URL, "company", "list", "--json")
	var got []api.Company
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || got[0].Slug != "acme" {
		t.Fatalf("json: %q %v", out, err)
	}
}

func TestCompanyListVazia(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies": jsonResp(200, list[api.Company]())})
	_, out, _ := exec(t, Runtime{}, nil, "", "--url", ts.URL, "company", "list")
	if !strings.Contains(out, "Nenhuma empresa") {
		t.Fatalf("%q", out)
	}
	_, out, _ = exec(t, Runtime{}, nil, "", "--url", ts.URL, "company", "list", "--json")
	if strings.TrimSpace(out) != "[]" && strings.TrimSpace(out) != "null" {
		t.Fatalf("%q", out)
	}
}

func TestCompanyCreate(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies": jsonResp(201, api.Company{ID: "i", Slug: "acme", Name: "Acme", TaskPrefix: "ACM"})})
	code, out, _ := exec(t, Runtime{}, nil, "", "--url", ts.URL, "company", "create", "Acme", "--prefix", "ACM")
	if code != 0 || !strings.Contains(out, "Empresa criada: Acme") || !strings.Contains(out, "HANKO_COMPANY=acme") {
		t.Fatalf("%d %q", code, out)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.last().Body), &body)
	if diff := cmp.Diff(map[string]any{"name": "Acme", "task_prefix": "ACM"}, body); diff != "" {
		t.Fatal(diff)
	}
}

func TestTaskListTabelaEFiltros(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/tasks": jsonResp(200, list(task1))})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme", "HANKO_TOKEN": "tok123"}
	code, out, _ := exec(t, Runtime{}, env, "", "task", "list", "--status", "todo,blocked", "-p", "cto", "-q", "login", "--limit", "10")
	if code != 0 {
		t.Fatal(code)
	}
	want := "CHAVE  STATUS  PRIORIDADE  PERSONA  TÍTULO\nHNK-1  todo    high        Marina   Implementar login\n"
	if diff := cmp.Diff(want, out); diff != "" {
		t.Fatalf("tabela:\n%s", diff)
	}
	h := f.last()
	if h.Auth != "Bearer tok123" {
		t.Fatalf("auth = %q", h.Auth)
	}
	for _, frag := range []string{"status=todo%2Cblocked", "persona=cto", "q=login", "limit=10"} {
		if !strings.Contains(h.Query, frag) {
			t.Fatalf("query %q sem %q", h.Query, frag)
		}
	}
}

func TestTaskCreateAssignStatusComment(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/tasks":                jsonResp(201, task1),
		"PATCH /api/v1/companies/acme/tasks/HNK-1":         jsonResp(200, task1),
		"POST /api/v1/companies/acme/tasks/HNK-1/comments": jsonResp(201, api.Comment{ID: "c1"}),
		"POST /api/v1/companies/acme/tasks/HNK-1/wake":     jsonResp(202, api.WakeResult{Queued: true}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	body := func() map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(f.last().Body), &m)
		return m
	}

	code, out, _ := exec(t, Runtime{}, env, "", "task", "create", "Implementar login", "-p", "cto", "--priority", "high", "-d", "texto")
	if code != 0 || !strings.Contains(out, "Task criada: HNK-1 Implementar login [todo] → Marina") {
		t.Fatalf("create: %d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"title": "Implementar login", "persona": "cto", "priority": "high", "description": "texto"}, body()); diff != "" {
		t.Fatal(diff)
	}

	exec(t, Runtime{}, env, "", "task", "assign", "HNK-1", "cto")
	if diff := cmp.Diff(map[string]any{"persona": "cto"}, body()); diff != "" {
		t.Fatal(diff)
	}
	code, out, _ = exec(t, Runtime{}, env, "", "task", "assign", "HNK-1", "--none")
	if code != 0 || !strings.Contains(out, "sem responsável") {
		t.Fatalf("%d %q", code, out)
	}
	if diff := cmp.Diff(map[string]any{"unassign": true}, body()); diff != "" {
		t.Fatal(diff)
	}
	if code, _, _ = exec(t, Runtime{}, env, "", "task", "assign", "HNK-1"); code != 2 {
		t.Fatalf("assign sem persona: %d", code)
	}

	exec(t, Runtime{}, env, "", "task", "status", "HNK-1", "in_review")
	if diff := cmp.Diff(map[string]any{"status": "in_review"}, body()); diff != "" {
		t.Fatal(diff)
	}
	code, _, errOut := exec(t, Runtime{}, env, "", "task", "status", "HNK-1", "feito")
	if code != 2 || !strings.Contains(errOut, "status desconhecido") {
		t.Fatalf("status inválido: %d %q", code, errOut)
	}

	exec(t, Runtime{}, env, "", "task", "comment", "HNK-1", "vamos", "em", "frente")
	if diff := cmp.Diff(map[string]any{"body": "vamos em frente"}, body()); diff != "" {
		t.Fatal(diff)
	}
	exec(t, Runtime{}, env, "linha 1\nlinha 2\n", "task", "comment", "HNK-1", "-")
	if diff := cmp.Diff(map[string]any{"body": "linha 1\nlinha 2"}, body()); diff != "" {
		t.Fatal(diff)
	}

	code, out, _ = exec(t, Runtime{}, env, "", "task", "wake", "HNK-1")
	if code != 0 || !strings.Contains(out, "acordada") {
		t.Fatalf("wake: %d %q", code, out)
	}
}

func TestTaskShow(t *testing.T) {
	run1 := api.Run{ID: "run-1", TaskKey: "HNK-1", PersonaName: "Marina", Status: "done", Trigger: "assign"}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tasks/HNK-1": jsonResp(200, task1),
		"GET /api/v1/companies/acme/tasks/" + task1.ID + "/comments": jsonResp(200, list(
			api.Comment{Body: "primeiro", Author: api.ActorRef{Type: "human"}, CreatedAt: time.Date(2026, 10, 13, 9, 1, 0, 0, time.UTC)})),
		"GET /api/v1/companies/acme/runs": jsonResp(200, list(run1)),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, _ := exec(t, Runtime{}, env, "", "task", "show", "HNK-1")
	if code != 0 {
		t.Fatal(code)
	}
	for _, frag := range []string{"HNK-1  Implementar login", "Status:", "Marina", "Comentários (1):", "humano: primeiro", "Execuções (1):", "run-1"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("saída sem %q:\n%s", frag, out)
		}
	}
	if q := f.last().Query; !strings.Contains(q, "task="+task1.ID) {
		t.Fatalf("runs sem filtro de task: %q", q)
	}
	_, out, _ = exec(t, Runtime{}, env, "", "task", "show", "HNK-1", "--json")
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil || m["task"] == nil || m["comments"] == nil || m["runs"] == nil {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestErroDaAPI(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/tasks/HNK-9": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"title":"Não encontrado","status":404,"detail":"task não encontrada"}`))
		},
		"POST /api/v1/companies": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"title":"Entrada inválida","status":422,"detail":"validation failed","errors":[{"message":"obrigatório","location":"body.name"}]}`))
		},
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, _, errOut := exec(t, Runtime{}, env, "", "task", "show", "HNK-9")
	if code != 1 || !strings.Contains(errOut, "erro: task não encontrada [HTTP 404]") {
		t.Fatalf("%d %q", code, errOut)
	}
	code, _, errOut = exec(t, Runtime{}, env, "", "company", "create", "x")
	if code != 1 || !strings.Contains(errOut, "body.name: obrigatório") {
		t.Fatalf("%d %q", code, errOut)
	}
	// servidor fora do ar
	code, _, errOut = exec(t, Runtime{}, nil, "", "--url", "http://127.0.0.1:1", "company", "list")
	if code != 1 || !strings.Contains(errOut, "falha ao falar com http://127.0.0.1:1") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestPersonaHireListShow(t *testing.T) {
	p := api.Persona{ID: "p1", Slug: "cto", Name: "Marina", Title: "CTO", Agent: "claude", Model: "m", Status: "active", HeartbeatIntervalSec: 300, Instructions: "Seja objetiva."}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/personas":    jsonResp(201, p),
		"GET /api/v1/companies/acme/personas":     jsonResp(200, list(p)),
		"GET /api/v1/companies/acme/personas/cto": jsonResp(200, p),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, _ := exec(t, Runtime{}, env, "", "persona", "hire", "Marina", "--agent", "claude", "--title", "CTO", "-i", "Seja objetiva.",
		"--interval", "300", "--config", "permission_mode=plan")
	if code != 0 || !strings.Contains(out, "Persona contratada: Marina (cto)") {
		t.Fatalf("%d %q", code, out)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.last().Body), &body)
	want := map[string]any{"name": "Marina", "agent": "claude", "title": "CTO", "instructions": "Seja objetiva.",
		"heartbeat_interval_sec": float64(300), "adapter_config": map[string]any{"permission_mode": "plan"}}
	if diff := cmp.Diff(want, body); diff != "" {
		t.Fatal(diff)
	}
	if code, _, errOut := exec(t, Runtime{}, env, "", "persona", "hire", "X"); code != 2 || !strings.Contains(errOut, "agent") {
		t.Fatalf("agent obrigatório: %d %q", code, errOut)
	}
	_, out, _ = exec(t, Runtime{}, env, "", "persona", "list")
	if !strings.Contains(out, "cto") || !strings.Contains(out, "CARGO") {
		t.Fatalf("%q", out)
	}
	_, out, _ = exec(t, Runtime{}, env, "", "persona", "show", "cto")
	if !strings.Contains(out, "a cada 300s") || !strings.Contains(out, "Seja objetiva.") {
		t.Fatalf("%q", out)
	}
}

func TestWorkspaceEProject(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/workspaces": jsonResp(201, api.Workspace{Slug: "api", Name: "API", Path: "/srv/api"}),
		"GET /api/v1/companies/acme/workspaces":  jsonResp(200, list(api.Workspace{Slug: "api", Name: "API", Path: "/srv/api"})),
		"POST /api/v1/companies/acme/projects":   jsonResp(201, api.Project{Slug: "lanc", Name: "Lançamento"}),
		"GET /api/v1/companies/acme/projects":    jsonResp(200, list(api.Project{Slug: "lanc", Name: "Lançamento", Description: "multi\nlinha"})),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	if code, out, _ := exec(t, Runtime{}, env, "", "workspace", "create", "API", "--path", "/srv/api"); code != 0 || !strings.Contains(out, "Workspace criado") {
		t.Fatalf("%d %q", code, out)
	}
	if !strings.Contains(f.last().Body, `"path":"/srv/api"`) {
		t.Fatalf("body %s", f.last().Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "workspace", "create", "API"); code != 2 {
		t.Fatalf("path obrigatório: %d", code)
	}
	if _, out, _ := exec(t, Runtime{}, env, "", "workspace", "list"); !strings.Contains(out, "/srv/api") {
		t.Fatalf("%q", out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "project", "create", "Lançamento", "--workspace", "api"); code != 0 || !strings.Contains(out, "Projeto criado") {
		t.Fatalf("%d %q", code, out)
	}
	if _, out, _ := exec(t, Runtime{}, env, "", "project", "list"); !strings.Contains(out, "multi linha") {
		t.Fatalf("descrição deveria ficar em uma linha: %q", out)
	}
}

func TestRunListShow(t *testing.T) {
	cost := 0.0123
	r := api.Run{ID: "run-1", TaskKey: "HNK-1", TaskTitle: "Login", PersonaName: "Marina", Status: "done", Trigger: "assign", Agent: "claude",
		CostUSD: &cost, Summary: "Feito.", CreatedAt: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC),
		StartedAt: ptr(time.Date(2026, 10, 13, 9, 0, 5, 0, time.UTC))}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/runs":       jsonResp(200, list(r)),
		"GET /api/v1/companies/acme/runs/run-1": jsonResp(200, r),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	_, out, _ := exec(t, Runtime{}, env, "", "run", "list", "--task", "HNK-1", "--status", "done", "--limit", "5")
	want := "ID     TASK   PERSONA  STATUS  GATILHO  INÍCIO            CUSTO\nrun-1  HNK-1  Marina   done    assign   13/10/2026 09:00  US$ 0.0123\n"
	if diff := cmp.Diff(want, out); diff != "" {
		t.Fatalf("tabela:\n%s", diff)
	}
	for _, frag := range []string{"task=HNK-1", "status=done", "limit=5"} {
		if !strings.Contains(f.hits[len(f.hits)-1].Query, frag) {
			t.Fatalf("query %q sem %q", f.last().Query, frag)
		}
	}
	_, out, _ = exec(t, Runtime{}, env, "", "run", "show", "run-1")
	if !strings.Contains(out, "Resumo:\nFeito.") || !strings.Contains(out, "US$ 0.0123") {
		t.Fatalf("%q", out)
	}
}

func ptr[T any](v T) *T { return &v }

func evJSON(seq int64, kind, text string) string {
	b, _ := json.Marshal(api.RunEvent{ID: fmt.Sprint("e", seq), RunID: "run-1", Seq: seq, Kind: kind, Text: text, OccurredAt: time.Date(2026, 10, 13, 9, 0, int(seq), 0, time.UTC)})
	return string(b)
}

func TestRunLogsPaginado(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"GET /api/v1/companies/acme/runs/run-1/events": jsonResp(200, list(
			api.RunEvent{Seq: 1, Kind: "assistant", Text: "olá\nsegunda linha", OccurredAt: time.Date(2026, 10, 13, 9, 0, 1, 0, time.UTC)},
			api.RunEvent{Seq: 2, Kind: "tool_use", Text: "Bash ls", OccurredAt: time.Date(2026, 10, 13, 9, 0, 2, 0, time.UTC)})),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, _ := exec(t, Runtime{}, env, "", "run", "logs", "run-1", "--after", "0")
	if code != 0 {
		t.Fatal(code)
	}
	want := "09:00:01  assistant   olá\n                      segunda linha\n09:00:02  tool_use    Bash ls\n"
	if diff := cmp.Diff(want, out); diff != "" {
		t.Fatalf("log:\n%s", diff)
	}
	if !strings.Contains(f.last().Query, "after=0") {
		t.Fatalf("query %q", f.last().Query)
	}
}

func sseHandler(frames string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, frames)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func TestRunLogsFollow(t *testing.T) {
	finalRun, _ := json.Marshal(api.Run{ID: "run-1", Status: "done"})
	stream := "retry: 3000\n\n" +
		"id: 1\nevent: run.event\ndata: " + evJSON(1, "assistant", "analisando") + "\n\n" +
		": ping\n\n" +
		"id: 2\nevent: run.event\ndata: " + evJSON(2, "result", "pronto") + "\n\n" +
		"event: run.status\ndata: " + string(finalRun) + "\n\n"
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/runs/run-1/stream": sseHandler(stream, 200)})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}

	code, out, _ := exec(t, Runtime{}, env, "", "run", "logs", "run-1", "-f")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, frag := range []string{"assistant   analisando", "result      pronto", "-- execução run-1: done"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("saída sem %q:\n%s", frag, out)
		}
	}
	if !strings.Contains(f.last().Query, "after=0") {
		t.Fatalf("query %q", f.last().Query)
	}

	// --json: NDJSON, uma linha por frame
	_, out, _ = exec(t, Runtime{}, env, "", "run", "logs", "run-1", "-f", "--json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("esperava 3 linhas NDJSON, veio %d:\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Fatalf("linha inválida: %q", l)
		}
	}
}

func TestRunLogsFollowStatusFalhoDaExitCode(t *testing.T) {
	finalRun, _ := json.Marshal(api.Run{ID: "run-1", Status: "failed"})
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/runs/run-1/stream": sseHandler("event: run.status\ndata: "+string(finalRun)+"\n\n", 200)})
	code, _, errOut := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}, "", "run", "logs", "run-1", "-f")
	if code != 1 || !strings.Contains(errOut, "status failed") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestRunLogsFollowReconecta(t *testing.T) {
	finalRun, _ := json.Marshal(api.Run{ID: "run-1", Status: "done"})
	var mu sync.Mutex
	var afters []string
	conn := 0
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/runs/run-1/stream": func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conn++
		n := conn
		afters = append(afters, r.URL.Query().Get("after"))
		mu.Unlock()
		if n == 1 { // cai sem run.status
			sseHandler("id: 1\nevent: run.event\ndata: "+evJSON(1, "assistant", "um")+"\n\n", 200)(w, r)
			return
		}
		sseHandler("id: 2\nevent: run.event\ndata: "+evJSON(2, "assistant", "dois")+"\n\nevent: run.status\ndata: "+string(finalRun)+"\n\n", 200)(w, r)
	}})
	var out, errb bytes.Buffer
	e := &env{in: strings.NewReader(""), out: &out, errOut: &errb, getenv: func(k string) string {
		return map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}[k]
	}, sleep: func(time.Duration) {}, followRetry: 3, followWait: 0}
	root := e.root()
	root.SetArgs([]string{"run", "logs", "run-1", "-f"})
	root.SetOut(&out)
	root.SetErr(&errb)
	if err := root.Execute(); err != nil {
		t.Fatalf("erro: %v\n%s", err, errb.String())
	}
	if diff := cmp.Diff([]string{"0", "1"}, afters); diff != "" {
		t.Fatalf("after nas conexões: %s", diff)
	}
	if !strings.Contains(out.String(), "um") || !strings.Contains(out.String(), "dois") || !strings.Contains(errb.String(), "reconectando") {
		t.Fatalf("out=%q err=%q", out.String(), errb.String())
	}
}

func TestRunLogsFollowDesistePosTentativas(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies/acme/runs/run-1/stream": sseHandler(": ping\n\n", 200)})
	var out, errb bytes.Buffer
	e := &env{in: strings.NewReader(""), out: &out, errOut: &errb, getenv: func(k string) string {
		return map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}[k]
	}, sleep: func(time.Duration) {}, followRetry: 2}
	root := e.root()
	root.SetArgs([]string{"run", "logs", "run-1", "-f"})
	root.SetOut(&out)
	root.SetErr(&errb)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--after 0") {
		t.Fatalf("esperava erro com dica de retomada, veio %v", err)
	}
}

func TestRunLogsFollowErroHTTP(t *testing.T) {
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{}) // 404 problem em tudo
	code, _, errOut := exec(t, Runtime{}, map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}, "", "run", "logs", "run-1", "-f")
	if code != 1 || !strings.Contains(errOut, "[HTTP 404]") {
		t.Fatalf("%d %q", code, errOut)
	}
}
