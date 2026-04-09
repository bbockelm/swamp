package oauth2

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ory/fosite"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// SessionFactory creates a new fosite.Session for request hydration.
// Callers must provide a function that returns the correct session type
// (typically a fosite.DefaultSession or an openid.DefaultSession).
type SessionFactory func() fosite.Session

// Handlers provides HTTP handlers for the OAuth2/OIDC endpoints.
type Handlers struct {
	provider       *Provider
	issuerURL      string
	sessionFactory SessionFactory
	consentHandler func(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester)
	dcrLimiter     *registrationRateLimiter
}

// NewHandlers creates OAuth2 HTTP handlers.
// consentHandler is called when the authorization endpoint needs to show
// a consent/login page. It should authenticate the user and call
// AcceptAuthorize when done.
func NewHandlers(
	provider *Provider,
	issuerURL string,
	sessionFactory SessionFactory,
	consentHandler func(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester),
) *Handlers {
	return &Handlers{
		provider:       provider,
		issuerURL:      strings.TrimRight(issuerURL, "/"),
		sessionFactory: sessionFactory,
		consentHandler: consentHandler,
		// 5 burst, refill 1 per minute per IP.
		dcrLimiter: newRegistrationRateLimiter(1.0/60.0, 5),
	}
}

// SetConsentHandler sets the consent handler after construction
// (needed when the consent handler requires a reference to Handlers).
func (h *Handlers) SetConsentHandler(fn func(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester)) {
	h.consentHandler = fn
}

// logFositeError logs a fosite error with its RFC error code and debug info.
func logFositeError(event *zerolog.Event, err error) *zerolog.Event {
	var rfcErr *fosite.RFC6749Error
	if errors.As(err, &rfcErr) {
		event = event.
			Str("oauth_error", rfcErr.ErrorField).
			Str("oauth_description", rfcErr.DescriptionField).
			Str("oauth_debug", rfcErr.DebugField).
			Int("oauth_code", rfcErr.CodeField)
		if rfcErr.Cause() != nil {
			event = event.Str("cause", rfcErr.Cause().Error())
		}
	}
	return event.Err(err)
}

// Authorize handles GET/POST /oauth/authorize.
func (h *Handlers) Authorize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ar, err := h.provider.NewAuthorizeRequest(ctx, r)
	if err != nil {
		logFositeError(log.Warn(), err).Msg("OAuth2 authorize request failed")
		h.provider.WriteAuthorizeError(ctx, w, ar, err)
		return
	}

	// Delegate to the consent handler which will call AcceptAuthorize
	// after authenticating the user.
	h.consentHandler(w, r, ar)
}

// AcceptAuthorize is called by the consent handler after the user has
// authenticated and approved the request.
func (h *Handlers) AcceptAuthorize(w http.ResponseWriter, r *http.Request, ar fosite.AuthorizeRequester, session fosite.Session) {
	ctx := r.Context()

	// Grant requested scopes.
	for _, scope := range ar.GetRequestedScopes() {
		ar.GrantScope(scope)
	}

	resp, err := h.provider.NewAuthorizeResponse(ctx, ar, session)
	if err != nil {
		logFositeError(log.Warn(), err).Msg("OAuth2 authorize response failed")
		h.provider.WriteAuthorizeError(ctx, w, ar, err)
		return
	}

	h.provider.WriteAuthorizeResponse(ctx, w, ar, resp)
}

// Token handles POST /oauth/token.
func (h *Handlers) Token(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session := h.sessionFactory()

	ar, err := h.provider.NewAccessRequest(ctx, r, session)
	if err != nil {
		logFositeError(log.Warn(), err).Msg("OAuth2 access request failed")
		h.provider.WriteAccessError(ctx, w, ar, err)
		return
	}

	// Grant requested scopes for the token.
	for _, scope := range ar.GetRequestedScopes() {
		ar.GrantScope(scope)
	}

	resp, err := h.provider.NewAccessResponse(ctx, ar)
	if err != nil {
		logFositeError(log.Warn(), err).Msg("OAuth2 access response failed")
		h.provider.WriteAccessError(ctx, w, ar, err)
		return
	}

	// Mark the client as used (for unused DCR cleanup).
	if client := ar.GetClient(); client != nil {
		if err := h.provider.Storage.TouchClientLastUsed(ctx, client.GetID()); err != nil {
			log.Warn().Err(err).Str("client_id", client.GetID()).Msg("Failed to touch client last_used_at")
		}
	}

	h.provider.WriteAccessResponse(ctx, w, ar, resp)
}

// Revoke handles POST /oauth/revoke (RFC 7009).
func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.provider.NewRevocationRequest(ctx, r); err != nil {
		log.Warn().Err(err).Msg("OAuth2 revocation request failed")
		h.provider.WriteRevocationResponse(ctx, w, err)
		return
	}
	h.provider.WriteRevocationResponse(ctx, w, nil)
}

