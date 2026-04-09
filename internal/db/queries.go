package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbockelm/swamp/internal/models"
)

// Queries provides database query methods.
type Queries struct {
	pool *pgxpool.Pool
}

// NewQueries creates a new Queries instance.
func NewQueries(pool *pgxpool.Pool) *Queries {
	return &Queries{pool: pool}
}

// Pool returns the underlying connection pool.
func (q *Queries) Pool() *pgxpool.Pool {
	return q.pool
}

// ============================================================
// Users
// ============================================================

func (q *Queries) CreateUser(ctx context.Context, u *models.User) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO users (display_name, email, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`,
		u.DisplayName, u.Email, u.Status).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
}

func (q *Queries) GetUser(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	err := q.pool.QueryRow(ctx, `
		SELECT id, display_name, email, status, last_login, created_at, updated_at
		FROM users WHERE id=$1`, id).Scan(
		&u.ID, &u.DisplayName, &u.Email, &u.Status, &u.LastLogin, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (q *Queries) UpdateUser(ctx context.Context, u *models.User) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE users SET display_name=$2, email=$3, status=$4, updated_at=NOW()
		WHERE id=$1`, u.ID, u.DisplayName, u.Email, u.Status)
	return err
}

func (q *Queries) UpdateUserLastLogin(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `UPDATE users SET last_login=NOW(), updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (q *Queries) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, display_name, email, status, last_login, created_at, updated_at
		FROM users WHERE deleted_at IS NULL ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.DisplayName, &u.Email, &u.Status, &u.LastLogin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (q *Queries) DeleteUser(ctx context.Context, id string) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Soft-delete: mark user as deleted and rename.
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET display_name = display_name || ' (Deleted)',
		    status = 'deleted',
		    deleted_at = NOW(),
		    updated_at = NOW()
		WHERE id=$1`, id); err != nil {
		return err
	}
	// Remove all identities so the user cannot log back in.
	if _, err := tx.Exec(ctx, `DELETE FROM user_identities WHERE user_id=$1`, id); err != nil {
		return err
	}
	// Invalidate all sessions.
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SearchUsers finds users whose display_name or email matches the query (case-insensitive prefix/substring).
func (q *Queries) SearchUsers(ctx context.Context, query string, limit int) ([]models.User, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := q.pool.Query(ctx, `
		SELECT id, display_name, email, status, last_login, created_at, updated_at
		FROM users
		WHERE display_name ILIKE $1 OR email ILIKE $1
		ORDER BY display_name
		LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.DisplayName, &u.Email, &u.Status, &u.LastLogin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// ============================================================
// User Roles
// ============================================================

func (q *Queries) ListUserRoles(ctx context.Context, userID string) ([]models.UserRole, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, user_id, role, created_at FROM user_roles WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []models.UserRole
	for rows.Next() {
		var r models.UserRole
		if err := rows.Scan(&r.ID, &r.UserID, &r.Role, &r.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, nil
}

func (q *Queries) AddUserRole(ctx context.Context, userID, role string) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role) VALUES ($1, $2)
		ON CONFLICT (user_id, role) DO NOTHING`, userID, role)
	return err
}

func (q *Queries) RemoveUserRole(ctx context.Context, userID, role string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1 AND role=$2`, userID, role)
	return err
}

// UserHasRole checks if a user has a specific role.
func (q *Queries) UserHasRole(ctx context.Context, userID, role string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role=$2)`,
		userID, role).Scan(&exists)
	return exists, err
}

// ============================================================
// User Identities
// ============================================================

func (q *Queries) FindIdentity(ctx context.Context, issuer, subject string) (*models.UserIdentity, error) {
	var i models.UserIdentity
	err := q.pool.QueryRow(ctx, `
		SELECT id, user_id, issuer, subject,
		       COALESCE(email,''), COALESCE(display_name,''), COALESCE(idp_name,''), created_at
		FROM user_identities WHERE issuer=$1 AND subject=$2`, issuer, subject).Scan(
		&i.ID, &i.UserID, &i.Issuer, &i.Subject, &i.Email, &i.DisplayName, &i.IDPName, &i.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

func (q *Queries) CreateIdentity(ctx context.Context, id *models.UserIdentity) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO user_identities (user_id, issuer, subject, email, display_name, idp_name)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		id.UserID, id.Issuer, id.Subject, id.Email, id.DisplayName, id.IDPName).Scan(&id.ID, &id.CreatedAt)
}

func (q *Queries) ListUserIdentities(ctx context.Context, userID string) ([]models.UserIdentity, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, user_id, issuer, subject,
		       COALESCE(email,''), COALESCE(display_name,''), COALESCE(idp_name,''), created_at
		FROM user_identities WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []models.UserIdentity
	for rows.Next() {
		var i models.UserIdentity
		if err := rows.Scan(&i.ID, &i.UserID, &i.Issuer, &i.Subject, &i.Email, &i.DisplayName, &i.IDPName, &i.CreatedAt); err != nil {
			return nil, err
		}
		ids = append(ids, i)
	}
	return ids, nil
}

func (q *Queries) DeleteIdentity(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM user_identities WHERE id=$1`, id)
	return err
}

// ============================================================
// Sessions
// ============================================================

func (q *Queries) CreateSession(ctx context.Context, s *models.Session) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, expires_at, token_hash)
		VALUES ($1, $2, $3) RETURNING id, created_at`,
		s.UserID, s.ExpiresAt, s.TokenHash).Scan(&s.ID, &s.CreatedAt)
}

func (q *Queries) GetSession(ctx context.Context, tokenHash []byte) (*models.Session, error) {
	var s models.Session
	err := q.pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, created_at
		FROM sessions WHERE token_hash=$1 AND expires_at > NOW()`, tokenHash).Scan(
		&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (q *Queries) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (q *Queries) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
	return err
}

// ============================================================
// User Invites (platform-level)
// ============================================================

func (q *Queries) CreateUserInvite(ctx context.Context, inv *models.UserInvite) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO user_invites (token_hash, created_by, email, expires_at)
		VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
		inv.TokenHash, inv.CreatedBy, inv.Email, inv.ExpiresAt).Scan(&inv.ID, &inv.CreatedAt)
}

func (q *Queries) GetUserInviteByToken(ctx context.Context, tokenHash []byte) (*models.UserInvite, error) {
	var inv models.UserInvite
	err := q.pool.QueryRow(ctx, `
		SELECT id, token_hash, created_by, email, used, used_by, expires_at, created_at
		FROM user_invites WHERE token_hash=$1`, tokenHash).Scan(
		&inv.ID, &inv.TokenHash, &inv.CreatedBy, &inv.Email, &inv.Used, &inv.UsedBy, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (q *Queries) MarkUserInviteUsed(ctx context.Context, id, usedByUserID string) error {
	_, err := q.pool.Exec(ctx, `UPDATE user_invites SET used=TRUE, used_by=$2 WHERE id=$1`, id, usedByUserID)
	return err
}

func (q *Queries) ListUserInvites(ctx context.Context, userID string) ([]models.UserInvite, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, created_by, email, used, used_by, expires_at, created_at
		FROM user_invites WHERE created_by=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []models.UserInvite
	for rows.Next() {
		var inv models.UserInvite
		if err := rows.Scan(&inv.ID, &inv.CreatedBy, &inv.Email, &inv.Used, &inv.UsedBy, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, nil
}

func (q *Queries) ListUserInvitesByTarget(ctx context.Context, userID string) ([]models.UserInvite, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, created_by, email, used, used_by, expires_at, created_at
		FROM user_invites WHERE used_by=$1 OR created_by=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []models.UserInvite
	for rows.Next() {
		var inv models.UserInvite
		if err := rows.Scan(&inv.ID, &inv.CreatedBy, &inv.Email, &inv.Used, &inv.UsedBy, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, nil
}

func (q *Queries) DeleteUserInvite(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM user_invites WHERE id=$1`, id)
	return err
}

// ============================================================
// AUP Agreements
// ============================================================

func (q *Queries) CreateAUPAgreement(ctx context.Context, a *models.AUPAgreement) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO aup_agreements (user_id, aup_version, ip_address)
		VALUES ($1, $2, $3) RETURNING id, agreed_at`,
		a.UserID, a.AUPVersion, a.IPAddress).Scan(&a.ID, &a.AgreedAt)
}

func (q *Queries) UserHasAgreedAUP(ctx context.Context, userID, version string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM aup_agreements WHERE user_id=$1 AND aup_version=$2)`,
		userID, version).Scan(&exists)
	return exists, err
}

// AUPUserStatus represents a user's AUP agreement status.
type AUPUserStatus struct {
	UserID      string  `json:"user_id"`
	DisplayName string  `json:"display_name"`
	Email       string  `json:"email"`
	Status      string  `json:"status"`
	AgreedAt    *string `json:"agreed_at"`
}

// ListAUPStatus returns all active users and whether they've agreed to the given version.
func (q *Queries) ListAUPStatus(ctx context.Context, version string) ([]AUPUserStatus, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT u.id, u.display_name, u.email, u.status,
		       (SELECT MAX(a.agreed_at)::text FROM aup_agreements a WHERE a.user_id=u.id AND a.aup_version=$1)
		FROM users u WHERE u.status='active'
		ORDER BY u.display_name`, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AUPUserStatus
	for rows.Next() {
		var s AUPUserStatus
		if err := rows.Scan(&s.UserID, &s.DisplayName, &s.Email, &s.Status, &s.AgreedAt); err != nil {
			continue
		}
		results = append(results, s)
	}
	return results, nil
}

// CountAUPAgreements returns (agreed, total active) user counts for the given version.
func (q *Queries) CountAUPAgreements(ctx context.Context, version string) (int, int, error) {
	var agreed, total int
	err := q.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE status='active'`).Scan(&total)
	if err != nil {
		return 0, 0, err
	}
	err = q.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT user_id) FROM aup_agreements WHERE aup_version=$1`, version).Scan(&agreed)
	return agreed, total, err
}

func (q *Queries) CreateGroup(ctx context.Context, g *models.Group) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO groups (name, description, owner_id, admin_group_id)
		VALUES ($1, $2, $3, $4) RETURNING id, created_at, updated_at`,
		g.Name, g.Description, g.OwnerID, g.AdminGroupID).Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
}

