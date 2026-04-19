-- +goose Up

-- Track who installed a GitHub App installation.
ALTER TABLE github_app_installations
    ADD COLUMN IF NOT EXISTS installed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

-- Index for looking up installations by account login (case-insensitive).
CREATE INDEX IF NOT EXISTS idx_gh_installations_account_login
    ON github_app_installations (lower(account_login));

-- Index for looking up installations by the user who installed them.
CREATE INDEX IF NOT EXISTS idx_gh_installations_installed_by
    ON github_app_installations (installed_by_user_id)
    WHERE installed_by_user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_gh_installations_installed_by;
DROP INDEX IF EXISTS idx_gh_installations_account_login;
ALTER TABLE github_app_installations DROP COLUMN IF EXISTS installed_by_user_id;
