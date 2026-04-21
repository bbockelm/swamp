-- 028_project_github_installations.sql
-- +goose Up
-- Track explicit project ↔ GitHub App installation associations.
-- Previously, installations were only linked per-package
-- (software_packages.installation_id). This table allows admins to
-- associate any installation with a project without needing their own
-- GitHub identity linked, and tracks the full M-N relationship.
CREATE TABLE project_github_installations (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    installation_id BIGINT      NOT NULL,
    enabled_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    enabled_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, installation_id)
);

CREATE INDEX idx_pgi_project_id      ON project_github_installations (project_id);
CREATE INDEX idx_pgi_installation_id ON project_github_installations (installation_id);

-- +goose Down
DROP TABLE IF EXISTS project_github_installations;
