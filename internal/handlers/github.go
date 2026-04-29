package handlers

import (
	"context"
	cryptoRandPkg "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/github"
	"github.com/bbockelm/swamp/internal/models"
)

var cryptoRand io.Reader = cryptoRandPkg.Reader

// SetGitHubClient sets the GitHub App client on the handler.
func (h *Handler) SetGitHubClient(ghClient *github.Client) {
	h.ghClient = ghClient
}

// userCanUseInstallation checks whether the given user is authorized to use
// the specified GitHub App installation. Admins can use any installation;
// non-admins can only use installations they own or that are linked to
// projects they admin.
func (h *Handler) userCanUseInstallation(ctx context.Context, userID string, installationID int64) bool {
	if UserHasRole(ctx, RoleAdmin) {
		return true
	}
	installations, err := h.queries.ListInstallationsForUser(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list installations for authorization check")
		return false
	}
	for _, inst := range installations {
		if inst.InstallationID == installationID {
			return true
		}
	}
	return false
}

// GetGitHubStatus returns the GitHub App integration status (admin only).
func (h *Handler) GetGitHubStatus(w http.ResponseWriter, r *http.Request) {
	status := h.ghClient.Status(r.Context())
	respondJSON(w, http.StatusOK, status)
}

// ListGitHubInstallations returns GitHub App installations the current user
// is authorized to see:
//   - Admins: all installations (optionally filtered by ?owner=)
//   - Others: installations visible via their linked GitHub token (cross-
//     referenced with SWAMP's DB), plus any installations they created or
//     that are linked to projects where they have admin access.
func (h *Handler) ListGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Installations []models.GitHubAppInstallation `json:"installations"`
		InstallURL    string                         `json:"install_url,omitempty"`
	}

	if h.ghClient == nil || !h.ghClient.Configured() {
		respondJSON(w, http.StatusOK, response{Installations: []models.GitHubAppInstallation{}})
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var installations []models.GitHubAppInstallation
	var err error

	if UserHasRole(r.Context(), RoleAdmin) {
		installations, err = h.queries.ListGitHubInstallations(r.Context())
	} else {
		// Start with DB-known installations for this user.
		installations, err = h.queries.ListInstallationsForUser(r.Context(), user.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to list installations from DB")
			respondError(w, http.StatusInternalServerError, "Failed to list installations")
			return
		}
		if installations == nil {
			installations = []models.GitHubAppInstallation{}
		}

		// Also discover installations via the user's linked GitHub token.
		token := h.getValidGitHubToken(r.Context(), user.ID)
		if token != "" {
			userInstalls, ghErr := h.ghClient.ListUserInstallations(r.Context(), token)
			if ghErr != nil {
				log.Warn().Err(ghErr).Str("user_id", user.ID).Msg("Failed to list user GitHub installations")
			} else {
				// Merge: add any SWAMP-known installations visible to the
				// user on GitHub that aren't already in the list.
				seen := make(map[int64]bool, len(installations))
				for _, inst := range installations {
					seen[inst.InstallationID] = true
				}
				for _, ui := range userInstalls {
					if seen[ui.ID] {
						continue
					}
					swampInst, lookupErr := h.queries.GetInstallationByID(r.Context(), ui.ID)
					if lookupErr != nil || swampInst == nil {
						continue // Not registered in SWAMP
					}
					installations = append(installations, *swampInst)
					seen[ui.ID] = true
				}
			}
		}
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list installations")
		respondError(w, http.StatusInternalServerError, "Failed to list installations")
		return
	}
	if installations == nil {
		installations = []models.GitHubAppInstallation{}
	}

	// Filter by owner if specified (case-insensitive).
	owner := r.URL.Query().Get("owner")
	if owner != "" {
		filtered := make([]models.GitHubAppInstallation, 0, 1)
		for _, inst := range installations {
			if strings.EqualFold(inst.AccountLogin, owner) {
				filtered = append(filtered, inst)
			}
		}
		installations = filtered
	}

	respondJSON(w, http.StatusOK, response{
		Installations: installations,
		InstallURL:    h.ghClient.InstallURL(r.Context()),
	})
}

// ClaimInstallation lets an authenticated user claim ownership of an
// installation (sets installed_by_user_id if not already set). This is
// called after the user returns from installing the GitHub App.
func (h *Handler) ClaimInstallation(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	installationIDStr := chi.URLParam(r, "installationID")
	installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid installation ID")
		return
	}

	// Sync installations from GitHub first to ensure this one exists.
	if err := h.ghClient.SyncInstallations(r.Context()); err != nil {
		log.Error().Err(err).Msg("Failed to sync installations before claim")
	}

	// Verify the installation exists and is not already claimed.
	inst, err := h.queries.GetInstallationByID(r.Context(), installationID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Installation not found")
		return
	}
	if inst.InstalledByUserID != nil && *inst.InstalledByUserID != "" {
		// Already claimed — only allow if the claimer is the current owner.
		if *inst.InstalledByUserID != user.ID {
			respondError(w, http.StatusForbidden, "Installation is already claimed by another user")
			return
		}
		respondJSON(w, http.StatusOK, inst)
		return
	}

	// Try to claim (only sets if not already claimed).
	if err := h.queries.SetInstallationInstalledBy(r.Context(), installationID, user.ID); err != nil {
		log.Error().Err(err).Int64("installation_id", installationID).Msg("Failed to claim installation")
		respondError(w, http.StatusInternalServerError, "Failed to claim installation")
		return
	}

	// Return the installation.
	inst, err = h.queries.GetInstallationByID(r.Context(), installationID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Installation not found")
		return
	}
	respondJSON(w, http.StatusOK, inst)
}

// GetGitHubAppInfo returns non-sensitive GitHub App info (configured status
// and install URL). Available to any authenticated user.
func (h *Handler) GetGitHubAppInfo(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Configured bool   `json:"configured"`
		InstallURL string `json:"install_url,omitempty"`
	}
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondJSON(w, http.StatusOK, response{Configured: false})
		return
	}
	respondJSON(w, http.StatusOK, response{
		Configured: true,
		InstallURL: h.ghClient.InstallURL(r.Context()),
	})
}

// SyncGitHubInstallations fetches installations from GitHub and syncs to DB (admin only).
func (h *Handler) SyncGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}
	log.Info().Str("user_id", user.ID).Str("email", user.Email).Msg("Admin triggered GitHub installation sync")
	if err := h.ghClient.SyncInstallations(r.Context()); err != nil {
		log.Error().Err(err).Msg("Failed to sync GitHub installations")
		respondError(w, http.StatusInternalServerError, "Failed to sync installations")
		return
	}
	status := h.ghClient.Status(r.Context())
	log.Info().Int("installations", len(status.Installations)).Msg("GitHub installation sync completed")
	respondJSON(w, http.StatusOK, status)
}

