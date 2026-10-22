package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

const showcasePath = "/c/hanko/plugins/showcase/painel"

func showcaseEnv(t *testing.T, role domain.MemberRole) (*env, *uitest.PluginsFake) {
	t.Helper()
	return newPluginsEnv(t, role, func(f *uitest.PluginsFake) { f.WithShowcase() })
}

func bridgePost(e *env, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	rq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rq.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		rq.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	return rec
}

func TestPluginTabInSidebarAndHostPage(t *testing.T) {
	e, f := showcaseEnv(t, domain.MemberMember)
	rec := e.get(t, "/c/hanko/")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `href="`+showcasePath+`"`, "Painel Showcase")

	rec = e.get(t, showcasePath)
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	// iframe isolado: sandbox só com allow-scripts (nunca allow-same-origin) e a ponte como arquivo próprio
	wantContains(t, rec, `id="plugin-frame"`, `sandbox="allow-scripts"`, `src="`+showcasePath+`/frame"`, `data-bridge="`+showcasePath+`/ponte"`,
		"/static/plugin-frame.js", "referrerpolicy=\"no-referrer\"")
	if strings.Contains(body, "allow-same-origin") || strings.Contains(body, "Olá do plugin") {
		t.Fatal("a página hospedeira não pode conter o HTML do plugin nem allow-same-origin")
	}

	// sem a capacidade pages: sem item no menu e a página responde 404
	if _, err := f.SetPluginCapability(t.Context(), "showcase", domain.CapPages, false); err != nil {
		t.Fatal(err)
	}
	rec = e.get(t, "/c/hanko/")
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, "Painel Showcase")
	wantStatus(t, e.get(t, showcasePath), http.StatusNotFound)
	wantStatus(t, e.get(t, showcasePath+"/frame"), http.StatusNotFound)
	wantStatus(t, e.get(t, "/c/hanko/plugins/showcase/inexistente"), http.StatusNotFound)
}

func TestPluginFrameIsolation(t *testing.T) {
	e, _ := showcaseEnv(t, domain.MemberViewer)
	rec := e.get(t, showcasePath+"/frame")
	wantStatus(t, rec, http.StatusOK)
	h := rec.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'unsafe-inline'", "sandbox allow-scripts", "frame-ancestors 'self'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP sem %q: %s", want, csp)
		}
	}
	// nada de rede nem de allow-same-origin
	for _, bad := range []string{"connect-src", "allow-same-origin", "http:", "https:", "*"} {
		if strings.Contains(csp, bad) {
			t.Errorf("CSP não pode conter %q: %s", bad, csp)
		}
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Errorf("cabeçalhos = %v", h)
	}
	// o documento traz a ponte e, depois dela, o HTML do plugin
	body := rec.Body.String()
	shim, page := strings.Index(body, "window.hanko="), strings.Index(body, "Olá do plugin")
	if shim < 0 || page < 0 || shim > page || !strings.HasPrefix(body, "<!doctype html>") {
		t.Fatalf("documento = %.300s", body)
	}
	wantContains(t, rec, `"company":"hanko"`, `"plugin":"showcase"`, `parent.postMessage`)
}

func TestPluginBridge(t *testing.T) {
	e, f := showcaseEnv(t, domain.MemberMember)
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	rec := bridgePost(e, showcasePath+"/ponte", `{"method":"registrar","params":{"a":1}}`, same)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `"result":{"total":1}`)
	if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("cabeçalhos = %v", rec.Header())
	}
	if got := f.BridgeCalls(); len(got) != 1 || got[0] != "registrar" {
		t.Fatalf("chamadas = %v", got)
	}
	// o plugin recusa: o erro volta como JSON, sem derrubar a página
	rec = bridgePost(e, showcasePath+"/ponte", `{"method":"quebra"}`, same)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, `"error":"ponte: recusado pelo plugin"`)
	// JSON inválido
	wantStatus(t, bridgePost(e, showcasePath+"/ponte", `não é json`, same), http.StatusBadRequest)
	// requisição vinda de outra origem (a página do plugin tem origem opaca) é recusada
	rec = bridgePost(e, showcasePath+"/ponte", `{"method":"registrar"}`, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rec.Code == http.StatusOK {
		t.Fatalf("cross-site não pode chamar a ponte: %d", rec.Code)
	}
	rec = bridgePost(e, showcasePath+"/ponte", `{"method":"registrar"}`, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("sem cabeçalho de origem não pode chamar a ponte: %d", rec.Code)
	}
	// sem a capacidade, a ponte fecha
	if _, err := f.SetPluginCapability(t.Context(), "showcase", domain.CapPages, false); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, bridgePost(e, showcasePath+"/ponte", `{"method":"registrar"}`, same), http.StatusNotFound)
	if got := f.BridgeCalls(); len(got) != 1 {
		t.Fatalf("a chamada sem capacidade não pode chegar ao plugin: %v", got)
	}
}

