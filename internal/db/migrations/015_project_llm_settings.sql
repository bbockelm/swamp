-- +goose Up
-- Add per-project LLM configuration columns.
-- All columns are nullable; NULL means "inherit the global config value".
ALTER TABLE projects
    ADD COLUMN agent_provider TEXT,
    ADD COLUMN ext_llm_analysis_model TEXT,
    ADD COLUMN ext_llm_poc_model TEXT,
    ADD COLUMN ext_llm_fallback TEXT;

-- +goose Down
ALTER TABLE projects
    DROP COLUMN agent_provider,
    DROP COLUMN ext_llm_analysis_model,
    DROP COLUMN ext_llm_poc_model,
    DROP COLUMN ext_llm_fallback;
