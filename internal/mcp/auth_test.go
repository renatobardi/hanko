package mcp

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

type testResolver struct{ user domain.User }

func (r testResolver) ResolveSession(_ context.Context, tok string) (domain.User, error) {
	if tok == "sess-ok" {
		return r.user, nil
	}
	return domain.User{}, domain.ErrUnauthenticated
}

func (r testResolver) ResolveAPIKey(_ context.Context, key string) (domain.User, error) {
	if key == "hk_valida" {
		return r.user, nil
	}
	return domain.User{}, domain.ErrUnauthenticated
}

// setupAuthenticated sobe /mcp no modo authenticated: Guard por fora, Trusted desligado.
func setupAuthenticated(t *testing.T) (*env, domain.User) {
	t.Helper()
	e := setup(t, false)
	user := domain.User{ID: domain.NewID(), Email: "ana@x.com", Name: "Ana"}
	h := auth.Guard(testResolver{user}, nil)(newHandler(e.f, Deps{Tokens: e.tokens, Trusted: false, Version: "test"}))
	e.srv.Close()
	// o endpoint de teste é a raiz do servidor; o Guard enxerga o caminho de produção.
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/mcp"
		h.ServeHTTP(w, r2)
	}))
	t.Cleanup(e.srv.Close)
	return e, user
}

func TestAuthenticatedAPIKeyAndRunToken(t *testing.T) {
	e, user := setupAuthenticated(t)

	// API key: ator humano com o ID do usuário, empresa por argumento.
	cs, err := e.connect(t, "hk_valida")
	if err != nil {
		t.Fatalf("API key deveria conectar: %v", err)
	}
	defer cs.Close()
	if res := call(t, cs, "hanko_task_list", map[string]any{}); !res.IsError || !strings.Contains(text(res), "company") {
		t.Fatalf("API key sem company deveria pedir company: %q", text(res))
	}
	res := call(t, cs, "hanko_task_comment", map[string]any{"company": "acme", "key": "HNK-1", "body": "via api key"})
	if res.IsError {
		t.Fatalf("comentar: %s", text(res))
	}
	if a := e.f.lastActor; a.Type != domain.ActorHuman || a.ID == nil || *a.ID != user.ID {
		t.Fatalf("ator da API key: %+v", a)
	}

	// run token continua valendo e continua sendo persona.
	cs2, err := e.connect(t, e.tok)
	if err != nil {
		t.Fatalf("run token deveria conectar: %v", err)
	}
	defer cs2.Close()
	if res := call(t, cs2, "hanko_task_comment", map[string]any{"key": "HNK-1", "body": "via run token"}); res.IsError {
		t.Fatalf("comentar com run token: %s", text(res))
	}
	if e.f.lastActor.Type != domain.ActorPersona {
		t.Fatalf("run token deveria agir como persona: %+v", e.f.lastActor)
	}
}

func TestAuthenticatedRejects(t *testing.T) {
	e, _ := setupAuthenticated(t)
	for name, hdr := range map[string]map[string]string{
		"sem credencial":       nil,
		"API key inválida":     {"Authorization": "Bearer hk_revogada"},
		"cookie de sessão":     {"Cookie": auth.SessionCookie + "=sess-ok"},
		"run token inválido":   {"Authorization": "Bearer lixo"},
		"esquema desconhecido": {"Authorization": "Basic abc"},
	} {
		req, _ := http.NewRequest(http.MethodPost, e.srv.URL, strings.NewReader("{}"))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, esperado 401", name, resp.StatusCode)
		}
	}
}
