-- name: GetUserAIProviderKeyByProviderID :one
SELECT
    *
FROM
    user_ai_provider_keys
WHERE
    user_id = @user_id::uuid
    AND ai_provider_id = @ai_provider_id::uuid;

-- name: GetUserAIProviderKeysByUserID :many
SELECT
    *
FROM
    user_ai_provider_keys
WHERE
    user_id = @user_id::uuid
ORDER BY
    ai_provider_id ASC,
    created_at ASC,
    id ASC;

-- GetUserAIProviderKeys is used by dbcrypt key rotation. Request paths should use
-- user-scoped lookups instead of this bulk accessor.
-- name: GetUserAIProviderKeys :many
SELECT
    *
FROM
    user_ai_provider_keys
ORDER BY
    user_id ASC,
    ai_provider_id ASC,
    created_at ASC,
    id ASC;

-- UpsertUserAIProviderKey preserves the original id and created_at when the
-- user/provider pair already exists. On conflict, callers provide id and
-- created_at for the insert path only.
--
-- OAuth columns overwrite wholesale (NULL clears): a pasted/static replace
-- carries no OAuth material and drops OAuth state by definition, while the
-- server-side device-grant approval path carries the full triple. The
-- dashboard no longer PUTs after a server-persisted grant, so a
-- server-persisted refresh token cannot be wiped by a lagging PUT.
-- name: UpsertUserAIProviderKey :one
INSERT INTO user_ai_provider_keys (
    id,
    user_id,
    ai_provider_id,
    api_key,
    api_key_key_id,
    oauth_refresh_token,
    oauth_refresh_token_key_id,
    oauth_expiry,
    account_id,
    oauth_extra,
    oauth_refresh_failure_reason,
    created_at,
    updated_at
) VALUES (
    @id::uuid,
    @user_id::uuid,
    @ai_provider_id::uuid,
    @api_key::text,
    sqlc.narg('api_key_key_id')::text,
    sqlc.narg('oauth_refresh_token')::text,
    sqlc.narg('oauth_refresh_token_key_id')::text,
    sqlc.narg('oauth_expiry')::timestamptz,
    sqlc.narg('account_id')::text,
    sqlc.narg('oauth_extra')::jsonb,
    sqlc.narg('oauth_refresh_failure_reason')::text,
    @created_at::timestamptz,
    @updated_at::timestamptz
)
ON CONFLICT (user_id, ai_provider_id) DO UPDATE
SET
    api_key = EXCLUDED.api_key,
    api_key_key_id = EXCLUDED.api_key_key_id,
    oauth_refresh_token = EXCLUDED.oauth_refresh_token,
    oauth_refresh_token_key_id = EXCLUDED.oauth_refresh_token_key_id,
    oauth_expiry = EXCLUDED.oauth_expiry,
    account_id = EXCLUDED.account_id,
    oauth_extra = EXCLUDED.oauth_extra,
    oauth_refresh_failure_reason = EXCLUDED.oauth_refresh_failure_reason,
    updated_at = EXCLUDED.updated_at
RETURNING
    *;

-- name: UpdateUserAIProviderKey :one
UPDATE
    user_ai_provider_keys
SET
    api_key = @api_key::text,
    api_key_key_id = sqlc.narg('api_key_key_id')::text,
    updated_at = NOW()
WHERE
    user_id = @user_id::uuid
    AND ai_provider_id = @ai_provider_id::uuid
RETURNING
    *;

-- name: DeleteUserAIProviderKey :exec
DELETE FROM
    user_ai_provider_keys
WHERE
    user_id = @user_id::uuid
    AND ai_provider_id = @ai_provider_id::uuid;

-- name: DeleteUserAIProviderKeysByProviderID :exec
DELETE FROM
    user_ai_provider_keys
WHERE
    ai_provider_id = @ai_provider_id::uuid;

-- name: UpdateEncryptedUserAIProviderKey :one
UPDATE
    user_ai_provider_keys
SET
    api_key = @api_key::text,
    api_key_key_id = sqlc.narg('api_key_key_id')::text,
    oauth_refresh_token = sqlc.narg('oauth_refresh_token')::text,
    oauth_refresh_token_key_id = sqlc.narg('oauth_refresh_token_key_id')::text,
    updated_at = NOW()
WHERE
    id = @id::uuid
RETURNING
    *;

-- name: AcquireUserAIProviderKeyRefreshLease :one
-- Set the lease to expire according to the provided timeout. If there is
-- already a lease, an exception is raised. Mirrors
-- AcquireExternalAuthLinkRefreshLease.
SELECT * FROM acquire_user_ai_provider_key_refresh_lease(@ai_provider_id, @user_id, @timeout_ms);

-- name: ReleaseUserAIProviderKeyRefreshLease :exec
-- The lease is only removed if it is the current lease.
UPDATE
    user_ai_provider_keys
SET
    refresh_lease_expires_at = NULL
WHERE
    ai_provider_id = @ai_provider_id
    AND user_id = @user_id
    AND refresh_lease_expires_at = @refresh_lease_expires_at;

-- UpdateUserAIProviderKeyOAuth writes the rotated OAuth credential triple
-- (and failure bookkeeping) under the refresh lease. If a refresh lease
-- is provided, the row is only updated if the lease matches, mirroring
-- UpdateExternalAuthLink. Rotation persist MUST happen before the new
-- access token is used: refresh rotates both tokens, so writing after the
-- request risks losing the new refresh token on a crash and permanently
-- breaking the credential. A terminal failure (invalid_grant) passes NULL
-- refresh material to take the down-path back to static-secret behavior.
-- name: UpdateUserAIProviderKeyOAuth :one
UPDATE
    user_ai_provider_keys
SET
    updated_at = NOW(),
    api_key = @api_key::text,
    api_key_key_id = sqlc.narg('api_key_key_id')::text,
    oauth_refresh_token = sqlc.narg('oauth_refresh_token')::text,
    oauth_refresh_token_key_id = sqlc.narg('oauth_refresh_token_key_id')::text,
    oauth_expiry = sqlc.narg('oauth_expiry')::timestamptz,
    account_id = sqlc.narg('account_id')::text,
    oauth_extra = sqlc.narg('oauth_extra')::jsonb,
    oauth_refresh_failure_reason = sqlc.narg('oauth_refresh_failure_reason')::text
WHERE
    user_id = @user_id::uuid
    AND ai_provider_id = @ai_provider_id::uuid
    AND (
        refresh_lease_expires_at = sqlc.narg('refresh_lease_expires_at')::timestamptz
        OR sqlc.narg('refresh_lease_expires_at')::timestamptz IS NULL
    )
RETURNING
    *;
