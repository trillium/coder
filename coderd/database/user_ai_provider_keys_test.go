package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/testutil"
)

// seedUserAIProviderKey inserts one user_ai_provider_keys row for lease and
// OAuth update tests.
func seedUserAIProviderKey(t testing.TB, db database.Store, userID, providerID uuid.UUID) database.UserAIProviderKey {
	t.Helper()
	now := time.Now()
	key, err := db.UpsertUserAIProviderKey(context.Background(), database.UpsertUserAIProviderKeyParams{
		ID:           uuid.New(),
		UserID:       userID,
		AIProviderID: providerID,
		APIKey:       "test-access-token",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	require.NoError(t, err)
	return key
}

func TestUserAIProviderKeyRefreshLease(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.SkipNow()
		return
	}

	t.Run("AcquireLeaseAtomicity", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitLong)

		one := dbtestutil.StartTx(t, db, nil)
		two := dbtestutil.StartTx(t, db, nil)

		user := dbgen.User(t, db, database.User{})
		provider := dbgen.AIProvider(t, db, database.AIProvider{})
		seedUserAIProviderKey(t, db, user.ID, provider.ID)

		// The winner acquires the lease inside an open transaction, holding
		// the row lock without committing.
		acquired, err := one.AcquireUserAIProviderKeyRefreshLease(ctx, database.AcquireUserAIProviderKeyRefreshLeaseParams{
			AIProviderID: provider.ID,
			UserID:       user.ID,
			TimeoutMs:    time.Hour.Milliseconds(),
		})
		require.NoError(t, err)
		require.True(t, acquired.RefreshLeaseExpiresAt.Valid)

		// The loser's acquire targets the same row and must block on the
		// winner's uncommitted row lock rather than return anything.
		loserErr := make(chan error, 1)
		go func() {
			_, err := two.AcquireUserAIProviderKeyRefreshLease(ctx, database.AcquireUserAIProviderKeyRefreshLeaseParams{
				AIProviderID: provider.ID,
				UserID:       user.ID,
				TimeoutMs:    time.Hour.Milliseconds(),
			})
			loserErr <- err
		}()

		select {
		case err := <-loserErr:
			t.Fatalf("loser returned %v before the winner committed; expected it to block on the row lock", err)
		case <-time.After(testutil.IntervalMedium):
		}

		// Commit the winner. The loser unblocks, re-checks its predicate
		// against the committed row, and errors with the active lease
		// check violation.
		require.NoError(t, one.Done())
		err = testutil.RequireReceive(ctx, t, loserErr)
		require.True(t, database.IsCheckViolation(err, "user_ai_provider_key_active_lease"))

		// The stored lease is the winner's, untouched by the loser.
		final, err := db.GetUserAIProviderKeyByProviderID(ctx, database.GetUserAIProviderKeyByProviderIDParams{
			UserID:       user.ID,
			AIProviderID: provider.ID,
		})
		require.NoError(t, err)
		require.Equal(t, acquired.RefreshLeaseExpiresAt.Time.Unix(), final.RefreshLeaseExpiresAt.Time.Unix())
	})

	t.Run("ReleaseLeaseExactMatch", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitLong)

		user := dbgen.User(t, db, database.User{})
		provider := dbgen.AIProvider(t, db, database.AIProvider{})
		seedUserAIProviderKey(t, db, user.ID, provider.ID)

		acquired, err := db.AcquireUserAIProviderKeyRefreshLease(ctx, database.AcquireUserAIProviderKeyRefreshLeaseParams{
			AIProviderID: provider.ID,
			UserID:       user.ID,
			TimeoutMs:    time.Hour.Milliseconds(),
		})
		require.NoError(t, err)

		// A stale lease value releases nothing.
		require.NoError(t, db.ReleaseUserAIProviderKeyRefreshLease(ctx, database.ReleaseUserAIProviderKeyRefreshLeaseParams{
			AIProviderID:          provider.ID,
			UserID:                user.ID,
			RefreshLeaseExpiresAt: sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		}))
		held, err := db.GetUserAIProviderKeyByProviderID(ctx, database.GetUserAIProviderKeyByProviderIDParams{
			UserID:       user.ID,
			AIProviderID: provider.ID,
		})
		require.NoError(t, err)
		require.True(t, held.RefreshLeaseExpiresAt.Valid, "stale release must not clear the lease")

		// The exact lease releases.
		require.NoError(t, db.ReleaseUserAIProviderKeyRefreshLease(ctx, database.ReleaseUserAIProviderKeyRefreshLeaseParams{
			AIProviderID:          provider.ID,
			UserID:                user.ID,
			RefreshLeaseExpiresAt: acquired.RefreshLeaseExpiresAt,
		}))
		released, err := db.GetUserAIProviderKeyByProviderID(ctx, database.GetUserAIProviderKeyByProviderIDParams{
			UserID:       user.ID,
			AIProviderID: provider.ID,
		})
		require.NoError(t, err)
		require.False(t, released.RefreshLeaseExpiresAt.Valid)
	})

	t.Run("OAuthUpdateLeaseGated", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitLong)

		user := dbgen.User(t, db, database.User{})
		provider := dbgen.AIProvider(t, db, database.AIProvider{})
		seedUserAIProviderKey(t, db, user.ID, provider.ID)

		leased, err := db.AcquireUserAIProviderKeyRefreshLease(ctx, database.AcquireUserAIProviderKeyRefreshLeaseParams{
			AIProviderID: provider.ID,
			UserID:       user.ID,
			TimeoutMs:    time.Hour.Milliseconds(),
		})
		require.NoError(t, err)

		expiry := time.Now().Add(time.Hour).Truncate(time.Second)
		updated, err := db.UpdateUserAIProviderKeyOAuth(ctx, database.UpdateUserAIProviderKeyOAuthParams{
			UserID:                    user.ID,
			AIProviderID:              provider.ID,
			APIKey:                    "rotated-access-token",
			OAuthRefreshToken:         sql.NullString{String: "rotated-refresh-token", Valid: true},
			OAuthExpiry:               sql.NullTime{Time: expiry, Valid: true},
			AccountID:                 sql.NullString{String: "acct-lease-test", Valid: true},
			OauthRefreshFailureReason: sql.NullString{},
			RefreshLeaseExpiresAt:     leased.RefreshLeaseExpiresAt,
		})
		require.NoError(t, err)
		require.Equal(t, "rotated-access-token", updated.APIKey)
		require.Equal(t, "rotated-refresh-token", updated.OAuthRefreshToken.String)
		require.WithinDuration(t, expiry, updated.OAuthExpiry.Time, time.Second)
		require.Equal(t, "acct-lease-test", updated.AccountID.String)

		// A stale lease writes nothing.
		_, err = db.UpdateUserAIProviderKeyOAuth(ctx, database.UpdateUserAIProviderKeyOAuthParams{
			UserID:                user.ID,
			AIProviderID:          provider.ID,
			APIKey:                "stale-write",
			RefreshLeaseExpiresAt: sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true},
		})
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("OAuthUpdateTerminalDownPath", func(t *testing.T) {
		t.Parallel()
		db, _ := dbtestutil.NewDB(t)
		ctx := testutil.Context(t, testutil.WaitLong)

		user := dbgen.User(t, db, database.User{})
		provider := dbgen.AIProvider(t, db, database.AIProvider{})
		seedUserAIProviderKey(t, db, user.ID, provider.ID)

		leased, err := db.AcquireUserAIProviderKeyRefreshLease(ctx, database.AcquireUserAIProviderKeyRefreshLeaseParams{
			AIProviderID: provider.ID,
			UserID:       user.ID,
			TimeoutMs:    time.Hour.Milliseconds(),
		})
		require.NoError(t, err)

		// Terminal failure: NULL refresh, kept access token, recorded
		// reason. The row returns to static-secret behavior.
		updated, err := db.UpdateUserAIProviderKeyOAuth(ctx, database.UpdateUserAIProviderKeyOAuthParams{
			UserID:                    leased.UserID,
			AIProviderID:              leased.AIProviderID,
			APIKey:                    leased.APIKey,
			ApiKeyKeyID:               leased.ApiKeyKeyID,
			OAuthRefreshToken:         sql.NullString{},
			OAuthRefreshTokenKeyID:    sql.NullString{},
			OAuthExpiry:               leased.OAuthExpiry,
			AccountID:                 leased.AccountID,
			OauthRefreshFailureReason: sql.NullString{String: "invalid_grant", Valid: true},
			RefreshLeaseExpiresAt:     leased.RefreshLeaseExpiresAt,
		})
		require.NoError(t, err)
		require.False(t, updated.OAuthRefreshToken.Valid)
		require.Equal(t, "test-access-token", updated.APIKey, "saved key is kept")
		require.Equal(t, "invalid_grant", updated.OauthRefreshFailureReason.String)
	})
}
