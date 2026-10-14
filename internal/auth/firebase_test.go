package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testProject = "hanko-test"

type jwksServer struct {
	*httptest.Server
	mu      sync.Mutex
	keys    map[string]*rsa.PrivateKey
	fetches atomic.Int32
	status  int
	cc      string
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newJWKS(t *testing.T, keys map[string]*rsa.PrivateKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: keys, status: 200, cc: "public, max-age=3600"}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.fetches.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.status != 200 {
			http.Error(w, "x", s.status)
			return
		}
		type jwk struct{ Kid, Kty, Alg, Use, N, E string }
		var doc struct{ Keys []jwk }
		for kid, k := range s.keys {
			doc.Keys = append(doc.Keys, jwk{kid, "RSA", "RS256", "sig",
				base64.RawURLEncoding.EncodeToString(k.N.Bytes()), base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())})
		}
		w.Header().Set("Cache-Control", s.cc)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) set(kid string, k *rsa.PrivateKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[kid] = k
}

var fixedNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type claimsOpt func(jwt.MapClaims)

func mint(t *testing.T, k *rsa.PrivateKey, kid string, opts ...claimsOpt) string {
	t.Helper()
	c := jwt.MapClaims{
		"iss": "https://securetoken.google.com/" + testProject, "aud": testProject, "sub": "uid-123",
		"iat": fixedNow.Add(-time.Minute).Unix(), "exp": fixedNow.Add(59 * time.Minute).Unix(), "auth_time": fixedNow.Add(-time.Minute).Unix(),
		"email": "Ana@Exemplo.com", "email_verified": true, "name": "Ana",
		"firebase": map[string]any{"sign_in_provider": "password"},
	}
	for _, o := range opts {
		o(c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func set(k string, v any) claimsOpt { return func(c jwt.MapClaims) { c[k] = v } }
func del(k string) claimsOpt        { return func(c jwt.MapClaims) { delete(c, k) } }

func newVerifier(srv *jwksServer, now *time.Time) *FirebaseVerifier {
	return &FirebaseVerifier{ProjectID: testProject, JWKSURL: srv.URL, Now: func() time.Time { return *now }, MinRefresh: -1}
}

func TestFirebaseVerify(t *testing.T) {
	key := newKey(t)
	other := newKey(t)
	srv := newJWKS(t, map[string]*rsa.PrivateKey{"k1": key})
	now := fixedNow
	v := newVerifier(srv, &now)
	ctx := context.Background()

	t.Run("válido", func(t *testing.T) {
		c, err := v.Verify(ctx, mint(t, key, "k1"))
		if err != nil {
			t.Fatal(err)
		}
		if c.UID != "uid-123" || c.Email != "Ana@Exemplo.com" || c.Name != "Ana" || c.SignInMethod != "password" || !c.EmailVerified {
			t.Fatalf("claims: %+v", c)
		}
	})

	bad := map[string]string{
		"aud errado":                mint(t, key, "k1", set("aud", "outro-projeto")),
		"iss errado":                mint(t, key, "k1", set("iss", "https://securetoken.google.com/outro")),
		"iss de outro emissor":      mint(t, key, "k1", set("iss", "https://accounts.google.com")),
		"expirado":                  mint(t, key, "k1", set("exp", fixedNow.Add(-2*time.Minute).Unix())),
		"sem exp":                   mint(t, key, "k1", del("exp")),
		"iat no futuro":             mint(t, key, "k1", set("iat", fixedNow.Add(time.Hour).Unix())),
		"sem iat":                   mint(t, key, "k1", del("iat")),
		"sem sub":                   mint(t, key, "k1", set("sub", "")),
		"sub ausente":               mint(t, key, "k1", del("sub")),
		"sem auth_time":             mint(t, key, "k1", del("auth_time")),
		"auth_time futuro":          mint(t, key, "k1", set("auth_time", fixedNow.Add(time.Hour).Unix())),
		"sem e-mail":                mint(t, key, "k1", del("email")),
		"assinatura de outra chave": mint(t, other, "k1"),
		"sem kid":                   mint(t, key, ""),
		"lixo":                      "não.é.jwt",
	}
	for name, tok := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(ctx, tok); !errors.Is(err, ErrInvalidIDToken) {
				t.Fatalf("esperava ErrInvalidIDToken, veio %v", err)
			}
		})
	}

	t.Run("alg none", func(t *testing.T) {
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"iss": "https://securetoken.google.com/" + testProject, "aud": testProject, "sub": "x",
			"exp": fixedNow.Add(time.Hour).Unix(), "iat": fixedNow.Unix(), "auth_time": fixedNow.Unix(), "email": "a@b.c", "email_verified": true})
		tok.Header["kid"] = "k1"
		s, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if _, err := v.Verify(ctx, s); !errors.Is(err, ErrInvalidIDToken) {
			t.Fatalf("alg none deve ser recusado: %v", err)
		}
	})

	t.Run("alg HS256 com a chave pública como segredo", func(t *testing.T) {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "https://securetoken.google.com/" + testProject, "aud": testProject, "sub": "x",
			"exp": fixedNow.Add(time.Hour).Unix(), "iat": fixedNow.Unix(), "auth_time": fixedNow.Unix(), "email": "a@b.c", "email_verified": true})
		tok.Header["kid"] = "k1"
		s, _ := tok.SignedString(key.N.Bytes())
		if _, err := v.Verify(ctx, s); !errors.Is(err, ErrInvalidIDToken) {
			t.Fatalf("HS256 deve ser recusado: %v", err)
		}
	})

	t.Run("e-mail não verificado", func(t *testing.T) {
		_, err := v.Verify(ctx, mint(t, key, "k1", set("email_verified", false)))
		if !errors.Is(err, ErrUnverifiedEmail) {
			t.Fatalf("esperava ErrUnverifiedEmail, veio %v", err)
		}
		if errors.Is(err, ErrInvalidIDToken) {
			t.Fatal("e-mail não verificado não é token inválido")
		}
	})

	t.Run("projeto não configurado", func(t *testing.T) {
		if _, err := (&FirebaseVerifier{JWKSURL: srv.URL}).Verify(ctx, mint(t, key, "k1")); !errors.Is(err, ErrInvalidIDToken) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestFirebaseKeyCacheAndRefresh(t *testing.T) {
	k1, k2 := newKey(t), newKey(t)
	srv := newJWKS(t, map[string]*rsa.PrivateKey{"k1": k1})
	now := fixedNow
	v := newVerifier(srv, &now)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := v.Verify(ctx, mint(t, k1, "k1")); err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("cache deveria evitar buscas repetidas: %d", n)
	}

	// kid desconhecido: força refresh e passa a valer quando o Google já publicou a chave nova.
	srv.set("k2", k2)
	if _, err := v.Verify(ctx, mint(t, k2, "k2")); err != nil {
		t.Fatalf("kid novo deveria validar após refresh: %v", err)
	}
	if n := srv.fetches.Load(); n != 2 {
		t.Fatalf("esperava 1 refresh por kid desconhecido: %d", n)
	}

	// kid que nem o Google conhece: um refresh e erro.
	if _, err := v.Verify(ctx, mint(t, newKey(t), "k9")); !errors.Is(err, ErrInvalidIDToken) {
		t.Fatalf("kid inexistente: %v", err)
	}
	if n := srv.fetches.Load(); n != 3 {
		t.Fatalf("esperava mais um refresh: %d", n)
	}

	// max-age: dentro da validade não busca; depois de vencer, busca de novo.
	now = now.Add(59 * time.Minute)
	before := srv.fetches.Load()
	if _, err := v.Verify(ctx, mint(t, k1, "k1", set("iat", now.Add(-time.Minute).Unix()), set("exp", now.Add(time.Hour).Unix()), set("auth_time", now.Add(-time.Minute).Unix()))); err != nil {
		t.Fatal(err)
	}
	if srv.fetches.Load() != before {
		t.Fatal("não deveria buscar antes do max-age")
	}
	now = now.Add(2 * time.Minute)
	if _, err := v.Verify(ctx, mint(t, k1, "k1", set("iat", now.Add(-time.Minute).Unix()), set("exp", now.Add(time.Hour).Unix()), set("auth_time", now.Add(-time.Minute).Unix()))); err != nil {
		t.Fatal(err)
	}
	if srv.fetches.Load() != before+1 {
		t.Fatal("deveria buscar após o max-age")
	}
}

func TestFirebaseRefreshThrottle(t *testing.T) {
	k1 := newKey(t)
	srv := newJWKS(t, map[string]*rsa.PrivateKey{"k1": k1})
	now := fixedNow
	v := &FirebaseVerifier{ProjectID: testProject, JWKSURL: srv.URL, Now: func() time.Time { return now }} // MinRefresh padrão (10 s)
	ctx := context.Background()
	if _, err := v.Verify(ctx, mint(t, k1, "k1")); err != nil {
		t.Fatal(err)
	}
	forged := mint(t, newKey(t), "kx")
	for i := 0; i < 5; i++ {
		_, _ = v.Verify(ctx, forged)
	}
	if n := srv.fetches.Load(); n != 1 {
		t.Fatalf("tokens com kid forjado não podem virar torrente de buscas: %d", n)
	}
	now = now.Add(11 * time.Second)
	_, _ = v.Verify(ctx, forged)
	if n := srv.fetches.Load(); n != 2 {
		t.Fatalf("após o intervalo mínimo, um refresh: %d", n)
	}
}

func TestFirebaseJWKSUnavailable(t *testing.T) {
	k1 := newKey(t)
	srv := newJWKS(t, map[string]*rsa.PrivateKey{"k1": k1})
	srv.status = 500
	now := fixedNow
	v := newVerifier(srv, &now)
	_, err := v.Verify(context.Background(), mint(t, k1, "k1"))
	if !errors.Is(err, ErrJWKSUnavailable) || errors.Is(err, ErrInvalidIDToken) {
		t.Fatalf("esperava ErrJWKSUnavailable, veio %v", err)
	}
	srv.status = 200
	if _, err := v.Verify(context.Background(), mint(t, k1, "k1")); err != nil {
		t.Fatalf("deveria se recuperar quando o Google volta: %v", err)
	}
}

func TestMaxAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": time.Hour, "public, max-age=19845, must-revalidate": 19845 * time.Second, "max-age=5": time.Minute,
		"max-age=999999999": 24 * time.Hour, "no-store": time.Minute, "max-age=abc": time.Hour,
	} {
		if got := maxAge(in); got != want {
			t.Errorf("maxAge(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAdmission(t *testing.T) {
	a := Admission{Emails: []string{"Ana@Exemplo.com"}, Domains: []string{"@empresa.com"}}
	for email, want := range map[string]bool{
		"ana@exemplo.com": true, "ANA@EXEMPLO.COM": true, "bob@empresa.com": true, "bob@sub.empresa.com": false,
		"bob@outra.com": false, "eve@empresa.com.evil.com": false, "": false, "semarroba": false, "@empresa.com": false,
	} {
		if got := a.Allows(email); got != want {
			t.Errorf("Allows(%q) = %v, want %v", email, got, want)
		}
	}
	if (Admission{}).Configured() || !a.Configured() {
		t.Error("Configured")
	}
}
