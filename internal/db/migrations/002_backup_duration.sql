-- +goose Up
ALTER TABLE backups ADD COLUMN duration_secs INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE backups DROP COLUMN duration_secs;
