-- +goose Up
-- Add DCR-related columns to oauth2_clients if they were missed (migration 012 was updated after initial apply).
ALTER TABLE oauth2_clients ADD COLUMN IF NOT EXISTS dynamically_registered BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE oauth2_clients ADD COLUMN IF NOT EXISTS registration_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE oauth2_clients ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE oauth2_clients DROP COLUMN IF EXISTS last_used_at;
ALTER TABLE oauth2_clients DROP COLUMN IF EXISTS registration_ip;
ALTER TABLE oauth2_clients DROP COLUMN IF EXISTS dynamically_registered;
