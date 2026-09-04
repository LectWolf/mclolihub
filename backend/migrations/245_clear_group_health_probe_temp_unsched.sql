-- Probe quarantine lives on account_health_states. Do not keep those accounts
-- paused via temp_unschedulable_until, so fixed single-group keys can still
-- select them. Dynamic routing continues to hide them via runtime_status.
WITH cleared AS (
    UPDATE accounts
    SET temp_unschedulable_until = NULL,
        temp_unschedulable_reason = NULL,
        updated_at = NOW()
    WHERE deleted_at IS NULL
      AND temp_unschedulable_reason LIKE 'group_health_probe:%'
    RETURNING id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT 'account_changed', id, NULL, NULL FROM cleared;
