package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var content embed.FS

// Handler serves immutable Vite assets and falls back to index.html for
// client-side routes. Reserved backend namespaces are never handled here.
func Handler() http.Handler {
	dist, err := fs.Sub(content, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		for _, prefix := range []string{"/api", "/mcp", "/v1"} {
			if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
				http.NotFound(w, r)
				return
			}
		}
		name := strings.TrimPrefix(clean, "/")
		if name == "" {
			name = "index.html"
		}
		if info, statErr := fs.Stat(dist, name); statErr == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		index, readErr := fs.ReadFile(dist, "index.html")
		if readErr != nil {
			http.Error(w, "embedded UI unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(index)))
	})
}
