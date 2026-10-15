package ui

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// callRe finds i18n.T(ctx, "..."), i18n.Tf(ctx, "...") and i18n.N("...") with a literal text.
var callRe = regexp.MustCompile(`i18n\.(?:Tf?\(\s*[^,()]+(?:\([^()]*\))?\s*,|N\()\s*("(?:[^"\\]|\\.)*")`)

// Every text marked for translation in the .templ and .go sources must have an English entry.
func TestCatalogCoversSources(t *testing.T) {
	var files []string
	for _, pat := range []string{"*.go", "views/*.templ", "views/*.go"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	cat := i18n.English()
	seen := map[string]bool{}
	var missing []string
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, "_templ.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range callRe.FindAllStringSubmatch(string(b), -1) {
			k, err := strconv.Unquote(m[1])
			if err != nil {
				t.Fatalf("%s: %s: %v", f, m[1], err)
			}
			n++
			seen[k] = true
			if _, ok := cat[k]; !ok {
				missing = append(missing, f+": "+k)
			}
		}
	}
	if n < 500 {
		t.Fatalf("only %d literals found; the scan is broken", n)
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no English entry: %s", m)
	}
	// stale entries are noise: every catalog key must still be used by the sources
	var stale []string
	for k := range cat {
		if !seen[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	for _, k := range stale {
		t.Errorf("catalog entry not used by any source: %q", k)
	}
}

var enPages = []struct {
	path string
	want []string
}{
	{"/c/hanko/", []string{"Dashboard", "Active personas", "Tasks by status", "Getting started", "Hire persona"}},
	{"/c/hanko/tasks", []string{"In progress", "Blocked", "Unassigned", "New task"}},
	{"/c/hanko/tasks/nova", []string{"New task", "Priority", "Create task"}},
	{"/c/hanko/personas", []string{"Personas", "Waiting on gate"}},
	{"/c/hanko/personas/nova", []string{"Hire persona", "Check interval"}},
	{"/c/hanko/execucoes", []string{"Runs", "Live"}},
	{"/c/hanko/atividade", []string{"Activity", "Today"}},
	{"/c/hanko/caixa", []string{"Inbox", "Blocked tasks", "Failed runs"}},
	{"/c/hanko/decisoes", []string{"Decisions", "Approved"}},
	{"/c/hanko/gates", []string{"Gates", "Pending"}},
	{"/c/hanko/organograma", []string{"Org chart", "Persona org chart"}},
	{"/c/hanko/custos", []string{"Costs and budgets", "Spend per day", "Company budget"}},
	{"/c/hanko/fluxos", []string{"Flows", "Run now", "Every Friday at 07:00"}},
	{"/c/hanko/fluxos/novo", []string{"New flow", "Schedule templates"}},
	{"/c/hanko/em-breve/projetos", []string{"Projects", "coming soon"}},
}

func TestEnglishPages(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, c := range enPages {
		t.Run(c.path, func(t *testing.T) {
			rec := e.get(t, c.path, "Accept-Language", "en-US,en;q=0.9")
			wantStatus(t, rec, http.StatusOK)
			wantContains(t, rec, append(c.want, `<html lang="en"`)...)
			if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Language") || !strings.Contains(v, "Cookie") {
				t.Errorf("Vary = %q", v)
			}
		})
	}
	t.Run("user data is not translated", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/tasks", "Accept-Language", "en")
		wantContains(t, rec, "Docslide: AI-first SDLC no banco")
	})
	t.Run("English formats", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/custos", "Accept-Language", "en")
		wantContains(t, rec, "$")
		wantNotContains(t, rec, "US$")
	})
}

func TestPortugueseIsDefault(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, hdr := range [][]string{nil, {"Accept-Language", "pt-BR,pt;q=0.9,en;q=0.8"}, {"Accept-Language", "fr"}} {
		rec := e.get(t, "/c/hanko/", hdr...)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `<html lang="pt-BR"`, "Painel", "Personas ativas")
		wantNotContains(t, rec, "Dashboard")
	}
}

func TestLangCookieBeatsHeader(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/", "Accept-Language", "en", "Cookie", "hanko_lang=pt-BR")
	wantContains(t, rec, `<html lang="pt-BR"`, "Painel")
	rec = e.get(t, "/c/hanko/", "Accept-Language", "pt-BR", "Cookie", "hanko_lang=en")
	wantContains(t, rec, `<html lang="en"`, "Dashboard")
	// an invalid cookie is ignored and the header decides
	rec = e.get(t, "/c/hanko/", "Accept-Language", "en", "Cookie", "hanko_lang=xx")
	wantContains(t, rec, `<html lang="en"`)
}

