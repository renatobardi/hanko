package ui

import (
	"bytes"
	"embed"
	"net/http"
	"strings"

	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// PWA mínimo: manifest + service worker (instalável, sem cache de dados). Os arquivos ficam fora de
// static/ porque o sw.js precisa do escopo "/" e é versionado pelo servidor.
//
//go:embed pwa
var pwaFS embed.FS

// Caminhos públicos do PWA (o Guard em internal/auth os libera sem sessão).
const (
	manifestPath = "/manifest.webmanifest"
	swPath       = "/sw.js"
	offlinePath  = "/offline"
)

// swVersion deixa só caracteres seguros num literal JS (a versão vem do build, mas não confiamos).
func swVersion(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '+':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "dev"
	}
	return b.String()
}

func (s *server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	b, err := pwaFS.ReadFile("pwa/manifest.webmanifest")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// handleSW serves the worker with the build version baked in, so each deploy yields a different
// script (the browser updates it and the worker drops the old static cache). no-cache: the worker
// itself must always be revalidated. No Service-Worker-Allowed: the scope stays "/".
func (s *server) handleSW(w http.ResponseWriter, _ *http.Request) {
	b, err := pwaFS.ReadFile("pwa/sw.js")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	b = bytes.ReplaceAll(b, []byte("__HANKO_VERSION__"), []byte(swVersion(s.version)))
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// handleOffline is the page the worker shows when navigation fails without network. Public and
// free of user data; ?hl=en|pt-BR picks the language so the worker can precache both.
func (s *server) handleOffline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if l, ok := i18n.Parse(r.URL.Query().Get("hl")); ok {
		ctx = i18n.WithLang(ctx, l)
	}
	var buf bytes.Buffer
	if err := views.Offline(s.version).Render(ctx, &buf); err != nil {
		http.Error(w, "offline", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(buf.Bytes())
}
