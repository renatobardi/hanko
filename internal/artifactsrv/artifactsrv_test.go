package artifactsrv

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestDisposition(t *testing.T) {
	for _, c := range []struct {
		mime, name string
		prefix     string
	}{
		{"text/plain", "a.txt", "inline"},
		{"text/markdown", "a.md", "inline"},
		{"image/png", "a.png", "inline"},
		{"image/svg+xml", "a.svg", "attachment"},
		{"text/html", "a.html", "attachment"},
		{"application/pdf", "a.pdf", "attachment"},
		{"application/json", "a.json", "attachment"},
	} {
		got := Disposition(domain.Artifact{Mime: c.mime, Name: c.name})
		if !strings.HasPrefix(got, c.prefix) {
			t.Errorf("%s: %q, quer prefixo %s", c.mime, got, c.prefix)
		}
	}
	// o nome nunca quebra o cabeçalho
	got := Disposition(domain.Artifact{Mime: "text/plain", Name: "a\"b\r\nX: 1.txt"})
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("injeção de cabeçalho: %q", got)
	}
}

func TestServe(t *testing.T) {
	rec := httptest.NewRecorder()
	Serve(rec, domain.Artifact{Mime: "text/markdown", Name: "n.md", SizeBytes: 3, SHA256: "abc"}, []byte("# x"))
	h := rec.Header()
	if rec.Code != 200 || rec.Body.String() != "# x" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if h.Get("Content-Type") != "text/markdown; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || h.Get("Content-Length") != "3" {
		t.Errorf("cabeçalhos: %v", h)
	}
}

// ServeAs com download força attachment até nos tipos que abrem no navegador, sem perder os cabeçalhos de segurança.
func TestServeAsDownload(t *testing.T) {
	a := domain.Artifact{Name: "nota.md", Mime: "text/markdown", SizeBytes: 4, SHA256: "ab"}
	inline := httptest.NewRecorder()
	ServeAs(inline, a, []byte("# oi"), false)
	if got := inline.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("sem download: %q", got)
	}
	dl := httptest.NewRecorder()
	ServeAs(dl, a, []byte("# oi"), true)
	if got := dl.Header().Get("Content-Disposition"); got != `attachment; filename=nota.md` {
		t.Errorf("com download: %q", got)
	}
	for _, h := range []string{"X-Content-Type-Options", "Content-Security-Policy", "Cross-Origin-Resource-Policy"} {
		if dl.Header().Get(h) == "" {
			t.Errorf("sem o cabeçalho %s", h)
		}
	}
	if dl.Body.String() != "# oi" {
		t.Errorf("corpo = %q", dl.Body.String())
	}
}
