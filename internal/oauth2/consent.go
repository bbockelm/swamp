package oauth2

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/openid"
	"github.com/rs/zerolog/log"
)

// pendingConsent stores authorization requests waiting for user approval.
type pendingConsent struct {
	ar        fosite.AuthorizeRequester
	expiresAt time.Time
}

// consentStore is an in-memory store for pending consent requests.
// These are short-lived (10 minutes) and cleaned on access.
var consentStore = make(map[string]*pendingConsent)

// ConsentHandler bridges SWAMP's cookie-based session auth with the OAuth2
// authorization flow. When a user isn't logged in, it redirects to the
// login page. When logged in, it shows a consent screen.
//
// getUserFromRequest should extract the authenticated SWAMP user from the
// request (checking session cookies). Returns (userID, displayName, ok).
type ConsentHandler struct {
	handlers       *Handlers
	baseURL        string
	getUserFromReq func(r *http.Request) (userID, displayName string, ok bool)
}

// NewConsentHandler creates a consent handler.
func NewConsentHandler(
	handlers *Handlers,
	baseURL string,
	getUserFunc func(r *http.Request) (userID, displayName string, ok bool),
) *ConsentHandler {
	return &ConsentHandler{
		handlers:       handlers,
		baseURL:        baseURL,
		getUserFromReq: getUserFunc,
	}
}

// HandleConsent is called by the OAuth2 authorize endpoint when it needs
// user approval for an authorization request.
func (c *ConsentHandler) HandleConsent(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester) {
	userID, displayName, ok := c.getUserFromReq(r)
	if !ok {
		// User not logged in. Store the authorize request and redirect to login.
		consentID := generateConsentID()
		consentStore[consentID] = &pendingConsent{
			ar:        ar,
			expiresAt: time.Now().Add(10 * time.Minute),
		}
		loginURL := fmt.Sprintf("%s/login?oauth_consent=%s", c.baseURL, consentID)
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}

	// User is logged in. Check if this is a POST (consent form submission).
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.FormValue("action") == "approve" {
			c.approveRequest(w, r, ar, userID, displayName)
		} else {
			ar.SetSession(&fosite.DefaultSession{})
			c.handlers.provider.WriteAuthorizeError(r.Context(), w, ar, fosite.ErrAccessDenied)
		}
		return
	}

	// Show consent page.
	c.renderConsentPage(w, r, ar, displayName)
}

// HandleConsentCallback handles the return from login when an oauth_consent
// parameter is present.
func (c *ConsentHandler) HandleConsentCallback(w http.ResponseWriter, r *http.Request, consentID string) {
	pc, ok := consentStore[consentID]
	if !ok || time.Now().After(pc.expiresAt) {
		delete(consentStore, consentID)
		http.Error(w, "consent request expired", http.StatusBadRequest)
		return
	}

	userID, displayName, ok := c.getUserFromReq(r)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	delete(consentStore, consentID)
	c.approveRequest(w, r, pc.ar, userID, displayName)
}

func (c *ConsentHandler) approveRequest(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester, userID, displayName string) {
	session := openid.NewDefaultSession()
	session.Subject = userID
	session.Username = displayName
	session.Claims.Subject = userID
	session.Claims.Add("user_id", userID)
	session.SetExpiresAt(fosite.AccessToken, time.Now().Add(1*time.Hour))
	session.SetExpiresAt(fosite.AuthorizeCode, time.Now().Add(10*time.Minute))

	ar.SetSession(session)
	c.handlers.AcceptAuthorize(w, r, ar, session)
}

func (c *ConsentHandler) renderConsentPage(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester, displayName string) {
	clientID := ar.GetClient().GetID()
	scopes := ar.GetRequestedScopes()

	// Re-serialize the original query parameters so the form POST
	// submits to the same authorize URL.
	data := struct {
		DisplayName string
		ClientID    string
		ClientName  string
		Scopes      []string
		FormAction  string
	}{
		DisplayName: displayName,
		ClientID:    clientID,
		ClientName:  clientID,
		Scopes:      scopes,
		FormAction:  r.URL.RequestURI(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := consentTmpl.Execute(w, data); err != nil {
		log.Error().Err(err).Msg("Failed to render consent page")
	}
}

var consentTmpl = template.Must(template.New("consent").Parse(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Authorize Application — SWAMP</title>
  <style>
    body { font-family: system-ui, sans-serif; max-width: 480px; margin: 60px auto; padding: 0 20px; color: #1a1a1a; }
    .card { border: 1px solid #e5e7eb; border-radius: 12px; padding: 32px; }
    h1 { font-size: 1.25rem; margin: 0 0 8px; }
    .desc { color: #6b7280; margin-bottom: 24px; font-size: 0.95rem; }
    .scopes { margin: 16px 0; }
    .scope { display: inline-block; background: #f3f4f6; border-radius: 6px; padding: 4px 10px; margin: 4px 4px 4px 0; font-size: 0.85rem; }
    .actions { display: flex; gap: 12px; margin-top: 24px; }
    button { flex: 1; padding: 10px; border: none; border-radius: 8px; font-size: 0.95rem; cursor: pointer; font-weight: 500; }
    .approve { background: #2563eb; color: white; }
    .approve:hover { background: #1d4ed8; }
    .deny { background: #f3f4f6; color: #374151; }
    .deny:hover { background: #e5e7eb; }
    .user { color: #6b7280; font-size: 0.85rem; margin-bottom: 16px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>Authorize Application</h1>
    <p class="desc"><strong>{{.ClientName}}</strong> wants to access your SWAMP account.</p>
    <p class="user">Signed in as <strong>{{.DisplayName}}</strong></p>
    {{if .Scopes}}
    <div class="scopes">
      <p style="margin-bottom:8px;font-size:0.9rem;color:#374151;">Requested permissions:</p>
      {{range .Scopes}}<span class="scope">{{.}}</span>{{end}}
    </div>
    {{end}}
    <form method="POST" action="{{.FormAction}}">
      <div class="actions">
        <button type="submit" name="action" value="deny" class="deny">Deny</button>
        <button type="submit" name="action" value="approve" class="approve">Approve</button>
      </div>
    </form>
  </div>
</body>
</html>`))

func generateConsentID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ListClients returns registered OAuth2 clients as JSON (admin endpoint).
func (h *Handlers) ListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.provider.Storage.ListClients(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list OAuth2 clients")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(clients)
}

// DeleteClient removes an OAuth2 client (admin endpoint).
func (h *Handlers) DeleteClient(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		http.Error(w, "client_id required", http.StatusBadRequest)
		return
	}
	if err := h.provider.Storage.DeleteClient(r.Context(), clientID); err != nil {
		log.Error().Err(err).Msg("Failed to delete OAuth2 client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
