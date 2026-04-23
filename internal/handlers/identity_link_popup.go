package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// renderIdentityLinkPopupResult renders a tiny HTML page that reports OAuth
// link completion back to the opener and optionally closes itself on success.
func renderIdentityLinkPopupResult(w http.ResponseWriter, provider string, success bool, message string) {
	status := "error"
	title := strings.ToUpper(provider) + " Link Failed"
	body := message
	if strings.TrimSpace(body) == "" {
		body = "The identity link did not complete successfully."
	}
	if success {
		status = "success"
		title = strings.ToUpper(provider) + " Account Linked"
		if strings.TrimSpace(message) == "" {
			body = "This window will close automatically..."
		}
	}
	payload, _ := json.Marshal(map[string]string{
		"type":     "identity-link-result",
		"provider": provider,
		"status":   status,
		"message":  message,
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    body { font-family: system-ui, sans-serif; background: #f8fafc; color: #0f172a; margin: 0; }
    .card { max-width: 32rem; margin: 10vh auto; background: white; border: 1px solid #e2e8f0; border-radius: 12px; padding: 2rem; box-shadow: 0 10px 30px rgba(15,23,42,0.08); }
    h1 { margin: 0 0 .75rem; font-size: 1.25rem; }
    p { margin: 0; line-height: 1.5; }
    .ok { color: #166534; }
    .err { color: #b91c1c; }
  </style>
</head>
<body>
  <div class="card">
    <h1 class="%s">%s</h1>
    <p>%s</p>
  </div>
  <script>
    const payload = %s;
    if (window.opener) {
      try {
        window.opener.postMessage(payload, window.location.origin);
      } catch (_) {
        // Ignore cross-window messaging failures.
      }
      if (payload.status === "success") {
        window.setTimeout(() => window.close(), 150);
      }
    }
  </script>
</body>
</html>`,
		template.HTMLEscapeString(title),
		map[bool]string{true: "ok", false: "err"}[success],
		template.HTMLEscapeString(title),
		template.HTMLEscapeString(body),
		string(payload),
	)
}