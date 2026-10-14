-- Score-based HTML assessment and the five most recent group-owned artworks.
ALTER TABLE group_quality_check_latest
    ADD COLUMN IF NOT EXISTS score NUMERIC(5,2) CHECK (score BETWEEN 0 AND 100);

CREATE TABLE IF NOT EXISTS group_quality_check_history (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    result_id UUID NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('success', 'degraded', 'unknown', 'failed', 'skipped')),
    reason VARCHAR(2000) NOT NULL DEFAULT '',
    model_id VARCHAR(200) NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL,
    score NUMERIC(5,2) CHECK (score BETWEEN 0 AND 100),
    artwork_html TEXT CHECK (artwork_html IS NULL OR octet_length(artwork_html) <= 1048576),
    PRIMARY KEY (group_id, result_id)
);
CREATE INDEX IF NOT EXISTS group_quality_history_recent_idx
    ON group_quality_check_history (group_id, checked_at DESC, result_id DESC);

-- Keep the saved legacy artwork; do not invent a numeric score for it.
INSERT INTO group_quality_check_history
    (group_id, account_id, result_id, status, reason, model_id, checked_at, score, artwork_html)
SELECT group_id, account_id, result_id, status, reason, model_id, checked_at, score, artwork_html
FROM group_quality_check_latest
ON CONFLICT (group_id, result_id) DO NOTHING;

-- A content score is a new grading method; preserve owned pauses for recovery,
-- but never combine an old visual failure with a new content failure streak.
UPDATE group_account_quality_checks SET degraded_streak = 0;
