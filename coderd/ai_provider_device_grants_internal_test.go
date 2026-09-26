package coderd

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/quartz"
)

// errTestDeviceExchange is a throwaway sentinel for the exchange-retry test.
var errTestDeviceExchange = xerrors.New("test device exchange failure")

// All key material below is throwaway and invalid. These tests never touch
// live credentials, the keychain, or a real subscription provider.

// testDeviceAccessJWT mints an unsigned throwaway JWT carrying the ChatGPT
// account claim, shaped like a real subscription access token. It is
// invalid as a credential and never leaves the test.
func testDeviceAccessJWT(t testing.TB, accountID string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
}

// fakeAIDeviceExchanger scripts one grant's provider side.
type fakeAIDeviceExchanger struct {
	mu            sync.Mutex
	deviceAuthID  string
	userCode      string
	interval      int
	requestErr    error
	polls         []AIDevicePollOutcome
	pollErr       error
	exchangeGrant AIDeviceTokenGrant
	exchangeErr   error
	exchanges     int
	pollCalls     int
}

func (f *fakeAIDeviceExchanger) RequestDeviceCode(_ context.Context) (deviceAuthID, userCode string, intervalSeconds int, err error) {
	if f.requestErr != nil {
		return "", "", 0, f.requestErr
	}
	return f.deviceAuthID, f.userCode, f.interval, nil
}

func (f *fakeAIDeviceExchanger) PollDeviceToken(_ context.Context, _, _ string) (AIDevicePollOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollCalls++
	if f.pollErr != nil {
		return AIDevicePollOutcome{}, f.pollErr
	}
	if len(f.polls) == 0 {
		return AIDeviceGrantPending(), nil
	}
	next := f.polls[0]
	f.polls = f.polls[1:]
	return next, nil
}

func (f *fakeAIDeviceExchanger) ExchangeAuthorizationCode(_ context.Context, _, _ string) (AIDeviceTokenGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges++
	if f.exchangeErr != nil {
		return AIDeviceTokenGrant{}, f.exchangeErr
	}
	return f.exchangeGrant, nil
}

func testChatGPTProvider() database.AIProvider {
	return database.AIProvider{
		ID:      uuid.New(),
		Type:    database.AIProviderTypeOpenai,
		Name:    "chatgpt",
		Enabled: true,
	}
}

func testDeviceManager(t testing.TB, fake *fakeAIDeviceExchanger) (*AIDeviceGrantManager, *quartz.Mock) {
	t.Helper()
	clock := quartz.NewMock(t)
	manager := NewAIDeviceGrantManager(clock)
	manager.ClientFactory(func(_ database.AIProviderType, _ string) AIDeviceGrantExchanger {
		return fake
	})
	return manager, clock
}

func TestAIDeviceGrantLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantPending(), AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeGrant: AIDeviceTokenGrant{
			AccessToken:  testDeviceAccessJWT(t, "acct-test-123"),
			RefreshToken: "test-device-refresh-token", // #nosec G101 -- test fixture, not a credential.
			ExpiresIn:    3600,
		},
	}
	manager, clock := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, grant.status)
	initResp := aiDeviceGrantInitiateResponse(grant, clock.Now())
	require.Equal(t, "ABCD-1234", initResp.UserCode)
	require.Contains(t, initResp.VerificationURI, "auth.openai.com")
	require.NotContains(t, initResp.VerificationURI, "eyJhbGciOiJub25lIn0")
	require.False(t, initResp.StoresAccessTokenOnly)
	require.True(t, initResp.RefreshSupported)
	require.NotEmpty(t, initResp.ReauthMessage)
	require.Equal(t, 15*60, initResp.ExpiresIn)
	require.Equal(t, 5, initResp.PollInterval)

	pending, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, pending.status)

	authorized, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.status)
	require.Equal(t, 1, fake.exchanges)
	require.Equal(t, "acct-test-123", authorized.oauth.AccountID)
	require.Equal(t, "test-device-refresh-token", authorized.oauth.RefreshToken)
	pollResp := aiDeviceGrantPollResponse(authorized, clock.Now())
	require.Equal(t, fake.exchangeGrant.AccessToken, pollResp.APIKey, "no persist hook: access token still rides the poll response")
	require.NotContains(t, pollResp.APIKey, "test-device-refresh-token")
	require.False(t, pollResp.StoresAccessTokenOnly)
	require.True(t, pollResp.RefreshSupported)
	require.NotEmpty(t, pollResp.ReauthMessage)

	again, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, again.status)
	require.Equal(t, 1, fake.exchanges, "authorized grants must not re-exchange")
}

func TestAIDeviceGrantExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
	}
	manager, clock := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)

	clock.Advance(15*time.Minute + time.Second)
	expired, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusExpired, expired.status)
	require.Empty(t, expired.accessToken)
	require.Zero(t, fake.pollCalls, "expired grants must not hit the provider")
}

func TestAIDeviceGrantProviderExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantExpired()},
	}
	manager, _ := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	expired, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusExpired, expired.status)
}

func TestAIDeviceGrantCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
	}
	manager, _ := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	require.NoError(t, manager.cancel(grant.id, owner))
	require.NoError(t, manager.cancel(grant.id, owner), "cancel is idempotent")

	canceled, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusCanceled, canceled.status)
	require.Zero(t, fake.pollCalls, "canceled grants must not hit the provider")
	require.Error(t, manager.cancel(uuid.New(), owner), "unknown grants stay unknown")
}

func TestAIDeviceGrantWrongUserIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	other := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
	}
	manager, _ := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)

	_, err = manager.poll(ctx, grant.id, other)
	require.ErrorContains(t, err, "grant not found")
	require.ErrorContains(t, manager.cancel(grant.id, other), "grant not found")
	_, err = manager.peek(grant.id, other)
	require.ErrorContains(t, err, "grant not found")

	mine, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, mine.status, "other-user probes must not advance the grant")
}

func TestAIDeviceGrantSlowDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls: []AIDevicePollOutcome{
			AIDeviceGrantSlowDown(0),
			AIDeviceGrantSlowDown(30),
		},
	}
	manager, clock := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)

	first, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, first.status)
	require.Equal(t, 10, aiDeviceGrantPollResponse(first, clock.Now()).PollInterval, "bare slow_down grows the interval by 5s")

	second, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, 30, aiDeviceGrantPollResponse(second, clock.Now()).PollInterval, "server-provided interval wins")
}

func TestAIDeviceGrantExchangeRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeGrant: AIDeviceTokenGrant{
			AccessToken:  testDeviceAccessJWT(t, "acct-test-123"),
			RefreshToken: "test-device-refresh-token", // #nosec G101 -- test fixture, not a credential.
			ExpiresIn:    3600,
		},
		exchangeErr: errTestDeviceExchange,
	}
	manager, _ := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)

	retryable, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, retryable.status, "exchange failure stays pending")

	fake.mu.Lock()
	fake.exchangeErr = nil
	fake.polls = []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")}
	fake.mu.Unlock()

	authorized, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.status)
}

func TestAIDeviceGrantDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantDenied()},
	}
	manager, _ := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	denied, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusDenied, denied.status)
}

func TestAIDeviceGrantUnsupportedProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager, _ := testDeviceManager(t, &fakeAIDeviceExchanger{})

	_, err := manager.initiate(ctx, uuid.New(), uuid.New(), database.AIProvider{
		Type: database.AIProviderTypeOpenai,
		Name: "plain-api-key-provider",
	})
	require.ErrorContains(t, err, "not supported for this provider")
}

func TestAIDeviceGrantConfigSelection(t *testing.T) {
	t.Parallel()

	config, ok := aiDeviceGrantConfigForProvider(database.AIProviderTypeOpenai, "chatgpt")
	require.True(t, ok)
	require.NotEmpty(t, config.clientID)
	require.NotEmpty(t, config.deviceUserCodeURL)
	require.NotEmpty(t, config.deviceTokenURL)
	require.NotEmpty(t, config.tokenURL)
	require.NotEmpty(t, config.verificationURI)

	_, ok = aiDeviceGrantConfigForProvider(database.AIProviderTypeOpenai, "other")
	require.False(t, ok)
	_, ok = aiDeviceGrantConfigForProvider(database.AIProviderTypeAnthropic, "chatgpt")
	require.False(t, ok)

	require.True(t, deviceFlowSupportedForProvider(database.AIProvider{Type: database.AIProviderTypeOpenai, Name: "chatgpt", Enabled: true}))
	require.False(t, deviceFlowSupportedForProvider(database.AIProvider{Type: database.AIProviderTypeOpenai, Name: "chatgpt", Enabled: false}))
	require.False(t, deviceFlowSupportedForProvider(database.AIProvider{Type: database.AIProviderTypeAnthropic, Name: "claude", Enabled: true}))
}