// GetProjectGitHubConfig returns the GitHub integration settings for a project.
func (h *Handler) GetProjectGitHubConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	cfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
	if err != nil {
		// Return empty config if not set up.
		respondJSON(w, http.StatusOK, &models.ProjectGitHubConfig{
			ProjectID: projectID,
		})
		return
	}
	respondJSON(w, http.StatusOK, cfg)
}

// UpdateProjectGitHubConfig creates or updates the GitHub config for a project.
func (h *Handler) UpdateProjectGitHubConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	var req struct {
		GitHubOwner        string   `json:"github_owner"`
		GitHubRepo         string   `json:"github_repo"`
		DefaultBranch      string   `json:"default_branch"`
		InstallationID     int64    `json:"installation_id"`
		SARIFUploadEnabled bool     `json:"sarif_upload_enabled"`
		WebhookEnabled     bool     `json:"webhook_enabled"`
		WebhookEvents      []string `json:"webhook_events"`
		WebhookAgentModel  string   `json:"webhook_agent_model"`
		WebhookProviderID  *string  `json:"webhook_provider_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.GitHubOwner == "" || req.GitHubRepo == "" {
		respondError(w, http.StatusBadRequest, "github_owner and github_repo are required")
		return
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.WebhookEvents == nil {
		req.WebhookEvents = []string{}
	}

	// Verify the user is authorized to use this installation.
	if req.InstallationID != 0 {
		user := GetUserFromContext(r.Context())
		if user == nil {
			respondError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		if !h.userCanUseInstallation(r.Context(), user.ID, req.InstallationID) {
			respondError(w, http.StatusForbidden, "You are not authorized to use this GitHub App installation")
			return
		}
	}

	if err := h.queries.UpsertProjectGitHubConfig(r.Context(), projectID, req.GitHubOwner, req.GitHubRepo, req.DefaultBranch, req.InstallationID, req.SARIFUploadEnabled, req.WebhookEnabled, req.WebhookEvents, req.WebhookAgentModel, req.WebhookProviderID); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Msg("Failed to save project GitHub config")
		respondError(w, http.StatusInternalServerError, "Failed to save GitHub config")
		return
	}

	cfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Saved but failed to retrieve config")
		return
	}
	respondJSON(w, http.StatusOK, cfg)
}

// DeleteProjectGitHubConfig removes the GitHub integration for a project.
func (h *Handler) DeleteProjectGitHubConfig(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if err := h.queries.DeleteProjectGitHubConfig(r.Context(), projectID); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to delete GitHub config")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ListPackageBranches lists branches for a package's GitHub repository,
// using the GitHub App installation token for private repo access.
func (h *Handler) ListPackageBranches(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}
	projectID := chi.URLParam(r, "projectID")
	pkgID := chi.URLParam(r, "packageID")
	pkg, err := h.queries.GetPackage(r.Context(), pkgID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Package not found")
		return
	}
	if pkg.GitHubOwner == "" || pkg.GitHubRepo == "" {
		respondError(w, http.StatusBadRequest, "Package has no GitHub App integration configured")
		return
	}
	installationID := int64(0)
	if inst, err := h.queries.GetProjectInstallationByOwner(r.Context(), projectID, pkg.GitHubOwner); err == nil {
		installationID = inst.InstallationID
	}
	if installationID == 0 {
		if ghCfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID); err == nil && ghCfg.InstallationID != 0 {
			if ghCfg.GitHubOwner == "" || strings.EqualFold(ghCfg.GitHubOwner, pkg.GitHubOwner) {
				installationID = ghCfg.InstallationID
			}
		}
	}
	if installationID == 0 {
		respondError(w, http.StatusBadRequest, "No project-linked GitHub App installation matches this repository owner")
		return
	}
	branches, err := h.ghClient.ListBranches(r.Context(), installationID, pkg.GitHubOwner, pkg.GitHubRepo)
	if err != nil {
		log.Error().Err(err).Str("package_id", pkgID).Msg("Failed to list branches")
		respondError(w, http.StatusBadGateway, "Failed to list branches from GitHub")
		return
	}
	respondJSON(w, http.StatusOK, branches)
}

// ListRepoBranches lists branches for a GitHub repo by owner/repo.
// Restricted to admins because it uses SWAMP's installation tokens to access
// repos without user-level authorization, which could expose private repos
// belonging to other organizations.
// GET /api/v1/github/branches?owner=X&repo=Y
func (h *Handler) ListRepoBranches(w http.ResponseWriter, r *http.Request) {
	if !UserHasRole(r.Context(), RoleAdmin) {
		respondError(w, http.StatusForbidden, "Admin access required")
		return
	}
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusBadRequest, "GitHub App is not configured")
		return
	}

	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	if owner == "" || repo == "" {
		respondError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}

	// Look up installation by owner (org-scoped, not user-scoped).
	inst, err := h.queries.GetInstallationByOwner(r.Context(), owner)
	if err != nil || inst == nil {
		respondError(w, http.StatusNotFound, "No GitHub App installation found for this repository owner")
		return
	}

	branches, err := h.ghClient.ListBranches(r.Context(), inst.InstallationID, owner, repo)
	if err != nil {
		log.Error().Err(err).Str("owner", owner).Str("repo", repo).Msg("Failed to list branches via installation")
		respondError(w, http.StatusBadGateway, "Failed to list branches from GitHub")
		return
	}
	respondJSON(w, http.StatusOK, branches)
}

// CheckRepoAccess verifies whether the GitHub App can access a specific
// repository. Restricted to admins because it uses SWAMP's installation tokens
// to probe private repo access without user-level authorization.
// Regular users should use UserRepoAccess instead.
// GET /api/v1/github/check-repo-access?owner=X&repo=Y
func (h *Handler) CheckRepoAccess(w http.ResponseWriter, r *http.Request) {
	if !UserHasRole(r.Context(), RoleAdmin) {
		respondError(w, http.StatusForbidden, "Admin access required")
		return
	}
	type response struct {
		HasInstallation bool   `json:"has_installation"`
		Accessible      bool   `json:"accessible"`
		DefaultBranch   string `json:"default_branch,omitempty"`
		Error           string `json:"error,omitempty"`
		InstallURL      string `json:"install_url,omitempty"`
	}

	if h.ghClient == nil || !h.ghClient.Configured() {
		respondJSON(w, http.StatusOK, response{Error: "GitHub App is not configured"})
		return
	}

	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	if owner == "" || repo == "" {
		respondError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}

	result := h.ghClient.CheckRepoAccess(r.Context(), owner, repo)

	resp := response{
		HasInstallation: result.HasInstallation,
		Accessible:      result.Accessible,
		DefaultBranch:   result.DefaultBranch,
		Error:           result.Error,
	}
	// Include install URL when there's no installation for this owner.
	if !result.HasInstallation {
		resp.InstallURL = h.ghClient.InstallURL(r.Context())
	}
	respondJSON(w, http.StatusOK, resp)
}

// ============================================================
// GitHub identity linking (OAuth user authorization)
// ============================================================

// GetGitHubLinkStatus returns the current user's GitHub link status.
// GET /api/v1/github/link
func (h *Handler) GetGitHubLinkStatus(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Linked          bool   `json:"linked"`
		GitHubLogin     string `json:"github_login,omitempty"`
		OAuthURL        string `json:"oauth_url,omitempty"`
		OAuthConfigured bool   `json:"oauth_configured"`
	}

	user := GetUserFromContext(r.Context())
	oauthConfigured := h.ghClient != nil && h.ghClient.OAuthConfigured()

	identity, err := h.queries.FindGitHubIdentity(r.Context(), user.ID)
	if err != nil || identity == nil {
		resp := response{OAuthConfigured: oauthConfigured}
		respondJSON(w, http.StatusOK, resp)
		return
	}

	respondJSON(w, http.StatusOK, response{
		Linked:          true,
		GitHubLogin:     identity.DisplayName,
		OAuthConfigured: oauthConfigured,
	})
}

// StartGitHubLink initiates the GitHub OAuth flow to link a GitHub account.
// POST /api/v1/github/link
func (h *Handler) StartGitHubLink(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.OAuthConfigured() {
		respondError(w, http.StatusBadRequest, "GitHub OAuth is not configured")
		return
	}

	// Generate a random state parameter and store it in the session cookie.
	stateBytes := make([]byte, 16)
	if _, err := io.ReadFull(cryptoRand, stateBytes); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate state")
		return
	}
	state := fmt.Sprintf("%x", stateBytes)

	// Store state in an HttpOnly cookie so we can validate on callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "github_link_state",
		Value:    state,
		Path:     "/api/v1/github/link",
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})

	respondJSON(w, http.StatusOK, map[string]string{
		"authorize_url": h.ghClient.OAuthAuthorizeURL(state),
	})
}

// GitHubLinkCallback handles the OAuth callback from GitHub.
// GET /api/v1/github/link/callback?code=X&state=Y
func (h *Handler) GitHubLinkCallback(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.OAuthConfigured() {
		renderIdentityLinkPopupResult(w, "github", false, "GitHub OAuth is not configured.")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		renderIdentityLinkPopupResult(w, "github", false, "Missing code or state parameter.")
		return
	}

	// Validate state against cookie.
	stateCookie, err := r.Cookie("github_link_state")
	if err != nil || stateCookie.Value != state {
		renderIdentityLinkPopupResult(w, "github", false, "Invalid state parameter.")
		return
	}

	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     "github_link_state",
		Value:    "",
		Path:     "/api/v1/github/link",
		MaxAge:   -1,
		HttpOnly: true,
	})

	// Exchange code for tokens.
	tokenResp, err := h.ghClient.OAuthExchangeCode(r.Context(), code)
	if err != nil {
		log.Error().Err(err).Msg("GitHub OAuth token exchange failed")
		renderIdentityLinkPopupResult(w, "github", false, "Failed to exchange authorization code.")
		return
	}

	// Get user info from GitHub.
	ghUser, err := h.ghClient.GetUser(r.Context(), tokenResp.AccessToken)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get GitHub user info")
		renderIdentityLinkPopupResult(w, "github", false, "Failed to retrieve GitHub user info.")
		return
	}

	// Encrypt tokens before storing.
	var accessTokenEnc, refreshTokenEnc *string
	if h.encryptor != nil {
		if tokenResp.AccessToken != "" {
			enc, err := h.encryptor.EncryptConfigValue(tokenResp.AccessToken)
			if err != nil {
				log.Error().Err(err).Msg("Failed to encrypt GitHub access token")
				renderIdentityLinkPopupResult(w, "github", false, "Failed to store GitHub tokens.")
				return
			}
			accessTokenEnc = &enc
		}
		if tokenResp.RefreshToken != "" {
			enc, err := h.encryptor.EncryptConfigValue(tokenResp.RefreshToken)
			if err != nil {
				log.Error().Err(err).Msg("Failed to encrypt GitHub refresh token")
				renderIdentityLinkPopupResult(w, "github", false, "Failed to store GitHub tokens.")
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

	user := GetUserFromContext(r.Context())
	identity := &models.UserIdentity{
		UserID:          user.ID,
		Subject:         fmt.Sprintf("%d", ghUser.ID),
		Email:           ghUser.Email,
		DisplayName:     ghUser.Login,
		AccessTokenEnc:  accessTokenEnc,
		RefreshTokenEnc: refreshTokenEnc,
		TokenExpiresAt:  tokenExpiry,
	}
	if err := h.queries.UpsertGitHubIdentity(r.Context(), identity); err != nil {
		log.Error().Err(err).Str("user_id", user.ID).Msg("Failed to upsert GitHub identity")
		renderIdentityLinkPopupResult(w, "github", false, "Failed to link GitHub account.")
		return
	}

	renderIdentityLinkPopupResult(w, "github", true, "Linked as "+ghUser.Login)
}

// DeleteGitHubLink removes the GitHub identity link for the current user.
// DELETE /api/v1/github/link
func (h *Handler) DeleteGitHubLink(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if err := h.queries.DeleteGitHubIdentity(r.Context(), user.ID); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to unlink GitHub account")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// getValidGitHubToken returns a valid GitHub access token for the user,
// refreshing if necessary. Returns "" if the user has no GitHub link or
// the token cannot be refreshed.
func (h *Handler) getValidGitHubToken(ctx context.Context, userID string) string {
	identity, err := h.queries.FindGitHubIdentity(ctx, userID)
	if err != nil || identity == nil || identity.AccessTokenEnc == nil {
		return ""
	}

	// Decrypt access token.
	if h.encryptor == nil {
		return ""
	}
	accessToken, err := h.encryptor.DecryptConfigValue(*identity.AccessTokenEnc)
	if err != nil {
		log.Warn().Err(err).Str("user_id", userID).Msg("Failed to decrypt GitHub access token")
		return ""
	}

	// Check if token is still valid (with 5-minute buffer).
	if identity.TokenExpiresAt != nil && identity.TokenExpiresAt.Before(time.Now().Add(5*time.Minute)) {
		// Token expired or expiring — try to refresh.
		if identity.RefreshTokenEnc == nil {
			return ""
		}
		refreshToken, err := h.encryptor.DecryptConfigValue(*identity.RefreshTokenEnc)
		if err != nil {
			log.Warn().Err(err).Str("user_id", userID).Msg("Failed to decrypt GitHub refresh token")
			return ""
		}

		tokenResp, err := h.ghClient.OAuthRefreshToken(ctx, refreshToken)
		if err != nil {
			log.Warn().Err(err).Str("user_id", userID).Msg("Failed to refresh GitHub token")
			return ""
		}

		// Encrypt and store new tokens.
		var newAccessEnc, newRefreshEnc *string
		enc, err := h.encryptor.EncryptConfigValue(tokenResp.AccessToken)
		if err == nil {
			newAccessEnc = &enc
		}
		if tokenResp.RefreshToken != "" {
			enc, err := h.encryptor.EncryptConfigValue(tokenResp.RefreshToken)
			if err == nil {
				newRefreshEnc = &enc
			}
		}
		var newExpiry *time.Time
		if tokenResp.ExpiresIn > 0 {
			t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
			newExpiry = &t
		}
		_ = h.queries.UpdateIdentityTokens(ctx, identity.ID, newAccessEnc, newRefreshEnc, newExpiry)

		return tokenResp.AccessToken
	}

	return accessToken
}

// UserRepoAccess reports whether a project's GitHub App installation can clone
// a specific repository — which is the same question SWAMP asks at analysis
// time. The user's personal GitHub access is irrelevant; what matters is the
// installation token the project will use.
//
// Primary path (project_id provided): check the project-linked installation
// for the repo owner directly.  This always works regardless of whether the
// user's personal GitHub token is fresh.
//
// Discovery path (no project link yet): if the user has a linked GitHub token,
// enumerate their visible installations via GET /user/installations to suggest
// which installation to link.
//
// GET /api/v1/github/user-repo-access?owner=X&repo=Y[&project_id=P]
func (h *Handler) UserRepoAccess(w http.ResponseWriter, r *http.Request) {
	type matchedInstallation struct {
		InstallationID int64  `json:"installation_id"`
		AccountLogin   string `json:"account_login"`
		Accessible     bool   `json:"accessible"`
		DefaultBranch  string `json:"default_branch,omitempty"`
	}
	type response struct {
		Linked               bool                  `json:"linked"`
		HasInstallation      bool                  `json:"has_installation"`
		Accessible           bool                  `json:"accessible"`
		DefaultBranch        string                `json:"default_branch,omitempty"`
		InstallationID       int64                 `json:"installation_id,omitempty"`
		InstallationAccount  string                `json:"installation_account_login,omitempty"`
		Error                string                `json:"error,omitempty"`
		InstallURL           string                `json:"install_url,omitempty"`
		NeedsLink            bool                  `json:"needs_link"`
		MatchedInstallations []matchedInstallation `json:"matched_installations,omitempty"`
	}

	if h.ghClient == nil || !h.ghClient.Configured() {
		respondJSON(w, http.StatusOK, response{Error: "GitHub App is not configured"})
		return
	}

	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")
	if owner == "" || repo == "" {
		respondError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}
	projectID := r.URL.Query().Get("project_id")

	// Primary path: if the project already has a linked installation for this
	// owner, check access using the installation token — no user OAuth token
	// required, because this is exactly the credential used at clone time.
	if projectID != "" {
		if inst, err := h.queries.GetProjectInstallationByOwner(r.Context(), projectID, owner); err == nil && inst != nil {
			accessible, defaultBranch, err := h.ghClient.UserCanAccessRepo(r.Context(), "", inst.InstallationID, owner, repo)
			if err == nil && accessible {
				respondJSON(w, http.StatusOK, response{
					HasInstallation:     true,
					Accessible:          true,
					DefaultBranch:       defaultBranch,
					InstallationID:      inst.InstallationID,
					InstallationAccount: inst.AccountLogin,
				})
				return
			}
			// Installation exists but can't reach this specific repo.
			respondJSON(w, http.StatusOK, response{
				HasInstallation: true,
				Error: fmt.Sprintf(
					"The GitHub App is installed for %q but cannot access %s/%s. Check the installation's repository scope.",
					owner, owner, repo),
			})
			return
		}
	}

	// Discovery path: no project-linked installation yet.
	// Use the user's GitHub token (if available) to enumerate installations
	// visible to them and suggest which one to link.
	user := GetUserFromContext(r.Context())
	token := h.getValidGitHubToken(r.Context(), user.ID)
	if token == "" {
		// No user token and no project-linked installation — guide the user.
		resp := response{
			NeedsLink: h.ghClient.OAuthConfigured(),
		}
		if h.ghClient.OAuthConfigured() {
			resp.Error = "Link your GitHub account so SWAMP can discover your App installations."
			resp.InstallURL = h.ghClient.InstallURL(r.Context())
		} else {
			resp.Error = "No GitHub App installation is linked to this project for this repository owner."
		}
		respondJSON(w, http.StatusOK, resp)
		return
	}

	// Enumerate installations visible to this user and cross-reference with
	// SWAMP's DB to find candidates the user could link to the project.
	userInstalls, err := h.ghClient.ListUserInstallations(r.Context(), token)
	if err != nil {
		log.Warn().Err(err).Str("user_id", user.ID).Msg("Failed to list GitHub user installations")
		// Stale token — can't discover, but we already confirmed there's no
		// project-linked installation, so tell the user to re-link or install.
		respondJSON(w, http.StatusOK, response{
			Linked: true,
			Error:  "Your GitHub link needs to be refreshed. Re-link your account to discover installations.",
		})
		return
	}

	var matched []matchedInstallation
	ownerMatched := false
	for _, ui := range userInstalls {
		swampInst, err := h.queries.GetInstallationByID(r.Context(), ui.ID)
		if err != nil || swampInst == nil {
			continue
		}
		mi := matchedInstallation{
			InstallationID: swampInst.InstallationID,
			AccountLogin:   swampInst.AccountLogin,
		}
		if strings.EqualFold(swampInst.AccountLogin, owner) {
			ownerMatched = true
			accessible, defaultBranch, err := h.ghClient.UserCanAccessRepo(r.Context(), "", swampInst.InstallationID, owner, repo)
			if err == nil && accessible {
				// Found a SWAMP-known installation that can clone the repo —
				// return it so the frontend can prompt the user to link it.
				respondJSON(w, http.StatusOK, response{
					Linked:              true,
					HasInstallation:     true,
					Accessible:          true,
					DefaultBranch:       defaultBranch,
					InstallationID:      swampInst.InstallationID,
					InstallationAccount: swampInst.AccountLogin,
				})
				return
			}
			mi.Accessible = accessible
		}
		matched = append(matched, mi)
	}

	resp := response{
		Linked:               true,
		MatchedInstallations: matched,
	}
	if ownerMatched {
		resp.HasInstallation = true
		resp.Error = fmt.Sprintf("The GitHub App is installed for %q but cannot access %s/%s. Check the installation's repository scope.", owner, owner, repo)
	} else {
		resp.InstallURL = h.ghClient.InstallURL(r.Context())
		resp.Error = fmt.Sprintf("No GitHub App installation found for %q. Install the app to enable access.", owner)
	}
	respondJSON(w, http.StatusOK, resp)
}

// projectInstallationView is the per-installation response for the project
// installations endpoint.
type projectInstallationView struct {
	models.GitHubAppInstallation
	LinkedToProject bool                 `json:"linked_to_project"`
	EnabledBy       *string              `json:"enabled_by,omitempty"`
	EnabledByName   string               `json:"enabled_by_name,omitempty"`
	EnabledAt       *time.Time           `json:"enabled_at,omitempty"`
	Packages        []packageInstallInfo `json:"packages"`
}

type packageInstallInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	GitHubOwner string `json:"github_owner"`
	GitHubRepo  string `json:"github_repo"`
}

// ListProjectInstallations returns all GitHub App installations relevant to a
// project: those explicitly linked via the M-N table and those in use by
// packages in the project. Admins additionally see all known installations.
func (h *Handler) ListProjectInstallations(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	isAdmin := UserHasRole(r.Context(), RoleAdmin)
	if !isAdmin {
		ok, err := h.queries.UserCanAccessProject(r.Context(), user.ID, projectID, "admin")
		if err != nil || !ok {
			respondError(w, http.StatusForbidden, "Access denied")
			return
		}
	}

	// Collect all installations we want to surface.
	installMap := make(map[int64]models.GitHubAppInstallation)

	if isAdmin {
		all, err := h.queries.ListGitHubInstallations(r.Context())
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to list installations")
			return
		}
		for _, inst := range all {
			installMap[inst.InstallationID] = inst
		}
	}

	// Explicit project links.
	links, err := h.queries.ListProjectInstallationLinks(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list project installation links")
		return
	}
	linkMap := make(map[int64]models.ProjectInstallationLink)
	for _, l := range links {
		linkMap[l.InstallationID] = l
		if _, ok := installMap[l.InstallationID]; !ok {
			inst, err := h.queries.GetInstallationByID(r.Context(), l.InstallationID)
			if err == nil {
				installMap[l.InstallationID] = *inst
			}
		}
	}

	// Installations in use by packages.
	pkgs, err := h.queries.ListProjectPackages(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list project packages")
		return
	}
	pkgsByInstall := make(map[int64][]packageInstallInfo)
	ownerInstallCache := make(map[string]int64)
	for _, pkg := range pkgs {
		if pkg.GitHubOwner == "" {
			continue
		}
		ownerKey := strings.ToLower(pkg.GitHubOwner)
		instID, ok := ownerInstallCache[ownerKey]
		if !ok {
			if inst, err := h.queries.GetProjectInstallationByOwner(r.Context(), projectID, pkg.GitHubOwner); err == nil {
				instID = inst.InstallationID
				if _, known := installMap[instID]; !known {
					installMap[instID] = *inst
				}
			}
			ownerInstallCache[ownerKey] = instID
		}
		if instID <= 0 {
			continue
		}
		pkgsByInstall[instID] = append(pkgsByInstall[instID], packageInstallInfo{
			ID:          pkg.ID,
			Name:        pkg.Name,
			GitHubOwner: pkg.GitHubOwner,
			GitHubRepo:  pkg.GitHubRepo,
		})
	}

	// Build the combined response slice.
	views := make([]projectInstallationView, 0, len(installMap))
	for id, inst := range installMap {
		view := projectInstallationView{
			GitHubAppInstallation: inst,
			Packages:              pkgsByInstall[id],
		}
		if view.Packages == nil {
			view.Packages = []packageInstallInfo{}
		}
		if link, ok := linkMap[id]; ok {
			view.LinkedToProject = true
			view.EnabledBy = link.EnabledBy
			view.EnabledByName = link.EnabledByName
			view.EnabledAt = &link.EnabledAt
		}
		views = append(views, view)
	}

	// Sort: linked-to-project first, then by account login.
	for i := 1; i < len(views); i++ {
		for j := i; j > 0; j-- {
			a, b := views[j-1], views[j]
			if (!a.LinkedToProject && b.LinkedToProject) ||
				(a.LinkedToProject == b.LinkedToProject && a.AccountLogin > b.AccountLogin) {
				views[j-1], views[j] = views[j], views[j-1]
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{"installations": views})
}

// AddProjectInstallation explicitly links a GitHub App installation to a
// project. Requires project admin or site admin.
func (h *Handler) AddProjectInstallation(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	installationIDStr := chi.URLParam(r, "installationID")
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	isAdmin := UserHasRole(r.Context(), RoleAdmin)
	if !isAdmin {
		ok, err := h.queries.UserCanAccessProject(r.Context(), user.ID, projectID, "admin")
		if err != nil || !ok {
			respondError(w, http.StatusForbidden, "Access denied")
			return
		}
	}
	installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid installation ID")
		return
	}
	// Verify the installation exists in SWAMP's DB.
	if _, err := h.queries.GetInstallationByID(r.Context(), installationID); err != nil {
		respondError(w, http.StatusNotFound, "Installation not found")
		return
	}
	if err := h.queries.AddProjectInstallation(r.Context(), projectID, installationID, user.ID); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Int64("installation_id", installationID).
			Msg("Failed to add project installation link")
		respondError(w, http.StatusInternalServerError, "Failed to link installation")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RemoveProjectInstallation removes the explicit link between a project and a
// GitHub App installation. Requires project admin or site admin.
func (h *Handler) RemoveProjectInstallation(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	installationIDStr := chi.URLParam(r, "installationID")
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	isAdmin := UserHasRole(r.Context(), RoleAdmin)
	if !isAdmin {
		ok, err := h.queries.UserCanAccessProject(r.Context(), user.ID, projectID, "admin")
		if err != nil || !ok {
			respondError(w, http.StatusForbidden, "Access denied")
			return
		}
	}
	installationID, err := strconv.ParseInt(installationIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid installation ID")
		return
	}
	if err := h.queries.RemoveProjectInstallation(r.Context(), projectID, installationID); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Int64("installation_id", installationID).
			Msg("Failed to remove project installation link")
		respondError(w, http.StatusInternalServerError, "Failed to unlink installation")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ListWebhookDeliveries returns webhook delivery logs for a project.
func (h *Handler) ListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	deliveries, err := h.queries.ListWebhookDeliveries(r.Context(), projectID, 100)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list webhook deliveries")
		return
	}
	if deliveries == nil {
		deliveries = []models.GitHubWebhookDelivery{}
	}
	respondJSON(w, http.StatusOK, deliveries)
}

// HandleGitHubWebhook processes incoming GitHub webhook events.
// This endpoint is public (no auth) but validates HMAC-SHA256 signatures.
func (h *Handler) HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if h.ghClient == nil || !h.ghClient.Configured() {
		respondError(w, http.StatusServiceUnavailable, "GitHub App not configured")
		return
	}

	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Validate signature.
	signature := r.Header.Get("X-Hub-Signature-256")
	if !h.ghClient.ValidateWebhookSignature(body, signature) {
		respondError(w, http.StatusUnauthorized, "Invalid webhook signature")
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	// Handle installation lifecycle events (created/deleted) before parsing
	// repo-specific payload fields, since these events don't have a repository.
	if eventType == "installation" {
		h.handleInstallationEvent(r.Context(), body, deliveryID)
		respondJSON(w, http.StatusOK, map[string]string{"status": "processed", "event": "installation"})
		return
	}

	if eventType == "code_scanning_alert" {
		h.handleCodeScanningAlertWebhook(w, r, body, deliveryID)
		return
	}

	// Parse common payload fields.
	var payload struct {
		Action     string `json:"action"`
		Repository struct {
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		} `json:"repository"`
		Ref    string `json:"ref"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
		// pull_request event fields
		PullRequest *struct {
			Number int    `json:"number"`
			State  string `json:"state"`
			Head   struct {
				Ref string `json:"ref"` // branch name
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		} `json:"pull_request,omitempty"`
		// release event fields
		Release *struct {
			TagName    string `json:"tag_name"`
			Name       string `json:"name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		} `json:"release,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	log.Info().
		Str("event", eventType).
		Str("delivery_id", deliveryID).
		Str("repo", payload.Repository.FullName).
		Str("action", payload.Action).
		Msg("Received GitHub webhook")

	// Find matching packages by repo.
	parts := strings.SplitN(payload.Repository.FullName, "/", 2)
	if len(parts) != 2 {
		respondError(w, http.StatusBadRequest, "Invalid repository name")
		return
	}
	owner, repo := parts[0], parts[1]

	matchedPackages, findErr := h.queries.FindPackagesByGitHubRepo(r.Context(), owner, repo)
	if findErr != nil {
		respondError(w, http.StatusInternalServerError, "Failed to resolve packages for repository")
		return
	}

	var projectIDPtr *string
	if len(matchedPackages) == 1 {
		projectIDPtr = &matchedPackages[0].ProjectID
	}

	packagesByProject := make(map[string][]models.SoftwarePackage)
	for _, pkg := range matchedPackages {
		packagesByProject[pkg.ProjectID] = append(packagesByProject[pkg.ProjectID], pkg)
	}

	// Record the delivery.
	delivery := &models.GitHubWebhookDelivery{
		DeliveryID:   deliveryID,
		EventType:    eventType,
		Action:       payload.Action,
		RepoFullName: payload.Repository.FullName,
		Ref:          payload.Ref,
		SenderLogin:  payload.Sender.Login,
		ProjectID:    projectIDPtr,
		Status:       "received",
		PayloadJSON:  json.RawMessage(body),
	}
	_ = h.queries.InsertWebhookDelivery(r.Context(), delivery)

	updateStatus := func(status, detail string, analysisID *string) {
		if delivery.ID != "" {
			_ = h.queries.UpdateWebhookDeliveryStatus(r.Context(), delivery.ID, status, detail, analysisID)
		}
	}

	if len(matchedPackages) == 0 {
		updateStatus("ignored", "No matching package found", nil)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no matching package"})
		return
	}

	// Trigger analysis based on event type.
	switch eventType {
	case "push":
		branch := normalizeWebhookBranch(payload.Ref)
		if branch == "" {
			updateStatus("ignored", "Push missing branch ref", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing branch ref"})
			return
		}
		info := webhookTriggerInfo{
			Event:  "push",
			Branch: branch,
			Meta: map[string]interface{}{
				"ref":         payload.Ref,
				"repo":        payload.Repository.FullName,
				"push_sender": payload.Sender.Login,
			},
		}

		var analysisIDs []string
		for projectID, pkgs := range packagesByProject {
			ghCfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
			if err != nil || !ghCfg.WebhookEnabled || !containsString(ghCfg.WebhookEvents, "push") {
				continue
			}

			hasExplicit := false
			selected := make([]models.SoftwarePackage, 0, len(pkgs))
			for _, pkg := range pkgs {
				if pkg.WebhookPushEnabled {
					hasExplicit = true
				}
			}
			for _, pkg := range pkgs {
				if !strings.EqualFold(pkg.GitBranch, branch) {
					continue
				}
				if hasExplicit {
					if pkg.WebhookPushEnabled {
						selected = append(selected, pkg)
					}
				} else {
					selected = append(selected, pkg)
				}
			}
			if len(selected) == 0 {
				continue
			}

			analysisID, triggerErr := h.triggerWebhookAnalysis(r.Context(), ghCfg, selected, payload.Sender.Login, info)
			if triggerErr != nil {
				log.Error().Err(triggerErr).Str("project_id", projectID).Msg("Failed to trigger webhook analysis")
				continue
			}
			analysisIDs = append(analysisIDs, analysisID)
		}

		if len(analysisIDs) == 0 {
			updateStatus("ignored", "No package matched push trigger settings", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no package matched"})
			return
		}

		updateStatus("processed", fmt.Sprintf("Triggered %d analyses", len(analysisIDs)), nil)
		respondJSON(w, http.StatusOK, map[string]interface{}{"status": "processed", "analysis_ids": analysisIDs})

	case "pull_request":
		if payload.PullRequest == nil {
			updateStatus("ignored", "Missing pull_request payload", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing pull_request payload"})
			return
		}
		// Only trigger on opened or synchronized (new commits pushed).
		if payload.Action != "opened" && payload.Action != "synchronize" {
			updateStatus("ignored", "Ignored pull_request action: "+payload.Action, nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "action not relevant"})
			return
		}
		prURL := fmt.Sprintf("https://github.com/%s/pull/%d", payload.Repository.FullName, payload.PullRequest.Number)
		info := webhookTriggerInfo{
			Event:  "pull_request",
			Branch: payload.PullRequest.Head.Ref,
			Commit: payload.PullRequest.Head.SHA,
			Meta: map[string]interface{}{
				"pr_number": payload.PullRequest.Number,
				"pr_url":    prURL,
				"pr_action": payload.Action,
				"head_ref":  payload.PullRequest.Head.Ref,
				"head_sha":  payload.PullRequest.Head.SHA,
				"base_ref":  payload.PullRequest.Base.Ref,
				"repo":      payload.Repository.FullName,
			},
		}

		baseBranch := payload.PullRequest.Base.Ref
		var analysisIDs []string
		for projectID, pkgs := range packagesByProject {
			ghCfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
			if err != nil || !ghCfg.WebhookEnabled || !containsString(ghCfg.WebhookEvents, "pull_request") {
				continue
			}

			hasExplicit := false
			selected := make([]models.SoftwarePackage, 0, len(pkgs))
			for _, pkg := range pkgs {
				if pkg.WebhookPREnabled {
					hasExplicit = true
				}
			}
			for _, pkg := range pkgs {
				if !strings.EqualFold(pkg.GitBranch, baseBranch) {
					continue
				}
				if hasExplicit {
					if pkg.WebhookPREnabled {
						selected = append(selected, pkg)
					}
				} else {
					selected = append(selected, pkg)
				}
			}
			if len(selected) == 0 {
				continue
			}

			analysisID, triggerErr := h.triggerWebhookAnalysis(r.Context(), ghCfg, selected, payload.Sender.Login, info)
			if triggerErr != nil {
				log.Error().Err(triggerErr).Str("project_id", projectID).Msg("Failed to trigger webhook analysis for PR")
				continue
			}
			analysisIDs = append(analysisIDs, analysisID)
		}

		if len(analysisIDs) == 0 {
			updateStatus("ignored", "No package matched pull request trigger settings", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no package matched"})
			return
		}

		log.Info().
			Int("pr_number", payload.PullRequest.Number).
			Str("branch", payload.PullRequest.Head.Ref).
			Int("analyses", len(analysisIDs)).
			Msg("Triggered analyses for pull request")
		updateStatus("processed", fmt.Sprintf("Triggered %d analyses for PR #%d", len(analysisIDs), payload.PullRequest.Number), nil)
		respondJSON(w, http.StatusOK, map[string]interface{}{"status": "processed", "analysis_ids": analysisIDs})

	case "release":
		if payload.Release == nil {
			updateStatus("ignored", "Missing release payload", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing release payload"})
			return
		}
		// Only trigger on published (not drafts, edits, or deletes).
		if payload.Action != "published" {
			updateStatus("ignored", "Ignored release action: "+payload.Action, nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "action not relevant"})
			return
		}
		if payload.Release.Draft {
			updateStatus("ignored", "Ignored draft release", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "draft release"})
			return
		}
		releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", payload.Repository.FullName, payload.Release.TagName)
		info := webhookTriggerInfo{
			Event:  "release",
			Branch: payload.Release.TagName,
			Meta: map[string]interface{}{
				"tag":          payload.Release.TagName,
				"release_name": payload.Release.Name,
				"release_url":  releaseURL,
				"prerelease":   payload.Release.Prerelease,
				"repo":         payload.Repository.FullName,
			},
		}
		var analysisIDs []string
		for projectID, pkgs := range packagesByProject {
			ghCfg, err := h.queries.GetProjectGitHubConfig(r.Context(), projectID)
			if err != nil || !ghCfg.WebhookEnabled || !containsString(ghCfg.WebhookEvents, "release") {
				continue
			}
			analysisID, triggerErr := h.triggerWebhookAnalysis(r.Context(), ghCfg, pkgs, payload.Sender.Login, info)
			if triggerErr != nil {
				log.Error().Err(triggerErr).Str("project_id", projectID).Msg("Failed to trigger webhook analysis for release")
				continue
			}
			analysisIDs = append(analysisIDs, analysisID)
		}
		if len(analysisIDs) == 0 {
			updateStatus("ignored", "No project matched release webhook settings", nil)
			respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no project matched"})
			return
		}
		log.Info().
			Str("tag", payload.Release.TagName).
			Int("analyses", len(analysisIDs)).
			Msg("Triggered analyses for release")
		updateStatus("processed", fmt.Sprintf("Triggered %d analyses for release %s", len(analysisIDs), payload.Release.TagName), nil)
		respondJSON(w, http.StatusOK, map[string]interface{}{"status": "processed", "analysis_ids": analysisIDs})

	default:
		updateStatus("ignored", "Unhandled event type: "+eventType, nil)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unhandled event type"})
	}
}

type codeScanningAlertWebhookPayload struct {
	Action     string `json:"action"`
	CommitOID  string `json:"commit_oid"`
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	Alert struct {
		Number           int64      `json:"number"`
		HTMLURL          string     `json:"html_url"`
		State            string     `json:"state"`
		DismissedReason  string     `json:"dismissed_reason"`
		DismissedComment string     `json:"dismissed_comment"`
		FixedAt          *time.Time `json:"fixed_at"`
		Rule             struct {
			ID string `json:"id"`
		} `json:"rule"`
		MostRecentInstance struct {
			Ref       string `json:"ref"`
			CommitSHA string `json:"commit_sha"`
			Location  struct {
				Path      string `json:"path"`
				StartLine int    `json:"start_line"`
				EndLine   int    `json:"end_line"`
			} `json:"location"`
		} `json:"most_recent_instance"`
	} `json:"alert"`
}

func (h *Handler) handleCodeScanningAlertWebhook(w http.ResponseWriter, r *http.Request, body []byte, deliveryID string) {
	var payload codeScanningAlertWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	log.Info().
		Str("event", "code_scanning_alert").
		Str("delivery_id", deliveryID).
		Str("repo", payload.Repository.FullName).
		Str("action", payload.Action).
		Int64("alert_number", payload.Alert.Number).
		Msg("Received GitHub code scanning alert webhook")

	parts := strings.SplitN(payload.Repository.FullName, "/", 2)
	if len(parts) != 2 {
		respondError(w, http.StatusBadRequest, "Invalid repository name")
		return
	}
	owner, repo := parts[0], parts[1]

	matchedPackages, findErr := h.queries.FindPackagesByGitHubRepo(r.Context(), owner, repo)
	if findErr != nil {
		respondError(w, http.StatusInternalServerError, "Failed to resolve packages for repository")
		return
	}
	var projectIDPtr *string
	if len(matchedPackages) == 1 {
		projectIDPtr = &matchedPackages[0].ProjectID
	}

	delivery := &models.GitHubWebhookDelivery{
		DeliveryID:   deliveryID,
		EventType:    "code_scanning_alert",
		Action:       payload.Action,
		RepoFullName: payload.Repository.FullName,
		Ref:          payload.Ref,
		SenderLogin:  payload.Sender.Login,
		ProjectID:    projectIDPtr,
		Status:       "received",
		PayloadJSON:  json.RawMessage(body),
	}
	_ = h.queries.InsertWebhookDelivery(r.Context(), delivery)

	updateStatus := func(status, detail string) {
		if delivery.ID != "" {
			_ = h.queries.UpdateWebhookDeliveryStatus(r.Context(), delivery.ID, status, detail, nil)
		}
	}

	if len(matchedPackages) == 0 {
		updateStatus("ignored", "No matching package found")
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no matching package"})
		return
	}

	if payload.Alert.Number == 0 {
		updateStatus("ignored", "Missing alert number")
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "missing alert number"})
		return
	}

	branch := normalizeWebhookBranch(payload.Ref)
	if branch == "" {
		branch = normalizeWebhookBranch(payload.Alert.MostRecentInstance.Ref)
	}

	packagesByProject := make(map[string][]string)
	for _, pkg := range matchedPackages {
		if !pkg.GitHubSyncEnabled {
			continue
		}
		if branch != "" && !strings.EqualFold(pkg.GitBranch, branch) {
			continue
		}
		packagesByProject[pkg.ProjectID] = append(packagesByProject[pkg.ProjectID], pkg.ID)
	}
	if len(packagesByProject) == 0 {
		updateStatus("ignored", "No package has GitHub sync enabled for this repo/branch")
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no package sync enabled"})
		return
	}

	syncedAt := time.Now().UTC()
	rowsUpdatedTotal := int64(0)
	commitSHA := payload.CommitOID
	if commitSHA == "" {
		commitSHA = payload.Alert.MostRecentInstance.CommitSHA
	}
	for projectID, packageIDs := range packagesByProject {
		rowsUpdated, err := h.queries.UpdatePackageFindingsGitHubAlertByNumber(
			r.Context(),
			projectID,
			packageIDs,
			payload.Alert.Number,
			payload.Alert.HTMLURL,
			payload.Alert.State,
			payload.Alert.DismissedReason,
			payload.Alert.DismissedComment,
			payload.Alert.FixedAt,
			syncedAt,
		)
		if err != nil {
			log.Error().Err(err).Str("project_id", projectID).Int64("alert_number", payload.Alert.Number).Msg("Failed to update finding by GitHub alert number")
			updateStatus("error", err.Error())
			respondError(w, http.StatusInternalServerError, "Failed to update finding state")
			return
		}

		if rowsUpdated == 0 {
			rowsUpdated, err = h.queries.UpdatePackageFindingsGitHubAlertByLocation(
				r.Context(),
				projectID,
				packageIDs,
				payload.Alert.Rule.ID,
				payload.Alert.MostRecentInstance.Location.Path,
				payload.Alert.MostRecentInstance.Location.StartLine,
				payload.Alert.MostRecentInstance.Location.EndLine,
				commitSHA,
				payload.Alert.Number,
				payload.Alert.HTMLURL,
				payload.Alert.State,
				payload.Alert.DismissedReason,
				payload.Alert.DismissedComment,
				payload.Alert.FixedAt,
				syncedAt,
			)
			if err != nil {
				log.Error().Err(err).Str("project_id", projectID).Int64("alert_number", payload.Alert.Number).Msg("Failed to update finding by GitHub alert location")
				updateStatus("error", err.Error())
				respondError(w, http.StatusInternalServerError, "Failed to update finding state")
				return
			}
		}
		rowsUpdatedTotal += rowsUpdated
	}

	if rowsUpdatedTotal == 0 {
		detail := fmt.Sprintf("No matching findings found for GitHub alert %d", payload.Alert.Number)
		updateStatus("ignored", detail)
		respondJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "no matching findings"})
		return
	}

	detail := fmt.Sprintf("Synchronized %d finding(s) for GitHub alert %d", rowsUpdatedTotal, payload.Alert.Number)
	updateStatus("processed", detail)
	respondJSON(w, http.StatusOK, map[string]interface{}{"status": "processed", "updated_findings": rowsUpdatedTotal, "alert_number": payload.Alert.Number})
}