func (q *Queries) GetGroup(ctx context.Context, id string) (*models.Group, error) {
	var g models.Group
	err := q.pool.QueryRow(ctx, `
		SELECT id, name, description, owner_id, admin_group_id, created_at, updated_at
		FROM groups WHERE id=$1`, id).Scan(
		&g.ID, &g.Name, &g.Description, &g.OwnerID, &g.AdminGroupID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (q *Queries) UpdateGroup(ctx context.Context, g *models.Group) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE groups SET name=$2, description=$3, admin_group_id=$4, updated_at=NOW()
		WHERE id=$1`, g.ID, g.Name, g.Description, g.AdminGroupID)
	return err
}

func (q *Queries) DeleteGroup(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, id)
	return err
}

// ListUserGroups returns groups where the user is owner or a member.
func (q *Queries) ListUserGroups(ctx context.Context, userID string) ([]models.Group, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT DISTINCT g.id, g.name, g.description, g.owner_id, g.admin_group_id, g.created_at, g.updated_at
		FROM groups g
		LEFT JOIN group_members gm ON gm.group_id = g.id
		WHERE g.owner_id=$1 OR gm.user_id=$1
		ORDER BY g.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.OwnerID, &g.AdminGroupID, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// ListAllGroups returns all groups (admin only).
func (q *Queries) ListAllGroups(ctx context.Context) ([]models.Group, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT DISTINCT g.id, g.name, g.description, g.owner_id, g.admin_group_id, g.created_at, g.updated_at
		FROM groups g
		ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []models.Group
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.OwnerID, &g.AdminGroupID, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// ============================================================
// Group Members
// ============================================================

func (q *Queries) AddGroupMember(ctx context.Context, m *models.GroupMember) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO group_members (group_id, user_id, role, added_by)
		VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
		m.GroupID, m.UserID, m.Role, m.AddedBy).Scan(&m.ID, &m.CreatedAt)
}

func (q *Queries) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID)
	return err
}

func (q *Queries) UpdateGroupMemberRole(ctx context.Context, groupID, userID, role string) error {
	_, err := q.pool.Exec(ctx, `UPDATE group_members SET role=$3 WHERE group_id=$1 AND user_id=$2`, groupID, userID, role)
	return err
}

func (q *Queries) ListGroupMembers(ctx context.Context, groupID string) ([]models.GroupMember, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT gm.id, gm.group_id, gm.user_id, gm.role, gm.added_by, gm.created_at,
		       COALESCE(u.display_name, ''), COALESCE(u.email, '')
		FROM group_members gm
		LEFT JOIN users u ON u.id = gm.user_id
		WHERE gm.group_id=$1 ORDER BY gm.created_at`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []models.GroupMember
	for rows.Next() {
		var m models.GroupMember
		if err := rows.Scan(&m.ID, &m.GroupID, &m.UserID, &m.Role, &m.AddedBy, &m.CreatedAt, &m.DisplayName, &m.Email); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

// IsGroupMember checks if a user is a member of a specific group.
func (q *Queries) IsGroupMember(ctx context.Context, groupID, userID string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2
			UNION ALL
			SELECT 1 FROM groups WHERE id=$1 AND owner_id=$2
		)`, groupID, userID).Scan(&exists)
	return exists, err
}

// GetGroupMemberRole returns the user's direct role in a group ("admin", "member"),
// or empty string if not a member.
func (q *Queries) GetGroupMemberRole(ctx context.Context, groupID, userID string) (string, error) {
	var role string
	err := q.pool.QueryRow(ctx, `
		SELECT role FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, userID).Scan(&role)
	if err != nil {
		return "", nil // not a member
	}
	return role, nil
}

// ============================================================
// Group Invites
// ============================================================

func (q *Queries) CreateGroupInvite(ctx context.Context, inv *models.GroupInvite) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO group_invites (group_id, token_hash, invited_by, email, role, allows_registration, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`,
		inv.GroupID, inv.TokenHash, inv.InvitedBy, inv.Email, inv.Role, inv.AllowsRegistration, inv.ExpiresAt).Scan(&inv.ID, &inv.CreatedAt)
}

func (q *Queries) GetGroupInviteByToken(ctx context.Context, tokenHash []byte) (*models.GroupInvite, error) {
	var inv models.GroupInvite
	err := q.pool.QueryRow(ctx, `
		SELECT id, group_id, token_hash, invited_by, email, role, allows_registration, used, expires_at, created_at
		FROM group_invites WHERE token_hash=$1`, tokenHash).Scan(
		&inv.ID, &inv.GroupID, &inv.TokenHash, &inv.InvitedBy, &inv.Email, &inv.Role,
		&inv.AllowsRegistration, &inv.Used, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (q *Queries) MarkGroupInviteUsed(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `UPDATE group_invites SET used=TRUE WHERE id=$1`, id)
	return err
}

func (q *Queries) DeleteGroupInvite(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM group_invites WHERE id=$1`, id)
	return err
}

func (q *Queries) ListGroupInvites(ctx context.Context, groupID string) ([]models.GroupInvite, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, group_id, invited_by, email, role, allows_registration, used, expires_at, created_at
		FROM group_invites WHERE group_id=$1 ORDER BY created_at DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invites []models.GroupInvite
	for rows.Next() {
		var inv models.GroupInvite
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InvitedBy, &inv.Email, &inv.Role,
			&inv.AllowsRegistration, &inv.Used, &inv.ExpiresAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, nil
}

// ============================================================
// Projects
// ============================================================

func (q *Queries) CreateProject(ctx context.Context, p *models.Project) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO projects (name, description, owner_id, read_group_id, write_group_id, admin_group_id, uses_global_key, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at, updated_at`,
		p.Name, p.Description, p.OwnerID, p.ReadGroupID, p.WriteGroupID, p.AdminGroupID, p.UsesGlobalKey, p.Status).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (q *Queries) GetProject(ctx context.Context, id string) (*models.Project, error) {
	var p models.Project
	err := q.pool.QueryRow(ctx, `
		SELECT id, name, description, owner_id, read_group_id, write_group_id, admin_group_id,
		       uses_global_key, status, created_at, updated_at,
		       agent_provider, ext_llm_analysis_model, ext_llm_poc_model, ext_llm_fallback
		FROM projects WHERE id=$1`, id).Scan(
		&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.ReadGroupID, &p.WriteGroupID, &p.AdminGroupID,
		&p.UsesGlobalKey, &p.Status, &p.CreatedAt, &p.UpdatedAt,
		&p.AgentProvider, &p.ExternalLLMAnalysisModel, &p.ExternalLLMPoCModel, &p.ExternalLLMFallback)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (q *Queries) UpdateProject(ctx context.Context, p *models.Project) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE projects SET name=$2, description=$3, read_group_id=$4, write_group_id=$5,
		       admin_group_id=$6, uses_global_key=$7, status=$8,
		       agent_provider=$9, ext_llm_analysis_model=$10, ext_llm_poc_model=$11, ext_llm_fallback=$12,
		       updated_at=NOW()
		WHERE id=$1`, p.ID, p.Name, p.Description, p.ReadGroupID, p.WriteGroupID, p.AdminGroupID,
		p.UsesGlobalKey, p.Status,
		p.AgentProvider, p.ExternalLLMAnalysisModel, p.ExternalLLMPoCModel, p.ExternalLLMFallback)
	return err
}

func (q *Queries) DeleteProject(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id)
	return err
}

// ListUserProjects returns projects accessible to a user (owner or in a project group).
func (q *Queries) ListUserProjects(ctx context.Context, userID string) ([]models.Project, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT DISTINCT p.id, p.name, p.description, p.owner_id,
		       p.read_group_id, p.write_group_id, p.admin_group_id,
		       p.uses_global_key, p.status, p.created_at, p.updated_at,
		       p.agent_provider, p.ext_llm_analysis_model, p.ext_llm_poc_model, p.ext_llm_fallback
		FROM projects p
		LEFT JOIN group_members gm_r ON gm_r.group_id = p.read_group_id AND gm_r.user_id = $1
		LEFT JOIN group_members gm_w ON gm_w.group_id = p.write_group_id AND gm_w.user_id = $1
		LEFT JOIN group_members gm_a ON gm_a.group_id = p.admin_group_id AND gm_a.user_id = $1
		WHERE p.owner_id = $1
		   OR gm_r.user_id IS NOT NULL
		   OR gm_w.user_id IS NOT NULL
		   OR gm_a.user_id IS NOT NULL
		ORDER BY p.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []models.Project
	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID,
			&p.ReadGroupID, &p.WriteGroupID, &p.AdminGroupID,
			&p.UsesGlobalKey, &p.Status, &p.CreatedAt, &p.UpdatedAt,
			&p.AgentProvider, &p.ExternalLLMAnalysisModel, &p.ExternalLLMPoCModel, &p.ExternalLLMFallback); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// ListAllProjects returns all projects (admin only).
