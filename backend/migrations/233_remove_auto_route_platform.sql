UPDATE api_keys
SET route_platform = 'openai'
WHERE route_platform IS NULL
   OR LOWER(BTRIM(route_platform)) IN ('', 'auto');

ALTER TABLE api_keys
    ALTER COLUMN route_platform SET DEFAULT 'openai';

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_route_platform_check;
ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_route_platform_check
    CHECK (route_platform IN ('openai', 'anthropic', 'grok'));
