package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
)

// errNRPSessionExpired is returned when the calling user's NRP OAuth
// session has expired (token expired with no working refresh, etc) and
// the user must re-authenticate to obtain a fresh access token.
var errNRPSessionExpired = errors.New("NRP session expired; re-authenticate to continue")

// nrpReauthRequiredCode is the machine-readable error code surfaced when
// the calling user's NRP session has expired and they must re-run the
// OAuth flow. The frontend keys off this to render a re-auth prompt.
const nrpReauthRequiredCode = "nrp_reauth_required"

// respondNRPReauthRequired writes a structured error response that the
// frontend can detect to trigger an interactive re-authentication.
func respondNRPReauthRequired(w http.ResponseWriter, message string) {
	if message == "" {
		message = errNRPSessionExpired.Error()
	}
	respondJSON(w, http.StatusBadRequest, map[string]string{
		"error": message,
		"code":  nrpReauthRequiredCode,
	})
}

// nrpListLLMGroupsResponse is the body of GET /projects/:id/nrp/llm-groups.
// Groups are returned to the frontend as flat names — the upstream already
// filters to LLM-eligible groups the user is a member of.
type nrpListLLMGroupsResponse struct {
	Groups []string `json:"groups"`
}

// nrpInstallLLMKeyRequest is the body of POST /projects/:id/nrp/install-llm-key.
type nrpInstallLLMKeyRequest struct {
	GroupName string `json:"group_name"`
}

// upstreamLLMGroupsResponse mirrors the body returned by the NRP
// /api/token/llm/groups endpoint: {"groups": ["sdsc-ai", "nrp-ai"]}.
type upstreamLLMGroupsResponse struct {
	Groups []string `json:"groups"`
}

