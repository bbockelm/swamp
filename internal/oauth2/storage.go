package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/handler/pkce"
)

// Storage implements all fosite storage interfaces using PostgreSQL via pgx.
//
// Implemented interfaces:
//   - fosite.Storage (ClientManager)
//   - oauth2.CoreStorage (AuthorizeCode, AccessToken, RefreshToken)
//   - oauth2.TokenRevocationStorage
//   - openid.OpenIDConnectRequestStorage
//   - pkce.PKCERequestStorage
type Storage struct {
	pool *pgxpool.Pool
}

// Compile-time interface checks.
var (
	_ fosite.Storage                     = (*Storage)(nil)
	_ fosite.ClientManager               = (*Storage)(nil)
	_ openid.OpenIDConnectRequestStorage = (*Storage)(nil)
	_ pkce.PKCERequestStorage            = (*Storage)(nil)
)

// NewStorage creates a new PostgreSQL-backed fosite storage.
func NewStorage(pool *pgxpool.Pool) *Storage {
	return &Storage{pool: pool}
}

// ============================================================
// Client Manager
// ============================================================

type pgClient struct {
	ID            string   `json:"id"`
	Secret        string   `json:"client_secret"`
	RedirectURIs  []string `json:"redirect_uris"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
	Scopes        []string `json:"scopes"`
	Public        bool     `json:"public"`
	ClientName    string   `json:"client_name"`
}

func (c *pgClient) GetID() string                      { return c.ID }
func (c *pgClient) GetHashedSecret() []byte            { return []byte(c.Secret) }
func (c *pgClient) GetRedirectURIs() []string          { return c.RedirectURIs }
func (c *pgClient) GetGrantTypes() fosite.Arguments    { return c.GrantTypes }
func (c *pgClient) GetResponseTypes() fosite.Arguments { return c.ResponseTypes }
func (c *pgClient) GetScopes() fosite.Arguments        { return c.Scopes }
func (c *pgClient) IsPublic() bool                     { return c.Public }
func (c *pgClient) GetAudience() fosite.Arguments      { return nil }

func (s *Storage) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	var (
		c            pgClient
		redirectJSON []byte
		grantJSON    []byte
		responseJSON []byte
		scopeJSON    []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, client_secret, redirect_uris, grant_types, response_types, scopes, public, client_name
		FROM oauth2_clients WHERE id = $1`, id).Scan(
		&c.ID, &c.Secret, &redirectJSON, &grantJSON, &responseJSON, &scopeJSON, &c.Public, &c.ClientName,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fosite.ErrNotFound
		}
		return nil, fmt.Errorf("get oauth2 client: %w", err)
	}
	_ = json.Unmarshal(redirectJSON, &c.RedirectURIs)
	_ = json.Unmarshal(grantJSON, &c.GrantTypes)
	_ = json.Unmarshal(responseJSON, &c.ResponseTypes)
	_ = json.Unmarshal(scopeJSON, &c.Scopes)
	return &c, nil
}

func (s *Storage) ClientAssertionJWTValid(_ context.Context, _ string) error {
	return nil // Not using JWT client assertions
}

func (s *Storage) SetClientAssertionJWT(_ context.Context, _ string, _ time.Time) error {
	return nil // Not using JWT client assertions
}

// CreateClient stores a new OAuth2 client.
func (s *Storage) CreateClient(ctx context.Context, id, hashedSecret string, redirectURIs, grantTypes, responseTypes, scopes []string, public bool, name string) error {
	return s.CreateClientFull(ctx, id, hashedSecret, redirectURIs, grantTypes, responseTypes, scopes, public, name, false, "")
}

// CreateDynamicClient stores a dynamically registered OAuth2 client with IP tracking.
func (s *Storage) CreateDynamicClient(ctx context.Context, id, hashedSecret string, redirectURIs, grantTypes, responseTypes, scopes []string, public bool, name, registrationIP string) error {
	return s.CreateClientFull(ctx, id, hashedSecret, redirectURIs, grantTypes, responseTypes, scopes, public, name, true, registrationIP)
}

