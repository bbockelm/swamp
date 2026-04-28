package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

type nrpWellKnownConfig struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

type nrpTokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

type nrpUserInfo struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	Username          string `json:"username"`
}

func (h *Handler) getNRPOAuthConfig(ctx context.Context) (issuer, clientID, clientSecret, exchangeURL string, err error) {
	issuer, _ = h.queries.GetAppConfig(ctx, "nrp_oidc_issuer")
	clientID, _ = h.queries.GetAppConfig(ctx, "nrp_oidc_client_id")
	clientSecret, _ = h.getDecryptedConfig(ctx, "nrp_oidc_client_secret")
	exchangeURL, _ = h.queries.GetAppConfig(ctx, "nrp_llm_exchange_url")
	if exchangeURL == "" {
		exchangeURL = h.cfg.NRPLLMExchangeURL
	}
	if issuer != "" || clientID != "" || clientSecret != "" {
		if issuer == "" || clientID == "" || clientSecret == "" {
			err = fmt.Errorf("NRP OAuth is not fully configured")
		}
		return
	}

	issuer = h.cfg.NRPOIDCIssuer
	clientID = h.cfg.NRPOIDCClientID
	clientSecret = h.cfg.NRPOIDCClientSecret
	if issuer == "" || clientID == "" || clientSecret == "" {
		err = fmt.Errorf("NRP OAuth is not configured")
	}
	return
}

func (h *Handler) nrpOAuthConfigured(ctx context.Context) bool {
	_, _, _, _, err := h.getNRPOAuthConfig(ctx)
	return err == nil
}

func (h *Handler) fetchNRPWellKnown(ctx context.Context, issuer string) (*nrpWellKnownConfig, error) {
	wellKnownURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching OIDC metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OIDC metadata returned %d: %s", resp.StatusCode, string(body))
	}
	var cfg nrpWellKnownConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decoding OIDC metadata: %w", err)
	}
	if cfg.AuthorizationEndpoint == "" || cfg.TokenEndpoint == "" || cfg.UserinfoEndpoint == "" {
		return nil, fmt.Errorf("OIDC metadata is missing required endpoints")
	}
	return &cfg, nil
}

func (h *Handler) nrpRedirectURL() string {
	return strings.TrimRight(h.cfg.BaseURL, "/") + "/api/v1/nrp/link/callback"
}

func (h *Handler) buildNRPAuthorizeURL(ctx context.Context, state string) (string, error) {
	issuer, clientID, _, _, err := h.getNRPOAuthConfig(ctx)
	if err != nil {
		return "", err
	}
	wk, err := h.fetchNRPWellKnown(ctx, issuer)
	if err != nil {
		return "", err
	}
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("redirect_uri", h.nrpRedirectURL())
	v.Set("scope", "openid profile email offline_access")
	v.Set("state", state)
	return wk.AuthorizationEndpoint + "?" + v.Encode(), nil
}

func (h *Handler) nrpExchangeCode(ctx context.Context, code string) (*nrpTokenResponse, string, error) {
	issuer, clientID, clientSecret, _, err := h.getNRPOAuthConfig(ctx)
	if err != nil {
		return nil, "", err
	}
	wk, err := h.fetchNRPWellKnown(ctx, issuer)
	if err != nil {
		return nil, issuer, err
	}
	v := url.Values{}
	v.Set("grant_type", "authorization_code")
	v.Set("code", code)
	v.Set("client_id", clientID)
	v.Set("client_secret", clientSecret)
	v.Set("redirect_uri", h.nrpRedirectURL())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wk.TokenEndpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, issuer, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, issuer, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, issuer, fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, string(body))
	}
	var tokenResp nrpTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, issuer, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, issuer, fmt.Errorf("NRP OAuth error: %s — %s", tokenResp.Error, tokenResp.ErrorDescription)
	}
	if tokenResp.AccessToken == "" {
		return nil, issuer, fmt.Errorf("no access token in response")
	}
	return &tokenResp, issuer, nil
}

func (h *Handler) nrpRefreshToken(ctx context.Context, issuer, refreshToken string) (*nrpTokenResponse, error) {
	_, clientID, clientSecret, _, err := h.getNRPOAuthConfig(ctx)
	if err != nil {
		return nil, err
	}
	wk, err := h.fetchNRPWellKnown(ctx, issuer)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("grant_type", "refresh_token")
	v.Set("refresh_token", refreshToken)
	v.Set("client_id", clientID)
	v.Set("client_secret", clientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wk.TokenEndpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh returned %d: %s", resp.StatusCode, string(body))
	}
	var tokenResp nrpTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("NRP OAuth refresh error: %s — %s", tokenResp.Error, tokenResp.ErrorDescription)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in refresh response")
	}
	return &tokenResp, nil
}

