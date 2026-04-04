package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

// RequireProjectAccess returns middleware that checks if the current user
// can access the project identified by the {projectID} URL parameter at the
// given level ("read", "write", or "admin"). System admins bypass the check.
func (h *Handler) RequireProjectAccess(level string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if UserHasRole(r.Context(), RoleAdmin) {
				next.ServeHTTP(w, r)
				return
			}
			user := GetUserFromContext(r.Context())
			if user == nil {
				respondError(w, http.StatusUnauthorized, "Not authenticated")
				return
			}
			projectID := chi.URLParam(r, "projectID")
			ok, err := h.queries.UserCanAccessProject(r.Context(), user.ID, projectID, level)
			if err != nil || !ok {
				respondError(w, http.StatusForbidden, "Insufficient project access")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// projectFromContext is a context key for the resolved project.
type projectCtxKey struct{}

// WithProject stores a Project in the request context.
func WithProject(ctx context.Context, p *models.Project) context.Context {
	return context.WithValue(ctx, projectCtxKey{}, p)
}

// GetProjectFromContext retrieves a Project from the request context.
func GetProjectFromContext(ctx context.Context) *models.Project {
	p, _ := ctx.Value(projectCtxKey{}).(*models.Project)
	return p
}

// --- Projects CRUD ---

func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var projects []models.Project
	var err error
	if UserHasRole(r.Context(), RoleAdmin) {
		// Admins see all projects
		projects, err = h.queries.ListAllProjects(r.Context())
	} else {
		// Regular users see only their accessible projects
		projects, err = h.queries.ListUserProjects(r.Context(), user.ID)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list projects")
		respondError(w, http.StatusInternalServerError, "Failed to list projects")
		return
	}
	respondJSON(w, http.StatusOK, projects)
}

func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	if !UserHasRole(r.Context(), RoleAdmin) && !UserHasRole(r.Context(), RoleProjectCreator) {
		respondError(w, http.StatusForbidden, "Requires project_creator or admin role")
		return
	}

	var project models.Project
	if err := decodeJSON(r, &project); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if project.Name == "" {
		respondError(w, http.StatusBadRequest, "Project name is required")
		return
	}
	project.OwnerID = user.ID
	if project.Status == "" {
		project.Status = "active"
	}
	if err := h.queries.CreateProject(r.Context(), &project); err != nil {
		log.Error().Err(err).Msg("Failed to create project")
		respondError(w, http.StatusInternalServerError, "Failed to create project")
		return
	}
	respondJSON(w, http.StatusCreated, project)
}

func (h *Handler) GetProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := h.queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}
	respondJSON(w, http.StatusOK, project)
}

func (h *Handler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	project, err := h.queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}

	var updates models.Project
	if err := decodeJSON(r, &updates); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if updates.Name != "" {
		project.Name = updates.Name
	}
	if updates.Description != "" {
		project.Description = updates.Description
	}
	if updates.ReadGroupID != nil {
		project.ReadGroupID = updates.ReadGroupID
	}
	if updates.WriteGroupID != nil {
		project.WriteGroupID = updates.WriteGroupID
	}
	if updates.AdminGroupID != nil {
		project.AdminGroupID = updates.AdminGroupID
	}
	if updates.Status != "" {
		project.Status = updates.Status
	}
	if err := h.queries.UpdateProject(r.Context(), project); err != nil {
		log.Error().Err(err).Msg("Failed to update project")
		respondError(w, http.StatusInternalServerError, "Failed to update project")
		return
	}
	respondJSON(w, http.StatusOK, project)
}

func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	if err := h.queries.DeleteProject(r.Context(), projectID); err != nil {
		log.Error().Err(err).Msg("Failed to delete project")
		respondError(w, http.StatusInternalServerError, "Failed to delete project")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