func TestAIDeviceGrantServerPersist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeGrant: AIDeviceTokenGrant{
			AccessToken:  testDeviceAccessJWT(t, "acct-persist-1"),
			RefreshToken: "test-refresh-persist-1",
			ExpiresIn:    3600,
		},
	}
	manager, clock := testDeviceManager(t, fake)
	var persisted []AIDeviceOAuthCredential
	manager.SetPersistAuthorized(func(_ context.Context, ownerID, providerID uuid.UUID, cred AIDeviceOAuthCredential) error {
		require.Equal(t, owner, ownerID)
		require.Equal(t, provider.ID, providerID)
		persisted = append(persisted, cred)
		return nil
	})

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	authorized, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.status)
	require.True(t, authorized.persisted)
	require.Empty(t, authorized.accessToken, "persisted grants carry no key material")

	require.Len(t, persisted, 1)
	require.Equal(t, fake.exchangeGrant.AccessToken, persisted[0].AccessToken)
	require.Equal(t, "test-refresh-persist-1", persisted[0].RefreshToken)
	require.Equal(t, "acct-persist-1", persisted[0].AccountID)
	require.WithinDuration(t, clock.Now().Add(time.Hour), persisted[0].ExpiresAt, 5*time.Second)

	pollResp := aiDeviceGrantPollResponse(authorized, clock.Now())
	require.Empty(t, pollResp.APIKey, "persisted poll responses carry no key material")
	require.NotContains(t, string(mustJSON(t, pollResp)), "test-refresh-persist-1", "refresh token never leaves the server")
}

func TestAIDeviceGrantPersistRetryWithoutReexchange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeGrant: AIDeviceTokenGrant{
			AccessToken:  testDeviceAccessJWT(t, "acct-retry-1"),
			RefreshToken: "test-refresh-retry-1",
			ExpiresIn:    3600,
		},
	}
	manager, _ := testDeviceManager(t, fake)
	persistCalls := 0
	persistErr := xerrors.New("test persist failure")
	manager.SetPersistAuthorized(func(context.Context, uuid.UUID, uuid.UUID, AIDeviceOAuthCredential) error {
		persistCalls++
		return persistErr
	})

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	pending, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, pending.status, "persist failure stays pending")
	require.NotNil(t, pending.oauth, "exchanged triple is held for the retry")
	require.Equal(t, 1, fake.exchanges)

	persistErr = nil
	authorized, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.status)
	require.Equal(t, 1, fake.exchanges, "the single-use code must not be re-exchanged")
	require.Equal(t, 2, persistCalls)
}

func TestAIDeviceGrantAccessOnlyExchange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeGrant: AIDeviceTokenGrant{
			AccessToken: testDeviceAccessJWT(t, "acct-access-only"),
		},
	}
	manager, _ := testDeviceManager(t, fake)
	var persisted []AIDeviceOAuthCredential
	manager.SetPersistAuthorized(func(context.Context, uuid.UUID, uuid.UUID, AIDeviceOAuthCredential) error {
		persisted = append(persisted, AIDeviceOAuthCredential{})
		return nil
	})

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	authorized, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.status)
	require.Empty(t, authorized.oauth.RefreshToken)
	require.True(t, authorized.oauth.ExpiresAt.IsZero(), "unknown expiry persists as NULL")
	require.Len(t, persisted, 1)
}

func TestAIDeviceGrantOpaqueTokenStaysPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	owner := uuid.New()
	provider := testChatGPTProvider()
	fake := &fakeAIDeviceExchanger{
		deviceAuthID: "test-device-auth-id",
		userCode:     "ABCD-1234",
		interval:     5,
		polls:        []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeGrant: AIDeviceTokenGrant{
			AccessToken:  "opaque-access-token",
			RefreshToken: "opaque-refresh-token", // #nosec G101 -- test fixture, not a credential.
			ExpiresIn:    3600,
		},
	}
	manager, _ := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	pending, err := manager.poll(ctx, grant.id, owner)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, pending.status, "missing account claim never authorizes")
	require.Nil(t, pending.oauth)
}

