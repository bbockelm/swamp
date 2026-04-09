-- +goose Up

-- Global LLM providers managed by admins.
-- Each provider has an API schema (anthropic or openai), a base URL, and an encrypted API key.
CREATE TABLE llm_providers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    label         TEXT NOT NULL,
    api_schema    TEXT NOT NULL CHECK (api_schema IN ('anthropic', 'openai')),
    base_url      TEXT NOT NULL DEFAULT '',
    encrypted_key BYTEA,
    encrypted_dek BYTEA,
    dek_nonce     BYTEA,
    key_hint      TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_by    UUID NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Add api_schema to project_provider_keys for agent selection.
ALTER TABLE project_provider_keys
    ADD COLUMN api_schema TEXT NOT NULL DEFAULT 'anthropic'
        CHECK (api_schema IN ('anthropic', 'openai'));

-- Set openai schema for non-anthropic providers.
UPDATE project_provider_keys
    SET api_schema = 'openai'
    WHERE provider IN ('nrp', 'custom', 'external_llm');

-- +goose Down
ALTER TABLE project_provider_keys DROP COLUMN IF EXISTS api_schema;
DROP TABLE IF EXISTS llm_providers;
