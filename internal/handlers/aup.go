package handlers

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/yuin/goldmark"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

// GetPublicAUP serves the current AUP as a public HTML page at /aup.
// Redirects to the versioned URL /aup-v{version}.
func (h *Handler) GetPublicAUP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	version := h.getAUPVersion(ctx)
	http.Redirect(w, r, fmt.Sprintf("/aup-v%s", version), http.StatusFound)
}

// GetPublicAUPVersioned serves the AUP for a specific version as a public HTML page.
// Returns 404 if the requested version doesn't match the current version.
func (h *Handler) GetPublicAUPVersioned(w http.ResponseWriter, r *http.Request) {
	requestedVersion := chi.URLParam(r, "version")
	ctx := r.Context()

	currentVersion := h.getAUPVersion(ctx)
	if requestedVersion != currentVersion {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		if _, err := fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>AUP Not Found</title></head>
<body style="font-family:system-ui,sans-serif;max-width:700px;margin:40px auto;padding:0 20px">
<h1>AUP Version Not Found</h1>
<p>Version %q is not the current acceptable use policy.</p>
<p><a href="/aup">View the current policy</a></p>
</body></html>`, html.EscapeString(requestedVersion)); err != nil {
			log.Error().Err(err).Msg("Failed to write AUP not-found response")
		}
		return
	}

	text := h.getAUPText(ctx)
	if text == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		if _, err := fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>AUP Not Configured</title></head>
<body style="font-family:system-ui,sans-serif;max-width:700px;margin:40px auto;padding:0 20px">
<h1>Acceptable Use Policy</h1>
<p>No acceptable use policy has been configured yet.</p>
</body></html>`); err != nil {
			log.Error().Err(err).Msg("Failed to write AUP not-configured response")
		}
		return
	}

	// Get the last-updated timestamp from the aup_text config entry.
	var updatedAt time.Time
	if _, ts, err := h.queries.GetAppConfigWithTimestamp(ctx, "aup_text"); err == nil {
		updatedAt = ts
	}
	updatedStr := "unknown"
	if !updatedAt.IsZero() {
		updatedStr = updatedAt.Format("2 January 2006")
	}

	canonicalURL := fmt.Sprintf("%s/aup-v%s", h.cfg.BaseURL, currentVersion)

	// Render AUP markdown to HTML.
	md := goldmark.New(goldmark.WithRendererOptions(gmhtml.WithHardWraps()))
	var bodyHTML bytes.Buffer
	if err := md.Convert([]byte(text), &bodyHTML); err != nil {
		// Fall back to escaped plain text on parse error.
		bodyHTML.Reset()
		bodyHTML.WriteString("<p>")
		bodyHTML.WriteString(html.EscapeString(text))
		bodyHTML.WriteString("</p>\n")
	}

	// Append the self-referential footer.
	fmt.Fprintf(&bodyHTML,
		`<hr><p><em>This is the acceptable use policy v%s. This text was last updated on %s and can be found at <a href="%s">%s</a>.</em></p>`,
		html.EscapeString(currentVersion),
		html.EscapeString(updatedStr),
		html.EscapeString(canonicalURL),
		html.EscapeString(canonicalURL),
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Acceptable Use Policy — SWAMP</title>
<style>
  body { font-family: system-ui, -apple-system, sans-serif; max-width: 700px; margin: 40px auto; padding: 0 20px; line-height: 1.6; color: #1a1a1a; }
  h1 { border-bottom: 1px solid #e5e7eb; padding-bottom: 12px; }
  h2, h3, h4 { margin-top: 1.5em; }
  p { margin: 1em 0; }
  ul, ol { margin: 1em 0; padding-left: 2em; }
  li { margin: 0.3em 0; }
  code { background: #f3f4f6; padding: 2px 5px; border-radius: 3px; font-size: 0.9em; }
  pre { background: #f3f4f6; padding: 12px; border-radius: 6px; overflow-x: auto; }
  pre code { background: none; padding: 0; }
  blockquote { border-left: 3px solid #d1d5db; margin: 1em 0; padding: 0.5em 1em; color: #4b5563; }
  a { color: #2563eb; }
  hr { border: none; border-top: 1px solid #e5e7eb; margin: 2em 0; }
  em { color: #6b7280; font-size: 0.9em; }
  table { border-collapse: collapse; margin: 1em 0; }
  th, td { border: 1px solid #d1d5db; padding: 6px 12px; text-align: left; }
  th { background: #f9fafb; }
</style>
</head>
<body>
<h1>Acceptable Use Policy</h1>
%s
</body>
</html>`, bodyHTML.String()); err != nil {
		log.Error().Err(err).Msg("Failed to write AUP response")
	}
}
