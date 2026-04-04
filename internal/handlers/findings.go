package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/db"
	"github.com/bbockelm/swamp/internal/models"
)

// --- Findings ---

// ListAllFindings returns findings across all projects visible to the current user.
func (h *Handler) ListAllFindings(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	filters := db.FindingFilters{
		Level:    r.URL.Query().Get("level"),
		RuleID:   r.URL.Query().Get("rule_id"),
		Status:   r.URL.Query().Get("status"),
		FilePath: r.URL.Query().Get("file_path"),
		Search:   r.URL.Query().Get("search"),
	}
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if v, err := strconv.Atoi(lim); err == nil && v > 0 && v <= 500 {
			filters.Limit = v
		}
	}
	if filters.Limit == 0 {
		filters.Limit = 50
	}
	if off := r.URL.Query().Get("offset"); off != "" {
		if v, err := strconv.Atoi(off); err == nil && v >= 0 {
			filters.Offset = v
		}
	}

	isAdmin := UserHasRole(r.Context(), RoleAdmin)
	findings, total, err := h.queries.ListAllFindings(r.Context(), user.ID, isAdmin, filters)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list all findings")
		respondError(w, http.StatusInternalServerError, "Failed to list findings")
		return
	}
	if findings == nil {
		findings = []models.Finding{}
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"findings": findings,
		"total":    total,
		"limit":    filters.Limit,
		"offset":   filters.Offset,
	})
}

// ListProjectFindings returns findings for a project (read access required).
func (h *Handler) ListProjectFindings(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	filters := db.FindingFilters{
		Level:      r.URL.Query().Get("level"),
		RuleID:     r.URL.Query().Get("rule_id"),
		Status:     r.URL.Query().Get("status"),
		AnalysisID: r.URL.Query().Get("analysis_id"),
		FilePath:   r.URL.Query().Get("file_path"),
		Search:     r.URL.Query().Get("search"),
	}

	if lim := r.URL.Query().Get("limit"); lim != "" {
		if v, err := strconv.Atoi(lim); err == nil && v > 0 && v <= 500 {
			filters.Limit = v
		}
	}
	if filters.Limit == 0 {
		filters.Limit = 50
	}
	if off := r.URL.Query().Get("offset"); off != "" {
		if v, err := strconv.Atoi(off); err == nil && v >= 0 {
			filters.Offset = v
		}
	}

	findings, err := h.queries.ListProjectFindings(r.Context(), projectID, filters)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list findings")
		respondError(w, http.StatusInternalServerError, "Failed to list findings")
		return
	}
	if findings == nil {
		findings = []models.Finding{}
	}

	total, err := h.queries.CountProjectFindings(r.Context(), projectID, filters)
	if err != nil {
		log.Error().Err(err).Msg("Failed to count findings")
		total = len(findings)
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"findings": findings,
		"total":    total,
		"limit":    filters.Limit,
		"offset":   filters.Offset,
	})
}

// GetFinding returns a single finding with its annotations.
func (h *Handler) GetFinding(w http.ResponseWriter, r *http.Request) {
	findingID := chi.URLParam(r, "findingID")

	finding, err := h.queries.GetFinding(r.Context(), findingID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Finding not found")
		return
	}

	annotations, _ := h.queries.ListFindingAnnotations(r.Context(), findingID)
	if annotations == nil {
		annotations = []models.FindingAnnotation{}
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"finding":     finding,
		"annotations": annotations,
	})
}

// AnnotateFinding creates or updates the current user's annotation on a finding.
func (h *Handler) AnnotateFinding(w http.ResponseWriter, r *http.Request) {
	findingID := chi.URLParam(r, "findingID")
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	validStatuses := map[string]bool{
		"open": true, "false_positive": true, "not_relevant": true,
		"confirmed": true, "wont_fix": true, "mitigated": true,
	}
	if !validStatuses[req.Status] {
		respondError(w, http.StatusBadRequest, "Invalid status. Must be one of: open, false_positive, not_relevant, confirmed, wont_fix, mitigated")
		return
	}

	// Verify finding exists and belongs to a project the user can access.
	finding, err := h.queries.GetFinding(r.Context(), findingID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Finding not found")
		return
	}

	// Verify user has read access to the project (middleware already checked, but double-check via finding).
	if !UserHasRole(r.Context(), RoleAdmin) {
		ok, err := h.queries.UserCanAccessProject(r.Context(), user.ID, finding.ProjectID, "read")
		if err != nil || !ok {
			respondError(w, http.StatusForbidden, "Insufficient project access")
			return
		}
	}

	annotation := &models.FindingAnnotation{
		FindingID: findingID,
		UserID:    user.ID,
		Status:    req.Status,
		Note:      req.Note,
	}
	if err := h.queries.UpsertFindingAnnotation(r.Context(), annotation); err != nil {
		log.Error().Err(err).Msg("Failed to annotate finding")
		respondError(w, http.StatusInternalServerError, "Failed to save annotation")
		return
	}

	respondJSON(w, http.StatusOK, annotation)
}

// ListFindingAnnotations returns all annotations for a finding.
func (h *Handler) ListFindingAnnotations(w http.ResponseWriter, r *http.Request) {
	findingID := chi.URLParam(r, "findingID")

	annotations, err := h.queries.ListFindingAnnotations(r.Context(), findingID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list annotations")
		respondError(w, http.StatusInternalServerError, "Failed to list annotations")
		return
	}
	if annotations == nil {
		annotations = []models.FindingAnnotation{}
	}
	respondJSON(w, http.StatusOK, annotations)
}
