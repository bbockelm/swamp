-- +goose Up

-- Provider API keys associated with projects.
-- Supports multiple providers and multiple keys per project.
CREATE TABLE project_provider_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider        TEXT NOT NULL DEFAULT 'anthropic',   -- e.g. 'anthropic', 'openai'
    label           TEXT NOT NULL DEFAULT '',             -- human-readable label
    key_hint        TEXT NOT NULL DEFAULT '',             -- last 4 chars for display
    encrypted_key   BYTEA NOT NULL,                      -- AES-256-GCM encrypted API key
    encrypted_dek   BYTEA NOT NULL,                      -- wrapped DEK (envelope encryption)
    dek_nonce       BYTEA NOT NULL,                      -- nonce used to wrap the DEK
    is_active       BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX idx_project_provider_keys_project ON project_provider_keys(project_id);
CREATE INDEX idx_project_provider_keys_active  ON project_provider_keys(project_id, provider) WHERE is_active AND revoked_at IS NULL;

-- +goose Down

DROP TABLE IF EXISTS project_provider_keys;
