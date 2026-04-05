-- +goose Up

-- Soft-delete users instead of removing them.
-- Add deleted_at column; a non-NULL value means the user is soft-deleted.
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE users DROP COLUMN deleted_at;
