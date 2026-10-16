package ui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

//go:embed static
var staticFS embed.FS

// The base images (distroless) ship no /etc/mime.types and Go's built-in table
// has no .woff2: without this the Inter files would go out as application/octet-stream.
func init() {
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

// staticHandler serves the embedded assets under /static/. Assets are
// versioned by query string in the layout, so they may be cached for a day.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// embed guarantees the directory exists; reaching this is a build error.
		return http.NotFoundHandler()
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fileServer.ServeHTTP(w, r)
	})
}