func (q *Queries) ListAllProjects(ctx context.Context) ([]models.Project, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT DISTINCT p.id, p.name, p.description, p.owner_id,
		       p.read_group_id, p.write_group_id, p.admin_group_id,
		       p.uses_global_key, p.status, p.created_at, p.updated_at,
		       p.agent_provider, p.ext_llm_analysis_model, p.ext_llm_poc_model, p.ext_llm_fallback
		FROM projects p
		ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []models.Project
	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID,
			&p.ReadGroupID, &p.WriteGroupID, &p.AdminGroupID,
			&p.UsesGlobalKey, &p.Status, &p.CreatedAt, &p.UpdatedAt,
			&p.AgentProvider, &p.ExternalLLMAnalysisModel, &p.ExternalLLMPoCModel, &p.ExternalLLMFallback); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// UserCanAccessProject checks if a user can access a project at the given level.
// Levels: "read", "write", "admin". Owner always has full access.
// Admin role: admin_group membership OR owner.
// Write role: write_group or admin_group membership OR owner.
// Read role: read_group, write_group, or admin_group membership OR owner.
func (q *Queries) UserCanAccessProject(ctx context.Context, userID, projectID, level string) (bool, error) {
	var query string
	switch level {
	case "admin":
		query = `
			SELECT EXISTS(
				SELECT 1 FROM projects p WHERE p.id=$2 AND p.owner_id=$1
				UNION ALL
				SELECT 1 FROM projects p JOIN group_members gm ON gm.group_id = p.admin_group_id
				WHERE p.id=$2 AND gm.user_id=$1
			)`
	case "write":
		query = `
			SELECT EXISTS(
				SELECT 1 FROM projects p WHERE p.id=$2 AND p.owner_id=$1
				UNION ALL
				SELECT 1 FROM projects p JOIN group_members gm ON gm.group_id = p.write_group_id
				WHERE p.id=$2 AND gm.user_id=$1
				UNION ALL
				SELECT 1 FROM projects p JOIN group_members gm ON gm.group_id = p.admin_group_id
				WHERE p.id=$2 AND gm.user_id=$1
			)`
	default: // "read"
		query = `
			SELECT EXISTS(
				SELECT 1 FROM projects p WHERE p.id=$2 AND p.owner_id=$1
				UNION ALL
				SELECT 1 FROM projects p JOIN group_members gm ON gm.group_id = p.read_group_id
				WHERE p.id=$2 AND gm.user_id=$1
				UNION ALL
				SELECT 1 FROM projects p JOIN group_members gm ON gm.group_id = p.write_group_id
				WHERE p.id=$2 AND gm.user_id=$1
				UNION ALL
				SELECT 1 FROM projects p JOIN group_members gm ON gm.group_id = p.admin_group_id
				WHERE p.id=$2 AND gm.user_id=$1
			)`
	}
	var ok bool
	err := q.pool.QueryRow(ctx, query, userID, projectID).Scan(&ok)
	return ok, err
}

// ============================================================
// Software Packages
// ============================================================

func (q *Queries) CreatePackage(ctx context.Context, pkg *models.SoftwarePackage) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO software_packages (project_id, name, git_url, git_branch, git_commit, analysis_prompt)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at, updated_at`,
		pkg.ProjectID, pkg.Name, pkg.GitURL, pkg.GitBranch, pkg.GitCommit, pkg.AnalysisPrompt).Scan(&pkg.ID, &pkg.CreatedAt, &pkg.UpdatedAt)
}

func (q *Queries) GetPackage(ctx context.Context, id string) (*models.SoftwarePackage, error) {
	var pkg models.SoftwarePackage
	err := q.pool.QueryRow(ctx, `
		SELECT id, project_id, name, git_url, git_branch, git_commit, analysis_prompt, created_at, updated_at
		FROM software_packages WHERE id=$1`, id).Scan(
		&pkg.ID, &pkg.ProjectID, &pkg.Name, &pkg.GitURL, &pkg.GitBranch, &pkg.GitCommit,
		&pkg.AnalysisPrompt, &pkg.CreatedAt, &pkg.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (q *Queries) UpdatePackage(ctx context.Context, pkg *models.SoftwarePackage) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE software_packages SET name=$2, git_url=$3, git_branch=$4, git_commit=$5,
		       analysis_prompt=$6, updated_at=NOW()
		WHERE id=$1`, pkg.ID, pkg.Name, pkg.GitURL, pkg.GitBranch, pkg.GitCommit, pkg.AnalysisPrompt)
	return err
}

func (q *Queries) DeletePackage(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM software_packages WHERE id=$1`, id)
	return err
}

func (q *Queries) ListProjectPackages(ctx context.Context, projectID string) ([]models.SoftwarePackage, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, project_id, name, git_url, git_branch, git_commit, analysis_prompt, created_at, updated_at
		FROM software_packages WHERE project_id=$1 ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pkgs []models.SoftwarePackage
	for rows.Next() {
		var pkg models.SoftwarePackage
		if err := rows.Scan(&pkg.ID, &pkg.ProjectID, &pkg.Name, &pkg.GitURL, &pkg.GitBranch,
			&pkg.GitCommit, &pkg.AnalysisPrompt, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// ============================================================
// Analyses
// ============================================================

func (q *Queries) CreateAnalysis(ctx context.Context, a *models.Analysis) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO analyses (project_id, triggered_by, status, agent_model, agent_config, environment, encrypted_dek, dek_nonce, custom_prompt)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, created_at, updated_at`,
		a.ProjectID, a.TriggeredBy, a.Status, a.AgentModel, a.AgentConfig, a.Environment,
		a.EncryptedDEK, a.DEKNonce, a.CustomPrompt).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

func (q *Queries) GetAnalysis(ctx context.Context, id string) (*models.Analysis, error) {
	var a models.Analysis
	err := q.pool.QueryRow(ctx, `
		SELECT a.id, a.project_id, a.triggered_by, COALESCE(u.display_name, ''), a.status, a.status_detail, a.agent_model, a.agent_config,
		       a.environment, a.started_at, a.completed_at, a.error_message, a.custom_prompt, a.git_commit, a.encrypted_dek, a.dek_nonce, a.created_at, a.updated_at
		FROM analyses a LEFT JOIN users u ON u.id = a.triggered_by
		WHERE a.id=$1`, id).Scan(
		&a.ID, &a.ProjectID, &a.TriggeredBy, &a.TriggeredByName, &a.Status, &a.StatusDetail, &a.AgentModel, &a.AgentConfig,
		&a.Environment, &a.StartedAt, &a.CompletedAt, &a.ErrorMessage, &a.CustomPrompt, &a.GitCommit, &a.EncryptedDEK, &a.DEKNonce, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (q *Queries) UpdateAnalysisStatus(ctx context.Context, id, status, detail, errorMsg string) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE analyses SET status=$2, status_detail=$3, error_message=$4, updated_at=NOW()
		WHERE id=$1`, id, status, detail, errorMsg)
	return err
}

func (q *Queries) SetAnalysisStarted(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE analyses SET status='running', started_at=NOW(), updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (q *Queries) SetAnalysisCompleted(ctx context.Context, id, status, errorMsg string) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE analyses SET status=$2, error_message=$3, completed_at=NOW(), updated_at=NOW()
		WHERE id=$1`, id, status, errorMsg)
	return err
}

func (q *Queries) SetAnalysisGitCommit(ctx context.Context, id, gitCommit string) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE analyses SET git_commit=$2, updated_at=NOW() WHERE id=$1`, id, gitCommit)
	return err
}

// MarkStaleAnalyses transitions any pending/running analyses to failed.
// Used on startup when the executor cannot persist jobs across restarts.
func (q *Queries) MarkStaleAnalyses(ctx context.Context) (int64, error) {
	tag, err := q.pool.Exec(ctx, `
		UPDATE analyses
		SET status='failed', error_message='Server restarted while analysis was in progress',
		    completed_at=COALESCE(completed_at, NOW()), updated_at=NOW()
		WHERE status IN ('pending', 'running')`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListActiveAnalyses returns all analyses in pending or running state.
func (q *Queries) ListActiveAnalyses(ctx context.Context) ([]models.Analysis, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, project_id, triggered_by, status, status_detail, agent_model, agent_config,
		       environment, started_at, completed_at, error_message, created_at, updated_at
		FROM analyses WHERE status IN ('pending', 'running')
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Analysis
	for rows.Next() {
		var a models.Analysis
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.TriggeredBy, &a.Status, &a.StatusDetail, &a.AgentModel,
			&a.AgentConfig, &a.Environment, &a.StartedAt, &a.CompletedAt, &a.ErrorMessage,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (q *Queries) ListProjectAnalyses(ctx context.Context, projectID string) ([]models.Analysis, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT a.id, a.project_id, a.triggered_by, COALESCE(u.display_name, ''), a.status, a.status_detail, a.agent_model, a.agent_config,
		       a.environment, a.started_at, a.completed_at, a.error_message, a.custom_prompt, a.git_commit, a.created_at, a.updated_at
		FROM analyses a LEFT JOIN users u ON u.id = a.triggered_by
		WHERE a.project_id=$1 ORDER BY a.created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var analyses []models.Analysis
	for rows.Next() {
		var a models.Analysis
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.TriggeredBy, &a.TriggeredByName, &a.Status, &a.StatusDetail, &a.AgentModel,
			&a.AgentConfig, &a.Environment, &a.StartedAt, &a.CompletedAt, &a.ErrorMessage, &a.CustomPrompt, &a.GitCommit,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		analyses = append(analyses, a)
	}
	return analyses, nil
}

// ListAllAnalyses returns analyses the user can see (owner or group member), joined with project name.
func (q *Queries) ListAllAnalyses(ctx context.Context, userID string) ([]models.Analysis, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT a.id, a.project_id, p.name, a.triggered_by, COALESCE(u.display_name, ''), a.status, a.status_detail, a.agent_model, a.agent_config,
		       a.environment, a.started_at, a.completed_at, a.error_message, a.custom_prompt, a.git_commit, a.created_at, a.updated_at
		FROM analyses a
		JOIN projects p ON p.id = a.project_id
		LEFT JOIN users u ON u.id = a.triggered_by
		LEFT JOIN group_members gm_r ON gm_r.group_id = p.read_group_id  AND gm_r.user_id = $1
		LEFT JOIN group_members gm_w ON gm_w.group_id = p.write_group_id AND gm_w.user_id = $1
		LEFT JOIN group_members gm_a ON gm_a.group_id = p.admin_group_id AND gm_a.user_id = $1
		WHERE p.owner_id = $1
		   OR gm_r.user_id IS NOT NULL
		   OR gm_w.user_id IS NOT NULL
		   OR gm_a.user_id IS NOT NULL
		ORDER BY a.created_at DESC
		LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var analyses []models.Analysis
	for rows.Next() {
		var a models.Analysis
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &a.TriggeredBy, &a.TriggeredByName, &a.Status, &a.StatusDetail,
			&a.AgentModel, &a.AgentConfig, &a.Environment, &a.StartedAt, &a.CompletedAt,
			&a.ErrorMessage, &a.CustomPrompt, &a.GitCommit, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		analyses = append(analyses, a)
	}
	return analyses, nil
}

// ListAllAnalysesAdmin returns all analyses across projects (for system admins).
func (q *Queries) ListAllAnalysesAdmin(ctx context.Context) ([]models.Analysis, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT a.id, a.project_id, p.name, a.triggered_by, COALESCE(u.display_name, ''), a.status, a.status_detail, a.agent_model, a.agent_config,
		       a.environment, a.started_at, a.completed_at, a.error_message, a.custom_prompt, a.git_commit, a.created_at, a.updated_at
		FROM analyses a
		JOIN projects p ON p.id = a.project_id
		LEFT JOIN users u ON u.id = a.triggered_by
		ORDER BY a.created_at DESC
		LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var analyses []models.Analysis
	for rows.Next() {
		var a models.Analysis
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &a.TriggeredBy, &a.TriggeredByName, &a.Status, &a.StatusDetail,
			&a.AgentModel, &a.AgentConfig, &a.Environment, &a.StartedAt, &a.CompletedAt,
			&a.ErrorMessage, &a.CustomPrompt, &a.GitCommit, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		analyses = append(analyses, a)
	}
	return analyses, nil
}

// ============================================================
// Analysis Packages (join table)
// ============================================================

func (q *Queries) AddAnalysisPackage(ctx context.Context, analysisID, packageID string) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO analysis_packages (analysis_id, package_id) VALUES ($1, $2)
		ON CONFLICT (analysis_id, package_id) DO NOTHING`, analysisID, packageID)
	return err
}