func (h *Handler) nrpGetUserInfo(ctx context.Context, issuer, accessToken string) (*nrpUserInfo, error) {
	wk, err := h.fetchNRPWellKnown(ctx, issuer)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wk.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo returned %d: %s", resp.StatusCode, string(body))
	}
	var info nrpUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parsing userinfo response: %w", err)
	}
	if info.Subject == "" {
		return nil, fmt.Errorf("userinfo response missing sub")
	}
	return &info, nil
}

// getValidNRPToken returns a usable NRP access token for the user, or
// empty string if none is available. Use validateNRPToken when the caller
// needs to know *why* the token is unavailable (for logging or to
// surface a specific error to the user).
func (h *Handler) getValidNRPToken(ctx context.Context, userID string) string {
	tok, _ := h.validateNRPToken(ctx, userID)
	return tok
}

// validateNRPToken is the explanatory variant of getValidNRPToken: it
// returns the same access token on success, and a descriptive error on
// failure so callers can log a meaningful reason. The bare returns of ""
// in getValidNRPToken now flow through this single function.
func (h *Handler) validateNRPToken(ctx context.Context, userID string) (string, error) {
	issuer, _, _, _, err := h.getNRPOAuthConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("NRP OAuth is not configured: %w", err)
	}
	if issuer == "" {
		return "", fmt.Errorf("NRP OAuth issuer is not configured")
	}
	identity, err := h.queries.FindLinkedIdentityByIssuer(ctx, userID, issuer)
	if err != nil {
		return "", fmt.Errorf("looking up NRP identity: %w", err)
	}
	if identity == nil {
		return "", fmt.Errorf("no linked NRP identity for user")
	}
	if identity.AccessTokenEnc == nil {
		return "", fmt.Errorf("linked NRP identity has no stored access token")
	}
	if h.encryptor == nil {
		return "", fmt.Errorf("encryption is not configured on this server")
	}
	accessToken, err := h.encryptor.DecryptConfigValue(*identity.AccessTokenEnc)
	if err != nil {
		return "", fmt.Errorf("decrypting NRP access token: %w", err)
	}
	if identity.TokenExpiresAt == nil || identity.TokenExpiresAt.After(time.Now().Add(5*time.Minute)) {
		// Token is fresh — no refresh needed.
		return accessToken, nil
	}
	// Token is expired or about to expire — try to refresh.
	if identity.RefreshTokenEnc == nil {
		// Some providers omit refresh tokens; continue using the current
		// access token until it actually expires.
		if identity.TokenExpiresAt.After(time.Now()) {
			return accessToken, nil
		}
		return "", fmt.Errorf("NRP access token expired and no refresh token is available")
	}
	refreshToken, err := h.encryptor.DecryptConfigValue(*identity.RefreshTokenEnc)
	if err != nil {
		if identity.TokenExpiresAt.After(time.Now()) {
			return accessToken, nil
		}
		return "", fmt.Errorf("decrypting NRP refresh token: %w", err)
	}
	tokenResp, err := h.nrpRefreshToken(ctx, issuer, refreshToken)
	if err != nil {
		if identity.TokenExpiresAt.After(time.Now()) {
			return accessToken, nil
		}
		return "", fmt.Errorf("refreshing NRP access token: %w", err)
	}
	var newAccessEnc, newRefreshEnc *string
	if enc, err := h.encryptor.EncryptConfigValue(tokenResp.AccessToken); err == nil {
		newAccessEnc = &enc
	}
	if tokenResp.RefreshToken != "" {
		if enc, err := h.encryptor.EncryptConfigValue(tokenResp.RefreshToken); err == nil {
			newRefreshEnc = &enc
		}
	}
	var newExpiry *time.Time
	if tokenResp.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		newExpiry = &t
	}
	_ = h.queries.UpdateIdentityTokens(ctx, identity.ID, newAccessEnc, newRefreshEnc, newExpiry)
	return tokenResp.AccessToken, nil
}

