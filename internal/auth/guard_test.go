package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

type fakeResolver struct {
	sessions map[string]domain.User
	keys     map[string]domain.User
	err      error
}

func (f fakeResolver) ResolveSession(_ context.Context, tok string) (domain.User, error) {
	if f.err != nil {
		return domain.User{}, f.err
	}
	if u, ok := f.sessions[tok]; ok {
		return u, nil
	}
	return domain.User{}, domain.ErrUnauthenticated
}

func (f fakeResolver) ResolveAPIKey(_ context.Context, key string) (domain.User, error) {
	if f.err != nil {
		return domain.User{}, f.err
	}
	if u, ok := f.keys[key]; ok {
		return u, nil
	}
	return domain.User{}, domain.ErrUnauthenticated
}

func guarded(res Resolver) (http.Handler, *string) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = "ok"
		if id, ok := IdentityFrom(r.Context()); ok {
			seen = string(id.Via) + ":" + id.User.Email
		}
		w.WriteHeader(http.StatusOK)
	})
	return Guard(res, nil)(next), &seen
}

func TestGuardRoutes(t *testing.T) {
	ana := domain.User{ID: domain.NewID(), Email: "ana@x.com", Name: "Ana"}
	res := fakeResolver{
		sessions: map[string]domain.User{"sess1": ana},
		keys:     map[string]domain.User{"hk_good": ana},
	}
	h, seen := guarded(res)

	type tc struct {
		name, method, path string
		hdr                map[string]string
		cookie             string
		remote             string
		want               int
		loc                string // prefixo esperado do Location
		identity           string
	}
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	cases := []tc{
		{name: "healthz livre", method: "GET", path: "/healthz", want: 200},
		{name: "readyz livre", method: "GET", path: "/readyz", want: 200},
		{name: "static livre", method: "GET", path: "/static/hanko.css", want: 200},
		{name: "entrar livre", method: "GET", path: "/entrar", want: 200},
		{name: "entrar/dono livre", method: "POST", path: "/entrar/dono", want: 200},
		{name: "auth/firebase livre", method: "POST", path: "/auth/firebase", want: 200},
		{name: "sair livre", method: "POST", path: "/sair", want: 200},
		{name: "convite livre (GET)", method: "GET", path: "/convite/abc", want: 200},
		{name: "convite livre (POST)", method: "POST", path: "/convite/abc", want: 200},
		{name: "idioma livre", method: "POST", path: "/idioma", want: 200},

		{name: "UI sem sessão redireciona", method: "GET", path: "/c/acme/tasks?status=todo", want: 303, loc: "/entrar?next=%2Fc%2Facme%2Ftasks%3Fstatus%3Dtodo"},
		{name: "raiz sem sessão redireciona", method: "GET", path: "/", want: 303, loc: "/entrar"},
		{name: "UI POST sem sessão redireciona sem next", method: "POST", path: "/c/acme/tasks/nova", want: 303, loc: "/entrar"},
		{name: "UI htmx sem sessão", method: "POST", path: "/c/acme/tasks/nova", hdr: map[string]string{"HX-Request": "true"}, want: 401},
		{name: "UI com sessão", method: "GET", path: "/c/acme/tasks", cookie: "sess1", want: 200, identity: "session:ana@x.com"},
		{name: "UI com cookie velho", method: "GET", path: "/c/acme/tasks", cookie: "velho", want: 303, loc: "/entrar?next=%2Fc%2Facme%2Ftasks"},
		{name: "UI ignora API key", method: "GET", path: "/c/acme/tasks", hdr: map[string]string{"Authorization": "Bearer hk_good"}, want: 303, loc: "/entrar?next=%2Fc%2Facme%2Ftasks"},

		{name: "API sem credencial", method: "GET", path: "/api/v1/companies", want: 401},
		{name: "API com sessão", method: "GET", path: "/api/v1/companies", cookie: "sess1", want: 200, identity: "session:ana@x.com"},
		{name: "API sessão POST mesma origem", method: "POST", path: "/api/v1/companies", cookie: "sess1", hdr: same, want: 200},
		{name: "API sessão POST cross-site", method: "POST", path: "/api/v1/companies", cookie: "sess1", hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}, want: 403},
		{name: "API sessão POST sem origem", method: "POST", path: "/api/v1/companies", cookie: "sess1", want: 403},
		{name: "API com API key", method: "POST", path: "/api/v1/companies", hdr: map[string]string{"Authorization": "Bearer hk_good"}, want: 200, identity: "api_key:ana@x.com"},
		{name: "API com API key inválida", method: "GET", path: "/api/v1/companies", hdr: map[string]string{"Authorization": "Bearer hk_ruim"}, want: 401},
		{name: "API key inválida vence cookie válido", method: "GET", path: "/api/v1/companies", cookie: "sess1", hdr: map[string]string{"Authorization": "Bearer hk_ruim"}, want: 401},
		{name: "API com run token passa para a API validar", method: "GET", path: "/api/v1/companies", hdr: map[string]string{"Authorization": "Bearer eyJ.run.token"}, want: 200, identity: "ok"},
		{name: "API Authorization presente ignora cookie", method: "GET", path: "/api/v1/companies", cookie: "sess1", hdr: map[string]string{"Authorization": "Basic xyz"}, want: 401},

		{name: "MCP sem credencial", method: "POST", path: "/mcp", want: 401},
		{name: "MCP com cookie não vale", method: "POST", path: "/mcp", cookie: "sess1", want: 401},
		{name: "MCP com API key", method: "POST", path: "/mcp", hdr: map[string]string{"Authorization": "Bearer hk_good"}, want: 200, identity: "api_key:ana@x.com"},
		{name: "MCP com run token", method: "POST", path: "/mcp", hdr: map[string]string{"Authorization": "Bearer eyJ.run.token"}, want: 200},
		{name: "memória sem credencial", method: "POST", path: "/memory-mcp", want: 401},
		{name: "memória com run token", method: "POST", path: "/memory-mcp", hdr: map[string]string{"Authorization": "Bearer eyJ.run.token"}, want: 200},

		{name: "metrics em loopback direto", method: "GET", path: "/metrics", remote: "127.0.0.1:4000", want: 200},
		{name: "metrics em loopback atrás de proxy", method: "GET", path: "/metrics", remote: "127.0.0.1:4000", hdr: map[string]string{"X-Forwarded-For": "8.8.8.8"}, want: 401},
		{name: "metrics remoto sem chave", method: "GET", path: "/metrics", remote: "203.0.113.5:4000", want: 401},
		{name: "metrics remoto com API key", method: "GET", path: "/metrics", remote: "203.0.113.5:4000", hdr: map[string]string{"Authorization": "Bearer hk_good"}, want: 200},
		{name: "metrics remoto com cookie não vale", method: "GET", path: "/metrics", remote: "203.0.113.5:4000", cookie: "sess1", want: 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			*seen = ""
			r := httptest.NewRequest(c.method, c.path, strings.NewReader(""))
			if c.remote != "" {
				r.RemoteAddr = c.remote
			}
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			if c.cookie != "" {
				r.AddCookie(&http.Cookie{Name: SessionCookie, Value: c.cookie})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("status %d, esperado %d (%s)", w.Code, c.want, w.Body.String())
			}
			if c.loc != "" && w.Header().Get("Location") != c.loc {
				t.Fatalf("Location %q, esperado %q", w.Header().Get("Location"), c.loc)
			}
			if c.want == 401 && strings.HasPrefix(c.path, "/api/") && !strings.Contains(w.Header().Get("Content-Type"), "problem+json") {
				t.Fatalf("API 401 deveria ser problem+json: %q", w.Header().Get("Content-Type"))
			}
			if c.identity != "" && *seen != c.identity {
				t.Fatalf("identidade %q, esperada %q", *seen, c.identity)
			}
		})
	}
	if w := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/c/acme/x", nil)
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}(); w.Header().Get("HX-Redirect") != "/entrar" {
		t.Fatalf("htmx deveria receber HX-Redirect, veio %q", w.Header().Get("HX-Redirect"))
	}
}

func TestGuardResolverFailureIs503(t *testing.T) {
	h, _ := guarded(fakeResolver{err: errors.New("banco fora")})
	for _, c := range []struct {
		path, hdr, cookie string
	}{{"/c/x", "", "s"}, {"/api/v1/x", "Bearer hk_a", ""}} {
		r := httptest.NewRequest("GET", c.path, nil)
		if c.hdr != "" {
			r.Header.Set("Authorization", c.hdr)
		}
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: c.cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, esperado 503 (falha de infra não vira 401/redirect)", c.path, w.Code)
		}
	}
}
