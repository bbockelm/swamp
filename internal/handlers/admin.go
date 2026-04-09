package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
)

// --- Admin: User Management ---

// GetRecentLogs returns the recent warn/error log entries from the in-memory ring buffer.
func (h *Handler) GetRecentLogs(w http.ResponseWriter, r *http.Request) {
	if h.logBuf == nil {
		respondJSON(w, http.StatusOK, []struct{}{})
		return
	}
	respondJSON(w, http.StatusOK, h.logBuf.Entries())
}

// ListValidRoles returns the list of valid role names.
func (h *Handler) ListValidRoles(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, ValidRolesList())
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if req.DisplayName == "" {
		respondError(w, http.StatusBadRequest, "Display name required")
		return
	}
	if req.Role != "" && !validRoles[req.Role] {
		respondError(w, http.StatusBadRequest, "Invalid role")
		return
	}

	user := &models.User{DisplayName: req.DisplayName, Status: "active"}
	if err := h.queries.CreateUser(r.Context(), user); err != nil {
		log.Error().Err(err).Msg("Failed to create user")
		respondError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}
	if req.Role != "" {
		_ = h.queries.AddUserRole(r.Context(), user.ID, req.Role)
	}

	respondJSON(w, http.StatusCreated, user)
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list users")
		respondError(w, http.StatusInternalServerError, "Failed to list users")
		return
	}
	respondJSON(w, http.StatusOK, users)
}

// SearchUsers is accessible to any authenticated user.
// It returns users matching a search query (display_name or email).
func (h *Handler) SearchUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		respondJSON(w, http.StatusOK, []models.User{})
		return
	}
	users, err := h.queries.SearchUsers(r.Context(), q, 20)
	if err != nil {
		log.Error().Err(err).Msg("Failed to search users")
		respondError(w, http.StatusInternalServerError, "Search failed")
		return
	}
	if users == nil {
		users = []models.User{}
	}
	respondJSON(w, http.StatusOK, users)
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusNotFound, "User not found")
		return
	}
	respondJSON(w, http.StatusOK, user)
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusNotFound, "User not found")
		return
	}
	var updates models.User
	if err := decodeJSON(r, &updates); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if updates.DisplayName != "" {
		user.DisplayName = updates.DisplayName
	}
	if updates.Email != "" {
		user.Email = updates.Email
	}
	if updates.Status != "" {
		user.Status = updates.Status
	}
	if err := h.queries.UpdateUser(r.Context(), user); err != nil {
		log.Error().Err(err).Msg("Failed to update user")
		respondError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}
	respondJSON(w, http.StatusOK, user)
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if err := h.queries.DeleteUser(r.Context(), userID); err != nil {
		log.Error().Err(err).Msg("Failed to delete user")
		respondError(w, http.StatusInternalServerError, "Failed to delete user")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}