func (q *Queries) ListAnalysisPackages(ctx context.Context, analysisID string) ([]models.SoftwarePackage, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT sp.id, sp.project_id, sp.name, sp.git_url, sp.git_branch, sp.git_commit,
		       sp.analysis_prompt, sp.created_at, sp.updated_at
		FROM software_packages sp
		JOIN analysis_packages ap ON ap.package_id = sp.id
		WHERE ap.analysis_id=$1 ORDER BY sp.name`, analysisID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pkgs []models.SoftwarePackage
	for rows.Next() {
		var pkg models.SoftwarePackage
		if err := rows.Scan(&pkg.ID, &pkg.ProjectID, &pkg.Name, &pkg.GitURL, &pkg.GitBranch,
			&pkg.GitCommit, &pkg.AnalysisPrompt, &pkg.CreatedAt, &pkg.UpdatedAt); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// ============================================================
// Analysis Results
// ============================================================

func (q *Queries) CreateAnalysisResult(ctx context.Context, r *models.AnalysisResult) error {
	// package_id is a nullable UUID column; pass nil when unset to avoid
	// "invalid input syntax for type uuid" errors.
	var pkgID any
	if r.PackageID != nil && *r.PackageID != "" {
		pkgID = *r.PackageID
	}
	// severity_counts defaults to '{}' in the schema but we pass it
	// explicitly, so use COALESCE to avoid NOT NULL violations when
	// the caller doesn't set it (e.g. worker uploads).
	return q.pool.QueryRow(ctx, `
		INSERT INTO analysis_results (analysis_id, package_id, result_type, s3_key, filename,
		       content_type, file_size, summary, finding_count, severity_counts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, ''), COALESCE($9, 0), COALESCE($10, '{}'::jsonb))
		RETURNING id, created_at`,
		r.AnalysisID, pkgID, r.ResultType, r.S3Key, r.Filename,
		r.ContentType, r.FileSize, r.Summary, r.FindingCount, r.SeverityCounts).Scan(&r.ID, &r.CreatedAt)
}

// UpdateAnalysisResultMetadata updates the summary, finding_count, and severity_counts
// for an existing analysis result (used after SARIF parsing).
func (q *Queries) UpdateAnalysisResultMetadata(ctx context.Context, id, summary string, findingCount int, severityCounts json.RawMessage) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE analysis_results
		SET summary = $2, finding_count = $3, severity_counts = COALESCE($4, '{}'::jsonb)
		WHERE id = $1`, id, summary, findingCount, severityCounts)
	return err
}

func (q *Queries) ListAnalysisResults(ctx context.Context, analysisID string) ([]models.AnalysisResult, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, analysis_id, package_id, result_type, s3_key, filename,
		       content_type, file_size, summary, finding_count, severity_counts, created_at
		FROM analysis_results WHERE analysis_id=$1 ORDER BY created_at`, analysisID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []models.AnalysisResult
	for rows.Next() {
		var r models.AnalysisResult
		if err := rows.Scan(&r.ID, &r.AnalysisID, &r.PackageID, &r.ResultType, &r.S3Key, &r.Filename,
			&r.ContentType, &r.FileSize, &r.Summary, &r.FindingCount, &r.SeverityCounts, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (q *Queries) GetAnalysisResult(ctx context.Context, id string) (*models.AnalysisResult, error) {
	var r models.AnalysisResult
	err := q.pool.QueryRow(ctx, `
		SELECT id, analysis_id, package_id, result_type, s3_key, filename,
		       content_type, file_size, summary, finding_count, severity_counts, created_at
		FROM analysis_results WHERE id=$1`, id).Scan(
		&r.ID, &r.AnalysisID, &r.PackageID, &r.ResultType, &r.S3Key, &r.Filename,
		&r.ContentType, &r.FileSize, &r.Summary, &r.FindingCount, &r.SeverityCounts, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ============================================================
// Findings
// ============================================================

func (q *Queries) CreateFinding(ctx context.Context, f *models.Finding) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO findings (project_id, analysis_id, result_id, rule_id, level, message,
		       file_path, start_line, end_line, snippet, fingerprint, raw_json, git_commit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at`,
		f.ProjectID, f.AnalysisID, f.ResultID, f.RuleID, f.Level, f.Message,
		f.FilePath, f.StartLine, f.EndLine, f.Snippet, f.Fingerprint, f.RawJSON, f.GitCommit,
	).Scan(&f.ID, &f.CreatedAt)
}

func (q *Queries) CreateFindingsBatch(ctx context.Context, findings []models.Finding) error {
	for i := range findings {
		if err := q.CreateFinding(ctx, &findings[i]); err != nil {
			return err
		}
	}
	return nil
}

