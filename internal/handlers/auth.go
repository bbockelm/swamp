package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

// devLoginToken holds a one-time dev login token.
type devLoginToken struct {
	userID    string
	expiresAt time.Time
}

var (
	devLoginTokens   = map[string]*devLoginToken{}
	devLoginTokensMu sync.Mutex
)

const (
	sessionCookieName = "swamp_session"
	sessionDuration   = 7 * 24 * time.Hour
	sessionTokenBytes = 32

	RoleAdmin          = "admin"
	RoleProjectCreator = "project_creator"
	RoleUser           = "user"
)

var validRoles = map[string]bool{
	RoleAdmin:          true,
	RoleProjectCreator: true,
	RoleUser:           true,
}

type contextKey string

const sessionContextKey contextKey = "session"
const userContextKey contextKey = "user"

// hashToken computes SHA-256 of a raw token string.
func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// generateToken creates a cryptographically random token and its SHA-256 hash.
func generateToken() (rawToken string, tokenHash []byte, err error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err = rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("generating random token: %w", err)
	}
	rawToken = base64.RawURLEncoding.EncodeToString(buf)
	tokenHash = hashToken(rawToken)
	return rawToken, tokenHash, nil
}

// GetSessionFromContext returns the session stored in context by RequireAuth.
func GetSessionFromContext(ctx context.Context) *models.Session {
	s, _ := ctx.Value(sessionContextKey).(*models.Session)
	return s
}

// GetUserFromContext returns the user stored in context by RequireAuth.
func GetUserFromContext(ctx context.Context) *models.User {
	u, _ := ctx.Value(userContextKey).(*models.User)
	return u
}

// --- Auth middleware ---

// RequireAuth checks for a valid session cookie and populates context.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			respondError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		session, err := h.queries.GetSession(r.Context(), hashToken(cookie.Value))
		if err != nil {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookieName, Value: "", Path: "/",
				MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			respondError(w, http.StatusUnauthorized, "Session expired")
			return
		}

		user, err := h.queries.GetUser(r.Context(), session.UserID)
		if err != nil || user.Status != "active" {
			respondError(w, http.StatusUnauthorized, "User not active")
			return
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, session)
		ctx = context.WithValue(ctx, userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAUP ensures the user has agreed to the current AUP version.
func (h *Handler) RequireAUP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			respondError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		aupVersion := h.getAUPVersion(r.Context())
		agreed, err := h.queries.UserHasAgreedAUP(r.Context(), user.ID, aupVersion)
		if err != nil || !agreed {
			respondJSON(w, http.StatusForbidden, map[string]any{
				"error":       "AUP agreement required",
				"aup_version": aupVersion,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole checks that the authenticated user has one of the given roles.
func RequireRole(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUserFromContext(r.Context())
			if user == nil {
				respondError(w, http.StatusUnauthorized, "Not authenticated")
				return
			}
			roles, _ := r.Context().Value(contextKey("user_roles")).([]string)
			for _, a := range allowed {
				for _, ur := range roles {
					if ur == a {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			respondError(w, http.StatusForbidden, "Insufficient permissions")
		})
	}
}

// LoadUserRoles middleware populates context with user role strings.
func (h *Handler) LoadUserRoles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			next.ServeHTTP(w, r)
			return
		}
		dbRoles, _ := h.queries.ListUserRoles(r.Context(), user.ID)
		roleStrs := make([]string, len(dbRoles))
		for i, rl := range dbRoles {
			roleStrs[i] = rl.Role
		}
		ctx := context.WithValue(r.Context(), contextKey("user_roles"), roleStrs)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUserRolesFromContext extracts the user role strings from context.
func GetUserRolesFromContext(ctx context.Context) []string {
	roles, _ := ctx.Value(contextKey("user_roles")).([]string)
	return roles
}

// UserHasRole checks if the user in context has a given role.
func UserHasRole(ctx context.Context, role string) bool {
	for _, r := range GetUserRolesFromContext(ctx) {
		if r == role {
			return true
		}
	}
	return false
}

// --- Auth endpoints ---

// GetCurrentSession returns info about the current user/session.
func (h *Handler) GetCurrentSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		respondJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	session, err := h.queries.GetSession(r.Context(), hashToken(cookie.Value))
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	user, _ := h.queries.GetUser(r.Context(), session.UserID)
	roles, _ := h.queries.ListUserRoles(r.Context(), session.UserID)
	roleStrs := make([]string, len(roles))
	for i, rl := range roles {
		roleStrs[i] = rl.Role
	}

	aupAgreed, _ := h.queries.UserHasAgreedAUP(r.Context(), session.UserID, h.getAUPVersion(r.Context()))

	respondJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
		"roles":         roleStrs,
		"aup_agreed":    aupAgreed,
		"aup_version":   h.getAUPVersion(r.Context()),
		"aup_text":      h.getAUPText(r.Context()),
	})
}

