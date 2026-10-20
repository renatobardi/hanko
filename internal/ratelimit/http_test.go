package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
)

// memStore implementa Store com o modelo puro.
type memStore struct {
	mu      sync.Mutex
	buckets map[string]State
	err     error
	taken   []string
}

func (m *memStore) TakeRateToken(_ context.Context, key string, p Params, cost float64, now time.Time) (float64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, false, m.err
	}
	if m.buckets == nil {
		m.buckets = map[string]State{}
	}
	m.taken = append(m.taken, key)
	s, ok := m.buckets[key]
	next, allowed := Step(s, !ok, p, cost, now)
	m.buckets[key] = next
	return next.Tokens, allowed, nil
}

func (m *memStore) PurgeRateBuckets(context.Context, time.Time) (int64, error) { return 0, nil }

type limits struct {
	on  bool
	per map[Class]int64
}

func (l limits) Enabled() bool             { return l.on }
func (l limits) PerMinute(c Class) int64   { return l.per[c] }
func newLimits(per map[Class]int64) limits { return limits{on: true, per: per} }

func newMW(t *testing.T, st *memStore, l limits, now *time.Time) http.Handler {
	t.Helper()
	lim, err := New(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	lim.Now = func() time.Time { return *now }
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return Middleware(Options{Limiter: lim, Limits: l, Tokens: &auth.RunTokens{Secret: []byte("segredo-de-teste-com-tamanho"), TTL: time.Hour}})(next)
}

func do(h http.Handler, method, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "203.0.113.7:4444"
	if mod != nil {
		mod(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestClassify(t *testing.T) {
	cases := []struct {
		method, path string
		class        Class
		ok           bool
	}{
		{"GET", "/c/acme/tasks", "", false},
		{"HEAD", "/api/v1/companies", "", false},
		{"POST", "/api/v1/companies/acme/tasks", ClassWrite, true},
		{"DELETE", "/c/acme/tasks/1", ClassWrite, true},
		{"PATCH", "/api/v1/x", ClassWrite, true},
		{"POST", "/mcp", ClassMCP, true},
		{"GET", "/mcp", ClassMCP, true},
		{"POST", "/mcp/run/abc", ClassMCP, true},
		{"POST", "/memory-mcp", ClassMCP, true},
		{"POST", "/hooks/flow/abc", ClassWebhook, true},
		{"POST", "/telemetry/errors", ClassBrowserError, true},
		{"GET", "/telemetry/errors", "", false},
		{"POST", "/entrar", "", false},
		{"POST", "/claim/tok", "", false},
		{"POST", "/convite/tok", "", false},
		{"POST", "/auth/firebase", "", false},
		{"POST", "/sair", "", false},
		{"GET", "/healthz", "", false},
		{"POST", "/static/x", "", false},
		{"GET", "/metrics", "", false},
	}
	for _, c := range cases {
		got, ok := Classify(httptest.NewRequest(c.method, c.path, nil))
		if got != c.class || ok != c.ok {
			t.Errorf("%s %s = (%q,%v), quer (%q,%v)", c.method, c.path, got, ok, c.class, c.ok)
		}
	}
}

func TestMiddlewareLimitsWritesWith429AndRetryAfter(t *testing.T) {
	now := t0
	st := &memStore{}
	h := newMW(t, st, newLimits(map[Class]int64{ClassWrite: 10, ClassIP: 1000}), &now)
	for i := 0; i < 10; i++ {
		if rec := do(h, "POST", "/api/v1/companies/a/tasks", nil); rec.Code != http.StatusNoContent {
			t.Fatalf("requisição %d: %d", i+1, rec.Code)
		}
	}
	rec := do(h, "POST", "/api/v1/companies/a/tasks", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11ª escrita = %d, quer 429", rec.Code)
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 6 {
		t.Errorf("Retry-After = %q (10/min recarrega 1 token em 6 s)", rec.Header().Get("Retry-After"))
	}
	if rec.Header().Get("RateLimit-Remaining") != "0" || rec.Header().Get("RateLimit-Limit") != "10" {
		t.Errorf("RateLimit-* = %q/%q", rec.Header().Get("RateLimit-Limit"), rec.Header().Get("RateLimit-Remaining"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") || !strings.Contains(rec.Body.String(), `"status":429`) {
		t.Errorf("corpo da API deveria ser problem+json: %q %q", ct, rec.Body.String())
	}
	// leituras nunca consomem
	if rec := do(h, "GET", "/api/v1/companies", nil); rec.Code != http.StatusNoContent {
		t.Errorf("GET = %d", rec.Code)
	}
	// depois de esperar o Retry-After, volta a passar
	now = now.Add(time.Duration(secs) * time.Second)
	if rec := do(h, "POST", "/api/v1/companies/a/tasks", nil); rec.Code != http.StatusNoContent {
		t.Errorf("após o Retry-After = %d", rec.Code)
	}
}

func TestMiddlewareUIAndHTMXResponses(t *testing.T) {
	now := t0
	h := newMW(t, &memStore{}, newLimits(map[Class]int64{ClassWrite: 10, ClassIP: 1000}), &now)
	for i := 0; i < 10; i++ {
		do(h, "POST", "/c/a/tasks", nil)
	}
	plain := do(h, "POST", "/c/a/tasks", nil)
	if plain.Code != 429 || !strings.HasPrefix(plain.Header().Get("Content-Type"), "text/plain") || plain.Header().Get("Retry-After") == "" {
		t.Errorf("UI: %d %q", plain.Code, plain.Header().Get("Content-Type"))
	}
	hx := do(h, "POST", "/c/a/tasks", func(r *http.Request) { r.Header.Set("HX-Request", "true"); r.Header.Set("Accept-Language", "en-US") })
	if hx.Code != 429 || hx.Header().Get("HX-Retarget") != "#flash" || !strings.Contains(hx.Body.String(), "Too many requests") {
		t.Errorf("htmx: %d retarget=%q body=%q", hx.Code, hx.Header().Get("HX-Retarget"), hx.Body.String())
	}
}

func TestMiddlewarePerIdentityBucketsAreIndependent(t *testing.T) {
	now := t0
	st := &memStore{}
	h := newMW(t, st, newLimits(map[Class]int64{ClassWrite: 10, ClassIP: 1000}), &now)
	asKey := func(k string) func(*http.Request) {
		return func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+k)
			id := auth.Identity{User: domain.User{ID: domain.NewID(), Email: "u@x.com"}, Via: auth.ViaAPIKey}
			*r = *r.WithContext(auth.WithIdentity(r.Context(), id))
		}
	}
	for i := 0; i < 10; i++ {
		do(h, "POST", "/api/v1/x", asKey("hk_chave-a"))
	}
	if rec := do(h, "POST", "/api/v1/x", asKey("hk_chave-a")); rec.Code != 429 {
		t.Fatalf("chave A esgotada deveria dar 429, deu %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/x", asKey("hk_chave-b")); rec.Code != http.StatusNoContent {
		t.Fatalf("chave B tem bucket próprio, deu %d", rec.Code)
	}
	for _, k := range st.taken {
		if strings.Contains(k, "hk_chave") {
			t.Fatalf("a chave bruta nunca vai para o bucket: %q", k)
		}
	}
}

func TestMiddlewareIPCeilingSumsIdentities(t *testing.T) {
	now := t0
	h := newMW(t, &memStore{}, newLimits(map[Class]int64{ClassWrite: 100, ClassIP: 10}), &now)
	for i := 0; i < 10; i++ {
		id := auth.Identity{User: domain.User{ID: domain.NewID()}, Via: auth.ViaSession}
		rec := do(h, "POST", "/c/a/x", func(r *http.Request) { *r = *r.WithContext(auth.WithIdentity(r.Context(), id)) })
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%d: %d", i, rec.Code)
		}
	}
	id := auth.Identity{User: domain.User{ID: domain.NewID()}, Via: auth.ViaSession}
	rec := do(h, "POST", "/c/a/x", func(r *http.Request) { *r = *r.WithContext(auth.WithIdentity(r.Context(), id)) })
	if rec.Code != 429 {
		t.Fatalf("11 usuários distintos do mesmo IP deveriam estourar o teto por IP, deu %d", rec.Code)
	}
}

func TestMiddlewareRunTokenKeyedByRun(t *testing.T) {
	now := t0
	st := &memStore{}
	h := newMW(t, st, newLimits(map[Class]int64{ClassMCP: 10}), &now)
	tokens := auth.RunTokens{Secret: []byte("segredo-de-teste-com-tamanho"), TTL: time.Hour}
	run1, run2 := domain.NewID(), domain.NewID()
	tok := func(run domain.ID) string {
		s, err := tokens.Issue(domain.NewID(), domain.NewID(), run, domain.NewID())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t1, t2 := tok(run1), tok(run2)
	call := func(token string) int {
		return do(h, "POST", "/mcp", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }).Code
	}
	for i := 0; i < 10; i++ {
		if c := call(t1); c != http.StatusNoContent {
			t.Fatalf("run1 #%d = %d", i, c)
		}
	}
	if c := call(t1); c != 429 {
		t.Fatalf("run1 esgotada = %d", c)
	}
	if c := call(t2); c != http.StatusNoContent {
		t.Fatalf("run2 tem bucket próprio = %d", c)
	}
	// token inválido cai no IP (não cria bucket por token forjado)
	for _, k := range st.taken {
		if strings.Contains(k, "forjado") {
			t.Fatalf("token forjado virou chave: %q", k)
		}
	}
	call("forjado.token.qualquer")
	if last := st.taken[len(st.taken)-1]; !strings.HasPrefix(last, "mcp|ip:") {
		t.Errorf("token inválido deveria usar o IP, usou %q", last)
	}
}

func TestMiddlewareFailOpenAndDisabled(t *testing.T) {
	now := t0
	h := newMW(t, &memStore{err: errors.New("banco fora")}, newLimits(map[Class]int64{ClassWrite: 10, ClassIP: 10}), &now)
	for i := 0; i < 30; i++ {
		if rec := do(h, "POST", "/c/a/x", nil); rec.Code != http.StatusNoContent {
			t.Fatalf("com o banco fora o limitador abre (fail-open), deu %d", rec.Code)
		}
	}
	off := newMW(t, &memStore{}, limits{on: false, per: map[Class]int64{ClassWrite: 1}}, &now)
	for i := 0; i < 5; i++ {
		if rec := do(off, "POST", "/c/a/x", nil); rec.Code != http.StatusNoContent {
			t.Fatalf("desligado nunca limita, deu %d", rec.Code)
		}
	}
	zero := newMW(t, &memStore{}, newLimits(map[Class]int64{}), &now)
	if rec := do(zero, "POST", "/c/a/x", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("teto zero = sem limite, deu %d", rec.Code)
	}
}

func TestBrowserErrorClassIsPerIP(t *testing.T) {
	now := t0
	h := newMW(t, &memStore{}, newLimits(map[Class]int64{ClassBrowserError: 3}), &now)
	for i := 0; i < 3; i++ {
		do(h, "POST", "/telemetry/errors", nil)
	}
	if rec := do(h, "POST", "/telemetry/errors", nil); rec.Code != 429 {
		t.Fatalf("4º relato = %d", rec.Code)
	}
	other := do(h, "POST", "/telemetry/errors", func(r *http.Request) { r.RemoteAddr = "198.51.100.9:1" })
	if other.Code != http.StatusNoContent {
		t.Fatalf("outro IP tem bucket próprio = %d", other.Code)
	}
}