// ListProjectFindings returns all findings for a project the user can access,
// with their latest annotation. Supports filtering by level, rule, status, and analysis.
func (q *Queries) ListProjectFindings(ctx context.Context, projectID string, filters FindingFilters) ([]models.Finding, error) {
	query := `
		SELECT f.id, f.project_id, f.analysis_id, f.result_id, f.rule_id, f.level,
		       f.message, f.file_path, f.start_line, f.end_line, f.snippet, f.fingerprint,
		       f.raw_json, f.git_commit, f.created_at,
		       COALESCE(fa.status, 'open') AS latest_status,
		       COALESCE(fa.note, '') AS latest_note,
		       COALESCE(u.display_name, '') AS annotation_by
		FROM findings f
		LEFT JOIN LATERAL (
		    SELECT fa2.status, fa2.note, fa2.user_id
		    FROM finding_annotations fa2
		    WHERE fa2.finding_id = f.id
		    ORDER BY fa2.updated_at DESC
		    LIMIT 1
		) fa ON true
		LEFT JOIN users u ON u.id = fa.user_id
		WHERE f.project_id = $1`

	args := []any{projectID}
	argN := 2

	if filters.Level != "" {
		query += fmt.Sprintf(" AND f.level = $%d", argN)
		args = append(args, filters.Level)
		argN++
	}
	if filters.RuleID != "" {
		query += fmt.Sprintf(" AND f.rule_id = $%d", argN)
		args = append(args, filters.RuleID)
		argN++
	}
	if filters.Status != "" {
		if filters.Status == "open" {
			query += " AND (fa.status IS NULL OR fa.status = 'open')"
		} else {
			query += fmt.Sprintf(" AND fa.status = $%d", argN)
			args = append(args, filters.Status)
			argN++
		}
	}
	if filters.AnalysisID != "" {
		query += fmt.Sprintf(" AND f.analysis_id = $%d", argN)
		args = append(args, filters.AnalysisID)
		argN++
	}
	if filters.FilePath != "" {
		query += fmt.Sprintf(" AND f.file_path ILIKE $%d", argN)
		args = append(args, "%"+filters.FilePath+"%")
		argN++
	}
	if filters.Search != "" {
		query += fmt.Sprintf(" AND (f.rule_id ILIKE $%d OR f.file_path ILIKE $%d OR f.message ILIKE $%d)", argN, argN, argN)
		args = append(args, "%"+filters.Search+"%")
		argN++
	}

	query += " ORDER BY CASE f.level WHEN 'error' THEN 1 WHEN 'warning' THEN 2 WHEN 'note' THEN 3 ELSE 4 END, f.created_at DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, filters.Limit)
		argN++
	}
	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, filters.Offset)
	}

	rows, err := q.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var findings []models.Finding
	for rows.Next() {
		var f models.Finding
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.AnalysisID, &f.ResultID, &f.RuleID,
			&f.Level, &f.Message, &f.FilePath, &f.StartLine, &f.EndLine, &f.Snippet,
			&f.Fingerprint, &f.RawJSON, &f.GitCommit, &f.CreatedAt,
			&f.LatestStatus, &f.LatestNote, &f.AnnotationBy); err != nil {
			return nil, err
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// CountProjectFindings returns total count for pagination.
func (q *Queries) CountProjectFindings(ctx context.Context, projectID string, filters FindingFilters) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM findings f
		LEFT JOIN LATERAL (
		    SELECT fa2.status FROM finding_annotations fa2
		    WHERE fa2.finding_id = f.id ORDER BY fa2.updated_at DESC LIMIT 1
		) fa ON true
		WHERE f.project_id = $1`

	args := []any{projectID}
	argN := 2

	if filters.Level != "" {
		query += fmt.Sprintf(" AND f.level = $%d", argN)
		args = append(args, filters.Level)
		argN++
	}
	if filters.RuleID != "" {
		query += fmt.Sprintf(" AND f.rule_id = $%d", argN)
		args = append(args, filters.RuleID)
		argN++
	}
	if filters.Status != "" {
		if filters.Status == "open" {
			query += " AND (fa.status IS NULL OR fa.status = 'open')"
		} else {
			query += fmt.Sprintf(" AND fa.status = $%d", argN)
			args = append(args, filters.Status)
			argN++
		}
	}
	if filters.AnalysisID != "" {
		query += fmt.Sprintf(" AND f.analysis_id = $%d", argN)
		args = append(args, filters.AnalysisID)
		argN++
	}
	if filters.FilePath != "" {
		query += fmt.Sprintf(" AND f.file_path ILIKE $%d", argN)
		args = append(args, "%"+filters.FilePath+"%")
		argN++
	}
	if filters.Search != "" {
		query += fmt.Sprintf(" AND (f.rule_id ILIKE $%d OR f.file_path ILIKE $%d OR f.message ILIKE $%d)", argN, argN, argN)
		args = append(args, "%"+filters.Search+"%")
	}

	var count int
	err := q.pool.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

// FindingFilters holds query parameters for listing findings.
type FindingFilters struct {
	Level      string
	RuleID     string
	Status     string
	AnalysisID string
	FilePath   string
	Search     string
	Limit      int
	Offset     int
}

func (q *Queries) GetFinding(ctx context.Context, id string) (*models.Finding, error) {
	var f models.Finding
	err := q.pool.QueryRow(ctx, `
		SELECT f.id, f.project_id, f.analysis_id, f.result_id, f.rule_id, f.level,
		       f.message, f.file_path, f.start_line, f.end_line, f.snippet, f.fingerprint,
		       f.raw_json, f.created_at,
		       COALESCE(fa.status, 'open'), COALESCE(fa.note, ''), COALESCE(u.display_name, '')
		FROM findings f
		LEFT JOIN LATERAL (
		    SELECT fa2.status, fa2.note, fa2.user_id
		    FROM finding_annotations fa2
		    WHERE fa2.finding_id = f.id
		    ORDER BY fa2.updated_at DESC LIMIT 1
		) fa ON true
		LEFT JOIN users u ON u.id = fa.user_id
		WHERE f.id = $1`, id).Scan(
		&f.ID, &f.ProjectID, &f.AnalysisID, &f.ResultID, &f.RuleID,
		&f.Level, &f.Message, &f.FilePath, &f.StartLine, &f.EndLine, &f.Snippet,
		&f.Fingerprint, &f.RawJSON, &f.CreatedAt,
		&f.LatestStatus, &f.LatestNote, &f.AnnotationBy)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (q *Queries) UpsertFindingAnnotation(ctx context.Context, a *models.FindingAnnotation) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO finding_annotations (finding_id, user_id, status, note)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (finding_id, user_id) DO UPDATE
		  SET status = EXCLUDED.status, note = EXCLUDED.note, updated_at = NOW()
		RETURNING id, created_at, updated_at`,
		a.FindingID, a.UserID, a.Status, a.Note).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
}