func TestAIDeviceAccountIDFromJWT(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		accountID, err := aiDeviceAccountIDFromJWT(testDeviceAccessJWT(t, "acct-abc"))
		require.NoError(t, err)
		require.Equal(t, "acct-abc", accountID)
	})
	t.Run("NotAJWT", func(t *testing.T) {
		t.Parallel()
		_, err := aiDeviceAccountIDFromJWT("opaque-token")
		require.ErrorContains(t, err, "not a JWT")
	})
	t.Run("BadPayloadEncoding", func(t *testing.T) {
		t.Parallel()
		_, err := aiDeviceAccountIDFromJWT("eyJhbGciOiJub25lIn0.%%%.c2ln")
		require.Error(t, err)
	})
	t.Run("MissingNamespace", func(t *testing.T) {
		t.Parallel()
		claims, err := json.Marshal(map[string]any{"sub": "user-1"})
		require.NoError(t, err)
		token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
		_, err = aiDeviceAccountIDFromJWT(token)
		require.ErrorContains(t, err, "claim namespace")
	})
	t.Run("MissingAccountID", func(t *testing.T) {
		t.Parallel()
		claims, err := json.Marshal(map[string]any{aiDeviceAuthClaimNamespace: map[string]any{}})
		require.NoError(t, err)
		token := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
		_, err = aiDeviceAccountIDFromJWT(token)
		require.ErrorContains(t, err, "chatgpt_account_id")
	})
}

func TestAIDeviceExpiresInSeconds(t *testing.T) {
	t.Parallel()
	require.Equal(t, 3600, aiDeviceExpiresInSeconds(float64(3600)))
	require.Equal(t, 3600, aiDeviceExpiresInSeconds(3600))
	require.Equal(t, 3600, aiDeviceExpiresInSeconds(int64(3600)))
	require.Equal(t, 3600, aiDeviceExpiresInSeconds("3600"))
	require.Equal(t, 0, aiDeviceExpiresInSeconds(nil))
	require.Equal(t, 0, aiDeviceExpiresInSeconds(-5))
	require.Equal(t, 0, aiDeviceExpiresInSeconds("soon"))
	require.Equal(t, 0, aiDeviceExpiresInSeconds(map[string]any{}))
}

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

func TestUserAIProviderKeyOAuthSlots(t *testing.T) {
	t.Parallel()
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	oauthRow := database.UserAIProviderKey{
		OAuthRefreshToken: sql.NullString{String: "refresh-live", Valid: true},
		OAuthExpiry:       sql.NullTime{Time: expiry, Valid: true},
	}
	require.Equal(t, &expiry, userAIProviderKeyOAuthExpiry(oauthRow))
	require.True(t, userAIProviderKeyRefreshSupported(oauthRow))
	require.False(t, userAIProviderKeyReauthRequired(oauthRow))

	transientRow := oauthRow
	transientRow.OauthRefreshFailureReason = sql.NullString{String: "refresh failed with status 500", Valid: true}
	require.False(t, userAIProviderKeyReauthRequired(transientRow), "transient failures never prompt")

	terminalRow := database.UserAIProviderKey{
		APIKey:                    "stale-access-token",
		OauthRefreshFailureReason: sql.NullString{String: "invalid_grant", Valid: true},
	}
	require.Nil(t, userAIProviderKeyOAuthExpiry(terminalRow))
	require.False(t, userAIProviderKeyRefreshSupported(terminalRow))
	require.True(t, userAIProviderKeyReauthRequired(terminalRow), "terminal failure prompts exactly the re-auth signal")

	staticRow := database.UserAIProviderKey{APIKey: "static-key"}
	require.Nil(t, userAIProviderKeyOAuthExpiry(staticRow))
	require.False(t, userAIProviderKeyRefreshSupported(staticRow))
	require.False(t, userAIProviderKeyReauthRequired(staticRow))

	missingRow := database.UserAIProviderKey{}
	require.Nil(t, userAIProviderKeyOAuthExpiry(missingRow))
	require.False(t, userAIProviderKeyRefreshSupported(missingRow))
	require.False(t, userAIProviderKeyReauthRequired(missingRow))
}
