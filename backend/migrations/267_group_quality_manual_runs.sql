-- Keep the most recent manual quality run per group, visible across instances.
CREATE TABLE IF NOT EXISTS group_quality_check_runs (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    run_id UUID NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    reason VARCHAR(2000) NOT NULL DEFAULT '',
    checked_accounts INTEGER NOT NULL DEFAULT 0 CHECK (checked_accounts >= 0),
    total_accounts INTEGER NOT NULL CHECK (total_accounts > 0 AND checked_accounts <= total_accounts),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);