// Logout destroys the current session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		_ = h.queries.DeleteSession(r.Context(), hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	respondJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// AgreeAUP records the user's agreement to the AUP.
func (h *Handler) AgreeAUP(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var req struct {
		AUPVersion string `json:"aup_version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if req.AUPVersion != h.getAUPVersion(r.Context()) {
		respondError(w, http.StatusBadRequest, "AUP version mismatch")
		return
	}

	agreement := &models.AUPAgreement{
		UserID:     user.ID,
		AUPVersion: req.AUPVersion,
		IPAddress:  r.RemoteAddr,
	}
	if err := h.queries.CreateAUPAgreement(r.Context(), agreement); err != nil {
		log.Error().Err(err).Msg("Failed to record AUP agreement")
		respondError(w, http.StatusInternalServerError, "Failed to record agreement")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "agreed"})
}

// UpdateMyProfile allows the logged-in user to update their display name.
// GetMyStats returns aggregate counts for the authenticated user.
func (h *Handler) GetMyStats(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	stats, err := h.queries.GetUserStats(r.Context(), user.ID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to load stats")
		return
	}
	respondJSON(w, http.StatusOK, stats)
}

func (h *Handler) UpdateMyProfile(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if req.DisplayName != "" {
		user.DisplayName = req.DisplayName
		_ = h.queries.UpdateUser(r.Context(), user)
	}
	respondJSON(w, http.StatusOK, user)
}

// --- Dev login ---

// GenerateDevLoginLink creates the Administrator account and a one-time login
// token, then logs the URL. Call this at startup in dev mode — there is no
// route that triggers token creation.
func (h *Handler) GenerateDevLoginLink(ctx context.Context) error {
	// Ensure the Administrator account exists.
	devIssuer := "dev"
	devSubject := "dev-admin"
	identity, err := h.queries.FindIdentity(ctx, devIssuer, devSubject)
	var userID string
	if err != nil {
		user := &models.User{DisplayName: "Administrator", Email: "admin@localhost", Status: "active"}
		if err := h.queries.CreateUser(ctx, user); err != nil {
			return fmt.Errorf("creating admin user: %w", err)
		}
		userID = user.ID
		ident := &models.UserIdentity{
			UserID: userID, Issuer: devIssuer, Subject: devSubject,
			Email: "admin@localhost", DisplayName: "Administrator",
		}
		_ = h.queries.CreateIdentity(ctx, ident)
		_ = h.queries.AddUserRole(ctx, userID, RoleAdmin)
		log.Info().Str("user_id", userID).Msg("Created Administrator account for dev mode")
	} else {
		userID = identity.UserID
	}

	// Generate a one-time login token.
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generating login token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	devLoginTokensMu.Lock()
	devLoginTokens[token] = &devLoginToken{userID: userID, expiresAt: time.Now().Add(24 * time.Hour)}
	devLoginTokensMu.Unlock()

	loginURL := h.cfg.BaseURL + "/api/v1/auth/dev-login-link/" + token
	log.Info().Str("url", loginURL).Msg("Dev login link (valid for 24 hours)")
	fmt.Fprintf(os.Stderr, "\n  ➜  Dev login URL: %s\n\n", loginURL)
	return nil
}

// DevLoginLinkComplete validates a one-time dev login token, creates a session, and redirects.
func (h *Handler) DevLoginLinkComplete(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.IsDevelopment() {
		respondError(w, http.StatusNotFound, "Not found")
		return
	}

	token := chi.URLParam(r, "token")
	if token == "" {
		respondError(w, http.StatusBadRequest, "Missing token")
		return
	}

	devLoginTokensMu.Lock()
	tok, ok := devLoginTokens[token]
	if ok {
		delete(devLoginTokens, token)
	}
	devLoginTokensMu.Unlock()

	if !ok || time.Now().After(tok.expiresAt) {
		respondError(w, http.StatusUnauthorized, "Invalid or expired login link")
		return
	}

	userID := tok.userID
	_ = h.queries.UpdateUserLastLogin(r.Context(), userID)
	_ = h.queries.DeleteUserSessions(r.Context(), userID)

	rawToken, tokenHash, genErr := generateToken()
	if genErr != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate session token")
		return
	}
	session := &models.Session{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(sessionDuration),
	}
	if err := h.queries.CreateSession(r.Context(), session); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: rawToken, Path: "/",
		MaxAge: int(sessionDuration.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

// GetAuthMode returns the auth configuration for the frontend.
func (h *Handler) GetAuthMode(w http.ResponseWriter, r *http.Request) {
	oidcConfigured := h.cfg.OIDCIssuer != "" && h.cfg.OIDCClientID != ""
	mode := "dev"
	if oidcConfigured {
		mode = "oidc"
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"mode":            mode,
		"oidc_configured": oidcConfigured,
		"callback_url":    h.cfg.BaseURL + "/api/v1/auth/oidc/callback",
		"aup_version":     h.cfg.AUPVersion,
	})
}

// --- OIDC flow ---

type wellKnownConfig struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

func fetchWellKnown(issuer string) (*wellKnownConfig, error) {
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var wk wellKnownConfig
	if err := json.NewDecoder(resp.Body).Decode(&wk); err != nil {
		return nil, err
	}
	return &wk, nil
}

// getOIDCConfig reads OIDC credentials from the database first, falling back to env config.
func (h *Handler) getOIDCConfig(ctx context.Context) (issuer, clientID, clientSecret string, err error) {
	issuer, _ = h.queries.GetAppConfig(ctx, "oidc_issuer")
	clientID, _ = h.queries.GetAppConfig(ctx, "oidc_client_id")
	clientSecret, _ = h.getDecryptedConfig(ctx, "oidc_client_secret")
	if issuer != "" && clientID != "" {
		return
	}
	issuer = h.cfg.OIDCIssuer
	clientID = h.cfg.OIDCClientID
	clientSecret = h.cfg.OIDCClientSecret
	if issuer == "" || clientID == "" {
		err = fmt.Errorf("OIDC not configured")
	}
	return
}

// getDecryptedConfig reads a config value and decrypts it if the encryptor is available.
func (h *Handler) getDecryptedConfig(ctx context.Context, key string) (string, error) {
	val, err := h.queries.GetAppConfig(ctx, key)
	if err != nil {
		return "", err
	}
	if h.encryptor != nil {
		if dec, decErr := h.encryptor.DecryptConfigValue(val); decErr == nil {
			return dec, nil
		}
	}
	return val, nil
}

// OIDCLogin initiates the OIDC authorization code flow.
func (h *Handler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	issuer, clientID, _, err := h.getOIDCConfig(r.Context())
	if err != nil {
		http.Redirect(w, r, "/login?error=OIDC+not+configured", http.StatusFound)
		return
	}

	// Preserve the original URL the user wanted to visit
	if returnTo := r.URL.Query().Get("return_to"); returnTo != "" {
		http.SetCookie(w, &http.Cookie{
			Name: "swamp_return_to", Value: returnTo, Path: "/",
			MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
	}

	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)

	mac := hmac.New(sha256.New, []byte(h.cfg.SessionSecret))
	mac.Write([]byte(state))
	sig := hex.EncodeToString(mac.Sum(nil))

	http.SetCookie(w, &http.Cookie{
		Name: "swamp_oidc_state", Value: sig + ":" + state, Path: "/",
		MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	callbackURL := h.cfg.BaseURL + "/api/v1/auth/oidc/callback"

	authURL := issuer + "/authorize"
	wellKnown, err := fetchWellKnown(issuer)
	if err == nil && wellKnown.AuthorizationEndpoint != "" {
		authURL = wellKnown.AuthorizationEndpoint
	}

	scopes := "openid email profile"
	if strings.Contains(issuer, "cilogon") {
		scopes = "openid email profile org.cilogon.userinfo"
	}

	redirectURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		authURL, clientID, callbackURL, scopes, state)

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// OIDCCallback handles the OIDC authorization code callback.
func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		respondError(w, http.StatusBadRequest, "Missing code or state")
		return
	}

	stateCookie, err := r.Cookie("swamp_oidc_state")
	if err != nil || stateCookie.Value == "" {
		respondError(w, http.StatusBadRequest, "Missing state cookie")
		return
	}

	parts := strings.SplitN(stateCookie.Value, ":", 2)
	if len(parts) != 2 {
		respondError(w, http.StatusBadRequest, "Invalid state cookie")
		return
	}
	storedSig := parts[0]
	storedState := parts[1]

	mac := hmac.New(sha256.New, []byte(h.cfg.SessionSecret))
	mac.Write([]byte(storedState))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(storedSig), []byte(expectedSig)) {
		respondError(w, http.StatusBadRequest, "State verification failed")
		return
	}
	if state != storedState {
		respondError(w, http.StatusBadRequest, "State mismatch")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: "swamp_oidc_state", Value: "", Path: "/", MaxAge: -1,
	})

	issuer, clientID, clientSecret, err := h.getOIDCConfig(r.Context())
	if err != nil {
		http.Redirect(w, r, "/login?error=OIDC+not+configured", http.StatusFound)
		return
	}

	wellKnown, _ := fetchWellKnown(issuer)
	tokenURL := issuer + "/token"
	if wellKnown != nil && wellKnown.TokenEndpoint != "" {
		tokenURL = wellKnown.TokenEndpoint
	}

	callbackURL := h.cfg.BaseURL + "/api/v1/auth/oidc/callback"
	tokenResp, err := exchangeCode(tokenURL, code, clientID, clientSecret, callbackURL)
	if err != nil {
		log.Error().Err(err).Msg("Failed to exchange OIDC code")
		respondError(w, http.StatusInternalServerError, "Failed to exchange code")
		return
	}

	claims, err := parseIDToken(tokenResp.IDToken)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse ID token")
		respondError(w, http.StatusInternalServerError, "Failed to parse ID token")
		return
	}

	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)
	if sub == "" {
		respondError(w, http.StatusBadRequest, "ID token missing subject")
		return
	}

	// Fetch userinfo for extra claims
	var idpName string
	if wellKnown != nil && wellKnown.UserinfoEndpoint != "" && tokenResp.AccessToken != "" {
		ui, uiErr := fetchUserinfo(wellKnown.UserinfoEndpoint, tokenResp.AccessToken)
		if uiErr == nil {
			if name == "" {
				if n, ok := ui["name"].(string); ok {
					name = n
				}
			}
			if email == "" {
				if e, ok := ui["email"].(string); ok {
					email = e
				}
			}
			if n, ok := ui["idp_name"].(string); ok {
				idpName = n
			}
		}
	}

	// Find or create user by identity
	identity, err := h.queries.FindIdentity(r.Context(), issuer, sub)
	var userID string
	if err != nil {
		// New user
		user := &models.User{DisplayName: name, Email: email, Status: "active"}
		if err := h.queries.CreateUser(r.Context(), user); err != nil {
			log.Error().Err(err).Msg("Failed to create user")
			respondError(w, http.StatusInternalServerError, "Failed to create user")
			return
		}
		userID = user.ID
		ident := &models.UserIdentity{
			UserID: userID, Issuer: issuer, Subject: sub,
			Email: email, DisplayName: name, IDPName: idpName,
		}
		_ = h.queries.CreateIdentity(r.Context(), ident)
		_ = h.queries.AddUserRole(r.Context(), userID, RoleUser)
	} else {
		userID = identity.UserID
	}

	_ = h.queries.UpdateUserLastLogin(r.Context(), userID)

	rawToken, tokenHash, genErr := generateToken()
	if genErr != nil {
		log.Error().Err(genErr).Msg("Failed to generate session token")
		respondError(w, http.StatusInternalServerError, "Internal error")
		return
	}
	session := &models.Session{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(sessionDuration),
	}
	if err := h.queries.CreateSession(r.Context(), session); err != nil {
		log.Error().Err(err).Msg("Failed to create session")
		respondError(w, http.StatusInternalServerError, "Internal error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: rawToken, Path: "/",
		MaxAge: int(sessionDuration.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	// Redirect to the original URL or frontend root
	redirectTo := "/"
	if returnCookie, err := r.Cookie("swamp_return_to"); err == nil && returnCookie.Value != "" {
		redirectTo = returnCookie.Value
		// Clear the cookie
		http.SetCookie(w, &http.Cookie{
			Name: "swamp_return_to", Value: "", Path: "/", MaxAge: -1,
		})
	}
	http.Redirect(w, r, h.cfg.BaseURL+redirectTo, http.StatusFound)
}

// --- OIDC helpers ---

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func exchangeCode(tokenURL, code, clientID, clientSecret, redirectURI string) (*tokenResponse, error) {
	data := fmt.Sprintf("grant_type=authorization_code&code=%s&client_id=%s&client_secret=%s&redirect_uri=%s",
		code, clientID, clientSecret, redirectURI)
	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

func parseIDToken(idToken string) (map[string]any, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func fetchUserinfo(endpoint, accessToken string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}
