-- +goose Up
-- Add uses_global_key column to projects table.
-- By default, projects cannot use the global agent API key.
-- An admin must explicitly enable this for a project.
ALTER TABLE projects ADD COLUMN uses_global_key BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE projects DROP COLUMN uses_global_key;
