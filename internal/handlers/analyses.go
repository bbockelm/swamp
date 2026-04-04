package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/crypto"
	"github.com/bbockelm/swamp/internal/models"
)

// --- Analyses ---

// ListAllAnalyses returns analyses visible to the current user (jobs table).
func (h *Handler) ListAllAnalyses(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var analyses []models.Analysis
	var err error
	if UserHasRole(r.Context(), RoleAdmin) {
		analyses, err = h.queries.ListAllAnalysesAdmin(r.Context())
	} else {
		analyses, err = h.queries.ListAllAnalyses(r.Context(), user.ID)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list all analyses")
		respondError(w, http.StatusInternalServerError, "Failed to list analyses")
		return
	}
	if analyses == nil {
		analyses = []models.Analysis{}
	}
	respondJSON(w, http.StatusOK, analyses)
}

// ListAnalyses returns analyses for a project.
func (h *Handler) ListAnalyses(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	analyses, err := h.queries.ListProjectAnalyses(r.Context(), projectID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list analyses")
		respondError(w, http.StatusInternalServerError, "Failed to list analyses")
		return
	}
	respondJSON(w, http.StatusOK, analyses)
}

// CheckAnalysisLiveness reports whether the executor is actively tracking a
// given analysis. The frontend polls this to detect stale "running" states.
// If the executor says the analysis is not running but the DB still shows
// running/pending, we auto-correct the DB to "failed".
func (h *Handler) CheckAnalysisLiveness(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")
	alive := h.executor != nil && h.executor.IsRunning(analysisID)

	if !alive {
		// Auto-fix: if the DB still says running/pending but the executor
		// no longer tracks it, mark the analysis as failed.
		a, err := h.queries.GetAnalysis(r.Context(), analysisID)
		if err == nil && (a.Status == "running" || a.Status == "pending") {
			_ = h.queries.SetAnalysisCompleted(r.Context(), analysisID, "failed", "Worker process is no longer running")
		}
	}

	respondJSON(w, http.StatusOK, map[string]bool{"alive": alive})
}

// CreateAnalysis triggers a new analysis for a project.
func (h *Handler) CreateAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	user := GetUserFromContext(r.Context())

	// Reject if the agent is not configured.
	if h.executor == nil || !h.executor.AgentReady() {
		respondError(w, http.StatusServiceUnavailable, "Analysis agent is not configured (missing API key or binary)")
		return
	}

	var req struct {
		PackageIDs   []string        `json:"package_ids"`
		AgentConfig  json.RawMessage `json:"agent_config,omitempty"`
		CustomPrompt string          `json:"custom_prompt"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.PackageIDs) == 0 {
		respondError(w, http.StatusBadRequest, "At least one package_id is required")
		return
	}

	// Verify all packages belong to the project
	for _, pkgID := range req.PackageIDs {
		pkg, err := h.queries.GetPackage(r.Context(), pkgID)
		if err != nil || pkg.ProjectID != projectID {
			respondError(w, http.StatusBadRequest, "Invalid package ID: "+pkgID)
			return
		}
	}

	analysis := &models.Analysis{
		ProjectID:    projectID,
		Status:       "pending",
		TriggeredBy:  user.ID,
		AgentConfig:  req.AgentConfig,
		CustomPrompt: req.CustomPrompt,
	}
	if len(analysis.AgentConfig) == 0 {
		analysis.AgentConfig = json.RawMessage(`{}`)
	}

	// Generate a per-analysis DEK for encrypting output artifacts.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		log.Error().Err(err).Msg("Failed to wrap analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := h.queries.CreateAnalysis(r.Context(), analysis); err != nil {
		log.Error().Err(err).Msg("Failed to create analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}

	// Link packages to analysis
	for _, pkgID := range req.PackageIDs {
		if err := h.queries.AddAnalysisPackage(r.Context(), analysis.ID, pkgID); err != nil {
			log.Error().Err(err).Str("package_id", pkgID).Msg("Failed to link package to analysis")
		}
	}

	// Submit to the agent executor for async processing.
	if h.executor != nil {
		packages, err := h.queries.ListAnalysisPackages(r.Context(), analysis.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to fetch packages for analysis submission")
		} else {
			h.executor.Submit(analysis, packages)
		}
	}

	respondJSON(w, http.StatusCreated, analysis)
}

// GetAnalysis returns details of a specific analysis.
func (h *Handler) GetAnalysis(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")

	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	// Also fetch linked packages
	packages, _ := h.queries.ListAnalysisPackages(r.Context(), analysisID)

	respondJSON(w, http.StatusOK, map[string]any{
		"analysis": analysis,
		"packages": packages,
	})
}

// CancelAnalysis cancels a running or pending analysis.
func (h *Handler) CancelAnalysis(w http.ResponseWriter, r *http.Request) {
	analysisID := chi.URLParam(r, "analysisID")

	analysis, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	if analysis.Status != "pending" && analysis.Status != "running" {
		respondError(w, http.StatusBadRequest, "Analysis cannot be cancelled in its current state")
		return
	}

	if err := h.queries.SetAnalysisCompleted(r.Context(), analysisID, "cancelled", "Cancelled by user"); err != nil {
		log.Error().Err(err).Msg("Failed to cancel analysis")
		respondError(w, http.StatusInternalServerError, "Failed to cancel analysis")
		return
	}

	// Signal the agent executor to kill the process.
	if h.executor != nil {
		h.executor.Cancel(analysisID)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// ResubmitAnalysis creates a new analysis with the same packages as an existing one.
func (h *Handler) ResubmitAnalysis(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	analysisID := chi.URLParam(r, "analysisID")
	user := GetUserFromContext(r.Context())

	// Fetch original analysis's packages
	packages, err := h.queries.ListAnalysisPackages(r.Context(), analysisID)
	if err != nil || len(packages) == 0 {
		respondError(w, http.StatusBadRequest, "Original analysis has no packages")
		return
	}

	// Get original analysis for config
	orig, err := h.queries.GetAnalysis(r.Context(), analysisID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Analysis not found")
		return
	}

	agentConfig := orig.AgentConfig
	if len(agentConfig) == 0 {
		agentConfig = json.RawMessage(`{}`)
	}

	analysis := &models.Analysis{
		ProjectID:    projectID,
		Status:       "pending",
		TriggeredBy:  user.ID,
		AgentConfig:  agentConfig,
		CustomPrompt: orig.CustomPrompt,
	}

	// Generate a per-analysis DEK for encrypting output artifacts.
	dek, err := crypto.GenerateDEK()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	encDEK, nonce, err := h.encryptor.WrapDEK(dek)
	if err != nil {
		log.Error().Err(err).Msg("Failed to wrap analysis DEK")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}
	analysis.EncryptedDEK = encDEK
	analysis.DEKNonce = nonce

	if err := h.queries.CreateAnalysis(r.Context(), analysis); err != nil {
		log.Error().Err(err).Msg("Failed to create resubmitted analysis")
		respondError(w, http.StatusInternalServerError, "Failed to create analysis")
		return
	}

	for _, pkg := range packages {
		if err := h.queries.AddAnalysisPackage(r.Context(), analysis.ID, pkg.ID); err != nil {
			log.Error().Err(err).Str("package_id", pkg.ID).Msg("Failed to link package to resubmitted analysis")
		}
	}

	if h.executor != nil {
		pkgs, err := h.queries.ListAnalysisPackages(r.Context(), analysis.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to fetch packages for resubmitted analysis")
		} else {
			h.executor.Submit(analysis, pkgs)
		}
	}

	respondJSON(w, http.StatusCreated, analysis)
}
