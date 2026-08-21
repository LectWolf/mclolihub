ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS route_platform VARCHAR(20) NOT NULL DEFAULT 'auto';

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_route_platform_check;
ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_route_platform_check
    CHECK (route_platform IN ('auto', 'openai', 'anthropic', 'grok'));
