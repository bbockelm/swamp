package frontend

import (
	"io/fs"
	"net/http"
	"strings"
)

// NewSPAHandler returns an http.Handler that serves the embedded frontend.
// All non-file requests fall back to index.html for client-side routing.
func NewSPAHandler() http.Handler {
	fsys, _ := DistFS()
	if fsys == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Frontend not embedded in this build", http.StatusNotFound)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Try the exact file first
		if fileExists(fsys, path) {
			serveFile(w, r, fsys, path)
			return
		}

		// SPA fallback: serve index.html for client-side routing
		serveFile(w, r, fsys, "index.html")
	})
}

// fileExists checks if a file exists in the given filesystem.
func fileExists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return !stat.IsDir()
}

// serveFile writes the contents of the named file from fsys to the response.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	http.ServeFileFS(w, r, fsys, name)
}
