package coderd

import (
	"context"
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

// fakeAIDeviceExchanger scripts one grant's provider side.
type fakeAIDeviceExchanger struct {
	mu            sync.Mutex
	deviceAuthID  string
	userCode      string
	interval      int
	requestErr    error
	polls         []AIDevicePollOutcome
	pollErr       error
	exchangeToken string
	exchangeErr   error
	exchanges     int
	pollCalls     int
}

func (f *fakeAIDeviceExchanger) RequestDeviceCode(_ context.Context) (string, string, int, error) {
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

func (f *fakeAIDeviceExchanger) ExchangeAuthorizationCode(_ context.Context, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges++
	if f.exchangeErr != nil {
		return "", f.exchangeErr
	}
	return f.exchangeToken, nil
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
		deviceAuthID:  "test-device-auth-id",
		userCode:      "ABCD-1234",
		interval:      5,
		polls:         []AIDevicePollOutcome{AIDeviceGrantPending(), AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeToken: "test-device-access-token",
	}
	manager, clock := testDeviceManager(t, fake)

	grant, err := manager.initiate(ctx, owner, provider.ID, provider)
	require.NoError(t, err)
	require.Equal(t, codersdk.AIDeviceGrantStatusPending, grant.status)
	initResp := aiDeviceGrantInitiateResponse(grant, clock.Now())
	require.Equal(t, "ABCD-1234", initResp.UserCode)
	require.Contains(t, initResp.VerificationURI, "auth.openai.com")
	require.NotContains(t, initResp.VerificationURI, "test-device-access-token")
	require.True(t, initResp.StoresAccessTokenOnly)
	require.False(t, initResp.RefreshSupported)
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
	pollResp := aiDeviceGrantPollResponse(authorized, clock.Now())
	require.Equal(t, "test-device-access-token", pollResp.APIKey)
	require.True(t, pollResp.StoresAccessTokenOnly)
	require.False(t, pollResp.RefreshSupported)
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
		deviceAuthID:  "test-device-auth-id",
		userCode:      "ABCD-1234",
		interval:      5,
		polls:         []AIDevicePollOutcome{AIDeviceGrantComplete("test-auth-code", "test-verifier")},
		exchangeToken: "test-device-access-token",
		exchangeErr:   errTestDeviceExchange,
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
