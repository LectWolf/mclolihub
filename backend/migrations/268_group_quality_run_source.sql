-- Persist the source of the latest manual or automatic quality run per group.
ALTER TABLE group_quality_check_runs
    ADD COLUMN source VARCHAR(20) NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'automatic'));
