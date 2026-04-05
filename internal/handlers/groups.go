package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/bbockelm/swamp/internal/models"
)

// --- Groups CRUD ---

// groupWithRole wraps a Group with the caller's effective role.
type groupWithRole struct {
	models.Group
	MyRole string `json:"my_role"`
}

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	var groups []models.Group
	var err error
	isSiteAdmin := UserHasRole(r.Context(), RoleAdmin)
	if isSiteAdmin {
		// Admins see all groups
		groups, err = h.queries.ListAllGroups(r.Context())
	} else {
		// Regular users see only their own groups
		groups, err = h.queries.ListUserGroups(r.Context(), user.ID)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to list groups")
		respondError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}

	// Enrich each group with the caller's role.
	result := make([]groupWithRole, len(groups))
	for i, g := range groups {
		var role string
		if isSiteAdmin || g.OwnerID == user.ID {
			role = "admin"
		} else {
			role, _ = h.queries.GetGroupMemberRole(r.Context(), g.ID, user.ID)
			// Check admin_group membership for elevated access.
			if role != "admin" && g.AdminGroupID != nil {
				isMember, _ := h.queries.IsGroupMember(r.Context(), *g.AdminGroupID, user.ID)
				if isMember {
					role = "admin"
				}
			}
		}
		result[i] = groupWithRole{Group: g, MyRole: role}
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	var group models.Group
	if err := decodeJSON(r, &group); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if group.Name == "" {
		respondError(w, http.StatusBadRequest, "Group name is required")
		return
	}
	group.OwnerID = user.ID
	if err := h.queries.CreateGroup(r.Context(), &group); err != nil {
		log.Error().Err(err).Msg("Failed to create group")
		respondError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}
	// Add creator as group admin
	member := &models.GroupMember{GroupID: group.ID, UserID: user.ID, Role: "admin", AddedBy: user.ID}
	if err := h.queries.AddGroupMember(r.Context(), member); err != nil {
		log.Error().Err(err).Msg("Failed to add creator as group admin")
	}
	respondJSON(w, http.StatusCreated, group)
}