// CreateClientFull stores an OAuth2 client with all fields.
func (s *Storage) CreateClientFull(ctx context.Context, id, hashedSecret string, redirectURIs, grantTypes, responseTypes, scopes []string, public bool, name string, dynamicallyRegistered bool, registrationIP string) error {
	redirectJSON, _ := json.Marshal(redirectURIs)
	grantJSON, _ := json.Marshal(grantTypes)
	responseJSON, _ := json.Marshal(responseTypes)
	scopeJSON, _ := json.Marshal(scopes)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth2_clients (id, client_secret, redirect_uris, grant_types, response_types, scopes, public, client_name, dynamically_registered, registration_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, hashedSecret, redirectJSON, grantJSON, responseJSON, scopeJSON, public, name, dynamicallyRegistered, registrationIP)
	return err
}

// DeleteClient removes an OAuth2 client and cascades to all its tokens.
func (s *Storage) DeleteClient(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM oauth2_clients WHERE id = $1`, id)
	return err
}

// TouchClientLastUsed updates the last_used_at timestamp for a client.
// Called when a token is issued for this client.
func (s *Storage) TouchClientLastUsed(ctx context.Context, clientID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE oauth2_clients SET last_used_at = now() WHERE id = $1`, clientID)
	return err
}

// DeleteUnusedDynamicClients removes dynamically registered clients that
// were created more than maxAge ago and have never been used to authenticate.
func (s *Storage) DeleteUnusedDynamicClients(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM oauth2_clients WHERE dynamically_registered = true AND last_used_at IS NULL AND created_at < $1`,
		cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ============================================================
// Request serialization helpers
// ============================================================

type serializedRequest struct {
	RequestID       string              `json:"request_id"`
	RequestedAt     time.Time           `json:"requested_at"`
	ClientID        string              `json:"client_id"`
	Scopes          []string            `json:"scopes"`
	GrantedScopes   []string            `json:"granted_scopes"`
	GrantedAudience []string            `json:"granted_audience"`
	Form            map[string][]string `json:"form"`
	Session         json.RawMessage     `json:"session"`
	Subject         string              `json:"subject"`
}

func serializeRequest(req fosite.Requester) (*serializedRequest, error) {
	sessionData, err := json.Marshal(req.GetSession())
	if err != nil {
		return nil, fmt.Errorf("marshal session: %w", err)
	}

	form := make(map[string][]string)
	if req.GetRequestForm() != nil {
		for k, v := range req.GetRequestForm() {
			form[k] = v
		}
	}

	return &serializedRequest{
		RequestID:       req.GetID(),
		RequestedAt:     req.GetRequestedAt(),
		ClientID:        req.GetClient().GetID(),
		Scopes:          []string(req.GetRequestedScopes()),
		GrantedScopes:   []string(req.GetGrantedScopes()),
		GrantedAudience: []string(req.GetGrantedAudience()),
		Form:            form,
		Session:         sessionData,
		Subject:         req.GetSession().GetSubject(),
	}, nil
}

func (s *Storage) hydrateRequest(ctx context.Context, sr *serializedRequest, session fosite.Session) (*fosite.Request, error) {
	client, err := s.GetClient(ctx, sr.ClientID)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(sr.Session, session); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}

	form := url.Values(sr.Form)

	return &fosite.Request{
		ID:                sr.RequestID,
		RequestedAt:       sr.RequestedAt,
		Client:            client,
		RequestedScope:    sr.Scopes,
		GrantedScope:      sr.GrantedScopes,
		RequestedAudience: sr.GrantedAudience,
		GrantedAudience:   sr.GrantedAudience,
		Form:              form,
		Session:           session,
	}, nil
}

// ============================================================
// Generic token session CRUD (shared across token tables)
// ============================================================

func (s *Storage) createTokenSession(ctx context.Context, table, signature string, req fosite.Requester) error {
	sr, err := serializeRequest(req)
	if err != nil {
		return err
	}
	formJSON, _ := json.Marshal(sr.Form)
	scopeJSON, _ := json.Marshal(sr.Scopes)
	grantedScopeJSON, _ := json.Marshal(sr.GrantedScopes)
	grantedAudienceJSON, _ := json.Marshal(sr.GrantedAudience)

	_, err = s.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (signature, request_id, requested_at, client_id, scopes, granted_scopes, granted_audience, form_data, session_data, subject, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true)`, table),
		signature, sr.RequestID, sr.RequestedAt, sr.ClientID,
		scopeJSON, grantedScopeJSON, grantedAudienceJSON,
		formJSON, sr.Session, sr.Subject,
	)
	return err
}

