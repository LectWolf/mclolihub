-- Replace the year-9999 health quarantine sentinel created by version 0.1.179.12.
-- It can round-trip as year 10000 in east-Asian time zones and break JSON encoding.
UPDATE accounts
SET temp_unschedulable_until = TIMESTAMPTZ '2099-12-31 23:59:59+00',
    updated_at = NOW()
WHERE temp_unschedulable_until >= TIMESTAMPTZ '9999-01-01 00:00:00+00'
  AND temp_unschedulable_reason LIKE 'group_health_probe:%'
  AND deleted_at IS NULL;
