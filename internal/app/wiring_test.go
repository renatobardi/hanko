package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

type noopJobs struct{}

func (noopJobs) WakeTx(context.Context, pgx.Tx, service.Wake) error                { return nil }
func (noopJobs) ExecuteRunTx(context.Context, pgx.Tx, domain.ID, domain.ID) error  { return nil }
func (noopJobs) StartReviewTx(context.Context, pgx.Tx, domain.ID, domain.ID) error { return nil }
func (noopJobs) DeliverMemoryTx(context.Context, pgx.Tx, domain.ID) error          { return nil }

func get(t *testing.T, base, method, path, host, bearer, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestWiringMuxReal sobe o mux de produção (buildHandler) com banco de teste e confere
// que autenticação por run token, /readyz, host check e proxy de memória estão ligados.
func TestWiringMuxReal(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := service.New(pool, noopJobs{}, events.NewBroker())
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}

	cfg := config.Defaults()
	cfg.Mode = config.ModeTrusted // este teste cobre o modo trusted; o authenticated tem wiring_auth_test.go
	cfg.PublicURL = "https://hanko.example.test"
	cfg.AllowedHosts = []string{"extra.test"}
	cfg.Memory = config.Memory{Provider: config.MemoryAIMemory, URL: "http://127.0.0.1:1", Token: "mem"}
	h, err := buildHandler(handlerDeps{Cfg: cfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool, Tokens: tokens})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	co, err := svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	per, err := svc.HirePersona(ctx, domain.HumanActor, co.ID, service.HirePersonaInput{Name: "Eng", Agent: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, domain.HumanActor, co.ID, service.CreateTaskInput{Title: "t", PersonaID: &per.ID})
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.CheckoutRun(ctx, service.Wake{CompanyID: co.ID, PersonaID: per.ID, TaskID: task.ID, Trigger: domain.TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := tokens.Issue(co.ID, per.ID, run.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("bearer inválido é 401, sem header é trusted", func(t *testing.T) {
		if code, _ := get(t, ts.URL, "GET", "/api/v1/companies/acme", "", "lixo", ""); code != 401 {
			t.Errorf("bearer inválido: %d", code)
		}
		if code, _ := get(t, ts.URL, "GET", "/api/v1/companies/acme", "", "", ""); code != 200 {
			t.Errorf("sem header: %d", code)
		}
	})
	t.Run("run token de run ativo vale e persona não administra", func(t *testing.T) {
		if code, body := get(t, ts.URL, "GET", "/api/v1/companies/acme", "", tok, ""); code != 200 {
			t.Fatalf("token ativo: %d %s", code, body)
		}
		if code, _ := get(t, ts.URL, "POST", "/api/v1/companies/acme/projects", "", tok, `{"name":"p"}`); code != 403 {
			t.Errorf("projeto por persona: %d", code)
		}
		if code, _ := get(t, ts.URL, "POST", "/api/v1/companies", "", tok, `{"name":"nova"}`); code != 403 {
			t.Errorf("empresa por persona: %d", code)
		}
		if code, _ := get(t, ts.URL, "POST", "/mcp", "", "lixo", `{}`); code != 401 {
			t.Errorf("/mcp bearer inválido: %d", code)
		}
	})
	t.Run("token de run inexistente é 401 na API, MCP e proxy de memória", func(t *testing.T) {
		ghost, _ := tokens.Issue(co.ID, per.ID, domain.NewID(), task.ID)
		for p, method := range map[string]string{"/api/v1/companies/acme": "GET", "/mcp": "POST", "/memory-mcp": "POST"} {
			if code, _ := get(t, ts.URL, method, p, "", ghost, `{}`); code != 401 {
				t.Errorf("%s: %d", p, code)
			}
		}
	})
	t.Run("proxy de memória exige bearer", func(t *testing.T) {
		if code, _ := get(t, ts.URL, "POST", "/memory-mcp", "", "", `{}`); code != 401 {
			t.Errorf("sem bearer: %d", code)
		}
		// token válido: passa a autenticação; upstream inalcançável => 502 (não 401)
		if code, _ := get(t, ts.URL, "POST", "/memory-mcp", "", tok, `{}`); code != http.StatusBadGateway {
			t.Errorf("com token ativo: %d, quer 502", code)
		}
	})
	t.Run("host check", func(t *testing.T) {
		for host, want := range map[string]int{
			"evil.example": 421, "hanko.example.test": 200, "extra.test:8080": 200, "localhost:7420": 200, "127.0.0.1:7420": 200, "[::1]:7420": 200,
		} {
			if code, _ := get(t, ts.URL, "GET", "/readyz", host, "", ""); code != want {
				t.Errorf("Host %q: %d, quer %d", host, code, want)
			}
		}
	})
	t.Run("readyz pinga o banco", func(t *testing.T) {
		if code, body := get(t, ts.URL, "GET", "/readyz", "", "", ""); code != 200 || body != "ok" {
			t.Fatalf("readyz: %d %q", code, body)
		}
	})
	t.Run("run encerrado invalida o token", func(t *testing.T) {
		if _, err := svc.MarkRunStarted(ctx, co.ID, run.ID, "", ""); err != nil {
			t.Fatal(err)
		}
		if code, _ := get(t, ts.URL, "GET", "/api/v1/companies/acme", "", tok, ""); code != 200 {
			t.Fatalf("running: %d", code)
		}
		if _, err := svc.FinishRun(ctx, co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: adapter.Result{}}); err != nil {
			t.Fatal(err)
		}
		if code, _ := get(t, ts.URL, "GET", "/api/v1/companies/acme", "", tok, ""); code != 401 {
			t.Errorf("done: %d, quer 401", code)
		}
		if code, _ := get(t, ts.URL, "POST", "/memory-mcp", "", tok, `{}`); code != 401 {
			t.Errorf("memória com run done: %d", code)
		}
	})
	t.Run("readyz falha com banco fechado", func(t *testing.T) {
		pool.Close()
		if code, _ := get(t, ts.URL, "GET", "/readyz", "", "", ""); code != 503 {
			t.Errorf("readyz com pool fechado: %d", code)
		}
	})
}

func TestHostHandler(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	h := hostHandler("http://hanko.oute.pro:8080", []string{"Extra.Test:9", " "}, next)
	for host, want := range map[string]int{
		"localhost": 204, "LOCALHOST:7420": 204, "127.0.0.1": 204, "[::1]": 204, "[::1]:7420": 204,
		"hanko.oute.pro": 204, "hanko.oute.pro:8080": 204, "extra.test": 204,
		"evil.com": 421, "evil.com:7420": 421, "localhost.evil.com": 421, "127.0.0.1.nip.io": 421, "": 421, "10.0.0.5": 421,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.Host = host
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q = %d, quer %d", host, rec.Code, want)
		}
	}
}
