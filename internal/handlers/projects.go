package handlers

import (
	"context"
	"encoding/json"
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

// canManageGlobalProviders returns true if the caller is a site admin or
// if they are the project owner AND hold the project_creator role.
func canManageGlobalProviders(ctx context.Context, projectOwnerID string) bool {
	if UserHasRole(ctx, RoleAdmin) {
		return true
	}
	user := GetUserFromContext(ctx)
	if user == nil {
		return false
	}
	return user.ID == projectOwnerID && UserHasRole(ctx, RoleProjectCreator)
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
	// Any authenticated user can create a project.

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
	// Admin and project_creator users get global key access by default
	if UserHasRole(r.Context(), RoleAdmin) || UserHasRole(r.Context(), RoleProjectCreator) {
		project.UsesGlobalKey = true
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

	// Compute the caller's effective role for this project.
	project.MyRole = "read"
	if UserHasRole(r.Context(), RoleAdmin) {
		project.MyRole = "admin"
	} else if user := GetUserFromContext(r.Context()); user != nil {
		if project.OwnerID == user.ID {
			project.MyRole = "admin"
		} else if project.AdminGroupID != nil {
			if ok, _ := h.queries.IsGroupMember(r.Context(), *project.AdminGroupID, user.ID); ok {
				project.MyRole = "admin"
			}
		}
		if project.MyRole != "admin" && project.WriteGroupID != nil {
			if ok, _ := h.queries.IsGroupMember(r.Context(), *project.WriteGroupID, user.ID); ok {
				project.MyRole = "write"
			}
		}
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

	// Decode request as a map to detect which fields were sent.
	var rawUpdate map[string]interface{}
	if err := decodeJSON(r, &rawUpdate); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if name, ok := rawUpdate["name"].(string); ok && name != "" {
		project.Name = name
	}
	if desc, ok := rawUpdate["description"].(string); ok {
		project.Description = desc
	}
	// Handle nullable group IDs: allow setting to null or a valid string.
	if _, hasKey := rawUpdate["read_group_id"]; hasKey {
		if rawUpdate["read_group_id"] == nil {
			project.ReadGroupID = nil
		} else if id, ok := rawUpdate["read_group_id"].(string); ok {
			project.ReadGroupID = &id
		}
	}
	if _, hasKey := rawUpdate["write_group_id"]; hasKey {
		if rawUpdate["write_group_id"] == nil {
			project.WriteGroupID = nil
		} else if id, ok := rawUpdate["write_group_id"].(string); ok {
			project.WriteGroupID = &id
		}
	}
	if _, hasKey := rawUpdate["admin_group_id"]; hasKey {
		if rawUpdate["admin_group_id"] == nil {
			project.AdminGroupID = nil
		} else if id, ok := rawUpdate["admin_group_id"].(string); ok {
			project.AdminGroupID = &id
		}
	}
	if status, ok := rawUpdate["status"].(string); ok && status != "" {
		project.Status = status
	}

	// Only system admins or project_creators can enable uses_global_key.
	if _, hasKey := rawUpdate["uses_global_key"]; hasKey {
		if wantGlobal, ok := rawUpdate["uses_global_key"].(bool); ok {
			if wantGlobal && !canManageGlobalProviders(r.Context(), project.OwnerID) {
				respondError(w, http.StatusForbidden, "Only admins or project owners with the project_creator role can enable global key usage")
				return
			}
			project.UsesGlobalKey = wantGlobal
		}
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

	// Only the project owner (or a system admin) may delete.
	if !UserHasRole(r.Context(), RoleAdmin) {
		user := GetUserFromContext(r.Context())
		project, err := h.queries.GetProject(r.Context(), projectID)
		if err != nil {
			respondError(w, http.StatusNotFound, "Project not found")
			return
		}
		if user == nil || project.OwnerID != user.ID {
			respondError(w, http.StatusForbidden, "Only the project owner can delete the project")
			return
		}
	}

	if err := h.queries.DeleteProject(r.Context(), projectID); err != nil {
		log.Error().Err(err).Msg("Failed to delete project")
		respondError(w, http.StatusInternalServerError, "Failed to delete project")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Project Allowed Providers ---

// ListProjectAllowedProviders returns all globally/env providers that this project is allowed to use.
func (h *Handler) ListProjectAllowedProviders(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	allowed, err := h.queries.ListProjectAllowedProviders(r.Context(), projectID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list project allowed providers")
		respondError(w, http.StatusInternalServerError, "Failed to list allowed providers")
		return
	}
	if allowed == nil {
		allowed = []models.ProjectAllowedProvider{}
	}
	respondJSON(w, http.StatusOK, allowed)
}

// AddProjectAllowedProvider grants a project access to a global or env provider.
// Requires site admin, or the project owner must hold the project_creator role.
func (h *Handler) AddProjectAllowedProvider(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	project, err := h.queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}
	if !canManageGlobalProviders(r.Context(), project.OwnerID) {
		respondError(w, http.StatusForbidden, "Only admins or the project owner with project_creator role can manage provider access")
		return
	}

	var req struct {
		ProviderID     string `json:"provider_id"`
		ProviderSource string `json:"provider_source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ProviderID == "" || req.ProviderSource == "" {
		respondError(w, http.StatusBadRequest, "provider_id and provider_source are required")
		return
	}
	if req.ProviderSource != "global" && req.ProviderSource != "env" {
		respondError(w, http.StatusBadRequest, "provider_source must be 'global' or 'env'")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	if err := h.queries.AddProjectAllowedProvider(r.Context(), projectID, req.ProviderID, req.ProviderSource, user.ID); err != nil {
		log.Error().Err(err).Msg("Failed to add project allowed provider")
		respondError(w, http.StatusInternalServerError, "Failed to add provider")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// RemoveProjectAllowedProvider revokes a project's access to a global or env provider.
// Requires site admin, or the project owner must hold the project_creator role.
func (h *Handler) RemoveProjectAllowedProvider(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	project, err := h.queries.GetProject(r.Context(), projectID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Project not found")
		return
	}
	if !canManageGlobalProviders(r.Context(), project.OwnerID) {
		respondError(w, http.StatusForbidden, "Only admins or the project owner with project_creator role can manage provider access")
		return
	}

	var req struct {
		ProviderID     string `json:"provider_id"`
		ProviderSource string `json:"provider_source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ProviderID == "" || req.ProviderSource == "" {
		respondError(w, http.StatusBadRequest, "provider_id and provider_source are required")
		return
	}

	if err := h.queries.RemoveProjectAllowedProvider(r.Context(), projectID, req.ProviderID, req.ProviderSource); err != nil {
		log.Error().Err(err).Msg("Failed to remove project allowed provider")
		respondError(w, http.StatusInternalServerError, "Failed to remove provider")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
