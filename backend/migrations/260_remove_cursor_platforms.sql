-- Cursor Sand / Cursor IDE 反向代理已下线：
--   1. 遗留的 cursor_sand / cursor 账号置为 error 并停止调度。只改状态不删行，
--      凭据仍按敏感键脱敏，由管理员在后台自行清理。
--   2. 删除引用这两个平台的 user_platform_quotas / composite_model_routes 配置行。
--      平台已没有转发实现，这些配置不再生效；留着会让后续按惯例重建 CHECK 的迁移失败。
--   3. 从两处平台 CHECK 约束中移除 cursor_sand / cursor（保留 qoder）。
--
-- 258/259 已发布且受 checksum 保护，不能改写，这里用新迁移收紧约束。
-- 整个文件在同一事务内执行；UPDATE/DELETE 重复执行影响 0 行，DROP ... IF EXISTS 保证约束可重建。

UPDATE accounts
   SET status = 'error',
       schedulable = FALSE,
       error_message = 'Cursor reverse proxy has been removed; this account can no longer serve requests. Delete it when convenient.',
       updated_at = NOW()
 WHERE platform IN ('cursor_sand', 'cursor')
   AND deleted_at IS NULL;

DELETE FROM user_platform_quotas
 WHERE platform IN ('cursor_sand', 'cursor');

DELETE FROM composite_model_routes
 WHERE target_platform IN ('cursor_sand', 'cursor');

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go',
                        'qoder'));

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;

ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                               'kimi', 'zhipu', 'deepseek', 'minimax', 'codebuddy', 'opencode_go',
                               'qoder'));
