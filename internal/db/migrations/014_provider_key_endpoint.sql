-- +goose Up
ALTER TABLE project_provider_keys ADD COLUMN endpoint_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE project_provider_keys DROP COLUMN IF EXISTS endpoint_url;
