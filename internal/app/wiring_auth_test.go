package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

type call struct {
	method, path, body string
	hdr                map[string]string
}

func send(t *testing.T, base string, c call) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(c.method, base+c.path, strings.NewReader(c.body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "hanko.example.test"
	for k, v := range c.hdr {
		req.Header.Set(k, v)
	}
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 10 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	return nil
}

func authCfg() config.Config {
	cfg := config.Defaults()
	cfg.Mode = config.ModeAuthenticated
	cfg.Secret = "0123456789abcdef0123456789abcdef"
	cfg.PublicURL = "https://hanko.example.test"
	cfg.Memory = config.Memory{Provider: config.MemoryNone}
	return cfg
}

const mcpInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// TestWiringAuthenticatedLocal sobe o mux de produção no modo authenticated (provider local)
// e percorre bootstrap do dono, sessão, API key, run token, /mcp, /metrics e as rotas livres.
func TestWiringAuthenticatedLocal(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := service.New(pool, noopJobs{}, events.NewBroker())
	tokens := auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("m 1\n")) })
	lcfg := authCfg()
	lcfg.Auth.WebBootstrap = true // PublicURL é um host público: o formulário do dono precisa ser liberado
	h, err := buildHandler(handlerDeps{Cfg: lcfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool, Tokens: tokens, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	same := map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/x-www-form-urlencoded"}

	t.Run("rotas livres e protegidas sem login", func(t *testing.T) {
		for p, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/static/hanko.css": 200, "/entrar": 200, "/metrics": 200, "/convite/inexistente": 404} {
			if resp, _ := send(t, ts.URL, call{method: "GET", path: p}); resp.StatusCode != want {
				t.Errorf("%s: %d, quer %d", p, resp.StatusCode, want)
			}
		}
		// /metrics atrás de proxy (X-Forwarded-For) deixa de contar como loopback
		if resp, _ := send(t, ts.URL, call{method: "GET", path: "/metrics", hdr: map[string]string{"X-Forwarded-For": "203.0.113.4"}}); resp.StatusCode != 401 {
			t.Errorf("/metrics via proxy: %d", resp.StatusCode)
		}
		for _, p := range []string{"/", "/c/acme/tasks", "/bem-vindo"} {
			resp, _ := send(t, ts.URL, call{method: "GET", path: p})
			if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/entrar") {
				t.Errorf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Location"))
			}
		}
		for _, c := range []call{{method: "GET", path: "/api/v1/companies"}, {method: "POST", path: "/mcp", body: mcpInit}, {method: "POST", path: "/memory-mcp", body: "{}"}} {
			if resp, _ := send(t, ts.URL, c); resp.StatusCode != 401 {
				t.Errorf("%s %s sem credencial: %d", c.method, c.path, resp.StatusCode)
			}
		}
	})

	var cookie *http.Cookie
	t.Run("bootstrap do dono pela UI", func(t *testing.T) {
		if _, body := send(t, ts.URL, call{method: "GET", path: "/entrar"}); !strings.Contains(body, "Criar conta de dono") {
			t.Fatal("sem usuários, /entrar deveria oferecer a conta de dono")
		}
		form := "name=Dona&email=dona%40exemplo.com&password=uma-senha-bem-longa"
		resp, _ := send(t, ts.URL, call{method: "POST", path: "/entrar/dono", body: form, hdr: same})
		if resp.StatusCode != 303 {
			t.Fatalf("criar dono: %d", resp.StatusCode)
		}
		cookie = sessionCookie(resp)
		if cookie == nil || !cookie.Secure || !cookie.HttpOnly {
			t.Fatalf("cookie sob https público deve ser Secure+HttpOnly: %+v", cookie)
		}
		if resp, _ := send(t, ts.URL, call{method: "POST", path: "/entrar/dono", body: form, hdr: same}); resp.StatusCode != 409 {
			t.Fatalf("segundo dono: %d", resp.StatusCode)
		}
	})

	t.Run("sessão abre UI e API; ator carrega o usuário", func(t *testing.T) {
		ck := map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}
		if resp, body := send(t, ts.URL, call{method: "GET", path: "/bem-vindo", hdr: ck}); resp.StatusCode != 200 || !strings.Contains(body, "Criar empresa") {
			t.Fatalf("UI com sessão: %d", resp.StatusCode)
		}
		api := map[string]string{"Cookie": ck["Cookie"], "Sec-Fetch-Site": "same-origin", "Content-Type": "application/json"}
		resp, body := send(t, ts.URL, call{method: "POST", path: "/api/v1/companies", body: `{"name":"Acme"}`, hdr: api})
		if resp.StatusCode != 201 {
			t.Fatalf("criar empresa: %d %s", resp.StatusCode, body)
		}
		if resp, _ := send(t, ts.URL, call{method: "POST", path: "/api/v1/companies", body: `{"name":"X"}`, hdr: map[string]string{"Cookie": ck["Cookie"], "Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}}); resp.StatusCode != 403 {
			t.Fatalf("CSRF na API: %d", resp.StatusCode)
		}
		var co struct{ ID string }
		_ = json.Unmarshal([]byte(body), &co)
		cid, _ := domain.ParseID(co.ID)
		acts, err := svc.ListActivity(ctx, cid, service.ActivityFilter{})
		if err != nil || len(acts) == 0 {
			t.Fatalf("atividade: %v %d", err, len(acts))
		}
		u, _ := svc.GetUserByEmail(ctx, "dona@exemplo.com")
		// o evento de criação da empresa é o do usuário; a semeadura dos modelos (ADR-0089) é do sistema.
		var created *domain.ActivityEvent
		for i := range acts {
			if acts[i].Verb == "company.created" {
				created = &acts[i]
			}
		}
		if created == nil {
			t.Fatalf("sem company.created: %+v", acts)
		}
		if a := created.Actor; a.Type != domain.ActorHuman || a.ID == nil || *a.ID != u.ID {
			t.Fatalf("o evento deveria ser do usuário: %+v", a)
		}
	})

	t.Run("API key na API e no MCP; revogada deixa de valer", func(t *testing.T) {
		u, _ := svc.GetUserByEmail(ctx, "dona@exemplo.com")
		key, k, err := svc.CreateAPIKey(ctx, u.ID, "ci")
		if err != nil {
			t.Fatal(err)
		}
		bearer := map[string]string{"Authorization": "Bearer " + key}
		if resp, body := send(t, ts.URL, call{method: "GET", path: "/api/v1/companies/acme", hdr: bearer}); resp.StatusCode != 200 {
			t.Fatalf("API key na API: %d %s", resp.StatusCode, body)
		}
		mcp := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
		if resp, body := send(t, ts.URL, call{method: "POST", path: "/mcp", body: mcpInit, hdr: mcp}); resp.StatusCode != 200 || !strings.Contains(body, "hanko") {
			t.Fatalf("API key no MCP: %d %s", resp.StatusCode, body)
		}
		if resp, _ := send(t, ts.URL, call{method: "GET", path: "/metrics", hdr: map[string]string{"X-Forwarded-For": "203.0.113.4", "Authorization": "Bearer " + key}}); resp.StatusCode != 200 {
			t.Fatalf("/metrics com API key: %d", resp.StatusCode)
		}
		if _, err := svc.RevokeAPIKey(ctx, u.ID, k.ID); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"/api/v1/companies/acme", "/mcp"} {
			if resp, _ := send(t, ts.URL, call{method: "POST", path: p, body: mcpInit, hdr: mcp}); resp.StatusCode != 401 {
				t.Errorf("%s com chave revogada: %d", p, resp.StatusCode)
			}
		}
	})

	t.Run("/memory-mcp não aceita API key", func(t *testing.T) {
		u, err := svc.GetUserByEmail(ctx, "dona@exemplo.com")
		if err != nil {
			t.Fatal(err)
		}
		key, _, err := svc.CreateAPIKey(ctx, u.ID, "memoria")
		if err != nil {
			t.Fatal(err)
		}
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("o proxy não pode encaminhar uma chamada autenticada só por API key")
			w.WriteHeader(200)
		}))
		t.Cleanup(upstream.Close)
		mcfg := lcfg
		mcfg.Memory = config.Memory{Provider: config.MemoryAIMemory, URL: upstream.URL}
		mh, err := buildHandler(handlerDeps{Cfg: mcfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool, Tokens: tokens})
		if err != nil {
			t.Fatal(err)
		}
		mts := httptest.NewServer(mh)
		t.Cleanup(mts.Close)
		hdr := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
		resp, body := send(t, mts.URL, call{method: "POST", path: "/memory-mcp", body: mcpInit, hdr: hdr})
		if resp.StatusCode != 401 {
			t.Fatalf("/memory-mcp com API key: %d %s (API key válida passa pelo Guard, mas o proxy só aceita run token)", resp.StatusCode, body)
		}
		// a mesma chave vale na API e no /mcp: o 401 é do proxy, não da chave
		if resp, _ := send(t, mts.URL, call{method: "GET", path: "/api/v1/companies", hdr: map[string]string{"Authorization": "Bearer " + key}}); resp.StatusCode != 200 {
			t.Fatalf("a chave deveria valer na API: %d", resp.StatusCode)
		}
	})

	t.Run("run token de persona segue valendo", func(t *testing.T) {
		co, _ := svc.GetCompany(ctx, "acme")
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
		tok, _ := tokens.Issue(co.ID, per.ID, run.ID, task.ID)
		bearer := map[string]string{"Authorization": "Bearer " + tok}
		if resp, body := send(t, ts.URL, call{method: "GET", path: "/api/v1/companies/acme", hdr: bearer}); resp.StatusCode != 200 {
			t.Fatalf("run token na API: %d %s", resp.StatusCode, body)
		}
		mcp := map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
		if resp, body := send(t, ts.URL, call{method: "POST", path: "/mcp", body: mcpInit, hdr: mcp}); resp.StatusCode != 200 {
			t.Fatalf("run token no MCP: %d %s", resp.StatusCode, body)
		}
		// agente local que omite o token deixa de virar o humano (ADR-0017)
		if resp, _ := send(t, ts.URL, call{method: "GET", path: "/api/v1/companies/acme"}); resp.StatusCode != 401 {
			t.Fatalf("sem token: %d", resp.StatusCode)
		}
	})

	t.Run("sair invalida a sessão", func(t *testing.T) {
		ck := map[string]string{"Cookie": cookie.Name + "=" + cookie.Value, "Sec-Fetch-Site": "same-origin"}
		if resp, _ := send(t, ts.URL, call{method: "POST", path: "/sair", hdr: ck}); resp.StatusCode != 303 {
			t.Fatalf("sair: %d", resp.StatusCode)
		}
		if resp, _ := send(t, ts.URL, call{method: "GET", path: "/api/v1/companies/acme", hdr: map[string]string{"Cookie": ck["Cookie"]}}); resp.StatusCode != 401 {
			t.Fatalf("sessão encerrada: %d", resp.StatusCode)
		}
	})
}