func (s *Storage) getTokenSession(ctx context.Context, table, signature string, session fosite.Session) (fosite.Requester, error) {
	var (
		sr           serializedRequest
		scopeJSON    []byte
		grantedJSON  []byte
		audienceJSON []byte
		formJSON     []byte
		active       bool
	)
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT request_id, requested_at, client_id, scopes, granted_scopes, granted_audience, form_data, session_data, subject, active
		FROM %s WHERE signature = $1`, table), signature).Scan(
		&sr.RequestID, &sr.RequestedAt, &sr.ClientID,
		&scopeJSON, &grantedJSON, &audienceJSON,
		&formJSON, &sr.Session, &sr.Subject, &active,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fosite.ErrNotFound
		}
		return nil, fmt.Errorf("get session from %s: %w", table, err)
	}

	_ = json.Unmarshal(scopeJSON, &sr.Scopes)
	_ = json.Unmarshal(grantedJSON, &sr.GrantedScopes)
	_ = json.Unmarshal(audienceJSON, &sr.GrantedAudience)

	var form map[string][]string
	_ = json.Unmarshal(formJSON, &form)
	sr.Form = form

	req, err := s.hydrateRequest(ctx, &sr, session)
	if err != nil {
		return nil, err
	}

	if !active {
		return req, fosite.ErrInvalidatedAuthorizeCode
	}

	return req, nil
}

func (s *Storage) deleteTokenSession(ctx context.Context, table, signature string) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE signature = $1`, table), signature)
	return err
}

func (s *Storage) revokeTokenByRequestID(ctx context.Context, table, requestID string) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET active = false WHERE request_id = $1`, table), requestID)
	return err
}

// ============================================================
// Authorization Code Storage
// ============================================================

func (s *Storage) CreateAuthorizeCodeSession(ctx context.Context, code string, req fosite.Requester) error {
	return s.createTokenSession(ctx, "oauth2_authorization_codes", code, req)
}

func (s *Storage) GetAuthorizeCodeSession(ctx context.Context, code string, session fosite.Session) (fosite.Requester, error) {
	return s.getTokenSession(ctx, "oauth2_authorization_codes", code, session)
}

func (s *Storage) InvalidateAuthorizeCodeSession(ctx context.Context, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE oauth2_authorization_codes SET active = false WHERE signature = $1`, code)
	return err
}

// ============================================================
// Access Token Storage
// ============================================================

func (s *Storage) CreateAccessTokenSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.createTokenSession(ctx, "oauth2_access_tokens", signature, req)
}

func (s *Storage) GetAccessTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getTokenSession(ctx, "oauth2_access_tokens", signature, session)
}

func (s *Storage) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	return s.deleteTokenSession(ctx, "oauth2_access_tokens", signature)
}

// ============================================================
// Refresh Token Storage
// ============================================================

func (s *Storage) CreateRefreshTokenSession(ctx context.Context, signature, accessSignature string, req fosite.Requester) error {
	return s.createTokenSession(ctx, "oauth2_refresh_tokens", signature, req)
}

func (s *Storage) GetRefreshTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getTokenSession(ctx, "oauth2_refresh_tokens", signature, session)
}

func (s *Storage) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	return s.deleteTokenSession(ctx, "oauth2_refresh_tokens", signature)
}