func (h *Handler) AddUserRole(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Role == "" {
		respondError(w, http.StatusBadRequest, "Role is required")
		return
	}
	if !validRoles[req.Role] {
		respondError(w, http.StatusBadRequest, "Invalid role")
		return
	}
	if err := h.queries.AddUserRole(r.Context(), userID, req.Role); err != nil {
		log.Error().Err(err).Msg("Failed to add role")
		respondError(w, http.StatusInternalServerError, "Failed to add role")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

func (h *Handler) RemoveUserRole(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	role := chi.URLParam(r, "role")
	if err := h.queries.RemoveUserRole(r.Context(), userID, role); err != nil {
		log.Error().Err(err).Msg("Failed to remove role")
		respondError(w, http.StatusInternalServerError, "Failed to remove role")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) ListUserRolesAdmin(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	roles, err := h.queries.ListUserRoles(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list user roles")
		respondError(w, http.StatusInternalServerError, "Failed to list user roles")
		return
	}
	respondJSON(w, http.StatusOK, roles)
}

func (h *Handler) ListUserIdentitiesAdmin(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	identities, err := h.queries.ListUserIdentities(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list user identities")
		respondError(w, http.StatusInternalServerError, "Failed to list user identities")
		return
	}
	respondJSON(w, http.StatusOK, identities)
}

func (h *Handler) DeleteUserIdentityAdmin(w http.ResponseWriter, r *http.Request) {
	identityID := chi.URLParam(r, "identityID")
	if err := h.queries.DeleteIdentity(r.Context(), identityID); err != nil {
		log.Error().Err(err).Msg("Failed to delete identity")
		respondError(w, http.StatusInternalServerError, "Failed to delete identity")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Admin: User Invites ---

func (h *Handler) CreateUserInviteHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")

	user, err := h.queries.GetUser(r.Context(), userID)
	if err != nil || user == nil {
		respondError(w, http.StatusNotFound, "User not found")
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	invite := &models.UserInvite{
		TokenHash: hashToken(token),
		CreatedBy: userID,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}
	if err := h.queries.CreateUserInvite(r.Context(), invite); err != nil {
		log.Error().Err(err).Str("user_id", userID).Msg("Failed to create invite")
		respondError(w, http.StatusInternalServerError, "Failed to create invite")
		return
	}

	inviteURL := fmt.Sprintf("%s/login/invite?token=%s", h.cfg.BaseURL, token)

	respondJSON(w, http.StatusCreated, map[string]any{
		"invite":     invite,
		"invite_url": inviteURL,
	})
}

func (h *Handler) ListUserInvitesHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	invites, err := h.queries.ListUserInvitesByTarget(r.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list invites")
		respondError(w, http.StatusInternalServerError, "Failed to list invites")
		return
	}
	if invites == nil {
		invites = []models.UserInvite{}
	}
	respondJSON(w, http.StatusOK, invites)
}

func (h *Handler) DeleteUserInviteHandler(w http.ResponseWriter, r *http.Request) {
	inviteID := chi.URLParam(r, "inviteID")
	if err := h.queries.DeleteUserInvite(r.Context(), inviteID); err != nil {
		log.Error().Err(err).Msg("Failed to delete invite")
		respondError(w, http.StatusInternalServerError, "Failed to delete invite")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Admin: OIDC Configuration ---

// setEncryptedConfig encrypts a config value before storing it.
func (h *Handler) setEncryptedConfig(ctx context.Context, key, plaintext string) error {
	if h.encryptor != nil && plaintext != "" {
		encrypted, err := h.encryptor.EncryptConfigValue(plaintext)
		if err != nil {
			return fmt.Errorf("encrypting config %s: %w", key, err)
		}
		return h.queries.SetAppConfig(ctx, key, encrypted)
	}
	return h.queries.SetAppConfig(ctx, key, plaintext)
}

func (h *Handler) GetOIDCConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	issuer, _ := h.queries.GetAppConfig(ctx, "oidc_issuer")
	clientID, _ := h.queries.GetAppConfig(ctx, "oidc_client_id")
	secretSet := false
	if s, _ := h.queries.GetAppConfig(ctx, "oidc_client_secret"); s != "" {
		secretSet = true
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"oidc_issuer":    issuer,
		"oidc_client_id": clientID,
		"secret_set":     secretSet,
		"callback_url":   h.cfg.BaseURL + "/api/v1/auth/oidc/callback",
	})
}

func (h *Handler) UpdateOIDCConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Issuer       string `json:"oidc_issuer"`
		ClientID     string `json:"oidc_client_id"`
		ClientSecret string `json:"oidc_client_secret"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	ctx := r.Context()
	if req.Issuer != "" {
		_ = h.queries.SetAppConfig(ctx, "oidc_issuer", req.Issuer)
	}
	if req.ClientID != "" {
		_ = h.queries.SetAppConfig(ctx, "oidc_client_id", req.ClientID)
	}
	if req.ClientSecret != "" {
		_ = h.setEncryptedConfig(ctx, "oidc_client_secret", req.ClientSecret)
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Admin: AUP Management ---

// getAUPVersion returns the effective AUP version: DB-stored value, or env var fallback.
func (h *Handler) getAUPVersion(ctx context.Context) string {
	if v, _ := h.queries.GetAppConfig(ctx, "aup_version"); v != "" {
		return v
	}
	return h.cfg.AUPVersion
}

// getAUPText returns the DB-stored AUP text, or a default if not set.
func (h *Handler) getAUPText(ctx context.Context) string {
	if v, _ := h.queries.GetAppConfig(ctx, "aup_text"); v != "" {
		return v
	}
	return ""
}

func (h *Handler) GetAUPConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	version := h.getAUPVersion(ctx)
	text := h.getAUPText(ctx)
	agreed, total, _ := h.queries.CountAUPAgreements(ctx, version)
	users, _ := h.queries.ListAUPStatus(ctx, version)
	if users == nil {
		users = []db.AUPUserStatus{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"version":     version,
		"text":        text,
		"agreed":      agreed,
		"total_users": total,
		"users":       users,
	})
}

func (h *Handler) UpdateAUPConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
		Text    string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	ctx := r.Context()
	if req.Version != "" {
		if err := h.queries.SetAppConfig(ctx, "aup_version", req.Version); err != nil {
			log.Error().Err(err).Msg("Failed to save AUP version")
			respondError(w, http.StatusInternalServerError, "Failed to save AUP version")
			return
		}
	}
	if err := h.queries.SetAppConfig(ctx, "aup_text", req.Text); err != nil {
		log.Error().Err(err).Msg("Failed to save AUP text")
		respondError(w, http.StatusInternalServerError, "Failed to save AUP text")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Admin: Executor Configuration ---

// executorConfigKeys lists all executor-related app_config keys and their
// corresponding JSON field names.
var executorConfigKeys = []struct {
	key  string
	json string
}{
	{"executor_mode", "executor_mode"},
	{"agent_provider", "agent_provider"},
	{"k8s_namespace", "k8s_namespace"},
	{"k8s_worker_image", "k8s_worker_image"},
	{"k8s_worker_service_account", "k8s_worker_service_account"},
	{"k8s_worker_cpu_request", "k8s_worker_cpu_request"},
	{"k8s_worker_cpu_limit", "k8s_worker_cpu_limit"},
	{"k8s_worker_mem_request", "k8s_worker_mem_request"},
	{"k8s_worker_mem_limit", "k8s_worker_mem_limit"},
	{"k8s_worker_node_selector", "k8s_worker_node_selector"},
	{"k8s_worker_tolerations", "k8s_worker_tolerations"},
	{"k8s_worker_labels", "k8s_worker_labels"},
	{"k8s_worker_annotations", "k8s_worker_annotations"},
	{"k8s_image_pull_secret", "k8s_image_pull_secret"},
	{"k8s_kubeconfig", "k8s_kubeconfig"},
	{"k8s_pod_ttl_seconds", "k8s_pod_ttl_seconds"},
	{"agent_model", "agent_model"},
	{"external_llm_analysis_model", "external_llm_analysis_model"},
	{"external_llm_poc_model", "external_llm_poc_model"},
	{"max_concurrent_analyses", "max_concurrent_analyses"},
}

func (h *Handler) GetExecutorConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result := map[string]string{
		"active_mode": h.cfg.ExecutorMode,
	}
	for _, kv := range executorConfigKeys {
		val, _ := h.queries.GetAppConfig(ctx, kv.key)
		result[kv.json] = val
	}
	// Provide env-based defaults as hints so the frontend can suggest them.
	result["hint_server_image"] = h.cfg.K8sServerImage
	result["hint_image_pull_secret"] = h.cfg.K8sImagePullSecret
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) UpdateExecutorConfig(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	validModes := map[string]bool{"local": true, "process": true, "kubernetes": true}
	if mode, ok := req["executor_mode"]; ok && !validModes[mode] {
		respondError(w, http.StatusBadRequest, "Invalid executor_mode: must be local, process, or kubernetes")
		return
	}
	validProviders := map[string]bool{"anthropic": true, "external": true}
	if provider, ok := req["agent_provider"]; ok && provider != "" && !validProviders[provider] {
		respondError(w, http.StatusBadRequest, "Invalid agent_provider: must be anthropic or external")
		return
	}

	ctx := r.Context()
	allowed := make(map[string]bool, len(executorConfigKeys))
	for _, kv := range executorConfigKeys {
		allowed[kv.key] = true
	}
	for key, value := range req {
		if !allowed[key] {
			continue
		}
		if err := h.queries.SetAppConfig(ctx, key, value); err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to save executor config")
			respondError(w, http.StatusInternalServerError, "Failed to save "+key)
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
