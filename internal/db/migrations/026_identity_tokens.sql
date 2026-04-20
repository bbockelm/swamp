-- +goose Up
-- Add optional OAuth token fields to user_identities.
-- These are used for identity providers (like GitHub) where the user's
-- access token is needed for subsequent API calls.
ALTER TABLE user_identities ADD COLUMN access_token_enc  TEXT;
ALTER TABLE user_identities ADD COLUMN refresh_token_enc TEXT;
ALTER TABLE user_identities ADD COLUMN token_expires_at  TIMESTAMPTZ;
ALTER TABLE user_identities ADD COLUMN updated_at        TIMESTAMPTZ NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE user_identities DROP COLUMN IF EXISTS access_token_enc;
ALTER TABLE user_identities DROP COLUMN IF EXISTS refresh_token_enc;
ALTER TABLE user_identities DROP COLUMN IF EXISTS token_expires_at;
ALTER TABLE user_identities DROP COLUMN IF EXISTS updated_at;
