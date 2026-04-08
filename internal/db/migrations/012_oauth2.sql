-- +goose Up

-- OAuth2 clients (both admin-registered and dynamically registered via RFC 7591)
CREATE TABLE oauth2_clients (
    id              TEXT PRIMARY KEY,
    client_secret   TEXT NOT NULL DEFAULT '',              -- bcrypt hash (empty for public clients)
    redirect_uris   JSONB NOT NULL DEFAULT '[]',
    grant_types     JSONB NOT NULL DEFAULT '["authorization_code"]',
    response_types  JSONB NOT NULL DEFAULT '["code"]',
    scopes          JSONB NOT NULL DEFAULT '[]',
    public          BOOLEAN NOT NULL DEFAULT false,
    client_name     TEXT NOT NULL DEFAULT '',
    dynamically_registered BOOLEAN NOT NULL DEFAULT false, -- true for RFC 7591 clients
    registration_ip TEXT NOT NULL DEFAULT '',               -- IP that registered the client
    last_used_at    TIMESTAMPTZ,                            -- updated on token issuance; NULL = never used
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OAuth2 authorization codes (short-lived, exchanged for tokens)
CREATE TABLE oauth2_authorization_codes (
    signature       TEXT PRIMARY KEY,
    request_id      TEXT NOT NULL,
    requested_at    TIMESTAMPTZ NOT NULL,
    client_id       TEXT NOT NULL REFERENCES oauth2_clients(id) ON DELETE CASCADE,
    scopes          JSONB NOT NULL DEFAULT '[]',
    granted_scopes  JSONB NOT NULL DEFAULT '[]',
    granted_audience JSONB NOT NULL DEFAULT '[]',
    form_data       JSONB NOT NULL DEFAULT '{}',
    session_data    JSONB NOT NULL DEFAULT '{}',
    subject         TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OAuth2 access tokens
CREATE TABLE oauth2_access_tokens (
    signature       TEXT PRIMARY KEY,
    request_id      TEXT NOT NULL,
    requested_at    TIMESTAMPTZ NOT NULL,
    client_id       TEXT NOT NULL REFERENCES oauth2_clients(id) ON DELETE CASCADE,
    scopes          JSONB NOT NULL DEFAULT '[]',
    granted_scopes  JSONB NOT NULL DEFAULT '[]',
    granted_audience JSONB NOT NULL DEFAULT '[]',
    form_data       JSONB NOT NULL DEFAULT '{}',
    session_data    JSONB NOT NULL DEFAULT '{}',
    subject         TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OAuth2 refresh tokens
CREATE TABLE oauth2_refresh_tokens (
    signature       TEXT PRIMARY KEY,
    request_id      TEXT NOT NULL,
    requested_at    TIMESTAMPTZ NOT NULL,
    client_id       TEXT NOT NULL REFERENCES oauth2_clients(id) ON DELETE CASCADE,
    scopes          JSONB NOT NULL DEFAULT '[]',
    granted_scopes  JSONB NOT NULL DEFAULT '[]',
    granted_audience JSONB NOT NULL DEFAULT '[]',
    form_data       JSONB NOT NULL DEFAULT '{}',
    session_data    JSONB NOT NULL DEFAULT '{}',
    subject         TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,
    first_used_at   TIMESTAMPTZ,  -- grace period tracking for refresh rotation
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- PKCE challenge storage (linked to authorization codes)
CREATE TABLE oauth2_pkce_requests (
    signature       TEXT PRIMARY KEY,
    request_id      TEXT NOT NULL,
    requested_at    TIMESTAMPTZ NOT NULL,
    client_id       TEXT NOT NULL REFERENCES oauth2_clients(id) ON DELETE CASCADE,
    scopes          JSONB NOT NULL DEFAULT '[]',
    granted_scopes  JSONB NOT NULL DEFAULT '[]',
    granted_audience JSONB NOT NULL DEFAULT '[]',
    form_data       JSONB NOT NULL DEFAULT '{}',
    session_data    JSONB NOT NULL DEFAULT '{}',
    subject         TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OpenID Connect sessions (for ID token issuance)
CREATE TABLE oauth2_oidc_sessions (
    signature       TEXT PRIMARY KEY,
    request_id      TEXT NOT NULL,
    requested_at    TIMESTAMPTZ NOT NULL,
    client_id       TEXT NOT NULL REFERENCES oauth2_clients(id) ON DELETE CASCADE,
    scopes          JSONB NOT NULL DEFAULT '[]',
    granted_scopes  JSONB NOT NULL DEFAULT '[]',
    granted_audience JSONB NOT NULL DEFAULT '[]',
    form_data       JSONB NOT NULL DEFAULT '{}',
    session_data    JSONB NOT NULL DEFAULT '{}',
    subject         TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OAuth2 signing key (persisted so tokens survive restarts)
CREATE TABLE oauth2_signing_keys (
    id              TEXT PRIMARY KEY DEFAULT 'default',
    kid             TEXT NOT NULL,
    algorithm       TEXT NOT NULL,        -- RS256
    private_key_pem TEXT NOT NULL,        -- PEM-encoded RSA private key (encrypted with instance key)
    public_key_pem  TEXT NOT NULL,        -- PEM-encoded RSA public key
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Indexes for token lookups and cleanup
CREATE INDEX idx_oauth2_access_tokens_request_id ON oauth2_access_tokens(request_id);
CREATE INDEX idx_oauth2_refresh_tokens_request_id ON oauth2_refresh_tokens(request_id);
CREATE INDEX idx_oauth2_authorization_codes_request_id ON oauth2_authorization_codes(request_id);
CREATE INDEX idx_oauth2_access_tokens_subject ON oauth2_access_tokens(subject);
CREATE INDEX idx_oauth2_refresh_tokens_subject ON oauth2_refresh_tokens(subject);

-- +goose Down
DROP TABLE IF EXISTS oauth2_oidc_sessions;
DROP TABLE IF EXISTS oauth2_pkce_requests;
DROP TABLE IF EXISTS oauth2_refresh_tokens;
DROP TABLE IF EXISTS oauth2_access_tokens;
DROP TABLE IF EXISTS oauth2_authorization_codes;
DROP TABLE IF EXISTS oauth2_signing_keys;
DROP TABLE IF EXISTS oauth2_clients;
