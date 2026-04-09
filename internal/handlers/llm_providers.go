package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
)

// --- Admin: Global LLM Provider Management ---

// ListLLMProviders returns all global LLM providers.
func (h *Handler) ListLLMProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.queries.ListLLMProviders(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list LLM providers")
		respondError(w, http.StatusInternalServerError, "Failed to list providers")
		return
	}
	if providers == nil {
		providers = []models.LLMProvider{}
	}
	respondJSON(w, http.StatusOK, providers)
}

// CreateLLMProvider creates a new global LLM provider with encrypted API key.
func (h *Handler) CreateLLMProvider(w http.ResponseWriter, r *http.Request) {
	if h.encryptor == nil {
		respondError(w, http.StatusServiceUnavailable, "Encryption not configured")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var req struct {
		Label        string `json:"label"`
		APISchema    string `json:"api_schema"`
		BaseURL      string `json:"base_url"`
		DefaultModel string `json:"default_model"`
		APIKey       string `json:"api_key"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if req.Label == "" {
		respondError(w, http.StatusBadRequest, "label is required")
		return
	}
	req.APISchema = strings.ToLower(strings.TrimSpace(req.APISchema))
	if req.APISchema != "anthropic" && req.APISchema != "openai" {
		respondError(w, http.StatusBadRequest, "api_schema must be 'anthropic' or 'openai'")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	p := &models.LLMProvider{
		Label:        req.Label,
		APISchema:    req.APISchema,
		BaseURL:      req.BaseURL,
		DefaultModel: req.DefaultModel,
		Enabled:      enabled,
		CreatedBy:    user.ID,
	}

	if req.APIKey != "" {
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
		p.EncryptedKey = encryptedKey
		p.EncryptedDEK = encryptedDEK
		p.DEKNonce = dekNonce
		if len(req.APIKey) >= 4 {
			p.KeyHint = "..." + req.APIKey[len(req.APIKey)-4:]
		}
	}

	if err := h.queries.CreateLLMProvider(r.Context(), p); err != nil {
		log.Error().Err(err).Msg("Failed to create LLM provider")
		respondError(w, http.StatusInternalServerError, "Failed to create provider")
		return
	}

	respondJSON(w, http.StatusCreated, p)
}

// UpdateLLMProvider updates label, base_url, api_schema, enabled, and optionally the API key.
func (h *Handler) UpdateLLMProvider(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	var req struct {
		Label        string `json:"label"`
		APISchema    string `json:"api_schema"`
		BaseURL      string `json:"base_url"`
		DefaultModel string `json:"default_model"`
		APIKey       string `json:"api_key"` // optional; if non-empty, re-encrypts
		Enabled      *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if req.Label == "" {
		respondError(w, http.StatusBadRequest, "label is required")
		return
	}
	req.APISchema = strings.ToLower(strings.TrimSpace(req.APISchema))
	if req.APISchema != "anthropic" && req.APISchema != "openai" {
		respondError(w, http.StatusBadRequest, "api_schema must be 'anthropic' or 'openai'")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if err := h.queries.UpdateLLMProvider(r.Context(), providerID, req.Label, req.APISchema, req.BaseURL, req.DefaultModel, enabled); err != nil {
		log.Error().Err(err).Msg("Failed to update LLM provider")
		respondError(w, http.StatusInternalServerError, "Failed to update provider")
		return
	}

	// If a new API key was provided, re-encrypt and store it.
	if req.APIKey != "" && h.encryptor != nil {
		dek, err := crypto.GenerateDEK()
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Encryption error")
			return
		}
		encryptedKey, err := crypto.Encrypt(dek, []byte(req.APIKey))
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
		if len(req.APIKey) >= 4 {
			hint = "..." + req.APIKey[len(req.APIKey)-4:]
		}
		if err := h.queries.UpdateLLMProviderKey(r.Context(), providerID, encryptedKey, encryptedDEK, dekNonce, hint); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to update provider key")
			return
		}
	}

	// Return the updated provider.
	p, err := h.queries.GetLLMProvider(r.Context(), providerID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Provider not found")
		return
	}
	respondJSON(w, http.StatusOK, p)
}

// DeleteLLMProvider permanently removes a global LLM provider.
func (h *Handler) DeleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")
	if err := h.queries.DeleteLLMProvider(r.Context(), providerID); err != nil {
		log.Error().Err(err).Msg("Failed to delete LLM provider")
		respondError(w, http.StatusInternalServerError, "Failed to delete provider")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DiscoverLLMProviderModels fetches models from a global LLM provider's API.
func (h *Handler) DiscoverLLMProviderModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	p, err := h.queries.GetLLMProvider(r.Context(), providerID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Provider not found")
		return
	}

	apiKey, err := h.decryptLLMProviderKey(p)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to decrypt provider key")
		return
	}

	models, err := agent.DiscoverModels(r.Context(), p.APISchema, p.BaseURL, apiKey)
	if err != nil {
		log.Warn().Err(err).Str("provider_id", providerID).Msg("Model discovery failed")
		respondError(w, http.StatusBadGateway, "Failed to discover models: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, models)
}

// DiscoverProjectProviderModels fetches models from a project provider key's API.
func (h *Handler) DiscoverProjectProviderModels(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	keyID := chi.URLParam(r, "keyID")

	key, err := h.queries.GetProjectProviderKey(r.Context(), keyID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Provider key not found")
		return
	}
	if key.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "Provider key not found")
		return
	}

	apiKey, err := h.DecryptProviderKey(key)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to decrypt provider key")
		return
	}

	models, err := agent.DiscoverModels(r.Context(), key.APISchema, key.EndpointURL, apiKey)
	if err != nil {
		log.Warn().Err(err).Str("key_id", keyID).Msg("Model discovery failed")
		respondError(w, http.StatusBadGateway, "Failed to discover models: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, models)
}

// ListAvailableProviders returns all available providers for a project
// (enabled env providers + enabled global providers + active project provider keys),
// filtered by project_allowed_providers to enforce admin access control.
// Use ?include_all=true to return all enabled providers unfiltered (for settings UI).
func (h *Handler) ListAvailableProviders(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	includeAll := r.URL.Query().Get("include_all") == "true"

	// Load the project's allowed provider list.
	allowedSet := make(map[string]bool) // key: "source:id"
	if !includeAll {
		allowed, err := h.queries.ListProjectAllowedProviders(r.Context(), projectID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to list project allowed providers")
			respondError(w, http.StatusInternalServerError, "Failed to list providers")
			return
		}
		for _, a := range allowed {
			allowedSet[a.ProviderSource+":"+a.ProviderID] = true
		}
	}

	var result []models.AvailableProvider

	// Add enabled environment-configured providers (if allowed for this project).
	if h.cfg != nil {
		anthropicKeySet := strings.TrimSpace(h.cfg.AgentAPIKey) != ""
		if !anthropicKeySet && h.cfg.AgentAPIKeyFile != "" {
			if _, err := os.Stat(h.cfg.AgentAPIKeyFile); err == nil {
				anthropicKeySet = true
			}
		}
		if anthropicKeySet && (includeAll || allowedSet["env:env-anthropic"]) {
			enabled := true
			if val, _ := h.queries.GetAppConfig(r.Context(), "env_provider_anthropic_enabled"); val == "false" {
				enabled = false
			}
			if enabled {
				result = append(result, models.AvailableProvider{
					ID:           "env-anthropic",
					Source:       "env",
					Label:        "Anthropic (env)",
					APISchema:    "anthropic",
					DefaultModel: h.cfg.AgentModel,
				})
			}
		}

		externalKeySet := strings.TrimSpace(h.cfg.ExternalLLMAPIKey) != ""
		if !externalKeySet && h.cfg.ExternalLLMAPIKeyFile != "" {
			if _, err := os.Stat(h.cfg.ExternalLLMAPIKeyFile); err == nil {
				externalKeySet = true
			}
		}
		if (externalKeySet || h.cfg.ExternalLLMEndpoint != "") && (includeAll || allowedSet["env:env-external"]) {
			enabled := true
			if val, _ := h.queries.GetAppConfig(r.Context(), "env_provider_external_enabled"); val == "false" {
				enabled = false
			}
			if enabled {
				result = append(result, models.AvailableProvider{
					ID:           "env-external",
					Source:       "env",
					Label:        "External LLM (env)",
					APISchema:    "openai",
					BaseURL:      h.cfg.ExternalLLMEndpoint,
					DefaultModel: h.cfg.ExternalLLMAnalysisModel,
				})
			}
		}
	}

	// Add enabled global providers (if allowed for this project).
	globals, err := h.queries.ListEnabledLLMProviders(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list global providers")
	} else {
		for _, g := range globals {
			if !includeAll && !allowedSet["global:"+g.ID] {
				continue
			}
			result = append(result, models.AvailableProvider{
				ID:           g.ID,
				Source:       "global",
				Label:        g.Label,
				APISchema:    g.APISchema,
				BaseURL:      g.BaseURL,
				DefaultModel: g.DefaultModel,
			})
		}
	}

	// Add active project provider keys (always available — project owns them).
	projectKeys, err := h.queries.ListProjectProviderKeys(r.Context(), projectID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list project provider keys")
	} else {
		for _, k := range projectKeys {
			if !k.IsActive || k.RevokedAt != nil {
				continue
			}
			result = append(result, models.AvailableProvider{
				ID:        k.ID,
				Source:    "project",
				Label:     k.Label,
				APISchema: k.APISchema,
				BaseURL:   k.EndpointURL,
			})
		}
	}

	if result == nil {
		result = []models.AvailableProvider{}
	}
	respondJSON(w, http.StatusOK, result)
}

// decryptLLMProviderKey decrypts the API key for a global LLM provider.
func (h *Handler) decryptLLMProviderKey(p *models.LLMProvider) (string, error) {
	if h.encryptor == nil {
		return "", nil
	}
	if len(p.EncryptedKey) == 0 {
		return "", nil
	}
	dek, err := h.encryptor.UnwrapDEK(p.EncryptedDEK, p.DEKNonce)
	if err != nil {
		return "", err
	}
	pt, err := crypto.Decrypt(dek, p.EncryptedKey)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// DiscoverEnvProviderModels fetches models from an env-configured provider.
func (h *Handler) DiscoverEnvProviderModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "providerID")

	if h.cfg == nil {
		respondError(w, http.StatusNotFound, "No config available")
		return
	}

	var apiSchema, baseURL, apiKey string

	switch providerID {
	case "env-anthropic":
		apiSchema = "anthropic"
		baseURL = "https://api.anthropic.com"
		apiKey = strings.TrimSpace(h.cfg.AgentAPIKey)
		if apiKey == "" && h.cfg.AgentAPIKeyFile != "" {
			data, err := os.ReadFile(h.cfg.AgentAPIKeyFile)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "Failed to read API key file")
				return
			}
			apiKey = strings.TrimSpace(string(data))
		}
	case "env-external":
		apiSchema = "openai"
		baseURL = h.cfg.ExternalLLMEndpoint
		apiKey = strings.TrimSpace(h.cfg.ExternalLLMAPIKey)
		if apiKey == "" && h.cfg.ExternalLLMAPIKeyFile != "" {
			data, err := os.ReadFile(h.cfg.ExternalLLMAPIKeyFile)
			if err != nil {
				respondError(w, http.StatusInternalServerError, "Failed to read API key file")
				return
			}
			apiKey = strings.TrimSpace(string(data))
		}
	default:
		respondError(w, http.StatusNotFound, "Unknown env provider")
		return
	}

	if apiKey == "" {
		respondError(w, http.StatusBadRequest, "No API key configured for this env provider")
		return
	}

	models, err := agent.DiscoverModels(r.Context(), apiSchema, baseURL, apiKey)
	if err != nil {
		log.Warn().Err(err).Str("provider", providerID).Msg("Env provider model discovery failed")
		respondError(w, http.StatusBadGateway, "Failed to discover models: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, models)
}
