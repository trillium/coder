DROP FUNCTION IF EXISTS acquire_user_ai_provider_key_refresh_lease;
ALTER TABLE user_ai_provider_keys DROP CONSTRAINT IF EXISTS user_ai_provider_keys_oauth_refresh_token_key_id_fkey;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS refresh_lease_expires_at;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS oauth_refresh_failure_reason;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS oauth_extra;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS account_id;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS oauth_expiry;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS oauth_refresh_token_key_id;
ALTER TABLE user_ai_provider_keys DROP COLUMN IF EXISTS oauth_refresh_token;
