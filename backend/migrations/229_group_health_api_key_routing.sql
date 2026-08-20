-- Group health probing, account runtime state and API key routing preferences.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS probe_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS probe_model VARCHAR(100) NOT NULL DEFAULT 'gpt-5.6-sol';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS probe_interval_seconds INTEGER NOT NULL DEFAULT 600;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS route_mode VARCHAR(20) NOT NULL DEFAULT 'fixed';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS max_rate_multiplier DECIMAL(10,4);

CREATE TABLE IF NOT EXISTS group_health_states (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL UNIQUE REFERENCES groups(id) ON DELETE CASCADE,
    status VARCHAR(20) NOT NULL DEFAULT 'unknown',
    reason TEXT,
    last_probe_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    next_probe_at TIMESTAMPTZ,
    failure_count INTEGER NOT NULL DEFAULT 0,
    probe_ttft_ms INTEGER NOT NULL DEFAULT 0,
    probe_availability_6h DECIMAL(7,4) NOT NULL DEFAULT 0,
    probe_ttft_avg_ms INTEGER NOT NULL DEFAULT 0,
    probe_ttft_p95_ms INTEGER NOT NULL DEFAULT 0,
    probe_samples INTEGER NOT NULL DEFAULT 0,
    real_ttft_p50_ms INTEGER NOT NULL DEFAULT 0,
    real_ttft_avg_ms INTEGER NOT NULL DEFAULT 0,
    real_ttft_p95_ms INTEGER NOT NULL DEFAULT 0,
    real_ttft_samples INTEGER NOT NULL DEFAULT 0,
    real_availability_6h DECIMAL(7,4) NOT NULL DEFAULT 0,
    real_total_avg_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_health_states_status ON group_health_states(status);
CREATE INDEX IF NOT EXISTS idx_group_health_states_next_probe ON group_health_states(next_probe_at);

CREATE TABLE IF NOT EXISTS group_health_events (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    kind VARCHAR(20) NOT NULL DEFAULT 'probe',
    success BOOLEAN NOT NULL DEFAULT FALSE,
    is_probe BOOLEAN NOT NULL DEFAULT TRUE,
    semantic_started BOOLEAN NOT NULL DEFAULT FALSE,
    error_category VARCHAR(50),
    ttft_ms INTEGER NOT NULL DEFAULT 0,
    total_ms INTEGER NOT NULL DEFAULT 0,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_health_events_group_time ON group_health_events(group_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_group_health_events_probe_time ON group_health_events(is_probe, observed_at);

CREATE TABLE IF NOT EXISTS account_health_states (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    probe_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    runtime_status VARCHAR(30) NOT NULL DEFAULT 'active',
    reason TEXT,
    retry_step INTEGER NOT NULL DEFAULT 0,
    next_probe_at TIMESTAMPTZ,
    last_probe_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    last_immediate_probe_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_account_health_states_due ON account_health_states(runtime_status, next_probe_at);
CREATE INDEX IF NOT EXISTS idx_account_health_states_group ON account_health_states(probe_group_id);

CREATE TABLE IF NOT EXISTS api_key_group_preferences (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(api_key_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_api_key_group_preferences_position ON api_key_group_preferences(api_key_id, position);
