package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var staticFS embed.FS

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
