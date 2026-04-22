-- 029_migrate_package_installations_to_project_links.sql
-- +goose Up
-- Backfill deprecated package-level installation links into project-level links.
INSERT INTO project_github_installations (project_id, installation_id, enabled_by)
SELECT DISTINCT sp.project_id, sp.installation_id, NULL::uuid
FROM software_packages sp
WHERE sp.installation_id IS NOT NULL
  AND sp.installation_id <> 0
ON CONFLICT (project_id, installation_id) DO NOTHING;

-- Package-level links are deprecated; project-level links are now canonical.
UPDATE software_packages
SET installation_id = 0
WHERE installation_id <> 0;

-- +goose Down
-- Best-effort restore of package installation IDs from project-level links by owner.
UPDATE software_packages sp
SET installation_id = gi.installation_id
FROM github_app_installations gi
JOIN project_github_installations pgi
  ON pgi.installation_id = gi.installation_id
 AND pgi.project_id = sp.project_id
WHERE sp.installation_id = 0
  AND sp.github_owner <> ''
  AND lower(gi.account_login) = lower(sp.github_owner);
