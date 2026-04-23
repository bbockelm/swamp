-- +goose Up
ALTER TABLE software_packages
    ADD COLUMN IF NOT EXISTS github_sync_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS webhook_push_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS webhook_pr_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_packages_sync_repo
    ON software_packages (github_owner, github_repo, git_branch)
    WHERE github_sync_enabled = true AND github_owner <> '' AND github_repo <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_packages_sync_repo;
ALTER TABLE software_packages DROP COLUMN IF EXISTS webhook_pr_enabled;
ALTER TABLE software_packages DROP COLUMN IF EXISTS webhook_push_enabled;
ALTER TABLE software_packages DROP COLUMN IF EXISTS github_sync_enabled;
