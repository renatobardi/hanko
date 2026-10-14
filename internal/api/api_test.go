package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

func TestSondas(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	resp, body := do(t, "GET", ts.URL+"/healthz", "", nil)
	if resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("healthz: %d %q", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", ts.URL+"/readyz", "", nil)
	if resp.StatusCode != 200 || body != "ok" {
		t.Fatalf("readyz sem DB: %d %q", resp.StatusCode, body)
	}

	ts2, _ := newTestServer(t, &fakeSvc{}, func(d *Deps) { d.DB = stubPinger{} })
	if resp, _ := do(t, "GET", ts2.URL+"/readyz", "", nil); resp.StatusCode != 200 {
		t.Fatalf("readyz com DB ok: %d", resp.StatusCode)
	}
	ts3, _ := newTestServer(t, &fakeSvc{}, func(d *Deps) { d.DB = stubPinger{errors.New("down")} })
	if resp, _ := do(t, "GET", ts3.URL+"/readyz", "", nil); resp.StatusCode != 503 {
		t.Fatalf("readyz com DB fora: %d", resp.StatusCode)
	}
}

func TestOpenAPIEDocsServidos(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	resp, body := do(t, "GET", ts.URL+"/openapi.json", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"/api/v1/companies/{company}/tasks"`) {
		t.Fatalf("openapi.json: %d", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", ts.URL+"/docs", "", nil); resp.StatusCode != 200 {
		t.Fatalf("docs: %d", resp.StatusCode)
	}
}

func TestRequestID(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	resp, _ := do(t, "GET", ts.URL+"/healthz", "", map[string]string{"X-Request-ID": "abc-123"})
	if got := resp.Header.Get("X-Request-ID"); got != "abc-123" {
		t.Fatalf("request-id não ecoado: %q", got)
	}
	resp, _ = do(t, "GET", ts.URL+"/healthz", "", nil)
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("request-id não gerado")
	}
	resp, _ = do(t, "GET", ts.URL+"/healthz", "", map[string]string{"X-Request-ID": strings.Repeat("x", 200)})
	if len(resp.Header.Get("X-Request-ID")) > 64 {
		t.Fatal("request-id longo demais aceito")
	}
	if resp.Header.Get("X-Hanko-Version") != "test" {
		t.Fatalf("X-Hanko-Version = %q", resp.Header.Get("X-Hanko-Version"))
	}
}

func TestRecover(t *testing.T) {
	f := &fakeSvc{} // backend nil: ListWorkspaces entra em panic
	seedCompany(f)
	ts, _ := newTestServer(t, f, nil)
	resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme/workspaces", "", nil)
	if resp.StatusCode != 500 || !strings.Contains(body, "erro interno") {
		t.Fatalf("panic deveria virar 500: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, "GET", ts.URL+"/healthz", "", nil); resp.StatusCode != 200 {
		t.Fatal("servidor caiu após panic")
	}
}

func TestCORS(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	resp, _ := do(t, "GET", ts.URL+"/healthz", "", map[string]string{"Origin": "https://x.example"})
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS deveria estar desligado por padrão")
	}
	ts2, _ := newTestServer(t, &fakeSvc{}, func(d *Deps) { d.CORSOrigins = []string{"https://ui.example"} })
	resp, _ = do(t, "GET", ts2.URL+"/healthz", "", map[string]string{"Origin": "https://ui.example"})
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://ui.example" {
		t.Fatalf("origem permitida não liberada: %v", resp.Header)
	}
	resp, _ = do(t, "GET", ts2.URL+"/healthz", "", map[string]string{"Origin": "https://evil.example"})
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("origem não listada liberada")
	}
	resp, _ = do(t, "OPTIONS", ts2.URL+"/api/v1/companies", "", map[string]string{
		"Origin": "https://ui.example", "Access-Control-Request-Method": "POST"})
	if resp.StatusCode != 204 {
		t.Fatalf("preflight = %d", resp.StatusCode)
	}
}

func TestCompaniesCRUD(t *testing.T) {
	f := &fakeSvc{}
	ts, _ := newTestServer(t, f, nil)

	resp, body := do(t, "POST", ts.URL+"/api/v1/companies", `{"name":"Acme Ltda"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var c Company
	if err := json.Unmarshal([]byte(body), &c); err != nil || c.Slug != "acme-ltda" || c.TaskPrefix != "HNK" {
		t.Fatalf("corpo: %+v %v", c, err)
	}
	if f.lastActor.Type != domain.ActorHuman {
		t.Fatalf("ator = %+v", f.lastActor)
	}

	resp, body = do(t, "GET", ts.URL+"/api/v1/companies", "", nil)
	var l ListBody[Company]
	if err := json.Unmarshal([]byte(body), &l); resp.StatusCode != 200 || err != nil || len(l.Items) != 1 || l.Limit != 50 {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme-ltda", "", nil); resp.StatusCode != 200 {
		t.Fatalf("get por slug: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/"+c.ID, "", nil); resp.StatusCode != 200 {
		t.Fatalf("get por id: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/nao-existe", "", nil); resp.StatusCode != 404 {
		t.Fatalf("get inexistente: %d", resp.StatusCode)
	}
	if resp, _ = do(t, "POST", ts.URL+"/api/v1/companies", `{"name":"dup"}`, nil); resp.StatusCode != 409 {
		t.Fatalf("conflito: %d", resp.StatusCode)
	}
}

func TestValidacao(t *testing.T) {
	ts, _ := newTestServer(t, &fakeSvc{}, nil)
	tests := []struct{ name, method, path, body string }{
		{"nome ausente", "POST", "/api/v1/companies", `{}`},
		{"slug inválido", "POST", "/api/v1/companies", `{"name":"x","slug":"Inválido!"}`},
		{"json quebrado", "POST", "/api/v1/companies", `{`},
		{"limit zero", "GET", "/api/v1/companies?limit=0", ""},
		{"limit enorme", "GET", "/api/v1/companies?limit=9999", ""},
		{"offset negativo", "GET", "/api/v1/companies?offset=-1", ""},
		{"agent fora do enum", "POST", "/api/v1/companies/acme/personas", `{"name":"x","agent":"gpt"}`},
		{"prioridade fora do enum", "POST", "/api/v1/companies/acme/tasks", `{"title":"x","priority":"urgentissima"}`},
		{"comentário vazio", "POST", "/api/v1/companies/acme/tasks/HNK-1/comments", `{"body":""}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := do(t, tc.method, ts.URL+tc.path, tc.body, nil)
			if resp.StatusCode != 422 && resp.StatusCode != 400 {
				t.Fatalf("status = %d, quer 422/400: %s", resp.StatusCode, body)
			}
		})
	}
}

func TestTasksFluxo(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	cto := domain.Persona{ID: domain.NewID(), CompanyID: c.ID, Slug: "cto", Name: "Marina", Agent: "fake", Status: domain.PersonaActive}
	f.personas = append(f.personas, cto)
	ts, _ := newTestServer(t, f, nil)
	base := ts.URL + "/api/v1/companies/acme"

	resp, body := do(t, "POST", base+"/tasks", `{"title":"Implementar login","persona":"cto","priority":"high","event_id":"e1"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create task: %d %s", resp.StatusCode, body)
	}
	if f.lastTaskIn.PersonaID == nil || *f.lastTaskIn.PersonaID != cto.ID || f.lastTaskIn.Priority != domain.PriorityHigh || f.lastTaskIn.EventID != "e1" {
		t.Fatalf("input do service: %+v", f.lastTaskIn)
	}
	var task Task
	_ = json.Unmarshal([]byte(body), &task)
	if task.Key != "HNK-1" || task.CreatedBy.Type != "human" {
		t.Fatalf("task: %+v", task)
	}

	if resp, _ = do(t, "POST", base+"/tasks", `{"title":"x","persona":"fantasma"}`, nil); resp.StatusCode != 404 {
		t.Fatalf("persona inexistente: %d", resp.StatusCode)
	}

	resp, body = do(t, "GET", base+"/tasks?status=todo,in_progress&persona=cto&q=login&limit=10&offset=5", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	fl := f.lastFilter
	if len(fl.Statuses) != 2 || fl.PersonaID == nil || fl.Query != "login" || fl.Limit != 10 || fl.Offset != 5 {
		t.Fatalf("filtro: %+v", fl)
	}
	if resp, _ = do(t, "GET", base+"/tasks?status=nada", "", nil); resp.StatusCode != 422 {
		t.Fatalf("status inválido: %d", resp.StatusCode)
	}

	resp, body = do(t, "POST", base+"/tasks/HNK-1/comments", `{"body":"olá"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("comment: %d %s", resp.StatusCode, body)
	}
	if f.lastComment.Body != "olá" || f.lastComment.RunID != nil {
		t.Fatalf("comment input: %+v", f.lastComment)
	}

	resp, body = do(t, "POST", base+"/tasks/HNK-1/wake", "", nil)
	if resp.StatusCode != 202 || !strings.Contains(body, `"queued":true`) {
		t.Fatalf("wake: %d %s", resp.StatusCode, body)
	}
	if resp, _ = do(t, "GET", base+"/tasks/HNK-99", "", nil); resp.StatusCode != 404 {
		t.Fatalf("task inexistente: %d", resp.StatusCode)
	}
}

func TestRunsListaOffset(t *testing.T) {
	f := &fakeSvc{runs: map[domain.ID]service.RunView{}}
	c := seedCompany(f)
	for range 3 {
		id := domain.NewID()
		f.runs[id] = service.RunView{Run: domain.Run{ID: id, CompanyID: c.ID, Status: domain.RunDone}}
	}
	ts, _ := newTestServer(t, f, nil)
	resp, body := do(t, "GET", ts.URL+"/api/v1/companies/acme/runs?status=done,failed&limit=2&offset=1", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if f.lastRunF.Limit != 3 || len(f.lastRunF.Statuses) != 2 {
		t.Fatalf("filtro: %+v", f.lastRunF)
	}
	var l ListBody[Run]
	_ = json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 2 {
		t.Fatalf("itens: %d", len(l.Items))
	}
}

func TestAutenticacaoPorRunToken(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	other := domain.Company{ID: domain.NewID(), Slug: "outra", Name: "Outra"}
	f.companies = append(f.companies, other)
	f.tasks = append(f.tasks, service.TaskView{Task: domain.Task{ID: domain.NewID(), CompanyID: c.ID, Key: "HNK-1", Number: 1}})
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123"), TTL: time.Hour}
	ts, _ := newTestServer(t, f, func(d *Deps) { d.Tokens = tokens })

	pid, rid := domain.NewID(), domain.NewID()
	tok, err := tokens.Issue(c.ID, pid, rid, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tok}

	// token válido: ator é a persona do token e o run_id do comentário vem do token
	resp, body := do(t, "POST", ts.URL+"/api/v1/companies/acme/tasks/HNK-1/comments", `{"body":"feito"}`, bearer)
	if resp.StatusCode != 201 {
		t.Fatalf("comment com token: %d %s", resp.StatusCode, body)
	}
	if f.lastActor.Type != domain.ActorPersona || f.lastActor.ID == nil || *f.lastActor.ID != pid {
		t.Fatalf("ator = %+v", f.lastActor)
	}
	if f.lastComment.RunID == nil || *f.lastComment.RunID != rid {
		t.Fatalf("run_id padrão = %v", f.lastComment.RunID)
	}

	// empresa diferente da do token: 403
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/outra", "", bearer); resp.StatusCode != 403 {
		t.Fatalf("empresa alheia: %d", resp.StatusCode)
	}
	// persona não cria empresa
	if resp, _ = do(t, "POST", ts.URL+"/api/v1/companies", `{"name":"nova"}`, bearer); resp.StatusCode != 403 {
		t.Fatalf("criar empresa com token: %d", resp.StatusCode)
	}
	// listagem de empresas só mostra a do token
	_, body = do(t, "GET", ts.URL+"/api/v1/companies", "", bearer)
	var l ListBody[Company]
	_ = json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 1 || l.Items[0].Slug != "acme" {
		t.Fatalf("listagem com token: %s", body)
	}
	// token inválido / mal formado: 401
	for _, h := range []string{"Bearer lixo", "Basic abc", "Bearer "} {
		if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme", "", map[string]string{"Authorization": h}); resp.StatusCode != 401 {
			t.Fatalf("%q: %d", h, resp.StatusCode)
		}
	}
	// token expirado
	old := &auth.RunTokens{Secret: tokens.Secret, TTL: time.Minute, Now: func() time.Time { return time.Now().Add(-time.Hour) }}
	expired, _ := old.Issue(c.ID, pid, rid, domain.NewID())
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/acme", "", map[string]string{"Authorization": "Bearer " + expired}); resp.StatusCode != 401 {
		t.Fatalf("expirado: %d", resp.StatusCode)
	}
	// sem header: modo trusted
	if resp, _ = do(t, "GET", ts.URL+"/api/v1/companies/outra", "", nil); resp.StatusCode != 200 {
		t.Fatalf("trusted: %d", resp.StatusCode)
	}
	// docs não exigem token válido
	if resp, _ = do(t, "GET", ts.URL+"/openapi.json", "", map[string]string{"Authorization": "Bearer lixo"}); resp.StatusCode != 200 {
		t.Fatalf("openapi com header lixo: %d", resp.StatusCode)
	}
}

func TestSemTokensIgnoraAuthorization(t *testing.T) {
	f := &fakeSvc{}
	seedCompany(f)
	ts, _ := newTestServer(t, f, nil)
	resp, _ := do(t, "GET", ts.URL+"/api/v1/companies/acme", "", map[string]string{"Authorization": "Bearer qualquer"})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
