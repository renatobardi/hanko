package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
)

// deadlineRecorder registra o prazo de leitura que o handler pede ao servidor.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	read time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error { d.read = t; return nil }

// O padrão do huma (5 s) derrubaria com 408 o upload de um arquivo grande: o import pede um prazo próprio.
func TestImportPedePrazoDeLeituraProprio(t *testing.T) {
	s := newServer(Deps{Svc: nil})
	s.svc = &fakeSvc{}
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodPost, importPath, strings.NewReader(`{"format":"hanko.company.v1","company":{"name":"x"}}`))
	s.handleImport(rec, req)
	if rec.read.IsZero() {
		t.Fatal("o handler não definiu o prazo de leitura do corpo")
	}
	if d := time.Until(rec.read); d < time.Minute || d > importReadTimeout+time.Second {
		t.Fatalf("prazo de leitura = %v, esperava cerca de %v", d, importReadTimeout)
	}
}

// credentialCheck reverifica a credencial que abriu o stream (sessão ou API key); sem identidade, nada.
func TestCredentialCheckPorVia(t *testing.T) {
	f := &fakeSvc{failWith: domain.ErrUnauthenticated}
	s := newServer(Deps{})
	s.svc = f

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if s.credentialCheck(req) != nil {
		t.Fatal("sem identidade (modo trusted) não há o que reverificar")
	}

	sess := httptest.NewRequest(http.MethodGet, "/x", nil)
	sess.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "tok"})
	sess = sess.WithContext(auth.WithIdentity(sess.Context(), auth.Identity{Via: auth.ViaSession}))
	chk := s.credentialCheck(sess)
	if chk == nil || chk(context.Background()) != domain.ErrUnauthenticated {
		t.Fatal("sessão encerrada deveria falhar com ErrUnauthenticated")
	}

	key := httptest.NewRequest(http.MethodGet, "/x", nil)
	key.Header.Set("Authorization", "Bearer hk_abc")
	key = key.WithContext(auth.WithIdentity(key.Context(), auth.Identity{Via: auth.ViaAPIKey}))
	chk = s.credentialCheck(key)
	if chk == nil || chk(context.Background()) != domain.ErrUnauthenticated {
		t.Fatal("API key revogada deveria falhar com ErrUnauthenticated")
	}
}
