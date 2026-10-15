package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// fakeAuth é um AuthBackend (e auth.Resolver) em memória, com senha em claro.
type fakeAuth struct {
	mu       sync.Mutex
	users    []domain.User
	passes   map[domain.ID]string
	sessions map[string]domain.ID
	keys     map[domain.ID]*domain.APIKey
	keyOwner map[domain.ID]domain.ID
	fbErr    error
	rotated  []string
	busy     bool                  // AuthenticateLocal/CreateOwner devolvem auth.ErrBusy
	lastFB   service.FirebaseLogin // último LoginFirebase recebido
	lastIP   string                // IP da última sessão aberta
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{passes: map[domain.ID]string{}, sessions: map[string]domain.ID{}, keys: map[domain.ID]*domain.APIKey{}, keyOwner: map[domain.ID]domain.ID{}}
}

func (f *fakeAuth) addUser(email, name, pass string) domain.User {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := domain.User{ID: domain.NewID(), Email: email, Name: name, Provider: domain.ProviderLocal, Role: domain.RoleOwner}
	f.users = append(f.users, u)
	f.passes[u.ID] = pass
	return u
}

func (f *fakeAuth) CountUsers(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.users), nil
}
func (f *fakeAuth) ListUsers(context.Context) ([]domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.User(nil), f.users...), nil
}

func (f *fakeAuth) CreateOwner(_ context.Context, email, name, password string) (domain.User, error) {
	if f.busy {
		return domain.User{}, auth.ErrBusy
	}
	if len(f.users) > 0 {
		return domain.User{}, domain.ErrConflict
	}
	if err := auth.ValidatePassword(password); err != nil {
		return domain.User{}, domain.Invalid("password", err.Error())
	}
	return f.addUser(email, name, password), nil
}

func (f *fakeAuth) AuthenticateLocal(_ context.Context, email, password string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy {
		return domain.User{}, auth.ErrBusy
	}
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) && f.passes[u.ID] == password && password != "" {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrUnauthenticated
}

func (f *fakeAuth) LoginFirebase(_ context.Context, in service.FirebaseLogin) (domain.User, error) {
	f.lastFB = in
	if f.fbErr != nil {
		return domain.User{}, f.fbErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	u := domain.User{ID: domain.NewID(), Email: in.Email, Name: in.Name, Provider: domain.ProviderFirebase, ExternalID: in.UID, Role: domain.RoleOwner}
	f.users = append(f.users, u)
	return u, nil
}

func (f *fakeAuth) CreateSession(_ context.Context, userID domain.ID, meta service.SessionMeta, replace string) (string, domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastIP = meta.IP
	if replace != "" {
		delete(f.sessions, replace)
		f.rotated = append(f.rotated, replace)
	}
	tok, _, _ := auth.NewSessionToken()
	f.sessions[tok] = userID
	return tok, domain.Session{ID: domain.NewID(), UserID: userID, ExpiresAt: time.Now().Add(auth.SessionTTL)}, nil
}

func (f *fakeAuth) EndSession(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, token)
	return nil
}

func (f *fakeAuth) userByID(id domain.ID) (domain.User, bool) {
	for _, u := range f.users {
		if u.ID == id {
			return u, true
		}
	}
	return domain.User{}, false
}

