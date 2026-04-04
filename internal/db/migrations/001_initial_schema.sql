-- +goose Up
-- SWAMP: Software Assurance Marketplace — Initial Schema

-- ============================================================
-- Users & Authentication
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name TEXT NOT NULL DEFAULT '',
    email       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'active', -- active, suspended, deactivated
    last_login  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_identities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer       TEXT NOT NULL,    -- OIDC issuer URL (e.g. https://cilogon.org)
    subject      TEXT NOT NULL,    -- OIDC sub claim
    email        TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    idp_name     TEXT NOT NULL DEFAULT '', -- which IdP inside the issuer
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(issuer, subject)
);
CREATE INDEX idx_user_identities_user ON user_identities(user_id);

CREATE TABLE user_roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL, -- admin, project_creator, user
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, role)
);
CREATE INDEX idx_user_roles_user ON user_roles(user_id);

CREATE TABLE sessions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_token ON sessions(token_hash);
CREATE INDEX idx_sessions_user  ON sessions(user_id);

CREATE TABLE user_invites (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash BYTEA NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    email      TEXT NOT NULL DEFAULT '',
    used       BOOLEAN NOT NULL DEFAULT false,
    used_by    UUID REFERENCES users(id),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_user_invites_token ON user_invites(token_hash);

-- AUP (Acceptable Use Policy) agreement tracking
CREATE TABLE aup_agreements (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    aup_version TEXT NOT NULL,
    agreed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip_address  TEXT NOT NULL DEFAULT '',
    UNIQUE(user_id, aup_version)
);
CREATE INDEX idx_aup_user ON aup_agreements(user_id);

-- ============================================================
-- Groups & Membership
-- ============================================================

CREATE TABLE groups (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    owner_id       UUID NOT NULL REFERENCES users(id),
    admin_group_id UUID REFERENCES groups(id), -- nullable: group that can administer this group
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_groups_owner ON groups(owner_id);

CREATE TABLE group_members (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id  UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      TEXT NOT NULL DEFAULT 'member', -- member, admin
    added_by  UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(group_id, user_id)
);
CREATE INDEX idx_group_members_group ON group_members(group_id);
CREATE INDEX idx_group_members_user  ON group_members(user_id);

CREATE TABLE group_invites (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id            UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    token_hash          BYTEA NOT NULL,
    invited_by          UUID NOT NULL REFERENCES users(id),
    email               TEXT NOT NULL DEFAULT '',
    role                TEXT NOT NULL DEFAULT 'member', -- member, admin
    allows_registration BOOLEAN NOT NULL DEFAULT true,
    used                BOOLEAN NOT NULL DEFAULT false,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_group_invites_token ON group_invites(token_hash);
CREATE INDEX idx_group_invites_group ON group_invites(group_id);

-- ============================================================
-- Projects & Software Packages
-- ============================================================

CREATE TABLE projects (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    owner_id       UUID NOT NULL REFERENCES users(id),
    read_group_id  UUID REFERENCES groups(id),  -- view results
    write_group_id UUID REFERENCES groups(id),  -- manipulate packages + trigger runs
    admin_group_id UUID REFERENCES groups(id),  -- full project admin
    status         TEXT NOT NULL DEFAULT 'active', -- active, archived
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_projects_owner ON projects(owner_id);

CREATE TABLE software_packages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    git_url         TEXT NOT NULL,
    git_branch      TEXT NOT NULL DEFAULT '',
    git_commit      TEXT NOT NULL DEFAULT '',  -- pin to specific commit
    analysis_prompt TEXT NOT NULL DEFAULT '',  -- custom instructions for the AI agent
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_packages_project ON software_packages(project_id);

-- ============================================================
-- Analyses & Results
-- ============================================================

CREATE TABLE analyses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    triggered_by    UUID NOT NULL REFERENCES users(id),
    status          TEXT NOT NULL DEFAULT 'queued', -- queued, running, completed, failed, cancelled
    status_detail   TEXT NOT NULL DEFAULT '',
    agent_model     TEXT NOT NULL DEFAULT '',
    agent_config    JSONB NOT NULL DEFAULT '{}',
    environment     TEXT NOT NULL DEFAULT 'local', -- local, htcondor
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_analyses_project ON analyses(project_id);
CREATE INDEX idx_analyses_status  ON analyses(status);

CREATE TABLE analysis_packages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    analysis_id UUID NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    package_id  UUID NOT NULL REFERENCES software_packages(id) ON DELETE CASCADE,
    UNIQUE(analysis_id, package_id)
);
CREATE INDEX idx_analysis_packages_analysis ON analysis_packages(analysis_id);

CREATE TABLE analysis_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    analysis_id     UUID NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    package_id      UUID REFERENCES software_packages(id) ON DELETE SET NULL,
    result_type     TEXT NOT NULL, -- sarif, markdown_report, exploit_tarball, agent_log
    s3_key          TEXT NOT NULL,
    filename        TEXT NOT NULL,
    content_type    TEXT NOT NULL DEFAULT 'application/octet-stream',
    file_size       BIGINT NOT NULL DEFAULT 0,
    summary         TEXT NOT NULL DEFAULT '',
    finding_count   INT NOT NULL DEFAULT 0,
    severity_counts JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_results_analysis ON analysis_results(analysis_id);

-- ============================================================
-- API Keys
-- ============================================================

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL,       -- bcrypt hash
    key_prefix   TEXT NOT NULL,       -- first 8 chars for lookup
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ
);
CREATE INDEX idx_api_keys_prefix ON api_keys(key_prefix);
CREATE INDEX idx_api_keys_user   ON api_keys(user_id);

-- ============================================================
-- App Configuration (key-value store)
-- ============================================================

CREATE TABLE app_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Backups
-- ============================================================

CREATE TABLE backups (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filename      TEXT NOT NULL,
    s3_key        TEXT NOT NULL,
    s3_bucket     TEXT NOT NULL DEFAULT '',
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'running', -- running, completed, failed
    status_detail TEXT NOT NULL DEFAULT '',
    error_msg     TEXT NOT NULL DEFAULT '',
    initiated_by  TEXT NOT NULL DEFAULT 'manual', -- manual, scheduler
    encrypted     BOOLEAN NOT NULL DEFAULT true,
    checksum      TEXT NOT NULL DEFAULT '',        -- SHA-256 hex
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE object_hashes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    s3_key     TEXT NOT NULL UNIQUE,
    sha256     TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS object_hashes;
DROP TABLE IF EXISTS backups;
DROP TABLE IF EXISTS app_config;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS analysis_results;
DROP TABLE IF EXISTS analysis_packages;
DROP TABLE IF EXISTS analyses;
DROP TABLE IF EXISTS software_packages;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS group_invites;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS aup_agreements;
DROP TABLE IF EXISTS user_invites;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS user_identities;
DROP TABLE IF EXISTS users;
