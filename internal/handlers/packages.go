package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

// --- Software Packages CRUD ---

func (h *Handler) ListPackages(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	pkgs, err := h.queries.ListProjectPackages(r.Context(), projectID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list packages")
		respondError(w, http.StatusInternalServerError, "Failed to list packages")
		return
	}
	respondJSON(w, http.StatusOK, pkgs)
}

func (h *Handler) CreatePackage(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	var pkg models.SoftwarePackage
	if err := decodeJSON(r, &pkg); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if pkg.Name == "" || pkg.GitURL == "" {
		respondError(w, http.StatusBadRequest, "Name and git_url are required")
		return
	}
	pkg.ProjectID = projectID
	if pkg.GitBranch == "" {
		pkg.GitBranch = "main"
	}
	if err := h.queries.CreatePackage(r.Context(), &pkg); err != nil {
		log.Error().Err(err).Msg("Failed to create package")
		respondError(w, http.StatusInternalServerError, "Failed to create package")
		return
	}
	respondJSON(w, http.StatusCreated, pkg)
}

func (h *Handler) GetPackage(w http.ResponseWriter, r *http.Request) {
	pkgID := chi.URLParam(r, "packageID")
	pkg, err := h.queries.GetPackage(r.Context(), pkgID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Package not found")
		return
	}
	respondJSON(w, http.StatusOK, pkg)
}

func (h *Handler) UpdatePackage(w http.ResponseWriter, r *http.Request) {
	pkgID := chi.URLParam(r, "packageID")
	pkg, err := h.queries.GetPackage(r.Context(), pkgID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Package not found")
		return
	}
	var updates models.SoftwarePackage
	if err := decodeJSON(r, &updates); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if updates.Name != "" {
		pkg.Name = updates.Name
	}
	if updates.GitURL != "" {
		pkg.GitURL = updates.GitURL
	}
	if updates.GitBranch != "" {
		pkg.GitBranch = updates.GitBranch
	}
	if updates.GitCommit != "" {
		pkg.GitCommit = updates.GitCommit
	}
	if updates.AnalysisPrompt != "" {
		pkg.AnalysisPrompt = updates.AnalysisPrompt
	}
	if err := h.queries.UpdatePackage(r.Context(), pkg); err != nil {
		log.Error().Err(err).Msg("Failed to update package")
		respondError(w, http.StatusInternalServerError, "Failed to update package")
		return
	}
	respondJSON(w, http.StatusOK, pkg)
}

func (h *Handler) DeletePackage(w http.ResponseWriter, r *http.Request) {
	pkgID := chi.URLParam(r, "packageID")
	if err := h.queries.DeletePackage(r.Context(), pkgID); err != nil {
		log.Error().Err(err).Msg("Failed to delete package")
		respondError(w, http.StatusInternalServerError, "Failed to delete package")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
