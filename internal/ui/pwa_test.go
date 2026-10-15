package ui

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/ui/uitest"
)

type manifestDoc struct {
	Name, ShortName, StartURL, Scope, Display, ThemeColor, BackgroundColor string
	Icons                                                                  []struct{ Src, Sizes, Type, Purpose string }
}

func readManifest(t *testing.T, body []byte) manifestDoc {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("manifest não é JSON: %v", err)
	}
	var m manifestDoc
	m.Name, _ = raw["name"].(string)
	m.ShortName, _ = raw["short_name"].(string)
	m.StartURL, _ = raw["start_url"].(string)
	m.Scope, _ = raw["scope"].(string)
	m.Display, _ = raw["display"].(string)
	m.ThemeColor, _ = raw["theme_color"].(string)
	m.BackgroundColor, _ = raw["background_color"].(string)
	icons, _ := raw["icons"].([]any)
	for _, i := range icons {
		o, _ := i.(map[string]any)
		str := func(k string) string { v, _ := o[k].(string); return v }
		m.Icons = append(m.Icons, struct{ Src, Sizes, Type, Purpose string }{str("src"), str("sizes"), str("type"), str("purpose")})
	}
	return m
}

func TestManifestServedAndValid(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/manifest.webmanifest")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	m := readManifest(t, rec.Body.Bytes())
	if m.Name != "Hanko" || m.ShortName == "" || m.StartURL != "/" || m.Scope != "/" || m.Display != "standalone" {
		t.Errorf("campos do manifest: %+v", m)
	}
	hex := regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	if !hex.MatchString(m.ThemeColor) || !hex.MatchString(m.BackgroundColor) {
		t.Errorf("cores inválidas: %q %q", m.ThemeColor, m.BackgroundColor)
	}
	var has192, has512, maskable bool
	for _, ic := range m.Icons {
		w, h, ok := strings.Cut(ic.Sizes, "x")
		if !ok || ic.Type != "image/png" || !strings.HasPrefix(ic.Src, "/static/icons/") {
			t.Errorf("ícone mal declarado: %+v", ic)
			continue
		}
		// o ícone existe, é servido e tem o tamanho declarado
		ir := e.get(t, ic.Src)
		wantStatus(t, ir, http.StatusOK)
		cfg, err := png.DecodeConfig(bytes.NewReader(ir.Body.Bytes()))
		if err != nil {
			t.Errorf("%s não é PNG: %v", ic.Src, err)
			continue
		}
		if strconv.Itoa(cfg.Width) != w || strconv.Itoa(cfg.Height) != h {
			t.Errorf("%s: declarado %s, real %dx%d", ic.Src, ic.Sizes, cfg.Width, cfg.Height)
		}
		has192 = has192 || (ic.Sizes == "192x192" && ic.Purpose == "any")
		has512 = has512 || (ic.Sizes == "512x512" && ic.Purpose == "any")
		maskable = maskable || ic.Purpose == "maskable"
	}
	if !has192 || !has512 || !maskable {
		t.Errorf("faltam ícones 192/512/maskable: %+v", m.Icons)
	}
	if _, err := os.Stat("static/icons/apple-touch-icon.png"); err != nil {
		t.Error(err)
	}
}

func TestPWARoutesPublicWithoutSession(t *testing.T) {
	e := newAuthEnv(t, AuthDeps{Provider: "local"})
	e.fa.addUser("dono@x.com", "Dona", "senha-bem-longa")
	for _, p := range []string{"/manifest.webmanifest", "/sw.js", "/offline", "/static/icons/icon-192.png", "/static/icons/icon-512.png", "/static/icons/icon-maskable-512.png", "/static/icons/apple-touch-icon.png"} {
		wantStatus(t, e.get(t, p), http.StatusOK)
	}
	// o offline é só uma casca: sem sessão não vaza nada e as demais páginas continuam protegidas
	wantStatus(t, e.get(t, "/c/hanko/tasks"), http.StatusSeeOther)
}

func TestServiceWorkerHeadersAndVersion(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/sw.js")
	wantStatus(t, rec, http.StatusOK)
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
	if h.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q", h.Get("Cache-Control"))
	}
	if h.Get("Service-Worker-Allowed") != "" {
		t.Error("o escopo é /; Service-Worker-Allowed não deve ser enviado")
	}
	body := rec.Body.String()
	if strings.Contains(body, "__HANKO_VERSION__") || !strings.Contains(body, `var VERSION = "test";`) {
		t.Errorf("versão não injetada:\n%s", body[:200])
	}
	if swVersion(`1.2"; evil()//`) != "1.2evil" || swVersion("") != "dev" {
		t.Error("swVersion deve manter só caracteres seguros")
	}
}

