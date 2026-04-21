-- +goose Up

ALTER TABLE analysis_token_usage
    ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT '';

-- Populate provider for historical rows from analysis configuration.
UPDATE analysis_token_usage u
SET provider = COALESCE(NULLIF(a.agent_config->>'provider_label', ''), a.agent_config->>'llm_provider_id', '')
FROM analyses a
WHERE a.id = u.analysis_id
  AND u.provider = '';

-- For historical OpenCode rows, use the configured analysis model.
UPDATE analysis_token_usage u
SET model = a.agent_model
FROM analyses a
WHERE a.id = u.analysis_id
  AND LOWER(u.model) = 'opencode'
  AND COALESCE(NULLIF(a.agent_model, ''), '') <> '';

ALTER TABLE analysis_token_usage
    DROP CONSTRAINT IF EXISTS analysis_token_usage_analysis_id_model_key;

ALTER TABLE analysis_token_usage
    ADD CONSTRAINT analysis_token_usage_analysis_id_provider_model_key UNIQUE (analysis_id, provider, model);

-- +goose Down

ALTER TABLE analysis_token_usage
    DROP CONSTRAINT IF EXISTS analysis_token_usage_analysis_id_provider_model_key;

ALTER TABLE analysis_token_usage
    ADD CONSTRAINT analysis_token_usage_analysis_id_model_key UNIQUE (analysis_id, model);

ALTER TABLE analysis_token_usage
    DROP COLUMN IF EXISTS provider;
