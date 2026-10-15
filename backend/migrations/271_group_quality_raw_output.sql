-- Preserve the bounded raw generator response for failed/non-HTML quality checks.
ALTER TABLE group_quality_check_latest
    ADD COLUMN IF NOT EXISTS raw_output TEXT CHECK (raw_output IS NULL OR octet_length(raw_output) <= 1048576);

ALTER TABLE group_quality_check_history
    ADD COLUMN IF NOT EXISTS raw_output TEXT CHECK (raw_output IS NULL OR octet_length(raw_output) <= 1048576);