// listFrom extrai os literais de string de `var NAME = [ ... ];` do sw.js.
func listFrom(t *testing.T, src, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)var ` + name + ` = (?:OFFLINE_PATHS\.concat\()?\[(.*?)\]\)?;`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("lista %s não encontrada no sw.js", name)
	}
	var out []string
	for _, l := range regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, l[1])
	}
	return out
}

func TestServiceWorkerExclusionRules(t *testing.T) {
	b, err := os.ReadFile("pwa/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	prefixes := listFrom(t, src, "NEVER_CACHE_PREFIXES")
	for _, want := range []string{"/api/", "/auth/", "/convite/", "/mcp", "/memory-mcp", "/metrics", "/entrar", "/sair"} {
		found := false
		for _, p := range prefixes {
			found = found || p == want
		}
		if !found {
			t.Errorf("NEVER_CACHE_PREFIXES sem %q: %v", want, prefixes)
		}
	}
	if suf := listFrom(t, src, "NEVER_CACHE_SUFFIXES"); len(suf) != 1 || suf[0] != "/sse" {
		t.Errorf("NEVER_CACHE_SUFFIXES = %v", suf)
	}
	// só GET e SSE ficam de fora por método/cabeçalho
	for _, frag := range []string{`request.method !== "GET"`, `text/event-stream`} {
		if !strings.Contains(src, frag) {
			t.Errorf("sw.js sem a regra %q", frag)
		}
	}
	// o pré-cache só tem a página offline e assets estáticos: nada de HTML de aplicação
	for _, p := range listFrom(t, src, "OFFLINE_PATHS") {
		if !strings.HasPrefix(p, "/offline?hl=") {
			t.Errorf("OFFLINE_PATHS inesperado: %q", p)
		}
	}
	for _, p := range listFrom(t, src, "PRECACHE") { // só o trecho literal do array concat
		if !strings.HasPrefix(p, "/static/") {
			t.Errorf("PRECACHE inesperado: %q", p)
		}
	}
	// nada é guardado fora de /static/: cache.put só aparece no ramo estático
	if n := strings.Count(src, ".put("); n != 1 {
		t.Errorf("cache.put deve aparecer uma vez (assets estáticos), apareceu %d", n)
	}
	i := strings.Index(src, ".put(")
	if !strings.Contains(src[:i], `url.pathname.indexOf("/static/") === 0`) {
		t.Error("cache.put fora do ramo /static/")
	}
	// os caminhos do pré-cache existem (css/js/favicon/htmx e a página offline)
	for _, p := range []string{"static/hanko.css", "static/hanko.js", "static/favicon.svg", "static/vendor/htmx.min.js"} {
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
}

func TestOfflinePageI18n(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	pt := e.get(t, "/offline?hl=pt-BR")
	wantStatus(t, pt, http.StatusOK)
	if !strings.Contains(pt.Body.String(), "Sem conexão") || !strings.Contains(pt.Body.String(), `lang="pt-BR"`) {
		t.Errorf("offline pt:\n%s", pt.Body.String())
	}
	en := e.get(t, "/offline?hl=en")
	if !strings.Contains(en.Body.String(), "Try again") || !strings.Contains(en.Body.String(), `lang="en"`) {
		t.Errorf("offline en:\n%s", en.Body.String())
	}
	if en.Header().Get("Cache-Control") != "no-cache" {
		t.Error("cabeçalho de cache ausente")
	}
}

func TestHeadHasPWATags(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, p := range []string{"/c/hanko/", "/bem-vindo", "/offline"} {
		body := e.get(t, p).Body.String()
		for _, tag := range []string{
			`<link rel="manifest" href="/manifest.webmanifest"`,
			`<meta name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)"`,
			`<meta name="theme-color" content="#0c0a09" media="(prefers-color-scheme: dark)"`,
			`<link rel="apple-touch-icon" href="/static/icons/apple-touch-icon.png"`,
		} {
			if !strings.Contains(body, tag) {
				t.Errorf("%s: head sem %s", p, tag)
			}
		}
	}
	js, err := os.ReadFile("static/hanko.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "isSecureContext") || !strings.Contains(string(js), `register("/sw.js"`) {
		t.Error("hanko.js deve registrar o SW só em contexto seguro")
	}
}
