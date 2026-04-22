package handlers

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

// gitHubURLRegexp matches GitHub HTTPS URLs and extracts owner + repo.
var gitHubURLRegexp = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/.]+?)(?:\.git)?/?$`)

// parseGitHubURL extracts owner and repo from a GitHub URL.
// Returns empty strings if the URL doesn't match.
func parseGitHubURL(gitURL string) (owner, repo string) {
	m := gitHubURLRegexp.FindStringSubmatch(gitURL)
	if len(m) == 3 {
		return m[1], m[2]
	}
	return "", ""
}

func (h *Handler) ensureProjectInstallationLink(r *http.Request, projectID string, installationID int64) (bool, int) {
	if installationID == 0 {
		return true, 0
	}
	user := GetUserFromContext(r.Context())
	if user == nil || !h.userCanUseInstallation(r.Context(), user.ID, installationID) {
		return false, http.StatusForbidden
	}
	if err := h.queries.AddProjectInstallation(r.Context(), projectID, installationID, user.ID); err != nil {
		log.Error().Err(err).Str("project_id", projectID).Int64("installation_id", installationID).Msg("Failed to link GitHub installation to project")
		return false, http.StatusInternalServerError
	}
	return true, 0
}

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
	// Auto-detect GitHub owner/repo from git_url.
	if pkg.GitHubOwner == "" || pkg.GitHubRepo == "" {
		owner, repo := parseGitHubURL(pkg.GitURL)
		if owner != "" {
			pkg.GitHubOwner = owner
			pkg.GitHubRepo = repo
		}
	}
	installationToLink := pkg.InstallationID
	if ok, status := h.ensureProjectInstallationLink(r, projectID, installationToLink); !ok {
		if status == http.StatusForbidden {
			respondError(w, http.StatusForbidden, "You are not authorized to use this GitHub App installation")
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to link GitHub App installation to project")
		}
		return
	}
	// Package-level installation links are deprecated in favor of project links.
	pkg.InstallationID = 0
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
	var updates struct {
		Name               *string `json:"name"`
		GitURL             *string `json:"git_url"`
		GitBranch          *string `json:"git_branch"`
		GitCommit          *string `json:"git_commit"`
		AnalysisPrompt     *string `json:"analysis_prompt"`
		GitHubOwner        *string `json:"github_owner"`
		GitHubRepo         *string `json:"github_repo"`
		InstallationID     *int64  `json:"installation_id"`
		SARIFUploadEnabled *bool   `json:"sarif_upload_enabled"`
	}
	if err := decodeJSON(r, &updates); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if updates.Name != nil {
		pkg.Name = *updates.Name
	}
	if updates.GitURL != nil {
		pkg.GitURL = *updates.GitURL
		// Re-detect GitHub owner/repo when URL changes.
		owner, repo := parseGitHubURL(pkg.GitURL)
		if owner != "" {
			pkg.GitHubOwner = owner
			pkg.GitHubRepo = repo
		}
	}
	if updates.GitBranch != nil {
		pkg.GitBranch = *updates.GitBranch
	}
	if updates.GitCommit != nil {
		pkg.GitCommit = *updates.GitCommit
	}
	if updates.AnalysisPrompt != nil {
		pkg.AnalysisPrompt = *updates.AnalysisPrompt
	}
	if updates.GitHubOwner != nil {
		pkg.GitHubOwner = *updates.GitHubOwner
	}
	if updates.GitHubRepo != nil {
		pkg.GitHubRepo = *updates.GitHubRepo
	}
	if updates.SARIFUploadEnabled != nil {
		pkg.SARIFUploadEnabled = *updates.SARIFUploadEnabled
	}

	projectID := chi.URLParam(r, "projectID")
	installationToLink := int64(0)
	if updates.InstallationID != nil {
		installationToLink = *updates.InstallationID
	}
	if ok, status := h.ensureProjectInstallationLink(r, projectID, installationToLink); !ok {
		if status == http.StatusForbidden {
			respondError(w, http.StatusForbidden, "You are not authorized to use this GitHub App installation")
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to link GitHub App installation to project")
		}
		return
	}

	// Package-level installation links are deprecated in favor of project links.
	pkg.InstallationID = 0
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
