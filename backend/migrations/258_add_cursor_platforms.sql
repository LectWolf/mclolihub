-- Add Cursor Sand / Cursor IDE to platform allowlists:
--   1. user_platform_quotas.platform CHECK
--   2. composite_model_routes.target_platform CHECK
--
-- Numbered 258 on this fork after 257_purge_unlimited_user_platform_quotas.sql.
-- Channel monitor provider enums are unchanged: Cursor is not a monitor adapter yet.

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go',
                        'cursor_sand', 'cursor'));

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                               'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go',
                               'cursor_sand', 'cursor'));
