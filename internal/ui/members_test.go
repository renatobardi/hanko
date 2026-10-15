package ui

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

const pass10 = "uma-senha-longa"

// signedIn abre uma sessão do usuário e devolve o cookie; seeds members with `me` as the given role.
func membersEnv(t *testing.T, me domain.MemberRole) (*authEnv, *http.Cookie, domain.User) {
	t.Helper()
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	u := e.fa.addUser("dona@x.com", "Dona", pass10)
	cookie := cookieOf(e.login(t, "dona@x.com", pass10, "/"))
	other := domain.NewID()
	e.f.SetMembers([]domain.Member{
		{CompanyID: e.f.Companies[0].ID, UserID: u.ID, Email: u.Email, Name: "Dona", Role: me, CreatedAt: time.Now()},
		{CompanyID: e.f.Companies[0].ID, UserID: other, Email: "bia@x.com", Name: "Bia", Role: domain.MemberMember, CreatedAt: time.Now()},
		{CompanyID: e.f.Companies[0].ID, UserID: domain.NewID(), Email: "chefe@x.com", Name: "Chefe", Role: domain.MemberAdmin, CreatedAt: time.Now()},
	}, []domain.Invite{{ID: domain.NewID(), Email: "novo@x.com", Role: domain.MemberViewer, ExpiresAt: time.Now().Add(24 * time.Hour)}})
	e.f.MyRoleIs = me
	return e, cookie, u
}

func (e *authEnv) postAs(t *testing.T, c *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return e.withCookie(t, c, req{method: "POST", path: path, form: form, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
}

func TestMembersSectionByRole(t *testing.T) {
	e, c, _ := membersEnv(t, domain.MemberOwner)
	rec := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Membros", "bia@x.com", "chefe@x.com", "Convites abertos", "novo@x.com",
		`action="/c/hanko/configuracoes/membros/convites"`, `value="owner"`, `value="admin"`, `value="viewer"`, "Sair da empresa", "Remover")

	// admin: convida e vê convites, mas só concede member/viewer e não edita admin
	e, c, _ = membersEnv(t, domain.MemberAdmin)
	rec = e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
	wantContains(t, rec, "Convidar", "Convites abertos", `value="member"`, `value="viewer"`)
	wantNotContains(t, rec, `value="owner"`, `value="admin"`)

	// member: só a lista, sem formulário nem convites
	e, c, _ = membersEnv(t, domain.MemberMember)
	rec = e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
	wantContains(t, rec, "Membros", "bia@x.com")
	wantNotContains(t, rec, "Convites abertos", `configuracoes/membros/convites"`, "novo@x.com", `/papel"`)
}

func TestMembersHiddenInTrustedMode(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/configuracoes")
	wantNotContains(t, rec, "Convites abertos", "Convidar")
	wantStatus(t, e.post(t, "/c/hanko/configuracoes/membros/convites", url.Values{"email": {"a@x.com"}, "role": {"member"}}, false), http.StatusNotFound)
}

func TestInviteMemberShowsLinkOnce(t *testing.T) {
	e, c, _ := membersEnv(t, domain.MemberOwner)
	rec := e.postAs(t, c, "/c/hanko/configuracoes/membros/convites", url.Values{"email": {"nova@x.com"}, "role": {"member"}})
	wantStatus(t, rec, http.StatusCreated)
	body := rec.Body.String()
	if !strings.Contains(body, "http://example.com/convite/tok-") || !strings.Contains(body, "o Hanko não envia e-mail") {
		t.Fatalf("link do convite ausente: %.800s", body)
	}
	if !e.f.Called("InviteMember") {
		t.Fatal("service não foi chamado")
	}
	// uma nova leitura não mostra o link (só o hash existe)
	again := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
	wantNotContains(t, again, "/convite/tok-")
	wantContains(t, again, "nova@x.com")

	// erro de validação volta com a mensagem e o e-mail digitado
	rec = e.postAs(t, c, "/c/hanko/configuracoes/membros/convites", url.Values{"email": {"sem-arroba"}, "role": {"member"}})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Dados inválidos", `value="sem-arroba"`)
	// papel insuficiente: 403 com mensagem própria
	e.f.Err = map[string]error{"InviteMember": domain.ErrForbidden}
	rec = e.postAs(t, c, "/c/hanko/configuracoes/membros/convites", url.Values{"email": {"a@x.com"}, "role": {"admin"}})
	wantStatus(t, rec, http.StatusForbidden)
	wantContains(t, rec, "Seu papel nesta empresa não permite esta ação.")
}

func TestMemberActions(t *testing.T) {
	e, c, me := membersEnv(t, domain.MemberOwner)
	bia := e.f.Members[1].UserID
	inv := e.f.Invites[0].ID

	rec := e.postAs(t, c, "/c/hanko/configuracoes/membros/"+bia.String()+"/papel", url.Values{"role": {"viewer"}})
	wantStatus(t, rec, http.StatusSeeOther)
	if e.f.Members[1].Role != domain.MemberViewer {
		t.Fatalf("papel: %+v", e.f.Members[1])
	}
	wantStatus(t, e.postAs(t, c, "/c/hanko/configuracoes/membros/lixo/papel", url.Values{"role": {"viewer"}}), http.StatusNotFound)

	wantStatus(t, e.postAs(t, c, "/c/hanko/configuracoes/membros/convites/"+inv.String()+"/revogar", nil), http.StatusSeeOther)
	if len(e.f.Invites) != 0 {
		t.Fatal("convite não foi revogado")
	}
	wantStatus(t, e.postAs(t, c, "/c/hanko/configuracoes/membros/"+bia.String()+"/remover", nil), http.StatusSeeOther)
	if len(e.f.Members) != 2 {
		t.Fatalf("membros: %+v", e.f.Members)
	}
	// último owner: o service devolve conflito e a página mostra o erro
	e.f.Err = map[string]error{"RemoveMember": domain.ErrConflict}
	rec = e.postAs(t, c, "/c/hanko/configuracoes/membros/"+me.ID.String()+"/remover", nil)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "Conflito")
	// sair da empresa volta para a raiz
	e.f.Err = nil
	rec = e.postAs(t, c, "/c/hanko/configuracoes/membros/"+me.ID.String()+"/remover", nil)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location %q", loc)
	}
	// CSRF
	rec = e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/membros/" + bia.String() + "/remover"})
	wantStatus(t, rec, http.StatusForbidden)
}