// nrpLLMGroupsURL returns the upstream REST endpoint for listing LLM-
// eligible groups, preferring DB config, then env config, then deriving
// from the exchange URL by replacing /exchange with /groups.
func (h *Handler) nrpLLMGroupsURL(ctx context.Context) string {
	if v, _ := h.queries.GetAppConfig(ctx, "nrp_llm_groups_url"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if strings.TrimSpace(h.cfg.NRPLLMGroupsURL) != "" {
		return strings.TrimSpace(h.cfg.NRPLLMGroupsURL)
	}
	exchangeURL, _ := h.queries.GetAppConfig(ctx, "nrp_llm_exchange_url")
	if strings.TrimSpace(exchangeURL) == "" {
		exchangeURL = h.cfg.NRPLLMExchangeURL
	}
	exchangeURL = strings.TrimSpace(exchangeURL)
	if strings.HasSuffix(exchangeURL, "/exchange") {
		return strings.TrimSuffix(exchangeURL, "/exchange") + "/groups"
	}
	return ""
}

// nrpLLMAPIBaseURL returns the OpenAI-compatible API base URL where the
// issued LiteLLM token can be used. DB config wins, then env config.
func (h *Handler) nrpLLMAPIBaseURL(ctx context.Context) string {
	if v, _ := h.queries.GetAppConfig(ctx, "nrp_llm_api_base_url"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(h.cfg.NRPLLMAPIBaseURL)
}


// nrpListUserLLMGroups calls the upstream /api/token/llm/groups endpoint
// with the user's NRP access token and returns the LLM-eligible group
// names. The upstream already filters to LiteLLM-enabled groups the user
// is a member of, so no client-side filtering is required.
func (h *Handler) nrpListUserLLMGroups(ctx context.Context, accessToken string) ([]string, error) {
	groupsURL := h.nrpLLMGroupsURL(ctx)
	if groupsURL == "" {
		return nil, fmt.Errorf("NRP LLM groups URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, groupsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling NRP llm/groups: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("NRP llm/groups returned %d: %s", resp.StatusCode, msg)
	}
	var out upstreamLLMGroupsResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decoding llm/groups response: %w", err)
	}

	groups := make([]string, 0, len(out.Groups))
	for _, g := range out.Groups {
		if name := strings.TrimSpace(g); name != "" {
			groups = append(groups, name)
		}
	}
	return groups, nil
}

// nrpExchangeLLMToken posts to the NRP llm/exchange endpoint and returns
// the issued plaintext token.
func (h *Handler) nrpExchangeLLMToken(ctx context.Context, accessToken, groupName string) (string, error) {
	exchangeURL, _ := h.queries.GetAppConfig(ctx, "nrp_llm_exchange_url")
	if strings.TrimSpace(exchangeURL) == "" {
		exchangeURL = h.cfg.NRPLLMExchangeURL
	}
	if exchangeURL == "" {
		return "", fmt.Errorf("NRP LLM exchange URL is not configured")
	}
	body, err := json.Marshal(map[string]string{"group_name": groupName})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling NRP llm/exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("NRP llm/exchange returned %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decoding exchange response: %w", err)
	}
	if strings.TrimSpace(out.Token) == "" {
		return "", fmt.Errorf("NRP llm/exchange returned an empty token")
	}
	return out.Token, nil
}

// ListProjectNRPLLMGroups returns the calling user's NRP groups that are
// eligible for LLM key issuance.
func (h *Handler) ListProjectNRPLLMGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUserFromContext(ctx)
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	projectID := chi.URLParam(r, "projectID")
	if !h.userIsProjectAdmin(ctx, projectID) {
		respondError(w, http.StatusForbidden, "Project admin access required")
		return
	}

	accessToken, err := h.validateNRPToken(ctx, user.ID)
	if err != nil {
		log.Info().Err(err).Str("user_id", user.ID).Str("project_id", projectID).Msg("NRP LLM groups: re-authentication required")
		respondNRPReauthRequired(w, "")
		return
	}

	groups, err := h.nrpListUserLLMGroups(ctx, accessToken)
	if err != nil {
		log.Warn().Err(err).Str("user_id", user.ID).Msg("Failed to list NRP LLM groups")
		respondError(w, http.StatusBadGateway, "Failed to list NRP LLM groups: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, nrpListLLMGroupsResponse{Groups: groups})
}

// InstallProjectNRPLLMKey exchanges the calling user's NRP token for an
// LLM-API token and stores it as a project provider key. If group_name is
// empty, it is auto-selected when the user belongs to exactly one
// LLM-eligible group.
func (h *Handler) InstallProjectNRPLLMKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUserFromContext(ctx)
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	if h.encryptor == nil {
		respondError(w, http.StatusServiceUnavailable, "Encryption not configured")
		return
	}

	projectID := chi.URLParam(r, "projectID")
	if !h.userIsProjectAdmin(ctx, projectID) {
		respondError(w, http.StatusForbidden, "Project admin access required")
		return
	}

	var req nrpInstallLLMKeyRequest
	// Allow empty body (auto-select).
	_ = decodeJSON(r, &req)
	groupName := strings.TrimSpace(req.GroupName)

	accessToken, err := h.validateNRPToken(ctx, user.ID)
	if err != nil {
		log.Info().Err(err).Str("user_id", user.ID).Str("project_id", projectID).Msg("NRP LLM key install: re-authentication required")
		respondNRPReauthRequired(w, "")
		return
	}

	// If no group_name was provided, look up the user's LLM-eligible groups.
	// If exactly one, use it. Otherwise require the caller to choose.
	if groupName == "" {
		groups, err := h.nrpListUserLLMGroups(ctx, accessToken)
		if err != nil {
			respondError(w, http.StatusBadGateway, "Failed to list NRP LLM groups: "+err.Error())
			return
		}
		if len(groups) == 0 {
			respondError(w, http.StatusBadRequest, "Your NRP account is not a member of any LLM-eligible groups")
			return
		}
		if len(groups) > 1 {
			respondError(w, http.StatusBadRequest, "Multiple LLM-eligible groups available; specify group_name")
			return
		}
		groupName = groups[0]
	}

	apiBaseURL := h.nrpLLMAPIBaseURL(ctx)
	if apiBaseURL == "" {
		respondError(w, http.StatusServiceUnavailable, "NRP LLM API base URL is not configured")
		return
	}

	llmToken, err := h.nrpExchangeLLMToken(ctx, accessToken, groupName)
	if err != nil {
		log.Warn().Err(err).Str("user_id", user.ID).Str("project_id", projectID).Str("group", groupName).Msg("NRP LLM key exchange failed")
		respondError(w, http.StatusBadGateway, "NRP LLM key exchange failed: "+err.Error())
		return
	}

	// Encrypt the issued token and store as a project provider key with
	// provider="nrp". Revoke any prior active NRP key so analyses pick up
	// the freshly issued one.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Encryption error")
		return
	}
	encryptedKey, err := crypto.Encrypt(dek, []byte(llmToken))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Encryption error")
		return
	}
	encryptedDEK, dekNonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Encryption error")
		return
	}

	hint := ""
	if len(llmToken) >= 4 {
		hint = "..." + llmToken[len(llmToken)-4:]
	}
	label := fmt.Sprintf("NRP LLM (%s)", groupName)

	if existing, err := h.queries.GetActiveProviderKey(ctx, projectID, "nrp"); err == nil && existing != nil {
		_ = h.queries.RevokeProjectProviderKey(ctx, existing.ID)
	}

	k := &models.ProjectProviderKey{
		ProjectID:    projectID,
		Provider:     "nrp",
		Label:        label,
		KeyHint:      hint,
		EndpointURL:  apiBaseURL,
		APISchema:    "openai",
		EncryptedKey: encryptedKey,
		EncryptedDEK: encryptedDEK,
		DEKNonce:     dekNonce,
		CreatedBy:    user.ID,
	}
	if err := h.queries.CreateProjectProviderKey(ctx, k); err != nil {
		log.Error().Err(err).Msg("Failed to store NRP LLM key")
		respondError(w, http.StatusInternalServerError, "Failed to store NRP LLM key")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"key_id":     k.ID,
		"label":      k.Label,
		"key_hint":   k.KeyHint,
		"group_name": groupName,
	})
}
