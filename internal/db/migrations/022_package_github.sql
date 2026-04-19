-- +goose Up
-- Move GitHub repository info to the package level.
-- Each package can have its own GitHub owner/repo and SARIF upload settings.
-- The project_github_config table is retained for webhook settings and
-- installation linkage (webhooks arrive at the repo level, but one project
-- may have multiple packages pointing at the same repo).

-- Use IF NOT EXISTS so this migration is safe to re-run after a partial failure.
ALTER TABLE software_packages
    ADD COLUMN IF NOT EXISTS github_owner         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS github_repo          TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS installation_id      BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sarif_upload_enabled BOOLEAN NOT NULL DEFAULT false;

-- Back-fill from the existing project_github_config when a package's
-- git_url matches the project's configured GitHub repo.
UPDATE software_packages sp
SET github_owner         = pgc.github_owner,
    github_repo          = pgc.github_repo,
    installation_id      = pgc.installation_id,
    sarif_upload_enabled = pgc.sarif_upload_enabled
FROM project_github_config pgc
WHERE sp.project_id = pgc.project_id
  AND pgc.github_owner != ''
  AND pgc.github_repo  != '';

-- Also try to parse owner/repo from git_url for packages that weren't
-- back-filled (different repo than the project-level one).
-- Handles: https://github.com/OWNER/REPO.git and https://github.com/OWNER/REPO
UPDATE software_packages
SET github_owner = split_part(
        regexp_replace(git_url, '^https?://github\.com/([^/]+)/([^/.]+?)(?:\.git)?/?$', '\1'),
        '/', 1),
    github_repo = split_part(
        regexp_replace(git_url, '^https?://github\.com/([^/]+)/([^/.]+?)(?:\.git)?/?$', '\2'),
        '/', 1)
WHERE github_owner = ''
  AND git_url ~ '^https?://github\.com/[^/]+/[^/]+';

-- Index for webhook matching: find packages by their GitHub repo.
CREATE INDEX IF NOT EXISTS idx_packages_github_repo
    ON software_packages (github_owner, github_repo)
    WHERE github_owner != '';

-- Add per-package SARIF upload URL to analysis results so we can track
-- which packages had their SARIF uploaded to GitHub.
ALTER TABLE analysis_results
    ADD COLUMN IF NOT EXISTS package_id       UUID REFERENCES software_packages(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS sarif_upload_url TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE analysis_results DROP COLUMN IF EXISTS sarif_upload_url;
ALTER TABLE analysis_results DROP COLUMN IF EXISTS package_id;
DROP INDEX IF EXISTS idx_packages_github_repo;
ALTER TABLE software_packages DROP COLUMN IF EXISTS sarif_upload_enabled;
ALTER TABLE software_packages DROP COLUMN IF EXISTS installation_id;
ALTER TABLE software_packages DROP COLUMN IF EXISTS github_repo;
ALTER TABLE software_packages DROP COLUMN IF EXISTS github_owner;
