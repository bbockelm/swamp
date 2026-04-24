// Package docs embeds and serves rendered Markdown documentation.
package docs

import (
	"bytes"
	"embed"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/yuin/goldmark"
	gmext "github.com/yuin/goldmark/extension"
	gmparser "github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

//go:embed all:content
var content embed.FS

// ContentFS returns the embedded content filesystem rooted at "content/".
func ContentFS() (fs.FS, error) {
	return fs.Sub(content, "content")
}

var md = goldmark.New(
	goldmark.WithExtensions(gmext.GFM, gmext.Table),
	goldmark.WithParserOptions(gmparser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
)

// pageHTML wraps rendered body HTML in the SWAMP documentation shell.
const pageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>%s — SWAMP Docs</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; }
    :root {
      --brand: #2563eb;
      --brand-light: #eff6ff;
      --text: #111827;
      --muted: #6b7280;
      --border: #e5e7eb;
      --code-bg: #f9fafb;
    }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
                   "Helvetica Neue", Arial, sans-serif;
      font-size: 16px; line-height: 1.7;
      color: var(--text); background: #fff;
      margin: 0; padding: 0;
    }
    header {
      background: #0f172a; border-bottom: 1px solid #1e293b;
      padding: 0.75rem 2rem;
      display: flex; align-items: center; gap: 1rem;
    }
    header a { color: #93c5fd; text-decoration: none; font-size: 0.875rem; }
    header a:hover { text-decoration: underline; }
    header .sep { color: #334155; }
    header .title { color: #f1f5f9; font-size: 0.9rem; font-weight: 600; }
    .layout {
      display: flex; max-width: 1100px;
      margin: 0 auto; padding: 2.5rem 1.5rem; gap: 3rem;
    }
    article { flex: 1; min-width: 0; }
    article h1 { font-size: 2rem; font-weight: 800; margin: 0 0 0.5rem; line-height: 1.2; }
    article h2 {
      font-size: 1.35rem; font-weight: 700;
      margin: 2.5rem 0 0.75rem;
      padding-bottom: 0.4rem; border-bottom: 1px solid var(--border);
    }
    article h3 { font-size: 1.1rem; font-weight: 600; margin: 1.75rem 0 0.5rem; }
    article h4 { font-size: 1rem; font-weight: 600; margin: 1.25rem 0 0.4rem; }
    article p { margin: 0.75rem 0; }
    article ul, article ol { padding-left: 1.5rem; margin: 0.75rem 0; }
    article li { margin: 0.3rem 0; }
    article a { color: var(--brand); text-decoration: none; }
    article a:hover { text-decoration: underline; }
    article code {
      background: var(--code-bg); border: 1px solid var(--border);
      border-radius: 4px; padding: 0.15em 0.4em;
      font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
      font-size: 0.875em;
    }
    article pre {
      background: var(--code-bg); border: 1px solid var(--border);
      border-radius: 6px; padding: 1rem 1.25rem;
      overflow-x: auto; margin: 1rem 0;
    }
    article pre code { background: none; border: none; padding: 0; }
    article img {
      max-width: 100%; height: auto;
      border: 1px solid var(--border); border-radius: 8px;
      margin: 1rem 0; box-shadow: 0 1px 4px rgba(0,0,0,0.08);
      display: block;
    }
    article hr { border: none; border-top: 1px solid var(--border); margin: 2.5rem 0; }
    article blockquote {
      border-left: 3px solid var(--brand); background: var(--brand-light);
      margin: 1rem 0; padding: 0.75rem 1.25rem; border-radius: 0 6px 6px 0;
    }
    article blockquote p { margin: 0; }
    @media (max-width: 768px) {
      .layout { padding: 1.5rem 1rem; }
    }
  </style>
</head>
<body>
  <header>
    <a href="/docs">← Docs</a>
    <span class="sep">|</span>
    <span class="title">%s</span>
  </header>
  <div class="layout">
    <article>%s</article>
  </div>
</body>
</html>
`

// Handler returns an http.Handler that serves the documentation.
//
//   GET /docs/tutorials/onboarding     — rendered HTML for the onboarding tutorial
//   GET /docs/tutorials/images/<file>  — images embedded alongside the tutorial
func Handler() http.Handler {
	fsys, err := ContentFS()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "docs unavailable", http.StatusInternalServerError)
		})
	}

	mux := http.NewServeMux()

	// Serve embedded image assets.
	mux.Handle("/docs/tutorials/images/",
		http.StripPrefix("/docs/tutorials/images/",
			http.FileServer(http.FS(mustSub(fsys, "tutorials/images")))))

	// Render and serve each tutorial Markdown file.
	mux.HandleFunc("/docs/tutorials/onboarding", func(w http.ResponseWriter, r *http.Request) {
		serveTutorial(w, r, fsys, "tutorials/onboarding.md", "Getting Started Tutorial")
	})

	return mux
}

func serveTutorial(w http.ResponseWriter, r *http.Request, fsys fs.FS, path, title string) {
	src, err := fs.ReadFile(fsys, path)
	if err != nil {
		http.Error(w, "tutorial not found", http.StatusNotFound)
		return
	}

	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	// Rewrite relative image paths so they resolve from the served URL.
	// goldmark emits e.g. <img src="images/foo.jpg"> — prefix with the tutorial base.
	body := strings.ReplaceAll(buf.String(),
		`src="images/`, `src="/docs/tutorials/images/`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, pageHTML, html.EscapeString(title), html.EscapeString(title), body)
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("docs: fs.Sub(%q): %v", dir, err))
	}
	return sub
}
