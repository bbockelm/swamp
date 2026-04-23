-- +goose Up
ALTER TABLE projects
    ADD COLUMN nrp_access_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN nrp_access_enabled_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN nrp_access_enabled_at TIMESTAMPTZ,
    ADD COLUMN nrp_execution_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN nrp_execution_enabled_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN nrp_execution_enabled_at TIMESTAMPTZ;

CREATE INDEX idx_projects_nrp_access_enabled_by ON projects(nrp_access_enabled_by) WHERE nrp_access_enabled_by IS NOT NULL;
CREATE INDEX idx_projects_nrp_execution_enabled_by ON projects(nrp_execution_enabled_by) WHERE nrp_execution_enabled_by IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_projects_nrp_execution_enabled_by;
DROP INDEX IF EXISTS idx_projects_nrp_access_enabled_by;

ALTER TABLE projects
    DROP COLUMN IF EXISTS nrp_execution_enabled_at,
    DROP COLUMN IF EXISTS nrp_execution_enabled_by,
    DROP COLUMN IF EXISTS nrp_execution_enabled,
    DROP COLUMN IF EXISTS nrp_access_enabled_at,
    DROP COLUMN IF EXISTS nrp_access_enabled_by,
    DROP COLUMN IF EXISTS nrp_access_enabled;