func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	group, err := h.queries.GetGroup(r.Context(), groupID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Determine the caller's effective role in this group.
	var myRole string
	if UserHasRole(r.Context(), RoleAdmin) {
		myRole = "admin"
	} else {
		myRole, _ = h.queries.GetGroupMemberRole(r.Context(), groupID, user.ID)
		if myRole == "" {
			// Check ownership.
			if group.OwnerID == user.ID {
				myRole = "admin"
			}
		}
		if myRole == "" {
			respondError(w, http.StatusForbidden, "Not a member of this group")
			return
		}
		// Also check admin_group membership for elevated access.
		if myRole != "admin" && group.AdminGroupID != nil {
			isMember, _ := h.queries.IsGroupMember(r.Context(), *group.AdminGroupID, user.ID)
			if isMember {
				myRole = "admin"
			}
		}
	}

	// Return the group plus the caller's role.
	respondJSON(w, http.StatusOK, groupWithRole{Group: *group, MyRole: myRole})
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	group, err := h.queries.GetGroup(r.Context(), groupID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Group not found")
		return
	}
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	var updates models.Group
	if err := decodeJSON(r, &updates); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if updates.Name != "" {
		group.Name = updates.Name
	}
	if updates.Description != "" {
		group.Description = updates.Description
	}
	group.AdminGroupID = updates.AdminGroupID
	if err := h.queries.UpdateGroup(r.Context(), group); err != nil {
		log.Error().Err(err).Msg("Failed to update group")
		respondError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}
	respondJSON(w, http.StatusOK, group)
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	if err := h.queries.DeleteGroup(r.Context(), groupID); err != nil {
		log.Error().Err(err).Msg("Failed to delete group")
		respondError(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Group Members ---

func (h *Handler) ListGroupMembers(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		isMember, _ := h.queries.IsGroupMember(r.Context(), groupID, user.ID)
		if !isMember {
			respondError(w, http.StatusForbidden, "Not a member of this group")
			return
		}
	}
	members, err := h.queries.ListGroupMembers(r.Context(), groupID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list group members")
		respondError(w, http.StatusInternalServerError, "Failed to list members")
		return
	}
	respondJSON(w, http.StatusOK, members)
}

func (h *Handler) AddGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	var member models.GroupMember
	if err := decodeJSON(r, &member); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	member.GroupID = groupID
	member.AddedBy = user.ID
	if member.Role == "" {
		member.Role = "member"
	}
	if err := h.queries.AddGroupMember(r.Context(), &member); err != nil {
		log.Error().Err(err).Msg("Failed to add group member")
		respondError(w, http.StatusInternalServerError, "Failed to add member")
		return
	}
	respondJSON(w, http.StatusCreated, member)
}

func (h *Handler) RemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	memberUserID := chi.URLParam(r, "userID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	if err := h.queries.RemoveGroupMember(r.Context(), groupID, memberUserID); err != nil {
		log.Error().Err(err).Msg("Failed to remove group member")
		respondError(w, http.StatusInternalServerError, "Failed to remove member")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) UpdateGroupMemberRole(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	memberUserID := chi.URLParam(r, "userID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Role != "member" && req.Role != "admin" {
		respondError(w, http.StatusBadRequest, "Role must be 'member' or 'admin'")
		return
	}
	if err := h.queries.UpdateGroupMemberRole(r.Context(), groupID, memberUserID, req.Role); err != nil {
		log.Error().Err(err).Msg("Failed to update member role")
		respondError(w, http.StatusInternalServerError, "Failed to update role")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- Group Invites ---

func (h *Handler) CreateGroupInvite(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	var req struct {
		Email              string `json:"email"`
		Role               string `json:"role"`
		AllowsRegistration bool   `json:"allows_registration"`
		ExpiresInHours     int    `json:"expires_in_hours"`
	}
	if err := decodeJSON(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.ExpiresInHours <= 0 {
		req.ExpiresInHours = 7 * 24 // default 7 days
	}

	rawToken, tokenHash, err := generateToken()
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate invite token")
		respondError(w, http.StatusInternalServerError, "Failed to create invite")
		return
	}

	invite := &models.GroupInvite{
		GroupID:            groupID,
		Email:              req.Email,
		Role:               req.Role,
		AllowsRegistration: req.AllowsRegistration,
		InvitedBy:          user.ID,
		TokenHash:          tokenHash,
		ExpiresAt:          time.Now().Add(time.Duration(req.ExpiresInHours) * time.Hour),
	}
	if err := h.queries.CreateGroupInvite(r.Context(), invite); err != nil {
		log.Error().Err(err).Msg("Failed to create group invite")
		respondError(w, http.StatusInternalServerError, "Failed to create invite")
		return
	}
	invite.Token = rawToken
	respondJSON(w, http.StatusCreated, invite)
}

func (h *Handler) ListGroupInvites(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	invites, err := h.queries.ListGroupInvites(r.Context(), groupID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list invites")
		respondError(w, http.StatusInternalServerError, "Failed to list invites")
		return
	}
	respondJSON(w, http.StatusOK, invites)
}

func (h *Handler) AcceptGroupInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Token == "" {
		respondError(w, http.StatusBadRequest, "Token required")
		return
	}
	invite, err := h.queries.GetGroupInviteByToken(r.Context(), hashToken(req.Token))
	if err != nil {
		respondError(w, http.StatusNotFound, "Invite not found or expired")
		return
	}
	if invite.Used {
		respondError(w, http.StatusGone, "This invite has already been used")
		return
	}
	if time.Now().After(invite.ExpiresAt) {
		respondError(w, http.StatusGone, "This invite has expired")
		return
	}
	user := GetUserFromContext(r.Context())
	if user == nil {
		respondError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	member := &models.GroupMember{GroupID: invite.GroupID, UserID: user.ID, Role: invite.Role, AddedBy: invite.InvitedBy}
	if err := h.queries.AddGroupMember(r.Context(), member); err != nil {
		log.Error().Err(err).Msg("Failed to add member from invite")
		respondError(w, http.StatusInternalServerError, "Failed to join group")
		return
	}
	_ = h.queries.MarkGroupInviteUsed(r.Context(), invite.ID)
	respondJSON(w, http.StatusOK, map[string]string{"status": "joined", "group_id": invite.GroupID})
}

// GetGroupInviteInfo returns public info about an invite (group name + role)
// so the accept page can show what the user is joining.
func (h *Handler) GetGroupInviteInfo(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		respondError(w, http.StatusBadRequest, "Token required")
		return
	}
	invite, err := h.queries.GetGroupInviteByToken(r.Context(), hashToken(token))
	if err != nil {
		respondError(w, http.StatusNotFound, "Invite not found")
		return
	}
	if invite.Used {
		respondError(w, http.StatusGone, "This invite has already been used")
		return
	}
	if time.Now().After(invite.ExpiresAt) {
		respondError(w, http.StatusGone, "This invite has expired")
		return
	}
	group, err := h.queries.GetGroup(r.Context(), invite.GroupID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to look up group")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"group_name": group.Name,
		"role":       invite.Role,
	})
}

// DeleteGroupInvite revokes a group invite.
func (h *Handler) DeleteGroupInvite(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "groupID")
	inviteID := chi.URLParam(r, "inviteID")
	user := GetUserFromContext(r.Context())
	if !UserHasRole(r.Context(), RoleAdmin) {
		if err := h.requireGroupRole(r, groupID, user.ID, "admin"); err != nil {
			respondError(w, http.StatusForbidden, "Must be group admin")
			return
		}
	}
	if err := h.queries.DeleteGroupInvite(r.Context(), inviteID); err != nil {
		log.Error().Err(err).Msg("Failed to delete group invite")
		respondError(w, http.StatusInternalServerError, "Failed to delete invite")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- helpers ---

func (h *Handler) requireGroupRole(r *http.Request, groupID, userID, role string) error {
	members, err := h.queries.ListGroupMembers(r.Context(), groupID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.UserID == userID && m.Role == role {
			return nil
		}
	}
	// If checking for admin role, also check admin_group membership
	if role == "admin" {
		group, err := h.queries.GetGroup(r.Context(), groupID)
		if err == nil && group.AdminGroupID != nil {
			isMember, _ := h.queries.IsGroupMember(r.Context(), *group.AdminGroupID, userID)
			if isMember {
				return nil
			}
		}
	}
	return fmt.Errorf("user does not have role %s in group %s", role, groupID)
}
