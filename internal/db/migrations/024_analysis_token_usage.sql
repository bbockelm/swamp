-- +goose Up

-- Per-model token usage for each analysis run.
CREATE TABLE analysis_token_usage (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    analysis_id   UUID NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    model         TEXT NOT NULL DEFAULT '',
    input_tokens  BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens  BIGINT NOT NULL DEFAULT 0,
    cache_write_tokens BIGINT NOT NULL DEFAULT 0,
    cost_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (analysis_id, model)
);

CREATE INDEX idx_token_usage_analysis ON analysis_token_usage (analysis_id);

-- +goose Down
DROP TABLE IF EXISTS analysis_token_usage;
