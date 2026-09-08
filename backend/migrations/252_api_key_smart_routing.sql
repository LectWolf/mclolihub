-- Convert cheapest/fastest/custom API keys to smart routing (ordered group list).
-- Snapshot cheapest/fastest order from current bindable groups, drop disabled
-- groups and natural-revert, then rename route_mode to smart.

-- Eligible groups: active, matching the key's protocol scope, user-bindable,
-- excluding groups the user disabled on that key.
CREATE TEMP TABLE tmp_smart_route_eligible ON COMMIT DROP AS
SELECT
    k.id AS api_key_id,
    k.route_mode,
    g.id AS group_id,
    COALESCE(ugr.rate_multiplier, g.rate_multiplier) AS effective_rate,
    COALESCE(g.sort_order, 0) AS admin_sort,
    COALESCE(hs.real_ttft_samples, 0) AS real_ttft_samples,
    COALESCE(hs.real_ttft_p50_ms, 0) AS real_ttft_p50_ms,
    COALESCE(hs.probe_ttft_ms, 0) AS probe_ttft_ms
FROM api_keys k
JOIN users u ON u.id = k.user_id
JOIN groups g ON g.deleted_at IS NULL AND g.status = 'active'
LEFT JOIN user_group_rate_multipliers ugr
    ON ugr.user_id = k.user_id AND ugr.group_id = g.id AND ugr.rate_multiplier IS NOT NULL
LEFT JOIN group_health_states hs ON hs.group_id = g.id
WHERE k.deleted_at IS NULL
  AND k.route_mode IN ('cheapest', 'fastest')
  AND g.platform = CASE
        WHEN lower(coalesce(nullif(k.route_platform, ''), 'openai')) IN ('auto', '') THEN 'openai'
        WHEN lower(k.route_platform) = 'anthropic' THEN 'anthropic'
        WHEN lower(k.route_platform) = 'grok' THEN 'grok'
        ELSE 'openai'
      END
  AND NOT EXISTS (
        SELECT 1
        FROM api_key_group_preferences p
        WHERE p.api_key_id = k.id AND p.group_id = g.id AND p.disabled
      )
  AND (
        CASE
            WHEN g.subscription_type = 'subscription' THEN EXISTS (
                SELECT 1
                FROM user_subscriptions s
                WHERE s.user_id = k.user_id
                  AND s.group_id = g.id
                  AND s.status = 'active'
                  AND s.expires_at > NOW()
            )
            ELSE (
                (NOT g.is_exclusive AND NOT COALESCE(u.restrict_public_groups, FALSE))
                OR EXISTS (
                    SELECT 1
                    FROM user_allowed_groups ag
                    WHERE ag.user_id = k.user_id AND ag.group_id = g.id
                )
            )
        END
      );

-- If every group was disabled, keep the key usable: snapshot all bindable groups.
INSERT INTO tmp_smart_route_eligible (
    api_key_id, route_mode, group_id, effective_rate, admin_sort,
    real_ttft_samples, real_ttft_p50_ms, probe_ttft_ms
)
SELECT
    k.id,
    k.route_mode,
    g.id,
    COALESCE(ugr.rate_multiplier, g.rate_multiplier),
    COALESCE(g.sort_order, 0),
    COALESCE(hs.real_ttft_samples, 0),
    COALESCE(hs.real_ttft_p50_ms, 0),
    COALESCE(hs.probe_ttft_ms, 0)
FROM api_keys k
JOIN users u ON u.id = k.user_id
JOIN groups g ON g.deleted_at IS NULL AND g.status = 'active'
LEFT JOIN user_group_rate_multipliers ugr
    ON ugr.user_id = k.user_id AND ugr.group_id = g.id AND ugr.rate_multiplier IS NOT NULL
LEFT JOIN group_health_states hs ON hs.group_id = g.id
WHERE k.deleted_at IS NULL
  AND k.route_mode IN ('cheapest', 'fastest')
  AND NOT EXISTS (
        SELECT 1 FROM tmp_smart_route_eligible e WHERE e.api_key_id = k.id
      )
  AND g.platform = CASE
        WHEN lower(coalesce(nullif(k.route_platform, ''), 'openai')) IN ('auto', '') THEN 'openai'
        WHEN lower(k.route_platform) = 'anthropic' THEN 'anthropic'
        WHEN lower(k.route_platform) = 'grok' THEN 'grok'
        ELSE 'openai'
      END
  AND (
        CASE
            WHEN g.subscription_type = 'subscription' THEN EXISTS (
                SELECT 1
                FROM user_subscriptions s
                WHERE s.user_id = k.user_id
                  AND s.group_id = g.id
                  AND s.status = 'active'
                  AND s.expires_at > NOW()
            )
            ELSE (
                (NOT g.is_exclusive AND NOT COALESCE(u.restrict_public_groups, FALSE))
                OR EXISTS (
                    SELECT 1
                    FROM user_allowed_groups ag
                    WHERE ag.user_id = k.user_id AND ag.group_id = g.id
                )
            )
        END
      );

INSERT INTO api_key_group_preferences (api_key_id, group_id, disabled, position, created_at, updated_at)
SELECT
    e.api_key_id,
    e.group_id,
    FALSE,
    (ROW_NUMBER() OVER (
        PARTITION BY e.api_key_id
        ORDER BY
            CASE
                WHEN e.route_mode = 'cheapest' THEN e.effective_rate
                ELSE NULL
            END ASC NULLS LAST,
            CASE
                WHEN e.route_mode = 'fastest' AND e.real_ttft_samples > 0 THEN 0
                WHEN e.route_mode = 'fastest' THEN 1
                ELSE 0
            END,
            CASE
                WHEN e.route_mode = 'fastest' AND e.real_ttft_samples > 0 THEN e.real_ttft_p50_ms
                ELSE NULL
            END ASC NULLS LAST,
            CASE
                WHEN e.route_mode = 'fastest' AND e.real_ttft_samples = 0 AND e.probe_ttft_ms > 0 THEN 0
                WHEN e.route_mode = 'fastest' THEN 1
                ELSE 0
            END,
            CASE
                WHEN e.route_mode = 'fastest' AND e.real_ttft_samples = 0 THEN e.probe_ttft_ms
                ELSE NULL
            END ASC NULLS LAST,
            e.admin_sort,
            e.group_id
    ) - 1)::INTEGER,
    NOW(),
    NOW()
FROM tmp_smart_route_eligible e
ON CONFLICT (api_key_id, group_id) DO UPDATE
SET disabled = FALSE,
    position = EXCLUDED.position,
    updated_at = NOW();

UPDATE api_keys
SET route_mode = 'smart'
WHERE route_mode IN ('cheapest', 'fastest', 'custom');

DELETE FROM api_key_group_preferences WHERE disabled;

ALTER TABLE api_keys DROP COLUMN IF EXISTS natural_revert_enabled;

COMMENT ON COLUMN api_keys.route_mode IS 'fixed or smart';
