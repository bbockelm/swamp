-- +goose Up
ALTER TABLE analyses ADD COLUMN custom_prompt TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE analyses DROP COLUMN custom_prompt;
