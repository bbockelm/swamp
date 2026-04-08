package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
)

// ListProjectProviderKeys returns all provider keys for a project (metadata only, no secrets).
func (h *Handler) ListProjectProviderKeys(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	keys, err := h.queries.ListProjectProviderKeys(r.Context(), projectID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list provider keys")
		respondError(w, http.StatusInternalServerError, "Failed to list provider keys")
		return
	}
	if keys == nil {
		keys = []models.ProjectProviderKey{}
	}
	respondJSON(w, http.StatusOK, keys)
}

// CreateProjectProviderKey encrypts and stores a new provider API key.
func (h *Handler) CreateProjectProviderKey(w http.ResponseWriter, r *http.Request) {
	if h.encryptor == nil {
		respondError(w, http.StatusServiceUnavailable, "Encryption not configured")
		return
	}

	projectID := chi.URLParam(r, "projectID")
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var req struct {
		Provider    string `json:"provider"`
		Label       string `json:"label"`
		APIKey      string `json:"api_key"`
		EndpointURL string `json:"endpoint_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if req.APIKey == "" {
		respondError(w, http.StatusBadRequest, "api_key is required")
		return
	}
	if req.Provider == "" {
		req.Provider = "anthropic"
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	validProviders := map[string]bool{"anthropic": true, "nrp": true, "custom": true, "external_llm": true}
	if !validProviders[req.Provider] {
		respondError(w, http.StatusBadRequest, "Provider must be one of: anthropic, nrp, custom, external_llm")
		return
	}
	if req.Provider == "custom" && req.EndpointURL == "" {
		respondError(w, http.StatusBadRequest, "endpoint_url is required for custom provider")
		return
	}
	if req.EndpointURL != "" {
		parsed, err := url.Parse(req.EndpointURL)
		if err != nil || parsed.Host == "" {
			respondError(w, http.StatusBadRequest, "endpoint_url must be a valid URL with a host")
			return
		}
		if parsed.Scheme != "https" {
			respondError(w, http.StatusBadRequest, "endpoint_url must use https")
			return
		}
	}

	// Generate a per-key DEK and encrypt the API key.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate DEK")
		respondError(w, http.StatusInternalServerError, "Encryption error")
		return
	}
	encryptedKey, err := crypto.Encrypt(dek, []byte(req.APIKey))
	if err != nil {
		log.Error().Err(err).Msg("Failed to encrypt API key")
		respondError(w, http.StatusInternalServerError, "Encryption error")
		return
	}
	encryptedDEK, dekNonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		log.Error().Err(err).Msg("Failed to wrap DEK")
		respondError(w, http.StatusInternalServerError, "Encryption error")
		return
	}

	// Derive a safe hint (last 4 chars).
	hint := ""
	if len(req.APIKey) >= 4 {
		hint = "..." + req.APIKey[len(req.APIKey)-4:]
	}

	k := &models.ProjectProviderKey{
		ProjectID:    projectID,
		Provider:     req.Provider,
		Label:        req.Label,
		KeyHint:      hint,
		EndpointURL:  req.EndpointURL,
		EncryptedKey: encryptedKey,
		EncryptedDEK: encryptedDEK,
		DEKNonce:     dekNonce,
		CreatedBy:    user.ID,
	}
	if err := h.queries.CreateProjectProviderKey(r.Context(), k); err != nil {
		log.Error().Err(err).Msg("Failed to create provider key")
		respondError(w, http.StatusInternalServerError, "Failed to create provider key")
		return
	}

	respondJSON(w, http.StatusCreated, k)
}

// RevokeProjectProviderKey soft-deletes a provider key.
func (h *Handler) RevokeProjectProviderKey(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")

	// Verify the key belongs to this project.
	projectID := chi.URLParam(r, "projectID")
	key, err := h.queries.GetProjectProviderKey(r.Context(), keyID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Key not found")
		return
	}
	if key.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Key not found")
		return
	}

	if err := h.queries.RevokeProjectProviderKey(r.Context(), keyID); err != nil {
		log.Error().Err(err).Msg("Failed to revoke provider key")
		respondError(w, http.StatusInternalServerError, "Failed to revoke key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteProjectProviderKey permanently deletes a provider key.
func (h *Handler) DeleteProjectProviderKey(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")

	// Verify the key belongs to this project.
	projectID := chi.URLParam(r, "projectID")
	key, err := h.queries.GetProjectProviderKey(r.Context(), keyID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Key not found")
		return
	}
	if key.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Key not found")
		return
	}

	if err := h.queries.DeleteProjectProviderKey(r.Context(), keyID); err != nil {
		log.Error().Err(err).Msg("Failed to delete provider key")
		respondError(w, http.StatusInternalServerError, "Failed to delete key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DecryptProviderKey decrypts a ProjectProviderKey and returns the plaintext API key.
// This is used internally by the agent executor, not exposed via HTTP.
func (h *Handler) DecryptProviderKey(key *models.ProjectProviderKey) (string, error) {
	dek, err := h.encryptor.UnwrapDEK(key.EncryptedDEK, key.DEKNonce)
	if err != nil {
		return "", err
	}
	plaintext, err := crypto.Decrypt(dek, key.EncryptedKey)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
