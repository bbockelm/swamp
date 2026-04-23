-- +goose Up
ALTER TABLE findings
    ADD COLUMN IF NOT EXISTS github_alert_number BIGINT,
    ADD COLUMN IF NOT EXISTS github_alert_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS github_alert_state TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS github_alert_dismissed_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS github_alert_dismissed_comment TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS github_alert_fixed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS github_alert_last_sync_at TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS idx_findings_project_github_alert_number
    ON findings(project_id, github_alert_number)
    WHERE github_alert_number IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_findings_github_alert_state
    ON findings(github_alert_state);

-- +goose Down
DROP INDEX IF EXISTS idx_findings_github_alert_state;
DROP INDEX IF EXISTS idx_findings_project_github_alert_number;

ALTER TABLE findings
    DROP COLUMN IF EXISTS github_alert_last_sync_at,
    DROP COLUMN IF EXISTS github_alert_fixed_at,
    DROP COLUMN IF EXISTS github_alert_dismissed_comment,
    DROP COLUMN IF EXISTS github_alert_dismissed_reason,
    DROP COLUMN IF EXISTS github_alert_state,
    DROP COLUMN IF EXISTS github_alert_url,
    DROP COLUMN IF EXISTS github_alert_number;