func (f *fakeAuth) ResolveSession(_ context.Context, token string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.sessions[token]; ok {
		if u, ok := f.userByID(id); ok {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrUnauthenticated
}

func (f *fakeAuth) ResolveAPIKey(context.Context, string) (domain.User, error) {
	return domain.User{}, domain.ErrUnauthenticated
}

func (f *fakeAuth) CreateAPIKey(_ context.Context, userID domain.ID, name string) (string, domain.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.TrimSpace(name) == "" {
		return "", domain.APIKey{}, domain.Invalid("name", "required")
	}
	key, prefix, _, _ := auth.NewAPIKey()
	k := &domain.APIKey{ID: domain.NewID(), UserID: userID, Name: name, Prefix: prefix, CreatedAt: time.Now()}
	f.keys[k.ID] = k
	f.keyOwner[k.ID] = userID
	return key, *k, nil
}

func (f *fakeAuth) ListAPIKeys(_ context.Context, userID domain.ID) ([]domain.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.APIKey
	for _, k := range f.keys {
		if k.UserID == userID {
			out = append(out, *k)
		}
	}
	return out, nil
}

func (f *fakeAuth) RevokeAPIKey(_ context.Context, userID, keyID domain.ID) (domain.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.keys[keyID]
	if !ok || k.UserID != userID || k.RevokedAt != nil {
		return domain.APIKey{}, domain.ErrNotFound
	}
	now := time.Now()
	k.RevokedAt = &now
	return *k, nil
}

type fakeVerifier struct {
	claims auth.FirebaseClaims
	err    error
	got    string
}

func (v *fakeVerifier) Verify(_ context.Context, tok string) (auth.FirebaseClaims, error) {
	v.got = tok
	return v.claims, v.err
}

type authEnv struct {
	*env
	fa *fakeAuth
	fv *fakeVerifier
}

// newAuthEnv monta a UI com o Guard por fora, como no app.
func newAuthEnv(t *testing.T, d AuthDeps) *authEnv {
	t.Helper()
	f := uitest.Sample()
	fa := newFakeAuth()
	fv := &fakeVerifier{claims: auth.FirebaseClaims{UID: "uid1", Email: "ana@exemplo.com", Name: "Ana Lima", EmailVerified: true}}
	if d.Provider == "firebase" {
		d.Verifier = fv
		d.Firebase = views.FirebaseWeb{APIKey: "key", AuthDomain: "p.firebaseapp.com", ProjectID: "p"}
	}
	s := &server{be: f, broker: events.NewBroker(), log: testLogger(t), version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond}
	s.auth = newAuthState(d, fa, s.now)
	mux := http.NewServeMux()
	s.routes(mux)
	return &authEnv{env: &env{f: f, broker: s.broker, h: auth.Guard(fa, nil)(mux)}, fa: fa, fv: fv}
}

func cookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	return nil
}

func (e *authEnv) login(t *testing.T, email, pass, next string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, req{method: "POST", path: "/entrar", form: url.Values{"email": {email}, "password": {pass}, "next": {next}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
}

func (e *authEnv) withCookie(t *testing.T, c *http.Cookie, r req) *httptest.ResponseRecorder {
	t.Helper()
	if r.hdr == nil {
		r.hdr = map[string]string{}
	}
	r.hdr["Cookie"] = c.Name + "=" + c.Value
	return e.do(t, r)
}

func TestAuthUIRequiresSession(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")

	for _, p := range []string{"/", "/c/hanko/", "/c/hanko/tasks", "/c/hanko/configuracoes", "/bem-vindo"} {
		rec := e.get(t, p)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/entrar") {
			t.Errorf("%s: Location %q", p, loc)
		}
	}
	rec := e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {"x"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if e.f.Called("CreateTask") {
		t.Fatal("POST sem sessão não pode chegar ao service")
	}
	wantStatus(t, e.get(t, "/static/hanko.css"), http.StatusOK)
	wantStatus(t, e.get(t, "/entrar"), http.StatusOK)
}

func TestLocalLoginFlow(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	u := e.fa.addUser("dono@x.com", "Dona da Silva", "senha-bem-longa")

	page := e.get(t, "/entrar?next=/c/hanko/tasks")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, `action="/entrar"`, `type="password"`, `value="/c/hanko/tasks"`, "Autenticação local")
	wantNotContains(t, page, "Criar conta de dono", "firebase")

	// credenciais erradas: mensagem genérica, sem cookie
	bad := e.login(t, "dono@x.com", "errada", "")
	wantStatus(t, bad, http.StatusUnauthorized)
	wantContains(t, bad, "E-mail ou senha incorretos.")
	if cookieOf(bad) != nil {
		t.Fatal("login falho não pode criar sessão")
	}
	nouser := e.login(t, "ninguem@x.com", "qualquer", "")
	wantContains(t, nouser, "E-mail ou senha incorretos.") // mesma mensagem para e-mail inexistente

	// sucesso
	ok := e.login(t, "DONO@x.com", "senha-bem-longa", "/c/hanko/tasks")
	wantStatus(t, ok, http.StatusSeeOther)
	if ok.Header().Get("Location") != "/c/hanko/tasks" {
		t.Fatalf("Location %q", ok.Header().Get("Location"))
	}
	c := cookieOf(ok)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure || c.MaxAge != int(auth.SessionTTL/time.Second) || len(c.Value) < 43 {
		t.Fatalf("cookie: %+v", c)
	}
	if id := e.fa.sessions[c.Value]; id != u.ID {
		t.Fatal("sessão deveria apontar para o usuário")
	}

	// com a sessão: UI abre, mostra usuário e Sair; /entrar redireciona
	tasks := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/tasks"})
	wantStatus(t, tasks, http.StatusOK)
	wantContains(t, tasks, "Dona da Silva", "dono@x.com", `action="/sair"`, "Sair")
	again := e.withCookie(t, c, req{method: "GET", path: "/entrar"})
	wantStatus(t, again, http.StatusSeeOther)

	// sair
	out := e.withCookie(t, c, req{method: "POST", path: "/sair", hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, out, http.StatusSeeOther)
	if cc := cookieOf(out); cc == nil || cc.MaxAge >= 0 || cc.Value != "" {
		t.Fatalf("cookie deveria ser limpo: %+v", cc)
	}
	if _, still := e.fa.sessions[c.Value]; still {
		t.Fatal("sessão deveria ter sido encerrada no servidor")
	}
	wantStatus(t, e.withCookie(t, c, req{method: "GET", path: "/c/hanko/tasks"}), http.StatusSeeOther)
}

func TestLoginRotatesSession(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	first := cookieOf(e.login(t, "dono@x.com", "senha-bem-longa", ""))
	rec := e.withCookie(t, first, req{method: "POST", path: "/entrar", form: url.Values{"email": {"dono@x.com"}, "password": {"senha-bem-longa"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	second := cookieOf(rec)
	if second == nil || second.Value == first.Value {
		t.Fatal("novo login deve emitir token novo")
	}
	if _, ok := e.fa.sessions[first.Value]; ok || len(e.fa.rotated) != 1 || e.fa.rotated[0] != first.Value {
		t.Fatal("a sessão apresentada deve ser rotacionada (removida)")
	}
}

func TestLoginSafeNextAndCSRF(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	for _, next := range []string{"https://evil.test/", "//evil.test", "/\\evil.test"} {
		rec := e.login(t, "dono@x.com", "senha-bem-longa", next)
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("next %q vazou: Location %q", next, loc)
		}
	}
	for name, h := range map[string]map[string]string{
		"cross-site":    {"Sec-Fetch-Site": "cross-site"},
		"sem origem":    nil,
		"origin alheio": {"Origin": "https://evil.test"},
	} {
		rec := e.do(t, req{method: "POST", path: "/entrar", form: url.Values{"email": {"dono@x.com"}, "password": {"senha-bem-longa"}}, hdr: h})
		if rec.Code != http.StatusForbidden || cookieOf(rec) != nil {
			t.Errorf("%s: login cross-origin deveria ser 403 sem cookie, veio %d", name, rec.Code)
		}
	}
	rec := e.do(t, req{method: "POST", path: "/sair", hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	wantStatus(t, rec, http.StatusForbidden)
}

func TestLoginRateLimit(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	for i := 0; i < 5; i++ {
		wantStatus(t, e.login(t, "dono@x.com", "errada", ""), http.StatusUnauthorized)
	}
	rec := e.login(t, "dono@x.com", "senha-bem-longa", "") // até a senha certa é barrada durante o bloqueio
	wantStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" || cookieOf(rec) != nil {
		t.Fatalf("429 deveria trazer Retry-After e nenhum cookie")
	}
	wantContains(t, rec, "Muitas tentativas")
}

func TestSecureCookieWhenHTTPS(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local", Secure: true})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	c := cookieOf(e.login(t, "dono@x.com", "senha-bem-longa", ""))
	if c == nil || !c.Secure || !c.HttpOnly {
		t.Fatalf("cookie sob https deve ser Secure+HttpOnly: %+v", c)
	}
	out := e.withCookie(t, c, req{method: "POST", path: "/sair", hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	if cc := cookieOf(out); cc == nil || !cc.Secure {
		t.Fatalf("cookie limpo também é Secure: %+v", cc)
	}
}

func TestOwnerBootstrapUI(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local", WebBootstrap: true})
	page := e.get(t, "/entrar")
	wantContains(t, page, "Criar conta de dono", `action="/entrar/dono"`, `name="name"`)

	form := func(pw string) url.Values {
		return url.Values{"name": {"Dona"}, "email": {"dona@x.com"}, "password": {pw}}
	}
	hdr := map[string]string{"Sec-Fetch-Site": "same-origin"}
	short := e.do(t, req{method: "POST", path: "/entrar/dono", form: form("curta"), hdr: hdr})
	wantStatus(t, short, http.StatusUnprocessableEntity)
	wantContains(t, short, "password")

	ok := e.do(t, req{method: "POST", path: "/entrar/dono", form: form("senha-bem-longa"), hdr: hdr})
	wantStatus(t, ok, http.StatusSeeOther)
	c := cookieOf(ok)
	if c == nil {
		t.Fatal("criar o dono deveria abrir sessão")
	}
	wantStatus(t, e.withCookie(t, c, req{method: "GET", path: "/c/hanko/"}), http.StatusOK)

	// depois do primeiro usuário: a tela não oferece mais e o POST é recusado
	wantNotContains(t, e.get(t, "/entrar"), "Criar conta de dono")
	second := e.do(t, req{method: "POST", path: "/entrar/dono", form: form("senha-bem-longa"), hdr: hdr})
	wantStatus(t, second, http.StatusConflict)
	if len(e.fa.users) != 1 {
		t.Fatal("não pode haver segundo dono")
	}
	cross := e.do(t, req{method: "POST", path: "/entrar/dono", form: form("senha-bem-longa"), hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	wantStatus(t, cross, http.StatusForbidden)
}

func TestHumanActorCarriesUserID(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	u := e.fa.addUser("dono@x.com", "Dona da Silva", "senha-bem-longa")
	c := cookieOf(e.login(t, "dono@x.com", "senha-bem-longa", ""))

	rec := e.withCookie(t, c, req{method: "POST", path: "/c/hanko/tasks/HNK-221/comentarios", form: url.Values{"body": {"olá do usuário"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, rec, http.StatusSeeOther)
	last := e.f.Comments[len(e.f.Comments)-1]
	if last.Author.Type != domain.ActorHuman || last.Author.ID == nil || *last.Author.ID != u.ID {
		t.Fatalf("comentário deveria levar o ID do usuário: %+v", last.Author)
	}
	page := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/tasks/HNK-221"})
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "Dona da Silva", "olá do usuário")
}

func TestActivityShowsUserNames(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	u := e.fa.addUser("dono@x.com", "Dona da Silva", "senha-bem-longa")
	c := cookieOf(e.login(t, "dono@x.com", "senha-bem-longa", ""))
	co := e.f.Companies[0]
	id := u.ID
	e.f.Activity = append([]domain.ActivityEvent{{ID: domain.NewID(), CompanyID: co.ID, Actor: domain.Actor{Type: domain.ActorHuman, ID: &id},
		Verb: "task.created", SubjectType: "task", SubjectID: domain.NewID(), OccurredAt: uitest.Now(), Data: json.RawMessage(`{}`)}}, e.f.Activity...)
	page := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/atividade"})
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "Dona da Silva")
}

func TestAPIKeySettings(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	other := e.fa.addUser("outro@x.com", "Outro", "outra-senha-longa")
	c := cookieOf(e.login(t, "dono@x.com", "senha-bem-longa", ""))
	hdr := map[string]string{"Sec-Fetch-Site": "same-origin"}

	page := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "Chaves de API", "dono@x.com", "Dona")

	created := e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/chaves", form: url.Values{"name": {"notebook"}}, hdr: hdr})
	wantStatus(t, created, http.StatusCreated)
	key := regexp.MustCompile(`hk_[A-Za-z0-9_-]{43}`).FindString(created.Body.String())
	if key == "" {
		t.Fatal("a chave deveria aparecer na resposta de criação")
	}
	wantContains(t, created, "não será mostrada de novo")

	// a listagem seguinte mostra só o prefixo
	list := e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"})
	wantContains(t, list, "notebook")
	wantNotContains(t, list, key)
	if !strings.Contains(list.Body.String(), key[:9]) {
		t.Fatal("deveria mostrar o prefixo")
	}

	empty := e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/chaves", form: url.Values{"name": {"  "}}, hdr: hdr})
	wantStatus(t, empty, http.StatusUnprocessableEntity)
	cross := e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/chaves", form: url.Values{"name": {"x"}}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
	wantStatus(t, cross, http.StatusForbidden)

	var kid domain.ID
	for id := range e.fa.keys {
		kid = id
	}
	// chave de outro usuário não pode ser revogada por este
	_, ok, _ := e.fa.CreateAPIKey(context.Background(), other.ID, "alheia")
	_ = ok
	var alien domain.ID
	for id, k := range e.fa.keys {
		if k.UserID == other.ID {
			alien = id
		}
	}
	wantStatus(t, e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/chaves/" + alien.String() + "/revogar", hdr: hdr}), http.StatusNotFound)
	wantStatus(t, e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/chaves/nao-e-id/revogar", hdr: hdr}), http.StatusNotFound)
	rev := e.withCookie(t, c, req{method: "POST", path: "/c/hanko/configuracoes/chaves/" + kid.String() + "/revogar", hdr: hdr})
	wantStatus(t, rev, http.StatusSeeOther)
	if e.fa.keys[kid].RevokedAt == nil {
		t.Fatal("deveria ter revogado")
	}
	wantContains(t, e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes"}), "Revogada")
}

func TestFirebaseLogin(t *testing.T) {
	adm := auth.Admission{Domains: []string{"exemplo.com"}}
	e := newAuthEnv(t, AuthDeps{Provider: "firebase", Admission: adm})

	page := e.get(t, "/entrar?next=/c/hanko/tasks")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, `id="fb-form"`, `data-api-key="key"`, `data-auth-domain="p.firebaseapp.com"`, `data-project-id="p"`,
		"Continuar com Google", "Esqueci a senha", "/static/auth-firebase.js", "Autenticação pelo Firebase", `data-next="/c/hanko/tasks"`)
	wantNotContains(t, page, "Criar conta de dono")

	post := func(body string, hdr map[string]string) *httptest.ResponseRecorder {
		h := map[string]string{"Sec-Fetch-Site": "same-origin", "Content-Type": "application/json"}
		for k, v := range hdr {
			if v == "" {
				delete(h, k)
			} else {
				h[k] = v
			}
		}
		rq := httptest.NewRequest("POST", "/auth/firebase", strings.NewReader(body))
		for k, v := range h {
			rq.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, rq)
		return rec
	}
	code := func(rec *httptest.ResponseRecorder) string {
		var m map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["code"]
	}

	t.Run("válido cria sessão", func(t *testing.T) {
		rec := post(`{"id_token":"tok-bom","next":"/c/hanko/tasks"}`, nil)
		wantStatus(t, rec, http.StatusOK)
		var m map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		if m["redirect"] != "/c/hanko/tasks" || e.fv.got != "tok-bom" {
			t.Fatalf("resposta %v, token %q", m, e.fv.got)
		}
		c := cookieOf(rec)
		if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Fatalf("cookie: %+v", c)
		}
		wantStatus(t, e.withCookie(t, c, req{method: "GET", path: "/c/hanko/tasks"}), http.StatusOK)
	})
	t.Run("next malicioso vira /", func(t *testing.T) {
		rec := post(`{"id_token":"t","next":"https://evil.test"}`, nil)
		var m map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		if m["redirect"] != "/" {
			t.Fatalf("redirect %q", m["redirect"])
		}
	})
	t.Run("cross-origin", func(t *testing.T) {
		for _, h := range []map[string]string{{"Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "", "Origin": "https://evil.test"}, {"Sec-Fetch-Site": ""}} {
			rec := post(`{"id_token":"t"}`, h)
			if rec.Code != http.StatusForbidden || cookieOf(rec) != nil || code(rec) != "cross_origin" {
				t.Errorf("%v: %d %s", h, rec.Code, rec.Body.String())
			}
		}
	})
	t.Run("content-type e corpo", func(t *testing.T) {
		wantStatus(t, post(`{"id_token":"t"}`, map[string]string{"Content-Type": "text/plain"}), http.StatusUnsupportedMediaType)
		wantStatus(t, post(`nope`, nil), http.StatusBadRequest)
		wantStatus(t, post(`{"id_token":""}`, nil), http.StatusBadRequest)
	})
	t.Run("token inválido", func(t *testing.T) {
		e.fv.err = errors.Join(auth.ErrInvalidIDToken, errors.New("aud"))
		rec := post(`{"id_token":"t"}`, nil)
		if rec.Code != http.StatusUnauthorized || code(rec) != "invalid_token" || cookieOf(rec) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("e-mail não verificado", func(t *testing.T) {
		e.fv.err = auth.ErrUnverifiedEmail
		rec := post(`{"id_token":"t"}`, nil)
		if rec.Code != http.StatusForbidden || code(rec) != "email_not_verified" || cookieOf(rec) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("chaves do Google indisponíveis", func(t *testing.T) {
		e.fv.err = auth.ErrJWKSUnavailable
		rec := post(`{"id_token":"t"}`, nil)
		if rec.Code != http.StatusServiceUnavailable || code(rec) != "unavailable" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("fora da admissão e conflito", func(t *testing.T) {
		e.fv.err = nil
		e.fa.fbErr = domain.ErrNotAllowed
		if rec := post(`{"id_token":"t"}`, nil); rec.Code != http.StatusForbidden || code(rec) != "not_admitted" || cookieOf(rec) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		e.fa.fbErr = domain.ErrConflict
		if rec := post(`{"id_token":"t"}`, nil); rec.Code != http.StatusConflict || code(rec) != "email_taken" {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		e.fa.fbErr = nil
	})

	t.Run("sem login local no provider firebase", func(t *testing.T) {
		wantStatus(t, e.login(t, "a@x.com", "x", ""), http.StatusNotFound)
		wantStatus(t, e.do(t, req{method: "POST", path: "/entrar/dono", form: url.Values{}, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}}), http.StatusNotFound)
	})
}

func TestFirebaseEndpointOffForLocal(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	rq := httptest.NewRequest("POST", "/auth/firebase", strings.NewReader(`{"id_token":"t"}`))
	rq.Header.Set("Sec-Fetch-Site", "same-origin")
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	wantStatus(t, rec, http.StatusNotFound)
}

func TestTrustedModeHasNoAuthRoutes(t *testing.T) {
	e := newEnv(t, uitest.Sample()) // sem AuthDeps: modo trusted
	wantStatus(t, e.get(t, "/entrar"), http.StatusNotFound)
	rec := e.get(t, "/c/hanko/tasks")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Você")
	wantNotContains(t, rec, `action="/sair"`)
	st := e.get(t, "/c/hanko/configuracoes")
	wantStatus(t, st, http.StatusOK)
	wantContains(t, st, "Modo trusted")
	// ator continua anônimo no trusted
	e.post(t, "/c/hanko/tasks/HNK-221/comentarios", url.Values{"body": {"oi"}}, false)
	if last := e.f.Comments[len(e.f.Comments)-1]; last.Author.ID != nil {
		t.Fatalf("em trusted o ator humano não carrega ID: %+v", last.Author)
	}
}

// Sem WebBootstrap (URL pública), o primeiro visitante não vira dono: a tela orienta a CLI e o POST some.
func TestOwnerBootstrapOffWhenPublic(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	page := e.get(t, "/entrar")
	wantStatus(t, page, http.StatusOK)
	wantContains(t, page, "hanko auth create-owner", "Criar conta de dono")
	wantNotContains(t, page, `action="/entrar/dono"`, `name="password"`)

	form := url.Values{"name": {"Intruso"}, "email": {"intruso@x.com"}, "password": {"senha-bem-longa"}}
	rec := e.do(t, req{method: "POST", path: "/entrar/dono", form: form, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, rec, http.StatusNotFound)
	if len(e.fa.users) != 0 || cookieOf(rec) != nil {
		t.Fatal("o formulário público não pode criar o dono")
	}

	// com usuário existente a tela volta a ser só o login (sem a orientação de CLI)
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	wantNotContains(t, e.get(t, "/entrar"), "hanko auth create-owner")
}

func TestLoginBusyReturns503(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local", WebBootstrap: true})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	e.fa.busy = true
	rec := e.login(t, "dono@x.com", "senha-bem-longa", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantContains(t, rec, "Tente de novo")
	if rec.Header().Get("Retry-After") == "" || cookieOf(rec) != nil {
		t.Fatalf("503 deveria trazer Retry-After e nenhum cookie: %v", rec.Header())
	}
	// ocupado não conta como falha: depois de 10 tentativas a senha certa ainda entra
	for i := 0; i < 10; i++ {
		wantStatus(t, e.login(t, "dono@x.com", "senha-bem-longa", ""), http.StatusServiceUnavailable)
	}
	e.fa.busy = false
	wantStatus(t, e.login(t, "dono@x.com", "senha-bem-longa", ""), http.StatusSeeOther)

	e2 := newAuthEnv(t, AuthDeps{Provider: "local", WebBootstrap: true})
	e2.fa.busy = true
	rec = e2.do(t, req{method: "POST", path: "/entrar/dono", form: url.Values{"name": {"D"}, "email": {"d@x.com"}, "password": {"senha-bem-longa"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
	wantStatus(t, rec, http.StatusServiceUnavailable)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("criar dono ocupado: faltou Retry-After")
	}
}

// X-Forwarded-For forjado não escapa do limite quando o par não é proxy confiável.
func TestLoginRateLimitIgnoresForgedXFF(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"}) // sem proxies confiáveis
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	do := func(i int, pass string) *httptest.ResponseRecorder {
		return e.do(t, req{method: "POST", path: "/entrar", form: url.Values{"email": {"dono@x.com"}, "password": {pass}},
			hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "X-Forwarded-For": "198.51.100." + strconv.Itoa(i)}})
	}
	for i := 0; i < 5; i++ {
		wantStatus(t, do(i, "errada"), http.StatusUnauthorized)
	}
	wantStatus(t, do(99, "senha-bem-longa"), http.StatusTooManyRequests)
	rec := do(100, "senha-bem-longa")
	wantStatus(t, rec, http.StatusTooManyRequests)
}

func TestSessionIPUsesTrustedProxies(t *testing.T) {
	proxies, err := auth.ParseTrustedProxies([]string{"192.0.2.0/24"}) // RemoteAddr padrão do httptest
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		deps AuthDeps
		want string
	}{
		{"proxy confiável: IP do XFF", AuthDeps{Provider: "local", Proxies: proxies}, "203.0.113.7"},
		{"sem proxies: RemoteAddr", AuthDeps{Provider: "local"}, "192.0.2.1"},
	} {
		e := newAuthEnv(t, c.deps)
		e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
		rec := e.do(t, req{method: "POST", path: "/entrar", form: url.Values{"email": {"dono@x.com"}, "password": {"senha-bem-longa"}},
			hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "X-Forwarded-For": "203.0.113.7"}})
		wantStatus(t, rec, http.StatusSeeOther)
		if e.fa.lastIP != c.want {
			t.Errorf("%s: IP da sessão = %q, quer %q", c.name, e.fa.lastIP, c.want)
		}
	}
}

func TestFirebaseLoginPassesOwnerEmail(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "firebase", OwnerEmail: "dona@exemplo.com"})
	rq := httptest.NewRequest("POST", "/auth/firebase", strings.NewReader(`{"id_token":"tok"}`))
	rq.Header.Set("Sec-Fetch-Site", "same-origin")
	rq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	wantStatus(t, rec, http.StatusOK)
	if e.fa.lastFB.OwnerEmail != "dona@exemplo.com" || e.fa.lastFB.ListsConfigured {
		t.Fatalf("LoginFirebase recebeu %+v", e.fa.lastFB)
	}
}
