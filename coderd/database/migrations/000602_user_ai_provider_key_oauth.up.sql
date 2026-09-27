-- Durable OAuth credential lifecycle for user AI provider keys (fork-only).
-- Adds nullable OAuth columns to user_ai_provider_keys, modeled on
-- external_auth_links, plus a per-row refresh lease function with the
-- matching lease-gated update discipline.
--
-- NULLABILITY DIVERGENCE (deliberate, not an oversight):
-- external_auth_links declares oauth_refresh_token, oauth_access_token and
-- oauth_expiry NOT NULL. These columns are NULLABLE here because existing
-- rows predate OAuth entirely: NULL means today's static-secret behavior
-- (the row's api_key is used as-is and no refresh is attempted).
-- DOWN-PATH: a row goes refresh-present -> refresh-NULL when its refresh
-- token is revoked or a terminal refresh failure (invalid_grant) is
-- recorded; the row then behaves exactly like a legacy static-secret row.
--
-- NON-GOAL: ai_provider_keys (admin/central-pool) intentionally gets no
-- OAuth columns. Pool keys are static admin config with no user identity
-- to attribute a refresh to; they keep failover semantics.
--
-- Rolling upgrade: older replicas keep working. SELECT statements use explicit
-- column lists so extra columns are ignored, and the extended Upsert only
-- writes OAuth state when the writer carries it.

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS oauth_refresh_token text DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.oauth_refresh_token IS 'OAuth refresh token for the subscription provider sign-in. Encrypted at rest via dbcrypt when oauth_refresh_token_key_id is set. NULL means static-secret behavior: use api_key as-is, attempt no refresh.';

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS oauth_refresh_token_key_id text DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.oauth_refresh_token_key_id IS 'The ID of the key used to encrypt oauth_refresh_token. If this is NULL, the refresh token is not encrypted.';

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS oauth_expiry timestamp WITH time zone DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.oauth_expiry IS 'When the current access token (api_key) expires, from the provider expires_in. NULL means unknown: no refresh is attempted.';

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS account_id text DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.account_id IS 'Provider account the tokens belong to, derived from the access JWT chatgpt_account_id claim. Re-derived on every refresh.';

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS oauth_extra jsonb DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.oauth_extra IS 'Opaque extra OAuth material from the provider token response. Reserved for forward use.';

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS oauth_refresh_failure_reason text DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.oauth_refresh_failure_reason IS 'Last refresh failure, transient or terminal. NULL means no failure recorded. A terminal failure (invalid_grant) also NULLs oauth_refresh_token.';

ALTER TABLE user_ai_provider_keys ADD COLUMN IF NOT EXISTS refresh_lease_expires_at timestamp WITH time zone DEFAULT NULL;
COMMENT ON COLUMN user_ai_provider_keys.refresh_lease_expires_at IS 'Indicates a replica is refreshing the token; prevents concurrent refreshes.';

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint WHERE conname = 'user_ai_provider_keys_oauth_refresh_token_key_id_fkey'
	) THEN
		ALTER TABLE user_ai_provider_keys
			ADD CONSTRAINT user_ai_provider_keys_oauth_refresh_token_key_id_fkey
			FOREIGN KEY (oauth_refresh_token_key_id) REFERENCES dbcrypt_keys(active_key_digest);
	END IF;
END
$$;

CREATE OR REPLACE FUNCTION acquire_user_ai_provider_key_refresh_lease(arg_ai_provider_id uuid, arg_user_id uuid, timeout_ms bigint)
RETURNS SETOF user_ai_provider_keys AS $$
DECLARE r user_ai_provider_keys;
BEGIN
	UPDATE user_ai_provider_keys
	SET
		refresh_lease_expires_at = NOW() + (timeout_ms || ' ms')::interval
	WHERE
		ai_provider_id = arg_ai_provider_id
		AND user_id = arg_user_id
		AND (refresh_lease_expires_at IS NULL OR refresh_lease_expires_at < NOW())
	RETURNING * INTO r;
	-- Got the lease, return the one row.
	IF FOUND THEN
		RETURN NEXT r;
		RETURN;
	END IF;
	-- Differentiate between unable to get the lease and the row being gone.
	IF EXISTS (SELECT 1 FROM user_ai_provider_keys WHERE ai_provider_id = arg_ai_provider_id AND user_id = arg_user_id) THEN
		RAISE EXCEPTION 'row is currently leased by another replica'
			USING ERRCODE = 'check_violation',
				CONSTRAINT = 'user_ai_provider_key_active_lease';
	END IF;
	-- Row is gone, return nothing.
	RETURN;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION acquire_user_ai_provider_key_refresh_lease IS 'Acquire a lease on the user AI provider key and return the row. If there is already an active lease, an exception is raised.';
