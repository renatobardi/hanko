package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	tok, hash, err := NewSessionToken()
	if err != nil || len(tok) < 43 || HashToken(tok) != hash || len(hash) != 64 {
		t.Fatalf("token de sessão: %q %q %v", tok, hash, err)
	}
	tok2, _, _ := NewSessionToken()
	if tok == tok2 {
		t.Fatal("tokens devem ser únicos")
	}
	key, prefix, kh, err := NewAPIKey()
	if err != nil || !IsAPIKey(key) || !strings.HasPrefix(key, prefix) || len(prefix) != len(APIKeyPrefix)+6 || HashToken(key) != kh {
		t.Fatalf("api key: %q %q %v", key, prefix, err)
	}
	if strings.Contains(kh, key) {
		t.Fatal("hash não pode conter a chave")
	}
	if IsAPIKey("eyJhbGciOi...") {
		t.Fatal("JWT não é API key")
	}
}

func TestBearerToken(t *testing.T) {
	for in, want := range map[string]string{"Bearer abc": "abc", "bearer  abc ": "abc", "Basic abc": "", "Bearer": "", "": "", "Bearer ": ""} {
		got, ok := BearerToken(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q: got %q,%v", in, got, ok)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"": "/", "/c/hanko/tasks?x=1": "/c/hanko/tasks?x=1", "//evil.com": "/", "https://evil.com": "/", "/\\evil.com": "/",
		"evil.com": "/", "/entrar": "/", "/entrar?next=/x": "/", "/a\r\nb": "/", "/ok#frag": "/ok#frag",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsDirectLoopback(t *testing.T) {
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	if !IsDirectLoopback(mk("127.0.0.1:1", "")) || IsDirectLoopback(mk("127.0.0.1:1", "8.8.8.8")) || IsDirectLoopback(mk("10.0.0.1:1", "")) {
		t.Error("IsDirectLoopback")
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(h map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "http://hanko.test/x", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		h    map[string]string
		want bool
	}{
		{map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{map[string]string{"Origin": "http://hanko.test"}, true},
		{map[string]string{"Origin": "https://evil.test"}, false},
		{map[string]string{"HX-Request": "true"}, false}, // estrito: sem atalho do htmx
		{nil, false},
	}
	for _, c := range cases {
		if got := SameOrigin(mk(c.h)); got != c.want {
			t.Errorf("%v: got %v", c.h, got)
		}
	}
}

func TestNewInviteToken(t *testing.T) {
	a, ha, err := NewInviteToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewInviteToken()
	if a == b || len(a) < 43 || ha != HashToken(a) || ha == a {
		t.Fatalf("token %q hash %q", a, ha)
	}
	if InviteTTL != 7*24*time.Hour {
		t.Fatalf("validade: %v", InviteTTL)
	}
}
