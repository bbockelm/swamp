-- +goose Up
-- GitHub App integration: stores app configuration, installation mappings,
-- per-project GitHub settings, and webhook events.

-- Global GitHub App configuration (stored in app_config for simplicity,
-- but installations need their own table for the N:1 relationship).
CREATE TABLE github_app_installations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id BIGINT NOT NULL UNIQUE,
    account_login   TEXT NOT NULL DEFAULT '',       -- e.g. "octocat" or "my-org"
    account_type    TEXT NOT NULL DEFAULT 'Organization', -- "User" or "Organization"
    permissions     JSONB NOT NULL DEFAULT '{}',    -- granted permissions snapshot
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-project GitHub integration settings.
CREATE TABLE project_github_config (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id           UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    github_owner         TEXT NOT NULL DEFAULT '',  -- repo owner (user or org)
    github_repo          TEXT NOT NULL DEFAULT '',  -- repo name
    default_branch       TEXT NOT NULL DEFAULT 'main',
    installation_id      BIGINT NOT NULL DEFAULT 0, -- GitHub App installation ID
    sarif_upload_enabled BOOLEAN NOT NULL DEFAULT false,
    webhook_enabled      BOOLEAN NOT NULL DEFAULT false,
    webhook_events       TEXT[] NOT NULL DEFAULT '{}', -- e.g. '{push,pull_request}'
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(project_id)
);

-- Webhook delivery log for debugging and replay.
CREATE TABLE github_webhook_deliveries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id     TEXT NOT NULL DEFAULT '',       -- X-GitHub-Delivery header
    event_type      TEXT NOT NULL,                  -- X-GitHub-Event header
    action          TEXT NOT NULL DEFAULT '',       -- payload.action
    repo_full_name  TEXT NOT NULL DEFAULT '',       -- payload.repository.full_name
    ref             TEXT NOT NULL DEFAULT '',       -- payload.ref (for push events)
    sender_login    TEXT NOT NULL DEFAULT '',       -- payload.sender.login
    project_id      UUID REFERENCES projects(id) ON DELETE SET NULL,
    analysis_id     UUID REFERENCES analyses(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'received', -- received, processed, ignored, error
    status_detail   TEXT NOT NULL DEFAULT '',
    payload_json    JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_webhook_deliveries_project ON github_webhook_deliveries(project_id);
CREATE INDEX idx_webhook_deliveries_created ON github_webhook_deliveries(created_at);
CREATE INDEX idx_project_github_config_owner_repo ON project_github_config(github_owner, github_repo);

-- +goose Down
DROP TABLE IF EXISTS github_webhook_deliveries;
DROP TABLE IF EXISTS project_github_config;
DROP TABLE IF EXISTS github_app_installations;
