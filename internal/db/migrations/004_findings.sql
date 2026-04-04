-- +goose Up
-- Individual SARIF findings extracted from analysis results, plus user annotations.

CREATE TABLE findings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    analysis_id     UUID NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    result_id       UUID NOT NULL REFERENCES analysis_results(id) ON DELETE CASCADE,
    rule_id         TEXT NOT NULL DEFAULT '',
    level           TEXT NOT NULL DEFAULT 'note',          -- error, warning, note
    message         TEXT NOT NULL DEFAULT '',
    file_path       TEXT NOT NULL DEFAULT '',               -- artifact URI
    start_line      INT NOT NULL DEFAULT 0,
    end_line        INT NOT NULL DEFAULT 0,
    snippet         TEXT NOT NULL DEFAULT '',               -- source code snippet if available
    fingerprint     TEXT NOT NULL DEFAULT '',               -- stable identity across runs
    raw_json        JSONB NOT NULL DEFAULT '{}',            -- full SARIF result object
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_findings_project   ON findings(project_id);
CREATE INDEX idx_findings_analysis  ON findings(analysis_id);
CREATE INDEX idx_findings_result    ON findings(result_id);
CREATE INDEX idx_findings_rule      ON findings(rule_id);
CREATE INDEX idx_findings_level     ON findings(level);
CREATE INDEX idx_findings_fingerprint ON findings(fingerprint);

CREATE TABLE finding_annotations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    finding_id  UUID NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'open',   -- open, false_positive, not_relevant, confirmed, wont_fix, mitigated
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_annotations_finding ON finding_annotations(finding_id);
CREATE INDEX idx_annotations_user    ON finding_annotations(user_id);
CREATE UNIQUE INDEX idx_annotations_finding_user ON finding_annotations(finding_id, user_id);

-- +goose Down
DROP TABLE IF EXISTS finding_annotations;
DROP TABLE IF EXISTS findings;
