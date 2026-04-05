package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/bbockelm/swamp/internal/agent"
	"github.com/bbockelm/swamp/internal/backup"
	"github.com/bbockelm/swamp/internal/config"
	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/db"
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

// HealthCheck returns a simple health status.
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// AgentStatus returns whether the analysis agent is configured and ready.
func (h *Handler) AgentStatus(w http.ResponseWriter, r *http.Request) {
	ready := h.executor != nil && h.executor.AgentReady()
	models := []map[string]string{
		{"id": "", "name": "Auto (claude CLI default)"},
		{"id": "claude-haiku-4-20250514", "name": "Claude Haiku 4"},
		{"id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"},
		{"id": "claude-opus-4-20250514", "name": "Claude Opus 4"},
	}
	defaultModel := ""
	if h.cfg != nil {
		defaultModel = h.cfg.AgentModel
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ready":         ready,
		"default_model": defaultModel,
		"models":        models,
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
