-- +goose Up
-- Analysis trigger metadata: branch, event type, and GitHub context.
ALTER TABLE analyses
    ADD COLUMN git_branch     TEXT NOT NULL DEFAULT '',
    ADD COLUMN trigger_event  TEXT NOT NULL DEFAULT 'manual',
    ADD COLUMN trigger_meta   JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN sarif_upload_url TEXT NOT NULL DEFAULT '';

-- Webhook-triggered analyses: allow configuring the LLM provider+model.
ALTER TABLE project_github_config
    ADD COLUMN webhook_agent_model TEXT NOT NULL DEFAULT '',
    ADD COLUMN webhook_provider_id UUID;

-- +goose Down
ALTER TABLE analyses
    DROP COLUMN IF EXISTS git_branch,
    DROP COLUMN IF EXISTS trigger_event,
    DROP COLUMN IF EXISTS trigger_meta,
    DROP COLUMN IF EXISTS sarif_upload_url;

ALTER TABLE project_github_config
    DROP COLUMN IF EXISTS webhook_agent_model,
    DROP COLUMN IF EXISTS webhook_provider_id;
