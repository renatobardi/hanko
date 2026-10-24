package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// A página de recebimento não guarda nada no navegador nem no cache, não manda referrer e não carrega script de
// terceiros (ADR-0069 §6): os cabeçalhos são o que impede isso, então o teste confere cada um.
func TestPaginaDeEnvioTemCabecalhosDeSeguranca(t *testing.T) {
	rec := httptest.NewRecorder()
	sendPage(rec, httptest.NewRequest("GET", "/s/AAAAAAAAAAAAAAAAAAAAAA", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, quer 200", rec.Code)
	}
	h := rec.Header()
	quer := map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
		"Content-Type":           "text/html; charset=utf-8",
	}
	for nome, valor := range quer {
		if got := h.Get(nome); got != valor {
			t.Errorf("%s = %q, quer %q", nome, got, valor)
		}
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "http") {
		t.Errorf("CSP = %q: script só do próprio site e sem origem externa", csp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `src="/static/send.js"`) || strings.Contains(body, "<script>") {
		t.Error("a página deve carregar o script do próprio site, e não embutir script")
	}
}
