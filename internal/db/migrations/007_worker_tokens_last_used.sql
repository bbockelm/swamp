-- +goose Up
-- Add last_used_at column if the table was created before it was added to 006.
ALTER TABLE worker_tokens ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_worker_tokens_last_used ON worker_tokens(last_used_at);

-- +goose Down
DROP INDEX IF EXISTS idx_worker_tokens_last_used;
ALTER TABLE worker_tokens DROP COLUMN IF EXISTS last_used_at;