func TestLangSwitch(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	post := func(form url.Values, hdr map[string]string) *http.Response {
		h := map[string]string{"Sec-Fetch-Site": "same-origin"}
		for k, v := range hdr {
			h[k] = v
		}
		return e.do(t, req{method: "POST", path: "/idioma", form: form, hdr: h}).Result()
	}
	t.Run("sets the cookie and goes back to the Referer", func(t *testing.T) {
		res := post(url.Values{"lang": {"en"}}, map[string]string{"Referer": "http://example.com/c/hanko/tasks?aba=todas"})
		if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusFound {
			t.Fatalf("status = %d", res.StatusCode)
		}
		if loc := res.Header.Get("Location"); loc != "/c/hanko/tasks?aba=todas" {
			t.Errorf("Location = %q", loc)
		}
		var c *http.Cookie
		for _, x := range res.Cookies() {
			if x.Name == "hanko_lang" {
				c = x
			}
		}
		if c == nil || c.Value != "en" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
			t.Fatalf("cookie = %+v", c)
		}
		if c.Secure {
			t.Error("Secure must follow the config (off here)")
		}
	})
	t.Run("Secure follows the config", func(t *testing.T) {
		s := &server{be: uitest.Sample(), now: uitest.Now, secure: true}
		s.log = testLogger(t)
		mux := http.NewServeMux()
		s.routes(mux)
		e2 := &env{h: mux}
		res := e2.do(t, req{method: "POST", path: "/idioma", form: url.Values{"lang": {"pt-BR"}}, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}}).Result()
		for _, x := range res.Cookies() {
			if x.Name == "hanko_lang" && (!x.Secure || x.Value != "pt-BR") {
				t.Errorf("cookie = %+v", x)
			}
		}
	})
	t.Run("foreign Referer goes to the root", func(t *testing.T) {
		res := post(url.Values{"lang": {"en"}}, map[string]string{"Referer": "http://evil.example/c/hanko/"})
		if loc := res.Header.Get("Location"); loc != "/" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("no Referer goes to the root", func(t *testing.T) {
		res := post(url.Values{"lang": {"en"}}, nil)
		if loc := res.Header.Get("Location"); loc != "/" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("the sign-in page keeps next", func(t *testing.T) {
		res := post(url.Values{"lang": {"en"}}, map[string]string{"Referer": "http://example.com/entrar?next=%2Fc%2Fhanko%2Ftasks"})
		if loc := res.Header.Get("Location"); loc != "/entrar?next=%2Fc%2Fhanko%2Ftasks" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("unknown language is rejected", func(t *testing.T) {
		res := post(url.Values{"lang": {"fr"}}, nil)
		if res.StatusCode < 400 {
			t.Errorf("status = %d", res.StatusCode)
		}
		for _, x := range res.Cookies() {
			if x.Name == "hanko_lang" {
				t.Error("cookie set for an unknown language")
			}
		}
	})
	t.Run("same-origin protection", func(t *testing.T) {
		res := e.do(t, req{method: "POST", path: "/idioma", form: url.Values{"lang": {"en"}}}).Result()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", res.StatusCode)
		}
	})
	t.Run("the switcher is in the page", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/", "Accept-Language", "en")
		wantContains(t, rec, `action="/idioma"`, `name="lang"`, "Language")
	})
}

// The sign-in screen, its errors and the settings page follow the language too, and
// /idioma works before there is a session (the Guard lets it through).
func TestEnglishAuth(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")

	rec := e.get(t, "/entrar", "Accept-Language", "en")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `<html lang="en"`, "Sign in", "Password", "Local sign-in", `action="/idioma"`)

	// choosing the language on the login screen, with no session, returns to it
	rec = e.do(t, req{method: "POST", path: "/idioma", form: url.Values{"lang": {"en"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Referer": "http://example.com/entrar?next=%2Fc%2Fhanko%2Ftasks"}})
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/entrar?next=%2Fc%2Fhanko%2Ftasks" {
		t.Errorf("Location = %q", loc)
	}

	rec = e.do(t, req{method: "POST", path: "/entrar", form: url.Values{"email": {"dono@x.com"}, "password": {"errada"}},
		hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Cookie": "hanko_lang=en"}})
	wantContains(t, rec, "Incorrect email or password.")

	ok := e.login(t, "dono@x.com", "senha-bem-longa", "")
	c := cookieOf(ok)
	if c == nil {
		t.Fatal("no session")
	}
	rec = e.withCookie(t, c, req{method: "GET", path: "/c/hanko/configuracoes", hdr: map[string]string{"Accept-Language": "en"}})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `<html lang="en"`, "API keys", "Create key", "Sign out")
}