func (h *Handler) buildProjectNRPConfig(ctx context.Context, project *models.Project) *models.ProjectNRPConfig {
	if project == nil {
		return nil
	}
	config := &models.ProjectNRPConfig{
		ProjectID:          project.ID,
		AccessEnabled:      project.NRPAccessEnabled,
		AccessEnabledBy:    project.NRPAccessEnabledBy,
		AccessEnabledAt:    project.NRPAccessEnabledAt,
		ExecutionEnabled:   project.NRPExecutionEnabled,
		ExecutionEnabledBy: project.NRPExecutionEnabledBy,
		ExecutionEnabledAt: project.NRPExecutionEnabledAt,
	}
	if project.NRPAccessEnabledBy != nil {
		if user, err := h.queries.GetUser(ctx, *project.NRPAccessEnabledBy); err == nil && user != nil {
			config.AccessEnabledByName = user.DisplayName
			if config.AccessEnabledByName == "" {
				config.AccessEnabledByName = user.Email
			}
		}
	}
	if project.NRPExecutionEnabledBy != nil {
		if user, err := h.queries.GetUser(ctx, *project.NRPExecutionEnabledBy); err == nil && user != nil {
			config.ExecutionEnabledByName = user.DisplayName
			if config.ExecutionEnabledByName == "" {
				config.ExecutionEnabledByName = user.Email
			}
		}
	}
	return config
}

func (h *Handler) userIsProjectAdmin(ctx context.Context, projectID string) bool {
	if UserHasRole(ctx, RoleAdmin) {
		return true
	}
	user := GetUserFromContext(ctx)
	if user == nil {
		return false
	}
	ok, err := h.queries.UserCanAccessProject(ctx, user.ID, projectID, "admin")
	return err == nil && ok
}

// GetNRPConfig returns the effective admin-managed NRP OAuth config.
func (h *Handler) GetNRPConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	issuer, _ := h.queries.GetAppConfig(ctx, "nrp_oidc_issuer")
	clientID, _ := h.queries.GetAppConfig(ctx, "nrp_oidc_client_id")
	exchangeURL, _ := h.queries.GetAppConfig(ctx, "nrp_llm_exchange_url")
	if issuer == "" {
		issuer = h.cfg.NRPOIDCIssuer
	}
	if clientID == "" {
		clientID = h.cfg.NRPOIDCClientID
	}
	if exchangeURL == "" {
		exchangeURL = h.cfg.NRPLLMExchangeURL
	}
	secretSet := false
	if s, _ := h.queries.GetAppConfig(ctx, "nrp_oidc_client_secret"); s != "" {
		secretSet = true
	} else if h.cfg.NRPOIDCClientSecret != "" {
		secretSet = true
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"nrp_oidc_issuer":      issuer,
		"nrp_oidc_client_id":   clientID,
		"nrp_llm_exchange_url": exchangeURL,
		"secret_set":           secretSet,
		"callback_url":         h.nrpRedirectURL(),
	})
}

