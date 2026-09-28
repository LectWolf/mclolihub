-- Per-group degradation (降智) detection.
-- The toggle lives here. There is no interval column: a check runs only after
-- both 30 minutes and 5 USD of actual_cost have accumulated since the last
-- check. Results are stored per account, not as a second probe log.

CREATE TABLE IF NOT EXISTS group_quality_check_settings (
    group_id   BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account_quality_checks (
    account_id         BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    window_started_at  TIMESTAMPTZ NOT NULL,
    last_checked_at    TIMESTAMPTZ,
    last_status        VARCHAR(20) NOT NULL DEFAULT '',
    paused             BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
