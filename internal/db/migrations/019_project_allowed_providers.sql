-- +goose Up

-- Track which global/env providers each project is allowed to use.
-- Projects only see providers that are BOTH globally enabled AND listed here.
-- Project-level provider keys (project_provider_keys) are always available.
CREATE TABLE project_allowed_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL,        -- global LLM provider UUID or env provider ID (e.g. "env-anthropic")
    provider_source TEXT NOT NULL,    -- "global" or "env"
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by UUID REFERENCES users(id),
    UNIQUE(project_id, provider_id, provider_source)
);

CREATE INDEX idx_project_allowed_providers_project ON project_allowed_providers(project_id);

-- +goose Down

DROP TABLE IF EXISTS project_allowed_providers;