// UpdateNRPConfig updates the DB-backed NRP OAuth config.
func (h *Handler) UpdateNRPConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Issuer       string `json:"nrp_oidc_issuer"`
		ClientID     string `json:"nrp_oidc_client_id"`
		ClientSecret string `json:"nrp_oidc_client_secret"`
		ExchangeURL  string `json:"nrp_llm_exchange_url"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	ctx := r.Context()
	if req.Issuer != "" {
		if err := h.queries.SetAppConfig(ctx, "nrp_oidc_issuer", req.Issuer); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to save NRP issuer")
			return
		}
	}
	if req.ClientID != "" {
		if err := h.queries.SetAppConfig(ctx, "nrp_oidc_client_id", req.ClientID); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to save NRP client ID")
			return
		}
	}
	if req.ClientSecret != "" {
		if err := h.setEncryptedConfig(ctx, "nrp_oidc_client_secret", req.ClientSecret); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to save NRP client secret")
			return
		}
	}
	if req.ExchangeURL != "" {
		if err := h.queries.SetAppConfig(ctx, "nrp_llm_exchange_url", req.ExchangeURL); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to save NRP exchange URL")
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetNRPLinkStatus returns the current user's NRP link status.
//
// TokenHealthy reports whether a valid (or refreshable) access token is
// available for downstream NRP API calls. It is only meaningful when
// Linked is true; the frontend uses it to prompt re-linking when the
// stored refresh token has expired.
func (h *Handler) GetNRPLinkStatus(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Linked          bool   `json:"linked"`
		NRPLogin        string `json:"nrp_login,omitempty"`
		OAuthConfigured bool   `json:"oauth_configured"`
		TokenHealthy    bool   `json:"token_healthy"`
	}
	ctx := r.Context()
	user := GetUserFromContext(ctx)
	issuer, _, _, _, err := h.getNRPOAuthConfig(ctx)
	oauthConfigured := err == nil
	if user == nil || !oauthConfigured {
		respondJSON(w, http.StatusOK, response{OAuthConfigured: oauthConfigured})
		return
	}
	identity, err := h.queries.FindLinkedIdentityByIssuer(ctx, user.ID, issuer)
	if err != nil || identity == nil {
		respondJSON(w, http.StatusOK, response{OAuthConfigured: oauthConfigured})
		return
	}
	// Probe the stored token (silent refresh if needed). If we can produce
	// a usable access token, the link is healthy. Failures here are not
	// fatal — the link still exists, the user just needs to re-link.
	tokenHealthy := h.getValidNRPToken(ctx, user.ID) != ""
	respondJSON(w, http.StatusOK, response{
		Linked:          true,
		NRPLogin:        identity.DisplayName,
		OAuthConfigured: oauthConfigured,
		TokenHealthy:    tokenHealthy,
	})
}

// StartNRPLink initiates the NRP OAuth flow to link a user identity.
func (h *Handler) StartNRPLink(w http.ResponseWriter, r *http.Request) {
	if !h.nrpOAuthConfigured(r.Context()) {
		respondError(w, http.StatusBadRequest, "NRP OAuth is not configured")
		return
	}
	stateBytes := make([]byte, 16)
	if _, err := io.ReadFull(cryptoRand, stateBytes); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate state")
		return
	}
	state := fmt.Sprintf("%x", stateBytes)
	http.SetCookie(w, &http.Cookie{
		Name:     "nrp_link_state",
		Value:    state,
		Path:     "/api/v1/nrp/link",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	authorizeURL, err := h.buildNRPAuthorizeURL(r.Context(), state)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build NRP authorize URL")
		respondError(w, http.StatusBadGateway, "Failed to initiate NRP authorization")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"authorize_url": authorizeURL})
}

// NRPLinkCallback handles the OAuth callback from the NRP OIDC provider.
func (h *Handler) NRPLinkCallback(w http.ResponseWriter, r *http.Request) {
	if !h.nrpOAuthConfigured(r.Context()) {
		renderIdentityLinkPopupResult(w, "nrp", false, "NRP OAuth is not configured.")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		renderIdentityLinkPopupResult(w, "nrp", false, "Missing code or state parameter.")
		return
	}
	stateCookie, err := r.Cookie("nrp_link_state")
	if err != nil || stateCookie.Value != state {
		renderIdentityLinkPopupResult(w, "nrp", false, "Invalid state parameter.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "nrp_link_state",
		Value:    "",
		Path:     "/api/v1/nrp/link",
		MaxAge:   -1,
		HttpOnly: true,
	})
	tokenResp, issuer, err := h.nrpExchangeCode(r.Context(), code)
	if err != nil {
		log.Error().Err(err).Msg("NRP OAuth token exchange failed")
		renderIdentityLinkPopupResult(w, "nrp", false, "Failed to exchange authorization code.")
		return
	}
	info, err := h.nrpGetUserInfo(r.Context(), issuer, tokenResp.AccessToken)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get NRP user info")
		renderIdentityLinkPopupResult(w, "nrp", false, "Failed to retrieve NRP user info.")
		return
	}
	var accessTokenEnc, refreshTokenEnc *string
	if h.encryptor != nil {
		if tokenResp.AccessToken != "" {
			enc, err := h.encryptor.EncryptConfigValue(tokenResp.AccessToken)
			if err != nil {
				log.Error().Err(err).Msg("Failed to encrypt NRP access token")
				renderIdentityLinkPopupResult(w, "nrp", false, "Failed to store NRP tokens.")
				return
			}
			accessTokenEnc = &enc
		}
		if tokenResp.RefreshToken != "" {
			enc, err := h.encryptor.EncryptConfigValue(tokenResp.RefreshToken)
			if err != nil {
				log.Error().Err(err).Msg("Failed to encrypt NRP refresh token")
				renderIdentityLinkPopupResult(w, "nrp", false, "Failed to store NRP tokens.")
				return
			}
			refreshTokenEnc = &enc
		}
	}
	var tokenExpiry *time.Time
	if tokenResp.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		tokenExpiry = &t
	}
	displayName := info.PreferredUsername
	if displayName == "" {
		displayName = info.Username
	}
	if displayName == "" {
		displayName = info.Name
	}
	if displayName == "" {
		displayName = info.Email
	}
	if displayName == "" {
		displayName = info.Subject
	}
	user := GetUserFromContext(r.Context())
	identity := &models.UserIdentity{
		UserID:          user.ID,
		Issuer:          issuer,
		Subject:         info.Subject,
		Email:           info.Email,
		DisplayName:     displayName,
		IDPName:         "nrp",
		AccessTokenEnc:  accessTokenEnc,
		RefreshTokenEnc: refreshTokenEnc,
		TokenExpiresAt:  tokenExpiry,
	}
	if err := h.queries.UpsertLinkedIdentity(r.Context(), identity); err != nil {
		log.Error().Err(err).Str("user_id", user.ID).Msg("Failed to upsert NRP identity")
		renderIdentityLinkPopupResult(w, "nrp", false, "Failed to link NRP account.")
		return
	}
	renderIdentityLinkPopupResult(w, "nrp", true, "Linked as "+displayName)
}

// DeleteNRPLink removes the configured NRP linked identity for the current user.
func (h *Handler) DeleteNRPLink(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	issuer, _, _, _, err := h.getNRPOAuthConfig(r.Context())
	if err != nil || issuer == "" {
		respondError(w, http.StatusBadRequest, "NRP OAuth is not configured")
		return
	}
	if err := h.queries.DeleteLinkedIdentityByIssuer(r.Context(), user.ID, issuer); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to unlink NRP account")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// GetProjectNRPConfig returns the NRP status for a project.
func (h *Handler) GetProjectNRPConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := h.queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}
	respondJSON(w, http.StatusOK, h.buildProjectNRPConfig(r.Context(), project))
}

// UpdateProjectNRPConfig updates project-scoped NRP settings.
func (h *Handler) UpdateProjectNRPConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := h.queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	var req struct {
		AccessEnabled    *bool `json:"access_enabled,omitempty"`
		ExecutionEnabled *bool `json:"execution_enabled,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	now := time.Now()
	if req.AccessEnabled != nil {
		isSystemAdmin := UserHasRole(r.Context(), RoleAdmin)
		isProjectAdmin := h.userIsProjectAdmin(r.Context(), projectID)
		hasLinkedNRPIdentity := h.getValidNRPToken(r.Context(), user.ID) != ""
		if !isSystemAdmin && !isProjectAdmin {
			respondError(w, http.StatusForbidden, "Project admin access required to change NRP access")
			return
		}
		if !isSystemAdmin && !hasLinkedNRPIdentity {
			respondError(w, http.StatusBadRequest, "Link your NRP account before changing NRP access")
			return
		}
		project.NRPAccessEnabled = *req.AccessEnabled
		project.NRPAccessEnabledBy = &user.ID
		project.NRPAccessEnabledAt = &now
		if !*req.AccessEnabled {
			project.NRPExecutionEnabled = false
			project.NRPExecutionEnabledBy = &user.ID
			project.NRPExecutionEnabledAt = &now
		}
	}
	if req.ExecutionEnabled != nil {
		isSystemAdmin := UserHasRole(r.Context(), RoleAdmin)
		if !isSystemAdmin && !h.userIsProjectAdmin(r.Context(), projectID) {
			respondError(w, http.StatusForbidden, "Project admin access required to change NRP execution")
			return
		}
		if !project.NRPAccessEnabled {
			respondError(w, http.StatusBadRequest, "NRP access must be enabled for this project first")
			return
		}
		if !isSystemAdmin && *req.ExecutionEnabled && h.getValidNRPToken(r.Context(), user.ID) == "" {
			respondError(w, http.StatusBadRequest, "Link your NRP account before enabling NRP execution")
			return
		}
		project.NRPExecutionEnabled = *req.ExecutionEnabled
		project.NRPExecutionEnabledBy = &user.ID
		project.NRPExecutionEnabledAt = &now
	}
	if err := h.queries.UpdateProject(r.Context(), project); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Msg("Failed to update NRP project config")
		respondError(w, http.StatusInternalServerError, "Failed to update NRP project config")
		return
	}
	respondJSON(w, http.StatusOK, h.buildProjectNRPConfig(r.Context(), project))
}