func TestPluginCapabilitiesOwnerUI(t *testing.T) {
	e, f := showcaseEnv(t, domain.MemberOwner)
	detail := pluginsPath + "/showcase"
	rec := e.get(t, detail)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Capacidades concedidas", detail+"/instalacao/revogar", "Abas na barra lateral", "Revogar",
		"Gatilhos de fluxo", "Novo pedido", "Providers de ambiente", "plugin:showcase/principal", "Fontes de skills", "plugin://showcase/catalogo", "Painel Showcase")

	rec = e.post(t, detail+"/instalacao/revogar", url.Values{"cap": {domain.CapPages}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != detail+"?ok=revogada" {
		t.Fatalf("redirect = %s", loc)
	}
	rec = e.get(t, detail+"?ok=revogada")
	wantContains(t, rec, "Capacidade revogada", detail+"/instalacao/conceder", "Conceder")
	rec = e.post(t, detail+"/instalacao/conceder", url.Values{"cap": {domain.CapPages}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != detail+"?ok=concedida" {
		t.Fatalf("redirect = %s", loc)
	}
	// capacidade desconhecida volta com o erro, sem 500
	rec = e.post(t, detail+"/instalacao/revogar", url.Values{"cap": {"nao.existe"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "erro=") {
		t.Fatalf("redirect = %s", loc)
	}
	var grants int
	for _, a := range f.Actions {
		if strings.HasPrefix(a, "grant ") || strings.HasPrefix(a, "revoke ") {
			grants++
		}
	}
	if grants != 2 {
		t.Fatalf("ações = %v", f.Actions)
	}
}

func TestPluginCapabilitiesAreOwnerOnly(t *testing.T) {
	e, _ := newPluginsEnv(t, domain.MemberAdmin, func(f *uitest.PluginsFake) { f.WithShowcase(); f.Owner = false })
	detail := pluginsPath + "/showcase"
	rec := e.get(t, detail)
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, "Capacidades concedidas", "/instalacao/revogar")
	rec = e.post(t, detail+"/instalacao/revogar", url.Values{"cap": {domain.CapPages}}, true)
	if rec.Code == http.StatusOK || rec.Code == http.StatusSeeOther {
		t.Fatalf("admin da empresa não revoga: %d", rec.Code)
	}
}

func TestPluginTriggerLinksUI(t *testing.T) {
	e, f := showcaseEnv(t, domain.MemberAdmin)
	detail := pluginsPath + "/showcase"
	rec := e.get(t, detail)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, detail+"/gatilhos/novo-pedido/ligar", "Ligar a um fluxo", "Nenhum fluxo ligado")

	flows, _ := f.ListFlows(t.Context(), domain.ID{})
	if len(flows) == 0 {
		t.Skip("o Fake de amostra não tem fluxos")
	}
	slug := flows[0].Slug
	rec = e.post(t, detail+"/gatilhos/novo-pedido/ligar", url.Values{"flow": {slug}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != detail+"?ok=gatilho-ligado" {
		t.Fatalf("redirect = %s", loc)
	}
	if got := f.Links(); len(got) != 1 || got[0] != "showcase/novo-pedido/"+slug {
		t.Fatalf("ligações = %v", got)
	}
	rec = e.get(t, detail)
	wantContains(t, rec, "Desligar", detail+"/gatilhos/novo-pedido/desligar?flow="+url.QueryEscape(slug))
	rec = e.post(t, detail+"/gatilhos/novo-pedido/desligar?flow="+url.QueryEscape(slug), url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if got := f.Links(); len(got) != 0 {
		t.Fatalf("ligações depois de desligar = %v", got)
	}
	// ação desconhecida não faz nada
	rec = e.post(t, detail+"/gatilhos/novo-pedido/apagar", url.Values{"flow": {slug}}, false)
	if rec.Code == http.StatusSeeOther && len(f.Links()) != 0 {
		t.Fatal("ação desconhecida não pode ligar nada")
	}
}

func TestPluginTriggerLinksAdminOnly(t *testing.T) {
	e, f := showcaseEnv(t, domain.MemberMember)
	detail := pluginsPath + "/showcase"
	rec := e.get(t, detail)
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, "Ligar a um fluxo")
	rec = e.post(t, detail+"/gatilhos/novo-pedido/ligar", url.Values{"flow": {"x"}}, false)
	if rec.Code == http.StatusSeeOther {
		t.Fatalf("membro não liga gatilho: %d", rec.Code)
	}
	if len(f.Links()) != 0 {
		t.Fatal("nada deveria ter sido ligado")
	}
}
