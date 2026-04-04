-- +goose Up
ALTER TABLE analyses ADD COLUMN encrypted_dek BYTEA;
ALTER TABLE analyses ADD COLUMN dek_nonce     BYTEA;

-- +goose Down
ALTER TABLE analyses DROP COLUMN IF EXISTS encrypted_dek;
ALTER TABLE analyses DROP COLUMN IF EXISTS dek_nonce;
