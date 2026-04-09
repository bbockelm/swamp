package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/backup"
	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/logbuffer"
	"github.com/bbockelm/swamp/internal/storage"
)

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	cfg       *config.Config
	queries   *db.Queries
	store     *storage.Store
	encryptor *crypto.Encryptor
	backupSvc *backup.Service
	executor  agent.AnalysisExecutor
	logBuf    *logbuffer.Buffer
}

// New creates a Handler with all dependencies.
func New(cfg *config.Config, queries *db.Queries, store *storage.Store, enc *crypto.Encryptor) *Handler {
	return &Handler{cfg: cfg, queries: queries, store: store, encryptor: enc}
}

// SetBackupService sets the backup service on the handler.
func (h *Handler) SetBackupService(svc *backup.Service) {
	h.backupSvc = svc
}

// SetExecutor sets the agent executor on the handler.
func (h *Handler) SetExecutor(exec agent.AnalysisExecutor) {
	h.executor = exec
}

// SetLogBuffer sets the log ring buffer on the handler.
func (h *Handler) SetLogBuffer(buf *logbuffer.Buffer) {
	h.logBuf = buf
}

// HealthCheck returns a simple health status.
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// AgentStatus returns whether the analysis agent is configured and ready.
func (h *Handler) AgentStatus(w http.ResponseWriter, r *http.Request) {
	ready := h.executor != nil && h.executor.AgentReady()
	provider := "anthropic"
	defaultModel := ""
	if h.cfg != nil {
		provider = strings.ToLower(strings.TrimSpace(h.cfg.AgentProvider))
		if provider == "external" {
			defaultModel = h.cfg.ExternalLLMAnalysisModel
		} else {
			defaultModel = h.cfg.AgentModel
		}
	}
	models := []map[string]string{
		{"id": "", "name": "Auto (claude CLI default)"},
		{"id": "claude-haiku-4-20250514", "name": "Claude Haiku 4"},
		{"id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"},
		{"id": "claude-opus-4-20250514", "name": "Claude Opus 4"},
	}
	if provider == "external" {
		label := "Configured external model"
		if defaultModel != "" {
			label = "Configured external model (" + defaultModel + ")"
		}
		models = []map[string]string{{"id": "", "name": label}}
	}

	// Include enabled global LLM providers.
	var globalProviders []map[string]string
	if h.queries != nil {
		if providers, err := h.queries.ListEnabledLLMProviders(r.Context()); err == nil {
			for _, p := range providers {
				globalProviders = append(globalProviders, map[string]string{
					"id":         p.ID,
					"label":      p.Label,
					"api_schema": p.APISchema,
					"base_url":   p.BaseURL,
				})
			}
		}
	}
	if globalProviders == nil {
		globalProviders = []map[string]string{}
	}

	// Describe environment-configured providers so the admin UI can show them.
	var envProviders []map[string]any
	if h.cfg != nil {
		// Check for Anthropic env key.
		anthropicKeySet := strings.TrimSpace(h.cfg.AgentAPIKey) != ""
		if !anthropicKeySet && h.cfg.AgentAPIKeyFile != "" {
			if _, err := os.Stat(h.cfg.AgentAPIKeyFile); err == nil {
				anthropicKeySet = true
			}
		}
		if anthropicKeySet {
			enabled := true
			if h.queries != nil {
				if val, _ := h.queries.GetAppConfig(r.Context(), "env_provider_anthropic_enabled"); val == "false" {
					enabled = false
				}
			}
			ep := map[string]any{
				"provider":       "anthropic",
				"api_schema":     "anthropic",
				"key_configured": true,
				"default_model":  h.cfg.AgentModel,
				"enabled":        enabled,
			}
			envProviders = append(envProviders, ep)
		}

		// Check for external LLM env key.
		externalKeySet := strings.TrimSpace(h.cfg.ExternalLLMAPIKey) != ""
		if !externalKeySet && h.cfg.ExternalLLMAPIKeyFile != "" {
			if _, err := os.Stat(h.cfg.ExternalLLMAPIKeyFile); err == nil {
				externalKeySet = true
			}
		}
		if externalKeySet || h.cfg.ExternalLLMEndpoint != "" {
			enabled := true
			if h.queries != nil {
				if val, _ := h.queries.GetAppConfig(r.Context(), "env_provider_external_enabled"); val == "false" {
					enabled = false
				}
			}
			ep := map[string]any{
				"provider":       "external",
				"api_schema":     "openai",
				"key_configured": externalKeySet,
				"base_url":       h.cfg.ExternalLLMEndpoint,
				"default_model":  h.cfg.ExternalLLMAnalysisModel,
				"enabled":        enabled,
			}
			envProviders = append(envProviders, ep)
		}
	}
	if envProviders == nil {
		envProviders = []map[string]any{}
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"ready":            ready,
		"provider":         provider,
		"default_model":    defaultModel,
		"models":           models,
		"global_providers": globalProviders,
		"env_providers":    envProviders,
	})
}

// DashboardStats returns aggregate statistics for the dashboard.
func (h *Handler) DashboardStats(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	isAdmin := UserHasRole(r.Context(), RoleAdmin)
	stats, err := h.queries.GetDashboardStats(r.Context(), user.ID, isAdmin)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to load dashboard stats")
		return
	}
	respondJSON(w, http.StatusOK, stats)
}

// respondJSON writes a JSON response with the given status code.
func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// respondError writes a JSON error response.
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// decodeJSON decodes a JSON request body into the given value.
func decodeJSON(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(r.Body).Decode(v)
}
