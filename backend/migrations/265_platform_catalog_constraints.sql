-- Fold the newly imported upstream 241/242 platform changes into one final step.
-- Earlier fork migrations are immutable and may contain CodeBuddy/Qoder rows.
-- Reinstalling the upstream TypeSafe-only CHECK first would reject those rows.
-- Application catalog validation now owns concrete platform membership, including
-- TypeSafe, Command Code, Cline, CodeBuddy and Qoder; preserve existing data.
-- Monitor provider CHECKs describe implemented probe adapters and stay unchanged.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
