package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// Claim do primeiro dono (ADR-0058 §A): o link vale uma vez, não distingue "usado" de "inexistente" e não vaza por Referer.

func TestClaimFlowCreatesOwnerOnce(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.claims["tok-bom"] = true

	page := e.get(t, "/claim/tok-bom")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, `action="/claim/tok-bom"`, `data-testid="claim-form"`, `name="password"`)
	if got := page.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := page.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}

	// senha fraca não queima o link
	weak := e.do(t, req{method: "POST", path: "/claim/tok-bom", form: url.Values{"name": {"Dona"}, "email": {"dona@x.com"}, "password": {"curta"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, weak, http.StatusUnprocessableEntity)
	if len(e.fa.users) != 0 {
		t.Fatal("senha fraca criou usuário")
	}
	wantStatus(t, e.get(t, "/claim/tok-bom"), http.StatusOK)

	form := url.Values{"name": {"Dona"}, "email": {"dona@x.com"}, "password": {"senha-bem-longa-123"}}
	ok := e.do(t, req{method: "POST", path: "/claim/tok-bom", form: form, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, ok, http.StatusSeeOther)
	if loc := ok.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q", loc)
	}
	if cookieOf(ok) == nil {
		t.Fatal("o claim deve abrir a sessão do dono")
	}
	if len(e.fa.users) != 1 || e.fa.users[0].Role != domain.RoleOwner {
		t.Fatalf("usuários = %+v", e.fa.users)
	}

	// o link se invalida: GET e POST respondem o mesmo 404 genérico
	wantStatus(t, e.get(t, "/claim/tok-bom"), http.StatusNotFound)
	again := e.do(t, req{method: "POST", path: "/claim/tok-bom", form: form, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, again, http.StatusNotFound)
	wantContains(t, again, "Link inválido ou expirado")
	if len(e.fa.users) != 1 {
		t.Fatal("segundo uso do link criou outro usuário")
	}
}

func TestClaimUnknownTokenAndOrigin(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.claims["tok-bom"] = true
	rec := e.get(t, "/claim/outro")
	wantStatus(t, rec, http.StatusNotFound)
	wantContains(t, rec, "Link inválido ou expirado", `data-testid="claim-invalid"`)
	wantNotContains(t, rec, "outro</")

	// POST de outra origem é barrado antes de qualquer coisa
	form := url.Values{"name": {"X"}, "email": {"x@x.com"}, "password": {"senha-bem-longa-123"}}
	cross := e.do(t, req{method: "POST", path: "/claim/tok-bom", form: form, hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	if cross.Code < 400 || len(e.fa.users) != 0 {
		t.Fatalf("POST cross-site: %d, usuários=%d", cross.Code, len(e.fa.users))
	}
}

func TestClaimLimiterBlocksGuessing(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	var last int
	for i := 0; i < 40; i++ {
		last = e.get(t, "/claim/chute-"+strings.Repeat("x", i%3)+string(rune('a'+i%26))).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("depois de muitos chutes o IP deveria tomar 429, veio %d", last)
	}
}

func TestClaimOnlyLocalProvider(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "firebase"})
	e.fa.claims["tok"] = true
	wantStatus(t, e.get(t, "/claim/tok"), http.StatusNotFound)
}