func (q *Queries) ListFindingAnnotations(ctx context.Context, findingID string) ([]models.FindingAnnotation, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT fa.id, fa.finding_id, fa.user_id, COALESCE(u.display_name, ''),
		       fa.status, fa.note, fa.created_at, fa.updated_at
		FROM finding_annotations fa
		LEFT JOIN users u ON u.id = fa.user_id
		WHERE fa.finding_id = $1
		ORDER BY fa.updated_at DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var annotations []models.FindingAnnotation
	for rows.Next() {
		var a models.FindingAnnotation
		if err := rows.Scan(&a.ID, &a.FindingID, &a.UserID, &a.UserDisplayName,
			&a.Status, &a.Note, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		annotations = append(annotations, a)
	}
	return annotations, nil
}

// ListAllFindings returns findings across all projects the user can access.
func (q *Queries) ListAllFindings(ctx context.Context, userID string, isAdmin bool, filters FindingFilters) ([]models.Finding, int, error) {
	baseFrom := `FROM findings f
		LEFT JOIN LATERAL (
		    SELECT fa2.status, fa2.note, fa2.user_id
		    FROM finding_annotations fa2 WHERE fa2.finding_id = f.id
		    ORDER BY fa2.updated_at DESC LIMIT 1
		) fa ON true
		LEFT JOIN users u ON u.id = fa.user_id`

	var accessWhere string
	args := []any{}
	argN := 1

	if !isAdmin {
		baseFrom += `
		JOIN analyses a ON f.analysis_id = a.id
		JOIN projects p ON a.project_id = p.id
		LEFT JOIN group_members gm ON gm.user_id = $1
		  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)`
		accessWhere = fmt.Sprintf(" AND (p.owner_id = $%d OR gm.id IS NOT NULL)", argN)
		args = append(args, userID)
		argN++
	}

	whereClause := "WHERE 1=1" + accessWhere

	if filters.Level != "" {
		whereClause += fmt.Sprintf(" AND f.level = $%d", argN)
		args = append(args, filters.Level)
		argN++
	}
	if filters.RuleID != "" {
		whereClause += fmt.Sprintf(" AND f.rule_id = $%d", argN)
		args = append(args, filters.RuleID)
		argN++
	}
	if filters.Status != "" {
		if filters.Status == "open" {
			whereClause += " AND (fa.status IS NULL OR fa.status = 'open')"
		} else {
			whereClause += fmt.Sprintf(" AND fa.status = $%d", argN)
			args = append(args, filters.Status)
			argN++
		}
	}
	if filters.FilePath != "" {
		whereClause += fmt.Sprintf(" AND f.file_path ILIKE $%d", argN)
		args = append(args, "%"+filters.FilePath+"%")
		argN++
	}
	if filters.Search != "" {
		whereClause += fmt.Sprintf(" AND (f.rule_id ILIKE $%d OR f.file_path ILIKE $%d OR f.message ILIKE $%d)", argN, argN, argN)
		args = append(args, "%"+filters.Search+"%")
		argN++
	}

	// Count
	var total int
	countQuery := "SELECT COUNT(*) " + baseFrom + " " + whereClause
	_ = q.pool.QueryRow(ctx, countQuery, args...).Scan(&total)

	// Fetch
	selectQuery := `SELECT f.id, f.project_id, f.analysis_id, f.result_id, f.rule_id, f.level,
		       f.message, f.file_path, f.start_line, f.end_line, f.snippet, f.fingerprint,
		       f.raw_json, f.git_commit, f.created_at,
		       COALESCE(fa.status, 'open'), COALESCE(fa.note, ''), COALESCE(u.display_name, ''),
		       COALESCE((SELECT sp.git_url FROM analysis_packages ap JOIN software_packages sp ON sp.id = ap.package_id WHERE ap.analysis_id = f.analysis_id LIMIT 1), '') ` +
		baseFrom + " " + whereClause +
		" ORDER BY CASE f.level WHEN 'error' THEN 1 WHEN 'warning' THEN 2 WHEN 'note' THEN 3 ELSE 4 END, f.created_at DESC"

	fetchArgs := make([]any, len(args))
	copy(fetchArgs, args)

	if filters.Limit > 0 {
		selectQuery += fmt.Sprintf(" LIMIT $%d", argN)
		fetchArgs = append(fetchArgs, filters.Limit)
		argN++
	}
	if filters.Offset > 0 {
		selectQuery += fmt.Sprintf(" OFFSET $%d", argN)
		fetchArgs = append(fetchArgs, filters.Offset)
	}

	rows, err := q.pool.Query(ctx, selectQuery, fetchArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var findings []models.Finding
	for rows.Next() {
		var f models.Finding
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.AnalysisID, &f.ResultID, &f.RuleID,
			&f.Level, &f.Message, &f.FilePath, &f.StartLine, &f.EndLine, &f.Snippet,
			&f.Fingerprint, &f.RawJSON, &f.GitCommit, &f.CreatedAt,
			&f.LatestStatus, &f.LatestNote, &f.AnnotationBy, &f.GitURL); err != nil {
			return nil, 0, err
		}
		findings = append(findings, f)
	}
	return findings, total, nil
}

// ListProjectFindingAnnotations returns all annotations for findings in a project,
// useful for feeding context to future analyses.
func (q *Queries) ListProjectFindingAnnotations(ctx context.Context, projectID string) ([]models.FindingAnnotation, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT fa.id, fa.finding_id, fa.user_id, COALESCE(u.display_name, ''),
		       fa.status, fa.note, fa.created_at, fa.updated_at
		FROM finding_annotations fa
		JOIN findings f ON f.id = fa.finding_id
		LEFT JOIN users u ON u.id = fa.user_id
		WHERE f.project_id = $1
		ORDER BY fa.updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var annotations []models.FindingAnnotation
	for rows.Next() {
		var a models.FindingAnnotation
		if err := rows.Scan(&a.ID, &a.FindingID, &a.UserID, &a.UserDisplayName,
			&a.Status, &a.Note, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		annotations = append(annotations, a)
	}
	return annotations, nil
}

// GetOpenFindingsSummary returns a compact summary of open (non-dismissed)
// findings for a project, suitable for injecting into an analysis prompt.
// Each row is: rule_id, level, file_path, start_line, message, status.
func (q *Queries) GetOpenFindingsSummary(ctx context.Context, projectID string) ([]models.FindingSummary, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT f.rule_id, f.level, f.file_path, f.start_line, f.message,
		       COALESCE(fa.status, 'open') AS status, COALESCE(fa.note, '') AS note
		FROM findings f
		LEFT JOIN LATERAL (
		    SELECT fa2.status, fa2.note
		    FROM finding_annotations fa2 WHERE fa2.finding_id = f.id
		    ORDER BY fa2.updated_at DESC LIMIT 1
		) fa ON true
		WHERE f.project_id = $1
		  AND COALESCE(fa.status, 'open') NOT IN ('false_positive', 'not_relevant')
		ORDER BY CASE f.level WHEN 'error' THEN 1 WHEN 'warning' THEN 2 WHEN 'note' THEN 3 ELSE 4 END,
		         f.file_path, f.start_line`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.FindingSummary
	for rows.Next() {
		var s models.FindingSummary
		if err := rows.Scan(&s.RuleID, &s.Level, &s.FilePath, &s.StartLine, &s.Message, &s.Status, &s.Note); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// GetRecentAnalysisNotes retrieves analysis_notes content from the last N
// completed analyses for the project. It joins through analysis_results to get
// the S3 key and metadata. The caller must decrypt and read the content from S3.
// If gitBranch is non-empty, it filters to analyses that used a matching branch
// (via analysis_packages → software_packages) or the default branch.
func (q *Queries) GetRecentAnalysisNotes(ctx context.Context, projectID string, gitBranch string, limit int) ([]models.AnalysisNoteRef, error) {
	query := `
		SELECT a.id, a.completed_at, ar.s3_key, a.encrypted_dek, a.dek_nonce
		FROM analyses a
		JOIN analysis_results ar ON ar.analysis_id = a.id AND ar.result_type = 'analysis_notes'
		WHERE a.project_id = $1
		  AND a.status = 'completed'`
	args := []any{projectID}
	argN := 2

	if gitBranch != "" {
		query += fmt.Sprintf(`
		  AND EXISTS (
		    SELECT 1 FROM analysis_packages ap
		    JOIN software_packages sp ON sp.id = ap.package_id
		    WHERE ap.analysis_id = a.id
		      AND (sp.git_branch = $%d OR sp.git_branch IN ('main', 'master'))
		  )`, argN)
		args = append(args, gitBranch)
		argN++
	}

	query += fmt.Sprintf(` ORDER BY a.completed_at DESC LIMIT $%d`, argN)
	args = append(args, limit)

	rows, err := q.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []models.AnalysisNoteRef
	for rows.Next() {
		var r models.AnalysisNoteRef
		if err := rows.Scan(&r.AnalysisID, &r.CompletedAt, &r.S3Key, &r.EncryptedDEK, &r.DEKNonce); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// ============================================================
// API Keys
// ============================================================

func (q *Queries) CreateAPIKey(ctx context.Context, k *models.APIKey) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO api_keys (name, key_hash, key_prefix, user_id, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		k.Name, k.KeyHash, k.KeyPrefix, k.UserID, k.ExpiresAt).Scan(&k.ID, &k.CreatedAt)
}

func (q *Queries) GetAPIKeyByPrefix(ctx context.Context, prefix string) (*models.APIKey, error) {
	var k models.APIKey
	err := q.pool.QueryRow(ctx, `
		SELECT id, name, key_hash, key_prefix, user_id, created_at, expires_at, revoked_at, last_used_at
		FROM api_keys WHERE key_prefix=$1 AND revoked_at IS NULL
		AND (expires_at IS NULL OR expires_at > NOW())`, prefix).Scan(
		&k.ID, &k.Name, &k.KeyHash, &k.KeyPrefix, &k.UserID,
		&k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (q *Queries) UpdateAPIKeyLastUsed(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `UPDATE api_keys SET last_used_at=NOW() WHERE id=$1`, id)
	return err
}

func (q *Queries) RevokeAPIKey(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `UPDATE api_keys SET revoked_at=NOW() WHERE id=$1`, id)
	return err
}

func (q *Queries) ListUserAPIKeys(ctx context.Context, userID string) ([]models.APIKey, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, name, key_prefix, user_id, created_at, expires_at, revoked_at, last_used_at
		FROM api_keys WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []models.APIKey
	for rows.Next() {
		var k models.APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.UserID,
			&k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// ============================================================
// App Config
// ============================================================

func (q *Queries) GetAppConfig(ctx context.Context, key string) (string, error) {
	var val string
	err := q.pool.QueryRow(ctx, `SELECT value FROM app_config WHERE key=$1`, key).Scan(&val)
	return val, err
}

func (q *Queries) SetAppConfig(ctx context.Context, key, value string) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO app_config (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value=$2, updated_at=NOW()`, key, value)
	return err
}

func (q *Queries) ListAppConfig(ctx context.Context) ([]models.AppConfig, error) {
	rows, err := q.pool.Query(ctx, `SELECT key, value, updated_at FROM app_config ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []models.AppConfig
	for rows.Next() {
		var c models.AppConfig
		if err := rows.Scan(&c.Key, &c.Value, &c.UpdatedAt); err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, nil
}

// ============================================================
// Backups
// ============================================================

func (q *Queries) CreateBackup(ctx context.Context, b *models.Backup) error {
	return q.pool.QueryRow(ctx, `
		INSERT INTO backups (filename, s3_key, s3_bucket, status, initiated_by, encrypted, started_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW()) RETURNING id, created_at`,
		b.Filename, b.S3Key, b.S3Bucket, b.Status, b.InitiatedBy, b.Encrypted).Scan(&b.ID, &b.CreatedAt)
}

func (q *Queries) UpdateBackup(ctx context.Context, b *models.Backup) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE backups SET status=$2, status_detail=$3, error_msg=$4, size_bytes=$5,
		       checksum=$6, completed_at=$7, encrypted=$8, duration_secs=$9, filename=$10
		WHERE id=$1`, b.ID, b.Status, b.StatusDetail, b.ErrorMsg, b.SizeBytes, b.Checksum,
		b.CompletedAt, b.Encrypted, b.DurationSecs, b.Filename)
	return err
}

func (q *Queries) ListBackups(ctx context.Context) ([]models.Backup, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, filename, s3_key, s3_bucket, size_bytes, status, status_detail,
		       error_msg, initiated_by, encrypted, checksum, duration_secs,
		       started_at, completed_at, created_at
		FROM backups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var backups []models.Backup
	for rows.Next() {
		var b models.Backup
		if err := rows.Scan(&b.ID, &b.Filename, &b.S3Key, &b.S3Bucket, &b.SizeBytes, &b.Status,
			&b.StatusDetail, &b.ErrorMsg, &b.InitiatedBy, &b.Encrypted, &b.Checksum,
			&b.DurationSecs, &b.StartedAt, &b.CompletedAt, &b.CreatedAt); err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	return backups, nil
}

func (q *Queries) GetBackup(ctx context.Context, id string) (*models.Backup, error) {
	var b models.Backup
	err := q.pool.QueryRow(ctx, `
		SELECT id, filename, s3_key, s3_bucket, size_bytes, status, status_detail,
		       error_msg, initiated_by, encrypted, checksum, duration_secs,
		       started_at, completed_at, created_at
		FROM backups WHERE id=$1`, id).Scan(
		&b.ID, &b.Filename, &b.S3Key, &b.S3Bucket, &b.SizeBytes, &b.Status,
		&b.StatusDetail, &b.ErrorMsg, &b.InitiatedBy, &b.Encrypted, &b.Checksum,
		&b.DurationSecs, &b.StartedAt, &b.CompletedAt, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (q *Queries) DeleteBackup(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM backups WHERE id=$1`, id)
	return err
}

// GetLastCompletedBackupTime returns the completion time of the most recent
// successful backup, or nil if no completed backup exists.
func (q *Queries) GetLastCompletedBackupTime(ctx context.Context) (*time.Time, error) {
	var t time.Time
	err := q.pool.QueryRow(ctx, `
		SELECT completed_at FROM backups
		WHERE status='completed' AND completed_at IS NOT NULL
		ORDER BY completed_at DESC LIMIT 1`).Scan(&t)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (q *Queries) DeleteFailedBackups(ctx context.Context) (int64, error) {
	tag, err := q.pool.Exec(ctx, `DELETE FROM backups WHERE status='failed'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkStaleRunningBackupsFailed marks backups stuck in "running" status as failed
// if they were started more than the given threshold ago. Backups in the exclude
// list are skipped (these are actively being processed by this server instance).
func (q *Queries) MarkStaleRunningBackupsFailed(ctx context.Context, olderThan time.Duration, excludeIDs []string) (int64, error) {
	cutoff := time.Now().Add(-olderThan)

	// Build query with optional exclusion
	query := `
		UPDATE backups 
		SET status='failed', error_msg='Backup timed out or server restarted', completed_at=NOW()
		WHERE status='running' AND started_at < $1`
	args := []any{cutoff}

	if len(excludeIDs) > 0 {
		query += ` AND id != ALL($2)`
		args = append(args, excludeIDs)
	}

	tag, err := q.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ============================================================
// Object Hashes (for incremental backup)
// ============================================================

func (q *Queries) GetObjectHash(ctx context.Context, s3Key string) (*models.ObjectHash, error) {
	var oh models.ObjectHash
	err := q.pool.QueryRow(ctx, `
		SELECT id, s3_key, sha256, size_bytes, updated_at
		FROM object_hashes WHERE s3_key=$1`, s3Key).Scan(
		&oh.ID, &oh.S3Key, &oh.SHA256, &oh.SizeBytes, &oh.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &oh, nil
}

func (q *Queries) UpsertObjectHash(ctx context.Context, s3Key, sha256Hash string, sizeBytes int64) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO object_hashes (s3_key, sha256, size_bytes, updated_at) VALUES ($1, $2, $3, NOW())
		ON CONFLICT (s3_key) DO UPDATE SET sha256=$2, size_bytes=$3, updated_at=NOW()`,
		s3Key, sha256Hash, sizeBytes)
	return err
}

// ExecRaw executes a raw SQL string against the database.
func (q *Queries) ExecRaw(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return fmt.Errorf("exec raw: %w", err)
}

// DashboardStats holds aggregate stats for the dashboard.
type DashboardStats struct {
	ProjectCount   int               `json:"project_count"`
	GroupCount     int               `json:"group_count"`
	AnalysisCounts map[string]int    `json:"analysis_counts"`
	RecentAnalyses []models.Analysis `json:"recent_analyses"`
	TotalFindings  int               `json:"total_findings"`
	SeverityCounts map[string]int    `json:"severity_counts"`
}

// GetDashboardStats returns aggregate statistics visible to the given user.
func (q *Queries) GetDashboardStats(ctx context.Context, userID string, isAdmin bool) (*DashboardStats, error) {
	s := &DashboardStats{
		AnalysisCounts: make(map[string]int),
		SeverityCounts: make(map[string]int),
	}

	// Project count
	if isAdmin {
		_ = q.pool.QueryRow(ctx, `SELECT COUNT(*) FROM projects`).Scan(&s.ProjectCount)
	} else {
		_ = q.pool.QueryRow(ctx, `
			SELECT COUNT(DISTINCT p.id) FROM projects p
			LEFT JOIN group_members gm ON gm.user_id=$1
			  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
			WHERE p.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.ProjectCount)
	}

	// Group count
	_ = q.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT g.id) FROM groups g
		LEFT JOIN group_members gm ON gm.group_id=g.id AND gm.user_id=$1
		WHERE g.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.GroupCount)

	// Analysis status counts
	var statusRows interface {
		Next() bool
		Scan(...interface{}) error
		Close()
	}
	var err error
	if isAdmin {
		statusRows, err = q.pool.Query(ctx, `SELECT status, COUNT(*) FROM analyses GROUP BY status`)
	} else {
		statusRows, err = q.pool.Query(ctx, `
			SELECT a.status, COUNT(*) FROM analyses a
			JOIN projects p ON a.project_id=p.id
			LEFT JOIN group_members gm ON gm.user_id=$1
			  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
			WHERE p.owner_id=$1 OR gm.id IS NOT NULL
			GROUP BY a.status`, userID)
	}
	if err == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var status string
			var count int
			if err := statusRows.Scan(&status, &count); err == nil {
				s.AnalysisCounts[status] = count
			}
		}
	}

	// Recent analyses (last 10)
	var recentRows interface {
		Next() bool
		Scan(...interface{}) error
		Close()
	}
	if isAdmin {
		recentRows, err = q.pool.Query(ctx, `
			SELECT a.id, a.project_id, COALESCE(p.name,'') as project_name,
			       a.triggered_by, COALESCE(u.display_name, ''), a.status, a.status_detail,
			       a.started_at, a.completed_at, a.error_message, a.created_at, a.updated_at
			FROM analyses a LEFT JOIN projects p ON a.project_id=p.id
			LEFT JOIN users u ON u.id = a.triggered_by
			ORDER BY a.created_at DESC LIMIT 10`)
	} else {
		recentRows, err = q.pool.Query(ctx, `
			SELECT a.id, a.project_id, COALESCE(p.name,'') as project_name,
			       a.triggered_by, COALESCE(u.display_name, ''), a.status, a.status_detail,
			       a.started_at, a.completed_at, a.error_message, a.created_at, a.updated_at
			FROM analyses a
			JOIN projects p ON a.project_id=p.id
			LEFT JOIN users u ON u.id = a.triggered_by
			LEFT JOIN group_members gm ON gm.user_id=$1
			  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
			WHERE p.owner_id=$1 OR gm.id IS NOT NULL
			ORDER BY a.created_at DESC LIMIT 10`, userID)
	}
	if err == nil {
		defer recentRows.Close()
		for recentRows.Next() {
			var a models.Analysis
			if err := recentRows.Scan(&a.ID, &a.ProjectID, &a.ProjectName,
				&a.TriggeredBy, &a.TriggeredByName, &a.Status, &a.StatusDetail,
				&a.StartedAt, &a.CompletedAt, &a.ErrorMessage, &a.CreatedAt, &a.UpdatedAt); err == nil {
				s.RecentAnalyses = append(s.RecentAnalyses, a)
			}
		}
	}

	// Finding counts from completed analyses' SARIF results
	if isAdmin {
		_ = q.pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(ar.finding_count), 0)
			FROM analysis_results ar
			WHERE ar.result_type='sarif'`).Scan(&s.TotalFindings)
	} else {
		_ = q.pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(ar.finding_count), 0)
			FROM analysis_results ar
			JOIN analyses a ON ar.analysis_id=a.id
			JOIN projects p ON a.project_id=p.id
			LEFT JOIN group_members gm ON gm.user_id=$1
			  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
			WHERE ar.result_type='sarif' AND (p.owner_id=$1 OR gm.id IS NOT NULL)`, userID).Scan(&s.TotalFindings)
	}

	// Aggregate severity counts from SARIF results
	var sevRows interface {
		Next() bool
		Scan(...interface{}) error
		Close()
	}
	if isAdmin {
		sevRows, err = q.pool.Query(ctx, `
			SELECT severity_counts FROM analysis_results WHERE result_type='sarif' AND severity_counts IS NOT NULL`)
	} else {
		sevRows, err = q.pool.Query(ctx, `
			SELECT ar.severity_counts
			FROM analysis_results ar
			JOIN analyses a ON ar.analysis_id=a.id
			JOIN projects p ON a.project_id=p.id
			LEFT JOIN group_members gm ON gm.user_id=$1
			  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
			WHERE ar.result_type='sarif' AND ar.severity_counts IS NOT NULL
			  AND (p.owner_id=$1 OR gm.id IS NOT NULL)`, userID)
	}
	if err == nil {
		defer sevRows.Close()
		for sevRows.Next() {
			var raw map[string]int
			if err := sevRows.Scan(&raw); err == nil {
				for k, v := range raw {
					s.SeverityCounts[k] += v
				}
			}
		}
	}

	return s, nil
}

