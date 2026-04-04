-- +goose Up
-- Worker session and proxy tokens. Only SHA-256 hashes are stored;
-- cleartext tokens never reach the database.
CREATE TABLE worker_tokens (
    token_hash   TEXT        NOT NULL PRIMARY KEY,
    token_type   TEXT        NOT NULL CHECK (token_type IN ('session', 'proxy')),
    analysis_id  UUID        NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    session_data JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_worker_tokens_analysis ON worker_tokens(analysis_id);
CREATE INDEX idx_worker_tokens_last_used ON worker_tokens(last_used_at);

-- +goose Down
DROP TABLE IF EXISTS worker_tokens;