func TestInvitePageStates(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	pv := service.InvitePreview{CompanyName: "Hanko Co", CompanySlug: "hanko", Email: "nova@x.com", Role: domain.MemberMember}
	e.fa.addInvite("tok-bom", pv)

	// sem sessão e sem redirecionamento ao login
	rec := e.get(t, "/convite/tok-bom")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Hanko Co", "nova@x.com", "Criar conta e aceitar", `name="name"`, `name="password"`)
	wantNotContains(t, rec, "tok-bom/") // a página não vaza o token além do action
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("convite deve ir com Referrer-Policy: no-referrer")
	}

	// token ruim: 404 genérico, mesma página para qualquer motivo
	bad := e.get(t, "/convite/ruim")
	wantStatus(t, bad, http.StatusNotFound)
	wantContains(t, bad, "Convite inválido", "foi revogado ou já foi usado")

	// conta já existe: pede a senha
	e.fa.addUser("nova@x.com", "Nova", pass10)
	wantContains(t, e.get(t, "/convite/tok-bom"), "Entrar e aceitar")
}

func TestInviteAcceptCreatesAccount(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addInvite("tok", service.InvitePreview{CompanyName: "Hanko Co", CompanySlug: "hanko", Email: "nova@x.com", Role: domain.MemberViewer})

	rec := e.post(t, "/convite/tok", url.Values{"name": {"Nova"}, "password": {"curta"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if cookieOf(rec) != nil {
		t.Fatal("erro não abre sessão")
	}
	rec = e.post(t, "/convite/tok", url.Values{"name": {"Nova"}, "password": {pass10}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/" || cookieOf(rec) == nil {
		t.Fatalf("Location %q cookie %v", loc, cookieOf(rec))
	}
	if len(e.fa.accepted) != 1 {
		t.Fatalf("aceites: %v", e.fa.accepted)
	}
	// uso único: o mesmo link agora é inválido
	wantStatus(t, e.get(t, "/convite/tok"), http.StatusNotFound)
	// POST precisa de mesma origem
	rec = e.do(t, req{method: "POST", path: "/convite/tok", form: url.Values{"name": {"x"}, "password": {pass10}}})
	wantStatus(t, rec, http.StatusForbidden)
}

func TestInviteAcceptExistingAccountNeedsPassword(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("nova@x.com", "Nova", pass10)
	e.fa.addInvite("tok", service.InvitePreview{CompanyName: "Hanko Co", CompanySlug: "hanko", Email: "nova@x.com", Role: domain.MemberMember})

	rec := e.post(t, "/convite/tok", url.Values{"password": {"errada"}}, false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "E-mail ou senha incorretos.")
	if len(e.fa.accepted) != 0 || cookieOf(rec) != nil {
		t.Fatal("senha errada não aceita nem abre sessão")
	}
	rec = e.post(t, "/convite/tok", url.Values{"password": {pass10}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if rec.Header().Get("Location") != "/c/hanko/" || cookieOf(rec) == nil || len(e.fa.accepted) != 1 {
		t.Fatalf("%v %v", rec.Header(), e.fa.accepted)
	}
}

func TestInviteSignedInStates(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("nova@x.com", "Nova", pass10)
	e.fa.addUser("outra@x.com", "Outra", pass10)
	e.fa.addInvite("tok", service.InvitePreview{CompanyName: "Hanko Co", CompanySlug: "hanko", Email: "nova@x.com", Role: domain.MemberMember})

	other := cookieOf(e.login(t, "outra@x.com", pass10, "/"))
	rec := e.withCookie(t, other, req{method: "GET", path: "/convite/tok"})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "conectado com outro e-mail")
	rec = e.postAs(t, other, "/convite/tok", nil)
	wantStatus(t, rec, http.StatusForbidden)
	if len(e.fa.accepted) != 0 {
		t.Fatal("e-mail diferente não aceita")
	}

	right := cookieOf(e.login(t, "NOVA@x.com", pass10, "/"))
	rec = e.withCookie(t, right, req{method: "GET", path: "/convite/tok"})
	wantContains(t, rec, "Aceitar convite")
	rec = e.postAs(t, right, "/convite/tok", nil)
	wantStatus(t, rec, http.StatusSeeOther)
	if rec.Header().Get("Location") != "/c/hanko/" || len(e.fa.accepted) != 1 {
		t.Fatalf("%v %v", rec.Header(), e.fa.accepted)
	}
}

func TestInviteRateLimit(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local", Limiter: &auth.LoginLimiter{MaxFails: 2, MaxPerIP: 3}})
	for i := 0; i < 2; i++ {
		wantStatus(t, e.get(t, "/convite/chute"+string(rune('a'+i))), http.StatusNotFound)
	}
	rec := e.get(t, "/convite/chutec")
	wantStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After ausente")
	}
}

func TestInviteFirebase(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "firebase", Admission: auth.Admission{Domains: []string{"outro.com"}}})
	e.fa.addInvite("tok", service.InvitePreview{CompanyName: "Hanko Co", CompanySlug: "hanko", Email: "ana@exemplo.com", Role: domain.MemberViewer})
	page := e.get(t, "/convite/tok")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, `id="fb-form"`, `data-invite="tok"`, "Continuar com Google", "ana@exemplo.com")

	post := func(body string) *httptest.ResponseRecorder {
		rq := httptest.NewRequest("POST", "/auth/firebase", strings.NewReader(body))
		rq.Header.Set("Sec-Fetch-Site", "same-origin")
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, rq)
		return rec
	}
	rec := post(`{"id_token":"t","invite":"tok"}`)
	wantStatus(t, rec, http.StatusOK)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["redirect"] != "/c/hanko/" || e.fa.lastFB.InviteToken != "tok" || len(e.fa.accepted) != 1 || cookieOf(rec) == nil {
		t.Fatalf("%v %+v %v", out, e.fa.lastFB, e.fa.accepted)
	}
	// convite inválido ou de outro e-mail: 403 invite_invalid e sem sessão
	rec = post(`{"id_token":"t","invite":"nao-existe"}`)
	wantStatus(t, rec, http.StatusForbidden)
	wantContains(t, rec, "invite_invalid")
	if cookieOf(rec) != nil {
		t.Fatal("sem sessão")
	}
	e.fa.addInvite("tok2", service.InvitePreview{CompanyName: "Hanko Co", CompanySlug: "hanko", Email: "outra@exemplo.com", Role: domain.MemberViewer})
	rec = post(`{"id_token":"t","invite":"tok2"}`) // o ID token é de ana@, o convite é de outra@
	wantStatus(t, rec, http.StatusForbidden)
	wantContains(t, rec, "invite_invalid")
	if cookieOf(rec) != nil {
		t.Fatal("sem sessão")
	}
}

// Erro 5xx numa rota com segredo no caminho não pode ir ao log com o segredo.
func TestFailLogRedactsInviteToken(t *testing.T) {
	var buf strings.Builder
	s := &server{log: slog.New(slog.NewTextHandler(&buf, nil)), version: "test", now: uitest.Now}
	r := httptest.NewRequest(http.MethodGet, "/convite/SEGREDO-123", nil)
	s.fail(httptest.NewRecorder(), r, nil, errors.New("falha interna"))
	if out := buf.String(); strings.Contains(out, "SEGREDO-123") || !strings.Contains(out, "/convite/…") {
		t.Fatalf("log vaza o token ou não mascara o caminho: %s", out)
	}
}
