package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type keyResolver struct {
	user domain.User
}

func (r keyResolver) ResolveSession(_ context.Context, tok string) (domain.User, error) {
	if tok == "sess-ok" {
		return r.user, nil
	}
	return domain.User{}, domain.ErrUnauthenticated
}

func (r keyResolver) ResolveAPIKey(_ context.Context, key string) (domain.User, error) {
	if key == "hk_valida" {
		return r.user, nil
	}
	return domain.User{}, domain.ErrUnauthenticated // inclui chave revogada
}

// newAuthServer monta a API em modo authenticated, com o Guard por fora (como no app).
func newAuthServer(t *testing.T, f *fakeSvc, withGuard bool) (*httptest.Server, domain.User, *auth.RunTokens) {
	t.Helper()
	user := domain.User{ID: domain.NewID(), Email: "ana@x.com", Name: "Ana", Role: domain.RoleOwner}
	tokens := &auth.RunTokens{Secret: []byte("0123456789abcdef0123456789abcdef"), TTL: time.Hour}
	s := newServer(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test", Tokens: tokens, Authenticated: true})
	s.svc = f
	mux := http.NewServeMux()
	s.mount(mux)
	var h http.Handler = mux
	if withGuard {
		h = auth.Guard(keyResolver{user}, nil)(mux)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, user, tokens
}

func TestModoAuthenticatedAPI(t *testing.T) {
	f := &fakeSvc{}
	c := seedCompany(f)
	ts, user, tokens := newAuthServer(t, f, true)
	base := ts.URL + "/api/v1/companies"
	same := map[string]string{"Sec-Fetch-Site": "same-origin", "Cookie": auth.SessionCookie + "=sess-ok"}

	t.Run("sem credencial é 401", func(t *testing.T) {
		resp, body := do(t, "GET", base, "", nil)
		if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("Content-Type"), "problem+json") {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
	})
	t.Run("sondas e docs", func(t *testing.T) {
		if resp, _ := do(t, "GET", ts.URL+"/healthz", "", nil); resp.StatusCode != 200 {
			t.Fatalf("healthz: %d", resp.StatusCode)
		}
		if resp, _ := do(t, "GET", ts.URL+"/readyz", "", nil); resp.StatusCode != 200 {
			t.Fatalf("readyz: %d", resp.StatusCode)
		}
	})
	t.Run("API key: ator humano com ID do usuário", func(t *testing.T) {
		resp, body := do(t, "POST", base, `{"name":"Nova Co"}`, map[string]string{"Authorization": "Bearer hk_valida"})
		if resp.StatusCode != 201 {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
		if f.lastActor.Type != domain.ActorHuman || f.lastActor.ID == nil || *f.lastActor.ID != user.ID {
			t.Fatalf("ator: %+v", f.lastActor)
		}
	})
	t.Run("API key inválida ou revogada", func(t *testing.T) {
		if resp, _ := do(t, "GET", base, "", map[string]string{"Authorization": "Bearer hk_revogada"}); resp.StatusCode != 401 {
			t.Fatalf("%d", resp.StatusCode)
		}
	})
	t.Run("sessão por cookie", func(t *testing.T) {
		if resp, body := do(t, "GET", base+"/acme", "", map[string]string{"Cookie": same["Cookie"]}); resp.StatusCode != 200 {
			t.Fatalf("GET: %d %s", resp.StatusCode, body)
		}
		resp, body := do(t, "POST", base, `{"name":"Via Sessão"}`, same)
		if resp.StatusCode != 201 || f.lastActor.ID == nil || *f.lastActor.ID != user.ID {
			t.Fatalf("POST mesma origem: %d %s %+v", resp.StatusCode, body, f.lastActor)
		}
		resp, _ = do(t, "POST", base, `{"name":"CSRF"}`, map[string]string{"Cookie": same["Cookie"], "Sec-Fetch-Site": "cross-site"})
		if resp.StatusCode != 403 {
			t.Fatalf("POST cross-site com cookie: %d", resp.StatusCode)
		}
		resp, _ = do(t, "POST", base, `{"name":"CSRF"}`, map[string]string{"Cookie": same["Cookie"]})
		if resp.StatusCode != 403 {
			t.Fatalf("POST sem origem com cookie: %d", resp.StatusCode)
		}
		if resp, _ := do(t, "GET", base, "", map[string]string{"Cookie": auth.SessionCookie + "=velho"}); resp.StatusCode != 401 {
			t.Fatalf("cookie desconhecido: %d", resp.StatusCode)
		}
	})
	t.Run("run token continua valendo", func(t *testing.T) {
		rid, pid := domain.NewID(), domain.NewID()
		f.runs = map[domain.ID]service.RunView{rid: {Run: domain.Run{ID: rid, CompanyID: c.ID, Status: domain.RunRunning}}}
		tok, _ := tokens.Issue(c.ID, pid, rid, domain.NewID())
		if resp, body := do(t, "GET", base+"/acme", "", map[string]string{"Authorization": "Bearer " + tok}); resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
		f.setStatus(rid, domain.RunDone)
		if resp, _ := do(t, "GET", base+"/acme", "", map[string]string{"Authorization": "Bearer " + tok}); resp.StatusCode != 401 {
			t.Fatalf("run encerrado: %d", resp.StatusCode)
		}
	})
	t.Run("Authorization lixo", func(t *testing.T) {
		if resp, _ := do(t, "GET", base, "", map[string]string{"Authorization": "Basic abc"}); resp.StatusCode != 401 {
			t.Fatalf("%d", resp.StatusCode)
		}
	})
}

func TestModoAuthenticatedSemGuardNegaAnonimo(t *testing.T) {
	// defesa em profundidade: mesmo sem o Guard por fora, a API não assume o humano local.
	f := &fakeSvc{}
	seedCompany(f)
	ts, _, _ := newAuthServer(t, f, false)
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/companies", "", nil); resp.StatusCode != 401 {
		t.Fatalf("anônimo sem Guard: %d", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/companies", "", map[string]string{"Authorization": "Bearer hk_valida"}); resp.StatusCode != 401 {
		t.Fatalf("hk_ sem Guard não é run token: %d", resp.StatusCode)
	}
}

func TestModoTrustedInalterado(t *testing.T) {
	f := &fakeSvc{}
	seedCompany(f)
	ts, _ := newTestServer(t, f, nil)
	if resp, _ := do(t, "GET", ts.URL+"/api/v1/companies", "", nil); resp.StatusCode != 200 {
		t.Fatalf("trusted sem credencial: %d", resp.StatusCode)
	}
}
