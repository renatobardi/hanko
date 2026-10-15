package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
)

// A revalidação do ping não pode renovar a sessão (nem anotar uso da API key): senão um stream aberto manteria
// a credencial viva sozinho. Só as variantes Check* (sem efeito colateral) são usadas.
func TestCredentialCheckUsaVarianteSemEfeitoColateral(t *testing.T) {
	f := &fakeSvc{}
	s := newServer(Deps{})
	s.svc = f

	sess := httptest.NewRequest(http.MethodGet, "/x", nil)
	sess.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "tok"})
	sess = sess.WithContext(auth.WithIdentity(sess.Context(), auth.Identity{Via: auth.ViaSession}))
	key := httptest.NewRequest(http.MethodGet, "/x", nil)
	key.Header.Set("Authorization", "Bearer hk_abc")
	key = key.WithContext(auth.WithIdentity(key.Context(), auth.Identity{Via: auth.ViaAPIKey}))

	for _, r := range []*http.Request{sess, key} {
		chk := s.credentialCheck(r)
		for range 5 {
			if err := chk(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if f.checks.Load() != 10 || f.resolves.Load() != 0 {
		t.Fatalf("checks=%d resolves=%d: o ping deve usar só CheckSession/CheckAPIKey", f.checks.Load(), f.resolves.Load())
	}
}
