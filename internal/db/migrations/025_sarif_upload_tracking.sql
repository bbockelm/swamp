-- +goose Up
-- Track whether SARIF upload was attempted for each result, and capture error details.
ALTER TABLE analysis_results
    ADD COLUMN IF NOT EXISTS sarif_upload_attempted BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS sarif_upload_error TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE analysis_results DROP COLUMN IF EXISTS sarif_upload_error;
ALTER TABLE analysis_results DROP COLUMN IF EXISTS sarif_upload_attempted;
