-- +goose Up
-- Add envelope encryption columns to oauth2_signing_keys so the RSA private
-- key can be encrypted at rest using the instance's KEK, matching the pattern
-- used for project_provider_keys.
ALTER TABLE oauth2_signing_keys
    ADD COLUMN encrypted_private_key BYTEA,
    ADD COLUMN encrypted_dek BYTEA,
    ADD COLUMN dek_nonce BYTEA;

-- +goose Down
ALTER TABLE oauth2_signing_keys
    DROP COLUMN IF EXISTS encrypted_private_key,
    DROP COLUMN IF EXISTS encrypted_dek,
    DROP COLUMN IF EXISTS dek_nonce;