// UserStats holds per-user counts for the Info page.
type UserStats struct {
	GroupCount    int    `json:"group_count"`
	ProjectCount  int    `json:"project_count"`
	PackageCount  int    `json:"package_count"`
	AnalysisCount int    `json:"analysis_count"`
	FindingCount  int    `json:"finding_count"`
	MemberSince   string `json:"member_since"`
}

// GetUserStats returns aggregate counts scoped to a single user.
func (q *Queries) GetUserStats(ctx context.Context, userID string) (*UserStats, error) {
	s := &UserStats{}
	_ = q.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT g.id) FROM groups g
		LEFT JOIN group_members gm ON gm.group_id=g.id AND gm.user_id=$1
		WHERE g.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.GroupCount)

	_ = q.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT p.id) FROM projects p
		LEFT JOIN group_members gm ON gm.user_id=$1
		  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
		WHERE p.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.ProjectCount)

	_ = q.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM software_packages sp
		JOIN projects p ON sp.project_id=p.id
		LEFT JOIN group_members gm ON gm.user_id=$1
		  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
		WHERE p.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.PackageCount)

	_ = q.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM analyses a
		JOIN projects p ON a.project_id=p.id
		LEFT JOIN group_members gm ON gm.user_id=$1
		  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
		WHERE p.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.AnalysisCount)

	_ = q.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM findings f
		JOIN analyses a ON f.analysis_id=a.id
		JOIN projects p ON a.project_id=p.id
		LEFT JOIN group_members gm ON gm.user_id=$1
		  AND (gm.group_id = p.read_group_id OR gm.group_id = p.write_group_id OR gm.group_id = p.admin_group_id)
		WHERE p.owner_id=$1 OR gm.id IS NOT NULL`, userID).Scan(&s.FindingCount)

	_ = q.pool.QueryRow(ctx, `SELECT created_at FROM users WHERE id=$1`, userID).Scan(&s.MemberSince)

	return s, nil
}

// ============================================================
// Worker Tokens (session/proxy — only SHA-256 hashes stored)
// ============================================================

func (q *Queries) CreateWorkerToken(ctx context.Context, tokenHash, tokenType, analysisID string, sessionData []byte) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO worker_tokens (token_hash, token_type, analysis_id, session_data)
		VALUES ($1, $2, $3, $4)`,
		tokenHash, tokenType, analysisID, sessionData)
	return err
}

func (q *Queries) GetWorkerSession(ctx context.Context, tokenHash string) (string, []byte, error) {
	var analysisID string
	var data []byte
	err := q.pool.QueryRow(ctx, `
		SELECT analysis_id, session_data FROM worker_tokens
		WHERE token_hash=$1 AND token_type='session'`, tokenHash).Scan(&analysisID, &data)
	return analysisID, data, err
}

