package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// scopedResolver é um fakeResolver que também informa o escopo da chave (auth.KeyResolver).
type scopedResolver struct {
	fakeResolver
	scope map[string]domain.KeyScope
}

func (s scopedResolver) ResolveAPIKeyInfo(ctx context.Context, key string) (domain.User, KeyInfo, error) {
	u, err := s.ResolveAPIKey(ctx, key)
	return u, KeyInfo{ID: domain.NewID(), Scope: s.scope[key]}, err
}

func TestGuardKeyScope(t *testing.T) {
	ana := domain.User{ID: domain.NewID(), Email: "ana@x.com"}
	res := scopedResolver{
		fakeResolver: fakeResolver{sessions: map[string]domain.User{"sess": ana}, keys: map[string]domain.User{"hk_read": ana, "hk_full": ana}},
		scope:        map[string]domain.KeyScope{"hk_read": domain.KeyScopeRead, "hk_full": domain.KeyScopeFull},
	}
	h, _ := guarded(res)
	do := func(method, path, key string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, c := range []struct {
		method, path, key string
		want              int
	}{
		{http.MethodGet, "/api/v1/companies", "hk_read", 200},
		{http.MethodPost, "/api/v1/companies", "hk_read", 401},
		{http.MethodDelete, "/api/v1/companies/x", "hk_read", 401},
		{http.MethodPost, "/mcp", "hk_read", 401},
		{http.MethodPost, "/api/v1/companies", "hk_full", 200},
		{http.MethodPost, "/mcp", "hk_full", 200},
	} {
		if got := do(c.method, c.path, c.key); got != c.want {
			t.Errorf("%s %s com %s = %d, quer %d", c.method, c.path, c.key, got, c.want)
		}
	}
	// a identidade carrega o id e o escopo da chave
	var got Identity
	capture := Guard(res, nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got, _ = IdentityFrom(r.Context()) }))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer hk_read")
	capture.ServeHTTP(httptest.NewRecorder(), req)
	if got.KeyID == nil || got.KeyScope != domain.KeyScopeRead || got.Via != ViaAPIKey {
		t.Errorf("identidade: %+v", got)
	}
	// resolver sem KeyResolver: a chave vale como full
	plain, _ := guarded(fakeResolver{keys: map[string]domain.User{"hk_old": ana}})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/companies", nil)
	req.Header.Set("Authorization", "Bearer hk_old")
	rec := httptest.NewRecorder()
	plain.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("resolver simples: %d", rec.Code)
	}
}

func TestGuardPublicPaths(t *testing.T) {
	h, _ := guarded(fakeResolver{})
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/v1/cli-auth", 200},
		{http.MethodPost, "/api/v1/cli-auth/poll", 200},
		{http.MethodGet, "/api/v1/cli-auth/outra", 401}, // só os dois endpoints são públicos
		{http.MethodGet, "/pedir-acesso/abc", 200},
		{http.MethodGet, "/cli-auth/abc", 303}, // a página de autorização exige sessão
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, quer %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestRedactPathSegredos(t *testing.T) {
	for in, want := range map[string]string{
		"/convite/abc":        "/convite/…",
		"/cli-auth/abc":       "/cli-auth/…",
		"/pedir-acesso/xy":    "/pedir-acesso/…",
		"/api/v1/x":           "/api/v1/x",
		"/hooks/channels/T0k": "/hooks/channels/…",
		"/hooks/flows/abc":    "/hooks/flows/abc",
	} {
		if got := RedactPath(in); got != want {
			t.Errorf("RedactPath(%q) = %q, quer %q", in, got, want)
		}
	}
	code, hash, err := NewCLIAuthCode()
	if err != nil || len(code) != 12 || hash != HashToken(code) {
		t.Errorf("NewCLIAuthCode: %q %q %v", code, hash, err)
	}
}
