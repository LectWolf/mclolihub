-- Persist the new recovery state names and keep health-quarantined accounts
-- excluded from scheduling even after their display recovery time elapses.
UPDATE account_health_states
SET runtime_status = 'probing'
WHERE runtime_status = 'failed';

UPDATE accounts a
SET temp_unschedulable_until = TIMESTAMPTZ '9999-12-31 23:59:59+00',
    temp_unschedulable_reason = 'group_health_probe: ' || COALESCE(h.reason, 'waiting for recovery probe'),
    updated_at = NOW()
FROM account_health_states h
WHERE h.account_id = a.id
  AND h.runtime_status IN ('probing', 'unavailable')
  AND a.status = 'active'
  AND a.deleted_at IS NULL;