func (s *Storage) RotateRefreshToken(ctx context.Context, requestID string, newSignature string) error {
	// Mark old refresh tokens for this request as inactive.
	_, err := s.pool.Exec(ctx,
		`UPDATE oauth2_refresh_tokens SET active = false WHERE request_id = $1 AND signature != $2`,
		requestID, newSignature)
	return err
}

// ============================================================
// Token Revocation (RFC 7009)
// ============================================================

func (s *Storage) RevokeRefreshToken(ctx context.Context, requestID string) error {
	return s.revokeTokenByRequestID(ctx, "oauth2_refresh_tokens", requestID)
}

func (s *Storage) RevokeAccessToken(ctx context.Context, requestID string) error {
	return s.revokeTokenByRequestID(ctx, "oauth2_access_tokens", requestID)
}

// RevokeRefreshTokenMaybeGracePeriod supports optional grace periods for refresh token rotation.
func (s *Storage) RevokeRefreshTokenMaybeGracePeriod(ctx context.Context, requestID string, _ string) error {
	return s.RevokeRefreshToken(ctx, requestID)
}

// ============================================================
// PKCE Storage
// ============================================================

func (s *Storage) CreatePKCERequestSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.createTokenSession(ctx, "oauth2_pkce_requests", signature, req)
}

func (s *Storage) GetPKCERequestSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getTokenSession(ctx, "oauth2_pkce_requests", signature, session)
}

func (s *Storage) DeletePKCERequestSession(ctx context.Context, signature string) error {
	return s.deleteTokenSession(ctx, "oauth2_pkce_requests", signature)
}

// ============================================================
// OpenID Connect Session Storage
// ============================================================

func (s *Storage) CreateOpenIDConnectSession(ctx context.Context, authorizeCode string, req fosite.Requester) error {
	return s.createTokenSession(ctx, "oauth2_oidc_sessions", authorizeCode, req)
}

func (s *Storage) GetOpenIDConnectSession(ctx context.Context, authorizeCode string, req fosite.Requester) (fosite.Requester, error) {
	return s.getTokenSession(ctx, "oauth2_oidc_sessions", authorizeCode, req.GetSession())
}

func (s *Storage) DeleteOpenIDConnectSession(ctx context.Context, authorizeCode string) error {
	return s.deleteTokenSession(ctx, "oauth2_oidc_sessions", authorizeCode)
}

// ============================================================
// Cleanup
// ============================================================

// CleanupExpiredTokens removes expired tokens from all tables.
func (s *Storage) CleanupExpiredTokens(ctx context.Context) error {
	tables := []string{
		"oauth2_access_tokens",
		"oauth2_refresh_tokens",
		"oauth2_authorization_codes",
		"oauth2_pkce_requests",
		"oauth2_oidc_sessions",
	}
	for _, t := range tables {
		if _, err := s.pool.Exec(ctx, fmt.Sprintf(
			`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at < now()`, t),
		); err != nil {
			return fmt.Errorf("cleanup %s: %w", t, err)
		}
	}
	return nil
}

// ListClients returns all registered OAuth2 clients.
func (s *Storage) ListClients(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, client_name, redirect_uris, grant_types, scopes, public, created_at
		FROM oauth2_clients ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []map[string]any
	for rows.Next() {
		var (
			id, name     string
			redirectJSON []byte
			grantJSON    []byte
			scopeJSON    []byte
			public       bool
			createdAt    time.Time
		)
		if err := rows.Scan(&id, &name, &redirectJSON, &grantJSON, &scopeJSON, &public, &createdAt); err != nil {
			return nil, err
		}
		var redirects, grants, scopes []string
		_ = json.Unmarshal(redirectJSON, &redirects)
		_ = json.Unmarshal(grantJSON, &grants)
		_ = json.Unmarshal(scopeJSON, &scopes)

		clients = append(clients, map[string]any{
			"id":            id,
			"client_name":   name,
			"redirect_uris": redirects,
			"grant_types":   grants,
			"scopes":        strings.Join(scopes, " "),
			"public":        public,
			"created_at":    createdAt,
		})
	}
	return clients, nil
}