// TestWiringAuthenticatedFirebase troca um ID token (JWKS falso) por sessão no mux real.
func TestWiringAuthenticatedFirebase(t *testing.T) {
	pool := testdb.New(t)
	svc := service.New(pool, noopJobs{}, events.NewBroker())
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kid": "k1", "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	t.Cleanup(jwks.Close)

	cfg := authCfg()
	cfg.Auth = config.Auth{Provider: config.AuthFirebase, Firebase: config.Firebase{ProjectID: "hanko-test", APIKey: "web-key", AuthDomain: "hanko-test.firebaseapp.com"}}
	h, err := buildHandler(handlerDeps{Cfg: cfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool,
		Tokens: auth.RunTokens{Secret: []byte(cfg.Secret), TTL: time.Hour}, JWKSURL: jwks.URL})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	mint := func(uid, email string, verified bool) string {
		now := time.Now()
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": "https://securetoken.google.com/hanko-test", "aud": "hanko-test", "sub": uid, "iat": now.Add(-time.Minute).Unix(),
			"exp": now.Add(time.Hour).Unix(), "auth_time": now.Add(-time.Minute).Unix(), "email": email, "email_verified": verified,
			"name": "Ana Lima", "firebase": map[string]any{"sign_in_provider": "google.com"}})
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	exchange := func(idToken string) (*http.Response, string) {
		return send(t, ts.URL, call{method: "POST", path: "/auth/firebase", body: `{"id_token":"` + idToken + `","next":"/c/acme/"}`,
			hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/json"}})
	}

	if _, body := send(t, ts.URL, call{method: "GET", path: "/entrar"}); !strings.Contains(body, "Continuar com Google") || !strings.Contains(body, `data-api-key="web-key"`) {
		t.Fatalf("tela firebase: %.300s", body)
	}
	if resp, _ := exchange(mint("u1", "ana@exemplo.com", false)); resp.StatusCode != 403 {
		t.Fatalf("e-mail não verificado: %d", resp.StatusCode)
	}
	resp, body := exchange(mint("u1", "ana@exemplo.com", true))
	if resp.StatusCode != 200 || !strings.Contains(body, `"redirect":"/c/acme/"`) {
		t.Fatalf("primeiro login: %d %s", resp.StatusCode, body)
	}
	c := sessionCookie(resp)
	if c == nil {
		t.Fatal("sem cookie")
	}
	if resp, _ := send(t, ts.URL, call{method: "GET", path: "/api/v1/companies", hdr: map[string]string{"Cookie": c.Name + "=" + c.Value}}); resp.StatusCode != 200 {
		t.Fatalf("API com a sessão: %d", resp.StatusCode)
	}
	// sem lista de admissão, só o primeiro usuário entra
	if resp, body := exchange(mint("u2", "bob@exemplo.com", true)); resp.StatusCode != 403 || !strings.Contains(body, "not_admitted") {
		t.Fatalf("segundo usuário sem lista: %d %s", resp.StatusCode, body)
	}
	// token de outro projeto
	bad := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://securetoken.google.com/outro", "aud": "outro", "sub": "x"})
	bad.Header["kid"] = "k1"
	s, _ := bad.SignedString(key)
	if resp, _ := exchange(s); resp.StatusCode != 401 {
		t.Fatalf("projeto errado: %d", resp.StatusCode)
	}
}

// Em URL pública o formulário "Criar conta de dono" não existe sem HANKO_AUTH_WEB_BOOTSTRAP: a tela manda usar a CLI.
func TestWiringOwnerBootstrapNeedsCLIOnPublicURL(t *testing.T) {
	pool := testdb.New(t)
	svc := service.New(pool, noopJobs{}, events.NewBroker())
	cfg := authCfg() // https://hanko.example.test, sem WebBootstrap
	h, err := buildHandler(handlerDeps{Cfg: cfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool,
		Tokens: auth.RunTokens{Secret: []byte(cfg.Secret), TTL: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	_, body := send(t, ts.URL, call{method: "GET", path: "/entrar"})
	if !strings.Contains(body, "hanko auth create-owner") || strings.Contains(body, `action="/entrar/dono"`) {
		t.Fatalf("tela sem formulário de dono esperada: %.400s", body)
	}
	form := "name=Intruso&email=i%40x.com&password=uma-senha-bem-longa"
	resp, _ := send(t, ts.URL, call{method: "POST", path: "/entrar/dono", body: form,
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/x-www-form-urlencoded"}})
	if resp.StatusCode != 404 || sessionCookie(resp) != nil {
		t.Fatalf("POST do dono em URL pública: %d", resp.StatusCode)
	}
	if n, _ := svc.CountUsers(context.Background()); n != 0 {
		t.Fatalf("nenhum usuário pode ter sido criado: %d", n)
	}

	// em loopback o formulário continua
	lcfg := authCfg()
	lcfg.PublicURL = "http://127.0.0.1:7420"
	lcfg.AllowedHosts = []string{"hanko.example.test"}
	lh, err := buildHandler(handlerDeps{Cfg: lcfg, Version: "test", Logger: discard(), Svc: svc, Broker: events.NewBroker(), Pool: pool,
		Tokens: auth.RunTokens{Secret: []byte(cfg.Secret), TTL: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	lts := httptest.NewServer(lh)
	t.Cleanup(lts.Close)
	if _, body := send(t, lts.URL, call{method: "GET", path: "/entrar"}); !strings.Contains(body, `action="/entrar/dono"`) {
		t.Fatal("loopback deveria oferecer o formulário")
	}
}
