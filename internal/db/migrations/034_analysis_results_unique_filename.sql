-- +goose Up
-- Remove duplicate (analysis_id, filename) rows before adding the UNIQUE constraint.
-- For each duplicate pair, keep the most recently created row because each duplicate
-- upload overwrote the same S3 key, so the newest DB row matches the current object.
DELETE FROM analysis_results
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY analysis_id, filename
                   ORDER BY created_at DESC
               ) AS rn
        FROM analysis_results
    ) ranked
    WHERE rn > 1
);

ALTER TABLE analysis_results
    ADD CONSTRAINT analysis_results_analysis_id_filename_key
    UNIQUE (analysis_id, filename);

-- +goose Down
ALTER TABLE analysis_results
    DROP CONSTRAINT IF EXISTS analysis_results_analysis_id_filename_key;
