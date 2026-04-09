-- +goose Up

ALTER TABLE llm_providers ADD COLUMN default_model TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE llm_providers DROP COLUMN IF EXISTS default_model;
