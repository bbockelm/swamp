-- +goose Up
ALTER TABLE analyses ADD COLUMN git_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN git_commit TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE findings DROP COLUMN git_commit;
ALTER TABLE analyses DROP COLUMN git_commit;