// Introspect handles POST /oauth/introspect (RFC 7662).
func (h *Handlers) Introspect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	session := h.sessionFactory()

	resp, err := h.provider.NewIntrospectionRequest(ctx, r, session)
	if err != nil {
		log.Warn().Err(err).Msg("OAuth2 introspection request failed")
		h.provider.WriteIntrospectionError(ctx, w, err)
		return
	}
	h.provider.WriteIntrospectionResponse(ctx, w, resp)
}

// JWKS returns the JSON Web Key Set (RFC 7517).
func (h *Handlers) JWKS(w http.ResponseWriter, r *http.Request) {
	pubKey := &h.provider.PrivateKey.PublicKey
	pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Build a minimal JWKS response with the RSA public key.
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": h.provider.KID,
				"n":   base64.RawURLEncoding.EncodeToString(pubKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}), // 65537
			},
		},
	}
	_ = pubDER // validated above

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(jwks)
}

// Discovery returns the OpenID Connect Discovery document (RFC 8414).
func (h *Handlers) Discovery(w http.ResponseWriter, r *http.Request) {
	base := h.issuerURL

	doc := map[string]any{
		"issuer":                 base,
		"authorization_endpoint": fmt.Sprintf("%s/oauth/authorize", base),
		"token_endpoint":         fmt.Sprintf("%s/oauth/token", base),
		"revocation_endpoint":    fmt.Sprintf("%s/oauth/revoke", base),
		"introspection_endpoint": fmt.Sprintf("%s/oauth/introspect", base),
		"jwks_uri":               fmt.Sprintf("%s/.well-known/jwks.json", base),

		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"scopes_supported":                      []string{"openid", "profile", "mcp", "offline_access"},
		"code_challenge_methods_supported":      []string{"S256"},

		"id_token_signing_alg_values_supported": []string{"RS256"},
		"subject_types_supported":               []string{"public"},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(doc)
}

// ClientRegistration handles dynamic client registration (RFC 7591).
// This is called by MCP clients (like VS Code) to register themselves.
func (h *Handlers) ClientRegistration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limit by client IP.
	clientIP := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		clientIP = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if !h.dcrLimiter.Allow(clientIP) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "rate_limit_exceeded",
			"error_description": "Too many client registrations from this address; try again later",
		})
		return
	}

	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ClientName              string   `json:"client_name"`
		Scope                   string   `json:"scope"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	log.Info().
		Strs("redirect_uris", req.RedirectURIs).
		Strs("grant_types", req.GrantTypes).
		Str("scope", req.Scope).
		Str("client_name", req.ClientName).
		Str("token_endpoint_auth_method", req.TokenEndpointAuthMethod).
		Str("remote", clientIP).
		Msg("DCR: incoming registration request")

	if len(req.RedirectURIs) == 0 {
		http.Error(w, "redirect_uris required", http.StatusBadRequest)
		return
	}

	// Validate redirect URIs against the allowlist to prevent open-redirector attacks.
	if err := validateDCRRedirectURIs(req.RedirectURIs); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_redirect_uri",
			"error_description": err.Error(),
		})
		return
	}

	// Default grant types and response types.
	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"authorization_code"}
	}

	// Parse scopes from space-delimited string.
	// Default to standard scopes if none specified (VS Code MCP client sends no scope in DCR).
	var scopes []string
	if req.Scope != "" {
		scopes = strings.Split(req.Scope, " ")
	} else {
		scopes = []string{"openid", "profile", "mcp", "offline_access"}
	}

	// If offline_access is requested, ensure refresh_token grant type is included.
	for _, s := range scopes {
		if s == "offline_access" {
			hasRefresh := false
			for _, gt := range req.GrantTypes {
				if gt == "refresh_token" {
					hasRefresh = true
					break
				}
			}
			if !hasRefresh {
				req.GrantTypes = append(req.GrantTypes, "refresh_token")
			}
			break
		}
	}

	// Determine if this is a public client.
	isPublic := req.TokenEndpointAuthMethod == "none" || req.TokenEndpointAuthMethod == ""

	// Generate client ID.
	clientID := generateClientID()

	// Public clients don't get a secret (use PKCE instead).
	hashedSecret := ""

	if err := h.provider.Storage.CreateDynamicClient(
		r.Context(),
		clientID,
		hashedSecret,
		req.RedirectURIs,
		req.GrantTypes,
		[]string{"code"},
		scopes,
		isPublic,
		req.ClientName,
		normalizeIP(clientIP),
	); err != nil {
		log.Error().Err(err).Msg("Failed to create OAuth2 client")
		http.Error(w, "failed to register client", http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                req.GrantTypes,
		"response_types":             []string{"code"},
		"client_name":                req.ClientName,
		"token_endpoint_auth_method": "none",
		"scope":                      req.Scope,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func generateClientID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("swamp_%x", b)
}