// webhookTriggerInfo carries metadata about the triggering event.
type webhookTriggerInfo struct {
	Event  string                 // "push", "pull_request", "release"
	Branch string                 // branch name or tag
	Commit string                 // commit SHA if known
	Meta   map[string]interface{} // additional fields (pr_number, pr_url, tag, etc.)
}

// triggerWebhookAnalysis creates and starts an analysis triggered by a webhook.
func (h *Handler) triggerWebhookAnalysis(ctx context.Context, ghCfg *models.ProjectGitHubConfig, packages []models.SoftwarePackage, senderLogin string, info webhookTriggerInfo) (string, error) {
	if len(packages) == 0 {
		return "", nil
	}

	// analyses.triggered_by is a UUID FK to users.id, so we attribute the
	// webhook-triggered analysis to the project owner. The actual GitHub
	// actor is recorded in trigger_meta.sender for display.
	project, err := h.queries.GetProject(ctx, ghCfg.ProjectID)
	if err != nil {
		return "", fmt.Errorf("looking up project for webhook trigger: %w", err)
	}
	if project.OwnerID == "" {
		return "", fmt.Errorf("project %s has no owner; cannot attribute webhook analysis", ghCfg.ProjectID)
	}

	meta := info.Meta
	if meta == nil {
		meta = map[string]interface{}{}
	}
	if senderLogin != "" {
		meta["sender"] = senderLogin
	}
	metaBytes, _ := json.Marshal(meta)

	// Build agent_config with provider info if configured.
	agentConfig := map[string]interface{}{}
	if ghCfg.WebhookProviderID != nil && *ghCfg.WebhookProviderID != "" {
		agentConfig["llm_provider_id"] = *ghCfg.WebhookProviderID
		agentConfig["provider_source"] = "global"
		if label := h.resolveProviderLabel(ctx, ghCfg.ProjectID, *ghCfg.WebhookProviderID, "global"); label != "" {
			agentConfig["provider_label"] = label
		}
	}
	configBytes, _ := json.Marshal(agentConfig)

	analysis := &models.Analysis{
		ProjectID:    ghCfg.ProjectID,
		Status:       "pending",
		TriggeredBy:  project.OwnerID,
		AgentModel:   ghCfg.WebhookAgentModel,
		AgentConfig:  json.RawMessage(configBytes),
		GitBranch:    info.Branch,
		GitCommit:    info.Commit,
		TriggerEvent: info.Event,
		TriggerMeta:  json.RawMessage(metaBytes),
	}

	// Generate a per-analysis DEK for encrypting output artifacts.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		return "", err
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		return "", err
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := h.queries.CreateAnalysis(ctx, analysis); err != nil {
		return "", err
	}

	packageMeta := make([]string, 0, len(packages))
	githubConfigured := 0
	for _, p := range packages {
		packageMeta = append(packageMeta, p.Name+"("+p.GitBranch+")")
		if p.GitHubOwner != "" && p.GitHubRepo != "" {
			githubConfigured++
		}
	}
	log.Info().
		Str("analysis_id", analysis.ID).
		Str("project_id", analysis.ProjectID).
		Str("trigger", "github_webhook").
		Int("package_count", len(packages)).
		Int("github_clone_capable_packages", githubConfigured).
		Strs("packages", packageMeta).
		Str("event", info.Event).
		Str("branch", info.Branch).
		Msg("Created analysis")

	// Link all packages.
	for _, pkg := range packages {
		if err := h.queries.AddAnalysisPackage(ctx, analysis.ID, pkg.ID); err != nil {
			log.Error().Err(err).Str("analysis_id", analysis.ID).Str("package_id", pkg.ID).Msg("Failed to link package")
		}
	}

	// Submit to executor.
	if h.executor != nil {
		h.executor.Submit(analysis, packages)
	}

	return analysis.ID, nil
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func normalizeWebhookBranch(ref string) string {
	if strings.HasPrefix(ref, "refs/heads/") {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	return ref
}

// handleInstallationEvent processes GitHub App installation/uninstallation events.
func (h *Handler) handleInstallationEvent(ctx context.Context, body []byte, deliveryID string) {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
		} `json:"installation"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Error().Err(err).Str("delivery_id", deliveryID).Msg("Failed to parse installation event")
		return
	}

	installationID := payload.Installation.ID
	accountLogin := payload.Installation.Account.Login
	accountType := payload.Installation.Account.Type

	log.Info().
		Str("action", payload.Action).
		Int64("installation_id", installationID).
		Str("account", accountLogin).
		Str("sender", payload.Sender.Login).
		Msg("Processing installation event")

	switch payload.Action {
	case "created":
		if err := h.queries.UpsertGitHubInstallation(ctx, installationID, accountLogin, accountType, []byte("{}")); err != nil {
			log.Error().Err(err).Int64("installation_id", installationID).Msg("Failed to upsert installation")
			return
		}
	case "deleted":
		if err := h.queries.DeleteGitHubInstallation(ctx, installationID); err != nil {
			log.Error().Err(err).Int64("installation_id", installationID).Msg("Failed to delete installation")
		}
	default:
		log.Debug().Str("action", payload.Action).Int64("installation_id", installationID).Msg("Ignored installation action")
	}
}
