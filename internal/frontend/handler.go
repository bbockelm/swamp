package frontend

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// NewSPAHandler returns an http.Handler that serves the embedded frontend.
// It resolves Next.js static-export dynamic routes (e.g. [id] → _) and
// falls back to index.html for client-side routing.
func NewSPAHandler() http.Handler {
	fsys, _ := DistFS()
	if fsys == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Frontend not embedded in this build", http.StatusNotFound)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath := strings.TrimPrefix(r.URL.Path, "/")
		if urlPath == "" {
			urlPath = "index.html"
		}

		// 1. Try the exact file (static assets, known pages)
		if fileExists(fsys, urlPath) {
			serveFile(w, r, fsys, urlPath)
			return
		}

		// 2. Try with .html extension (e.g. /projects → projects.html)
		if !strings.Contains(path.Base(urlPath), ".") {
			if fileExists(fsys, urlPath+".html") {
				serveFile(w, r, fsys, urlPath+".html")
				return
			}
		}

		// 3. Resolve Next.js dynamic routes: replace non-matching segments with _
		if resolved := resolveDynamicRoute(fsys, urlPath); resolved != "" {
			serveFile(w, r, fsys, resolved)
			return
		}

		// 4. SPA fallback: serve index.html for client-side routing
		serveFile(w, r, fsys, "index.html")
	})
}

// resolveDynamicRoute attempts to match a URL path against Next.js static
// export dynamic route files. For [param] routes, Next.js generates files
// using _ as the placeholder (e.g. projects/[id] → projects/_.html).
//
// For a path like "projects/UUID/analyses/UUID2", it tries replacing
// non-matching segments with _ and looks for the corresponding .html file.
// It also handles .txt suffixes for RSC (React Server Component) payloads.
func resolveDynamicRoute(fsys fs.FS, urlPath string) string {
	// Handle .txt suffix (RSC payload requests like /projects/UUID.txt)
	suffix := ".html"
	if strings.HasSuffix(urlPath, ".txt") {
		suffix = ".txt"
		urlPath = strings.TrimSuffix(urlPath, ".txt")
	}

	segments := strings.Split(urlPath, "/")
	if len(segments) == 0 {
		return ""
	}

	// Build the resolved path by checking each directory level.
	// For each segment except the last, check if a literal directory exists;
	// if not, try _ as the dynamic placeholder.
	// For the last segment, check for the file with the appropriate suffix.
	resolved := make([]string, 0, len(segments))

	for i, seg := range segments {
		isLast := i == len(segments)-1

		if isLast {
			// Last segment: try literal, then wildcard _
			for _, candidate := range []string{seg, "_"} {
				filePath := strings.Join(append(resolved, candidate), "/") + suffix
				if fileExists(fsys, filePath) {
					return filePath
				}
			}
			return ""
		}

		// Directory segment: try literal, then wildcard _
		literalDir := strings.Join(append(resolved, seg), "/")
		wildcardDir := strings.Join(append(resolved, "_"), "/")

		if dirExists(fsys, literalDir) {
			resolved = append(resolved, seg)
		} else if dirExists(fsys, wildcardDir) {
			resolved = append(resolved, "_")
		} else {
			return ""
		}
	}

	return ""
}

// fileExists checks if a file exists in the given filesystem.
func fileExists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return !stat.IsDir()
}

// dirExists checks if a directory exists in the given filesystem.
func dirExists(fsys fs.FS, name string) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// serveFile writes the contents of the named file from fsys to the response.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	http.ServeFileFS(w, r, fsys, name)
}