func (q *Queries) GetWorkerProxyToken(ctx context.Context, tokenHash string) (string, error) {
	var analysisID string
	err := q.pool.QueryRow(ctx, `
		SELECT analysis_id FROM worker_tokens
		WHERE token_hash=$1 AND token_type='proxy'`, tokenHash).Scan(&analysisID)
	return analysisID, err
}

func (q *Queries) DeleteWorkerToken(ctx context.Context, tokenHash string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM worker_tokens WHERE token_hash=$1`, tokenHash)
	return err
}

func (q *Queries) DeleteWorkerTokensByAnalysis(ctx context.Context, analysisID string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM worker_tokens WHERE analysis_id=$1`, analysisID)
	return err
}

func (q *Queries) TouchWorkerToken(ctx context.Context, tokenHash string) error {
	_, err := q.pool.Exec(ctx, `UPDATE worker_tokens SET last_used_at=NOW() WHERE token_hash=$1`, tokenHash)
	return err
}

func (q *Queries) DeleteStaleWorkerTokens(ctx context.Context, maxAge time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx, `DELETE FROM worker_tokens WHERE last_used_at < NOW() - $1::interval`,
		maxAge.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (q *Queries) LoadAllWorkerTokens(ctx context.Context) (sessions map[string][]byte, proxyTokens map[string]string, err error) {
	sessions = make(map[string][]byte)
	proxyTokens = make(map[string]string)

	rows, err := q.pool.Query(ctx, `SELECT token_hash, token_type, analysis_id, session_data FROM worker_tokens`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var hash, ttype, aID string
		var data []byte
		if err := rows.Scan(&hash, &ttype, &aID, &data); err != nil {
			return nil, nil, err
		}
		switch ttype {
		case "session":
			sessions[hash] = data
		case "proxy":
			proxyTokens[hash] = aID
		}
	}
	return sessions, proxyTokens, rows.Err()
}

// --- Project Provider Keys ---

func (q *Queries) CreateProjectProviderKey(ctx context.Context, k *models.ProjectProviderKey) error {
	return q.pool.QueryRow(ctx,
		`INSERT INTO project_provider_keys (project_id, provider, label, key_hint, encrypted_key, encrypted_dek, dek_nonce, created_by, endpoint_url, api_schema)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, created_at`,
		k.ProjectID, k.Provider, k.Label, k.KeyHint,
		k.EncryptedKey, k.EncryptedDEK, k.DEKNonce, k.CreatedBy, k.EndpointURL, k.APISchema,
	).Scan(&k.ID, &k.CreatedAt)
}

func (q *Queries) ListProjectProviderKeys(ctx context.Context, projectID string) ([]models.ProjectProviderKey, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, project_id, provider, label, key_hint, is_active, created_by, created_at, revoked_at, endpoint_url, api_schema
		 FROM project_provider_keys
		 WHERE project_id = $1
		 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []models.ProjectProviderKey
	for rows.Next() {
		var k models.ProjectProviderKey
		if err := rows.Scan(&k.ID, &k.ProjectID, &k.Provider, &k.Label, &k.KeyHint,
			&k.IsActive, &k.CreatedBy, &k.CreatedAt, &k.RevokedAt, &k.EndpointURL, &k.APISchema); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (q *Queries) GetProjectProviderKey(ctx context.Context, id string) (*models.ProjectProviderKey, error) {
	var k models.ProjectProviderKey
	err := q.pool.QueryRow(ctx,
		`SELECT id, project_id, provider, label, key_hint, encrypted_key, encrypted_dek, dek_nonce,
		        is_active, created_by, created_at, revoked_at, endpoint_url, api_schema
		 FROM project_provider_keys WHERE id = $1`, id,
	).Scan(&k.ID, &k.ProjectID, &k.Provider, &k.Label, &k.KeyHint,
		&k.EncryptedKey, &k.EncryptedDEK, &k.DEKNonce,
		&k.IsActive, &k.CreatedBy, &k.CreatedAt, &k.RevokedAt, &k.EndpointURL, &k.APISchema)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (q *Queries) RevokeProjectProviderKey(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE project_provider_keys SET is_active = false, revoked_at = NOW() WHERE id = $1`, id)
	return err
}

func (q *Queries) DeleteProjectProviderKey(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx,
		`DELETE FROM project_provider_keys WHERE id = $1`, id)
	return err
}

// GetActiveProviderKey returns the newest active key for a project+provider.
func (q *Queries) GetActiveProviderKey(ctx context.Context, projectID, provider string) (*models.ProjectProviderKey, error) {
	var k models.ProjectProviderKey
	err := q.pool.QueryRow(ctx,
		`SELECT id, project_id, provider, label, key_hint, encrypted_key, encrypted_dek, dek_nonce,
		        is_active, created_by, created_at, revoked_at, endpoint_url, api_schema
		 FROM project_provider_keys
		 WHERE project_id = $1 AND provider = $2 AND is_active AND revoked_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`, projectID, provider,
	).Scan(&k.ID, &k.ProjectID, &k.Provider, &k.Label, &k.KeyHint,
		&k.EncryptedKey, &k.EncryptedDEK, &k.DEKNonce,
		&k.IsActive, &k.CreatedBy, &k.CreatedAt, &k.RevokedAt, &k.EndpointURL, &k.APISchema)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// --- Global LLM Providers ---

func (q *Queries) CreateLLMProvider(ctx context.Context, p *models.LLMProvider) error {
	return q.pool.QueryRow(ctx,
		`INSERT INTO llm_providers (label, api_schema, base_url, default_model, encrypted_key, encrypted_dek, dek_nonce, key_hint, enabled, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, created_at, updated_at`,
		p.Label, p.APISchema, p.BaseURL, p.DefaultModel,
		p.EncryptedKey, p.EncryptedDEK, p.DEKNonce,
		p.KeyHint, p.Enabled, p.CreatedBy,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (q *Queries) ListLLMProviders(ctx context.Context) ([]models.LLMProvider, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, label, api_schema, base_url, default_model, key_hint, enabled, created_by, created_at, updated_at
		 FROM llm_providers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []models.LLMProvider
	for rows.Next() {
		var p models.LLMProvider
		if err := rows.Scan(&p.ID, &p.Label, &p.APISchema, &p.BaseURL, &p.DefaultModel, &p.KeyHint,
			&p.Enabled, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

func (q *Queries) ListEnabledLLMProviders(ctx context.Context) ([]models.LLMProvider, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, label, api_schema, base_url, default_model, key_hint, enabled, created_by, created_at, updated_at
		 FROM llm_providers WHERE enabled ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var providers []models.LLMProvider
	for rows.Next() {
		var p models.LLMProvider
		if err := rows.Scan(&p.ID, &p.Label, &p.APISchema, &p.BaseURL, &p.DefaultModel, &p.KeyHint,
			&p.Enabled, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

func (q *Queries) GetLLMProvider(ctx context.Context, id string) (*models.LLMProvider, error) {
	var p models.LLMProvider
	err := q.pool.QueryRow(ctx,
		`SELECT id, label, api_schema, base_url, default_model, key_hint, encrypted_key, encrypted_dek, dek_nonce,
		        enabled, created_by, created_at, updated_at
		 FROM llm_providers WHERE id = $1`, id,
	).Scan(&p.ID, &p.Label, &p.APISchema, &p.BaseURL, &p.DefaultModel, &p.KeyHint,
		&p.EncryptedKey, &p.EncryptedDEK, &p.DEKNonce,
		&p.Enabled, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (q *Queries) UpdateLLMProvider(ctx context.Context, id, label, apiSchema, baseURL, defaultModel string, enabled bool) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE llm_providers SET label=$2, api_schema=$3, base_url=$4, default_model=$5, enabled=$6, updated_at=NOW()
		 WHERE id=$1`, id, label, apiSchema, baseURL, defaultModel, enabled)
	return err
}

func (q *Queries) UpdateLLMProviderKey(ctx context.Context, id string, encryptedKey, encryptedDEK, dekNonce []byte, keyHint string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE llm_providers SET encrypted_key=$2, encrypted_dek=$3, dek_nonce=$4, key_hint=$5, updated_at=NOW()
		 WHERE id=$1`, id, encryptedKey, encryptedDEK, dekNonce, keyHint)
	return err
}

func (q *Queries) DeleteLLMProvider(ctx context.Context, id string) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM llm_providers WHERE id = $1`, id)
	return err
}

// --- Project Allowed Providers ---

func (q *Queries) ListProjectAllowedProviders(ctx context.Context, projectID string) ([]models.ProjectAllowedProvider, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, project_id, provider_id, provider_source, created_at, COALESCE(created_by::text, '')
		 FROM project_allowed_providers WHERE project_id = $1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []models.ProjectAllowedProvider
	for rows.Next() {
		var p models.ProjectAllowedProvider
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.ProviderID, &p.ProviderSource, &p.CreatedAt, &p.CreatedBy); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (q *Queries) AddProjectAllowedProvider(ctx context.Context, projectID, providerID, providerSource, createdBy string) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO project_allowed_providers (project_id, provider_id, provider_source, created_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (project_id, provider_id, provider_source) DO NOTHING`,
		projectID, providerID, providerSource, createdBy)
	return err
}

func (q *Queries) RemoveProjectAllowedProvider(ctx context.Context, projectID, providerID, providerSource string) error {
	_, err := q.pool.Exec(ctx,
		`DELETE FROM project_allowed_providers
		 WHERE project_id = $1 AND provider_id = $2 AND provider_source = $3`,
		projectID, providerID, providerSource)
	return err
}

func (q *Queries) IsProviderAllowedForProject(ctx context.Context, projectID, providerID, providerSource string) (bool, error) {
	var exists bool
	err := q.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM project_allowed_providers
		 WHERE project_id = $1 AND provider_id = $2 AND provider_source = $3)`,
		projectID, providerID, providerSource).Scan(&exists)
	return exists, err
}
