package ui

import "net/http"

// sendPageCSP só deixa a página carregar o próprio script e estilo: sem script de terceiros, sem formulário que sai
// do site (ADR-0069 §6). A chave do envio fica no fragmento, que nem o nginx vê.
const sendPageCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// sendPage serve a página de recebimento de um envio. O HTML é o mesmo para todo id: o id vem do caminho no
// navegador, e nada da página depende do servidor saber qual envio é.
func sendPage(w http.ResponseWriter, _ *http.Request) {
	body, err := staticFS.ReadFile("static/send.html")
	if err != nil {
		http.Error(w, "página indisponível", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", sendPageCSP)
	_, _ = w.Write(body)
}
