-- Configurable group-scoped quality windows; keep 263 immutable.
ALTER TABLE group_quality_check_settings
    ADD COLUMN IF NOT EXISTS min_spend_usd NUMERIC(14,4) NOT NULL DEFAULT 5 CHECK (min_spend_usd BETWEEN 0.01 AND 1000000),
    ADD COLUMN IF NOT EXISTS min_interval_minutes INTEGER NOT NULL DEFAULT 30 CHECK (min_interval_minutes BETWEEN 1 AND 10080),
    ADD COLUMN IF NOT EXISTS model_id VARCHAR(200) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS group_account_quality_checks (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    window_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_checked_at TIMESTAMPTZ,
    last_attempt_at TIMESTAMPTZ,
    last_status VARCHAR(20) NOT NULL DEFAULT '',
    last_model_id VARCHAR(200) NOT NULL DEFAULT '',
    degraded_streak INTEGER NOT NULL DEFAULT 0 CHECK (degraded_streak BETWEEN 0 AND 2),
    paused BOOLEAN NOT NULL DEFAULT FALSE,
    retry_after TIMESTAMPTZ,
    PRIMARY KEY (group_id, account_id)
);

-- Retain owned pauses for recovery, but do not carry over potentially false verdicts.
INSERT INTO group_account_quality_checks (group_id, account_id, window_started_at, paused)
SELECT ag.group_id, q.account_id, NOW(), q.paused
FROM account_quality_checks q
JOIN account_groups ag ON ag.account_id = q.account_id
JOIN group_quality_check_settings s ON s.group_id = ag.group_id AND s.enabled
ON CONFLICT (group_id, account_id) DO NOTHING;

-- One artifact per group, replaced atomically with the latest actual attempt.
CREATE TABLE IF NOT EXISTS group_quality_check_latest (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    result_id UUID NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('success', 'degraded', 'unknown', 'failed', 'skipped')),
    reason VARCHAR(2000) NOT NULL DEFAULT '',
    model_id VARCHAR(200) NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL,
    preview_png BYTEA CHECK (preview_png IS NULL OR octet_length(preview_png) <= 2097152)
);
CREATE INDEX IF NOT EXISTS group_account_quality_retry_idx ON group_account_quality_checks (retry_after, last_attempt_at);
CREATE INDEX IF NOT EXISTS usage_logs_quality_group_account_idx ON usage_logs (group_id, account_id, created_at) INCLUDE (actual_cost);
