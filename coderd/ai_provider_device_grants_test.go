package coderd_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/testutil"
)

// All key material below is throwaway and invalid. These tests never touch
// live credentials, the keychain, or a real subscription provider.

// scriptedDeviceExchanger serves one scripted provider side per test.
type scriptedDeviceExchanger struct {
	mu            sync.Mutex
	userCode      string
	interval      int
	polls         []coderd.AIDevicePollOutcome
	exchangeToken string
}

func (f *scriptedDeviceExchanger) RequestDeviceCode(_ context.Context) (string, string, int, error) {
	return "test-device-auth-id", f.userCode, f.interval, nil
}

func (f *scriptedDeviceExchanger) PollDeviceToken(_ context.Context, _, _ string) (coderd.AIDevicePollOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.polls) == 0 {
		return coderd.AIDeviceGrantPending(), nil
	}
	next := f.polls[0]
	f.polls = f.polls[1:]
	return next, nil
}

func (f *scriptedDeviceExchanger) ExchangeAuthorizationCode(_ context.Context, _, _ string) (string, error) {
	return f.exchangeToken, nil
}

func createChatGPTProvider(t *testing.T, admin *codersdk.ExperimentalClient) codersdk.AIProvider {
	t.Helper()
	provider, err := admin.CreateAIProvider(testutil.Context(t, testutil.WaitLong), codersdk.CreateAIProviderRequest{
		Type:    codersdk.AIProviderTypeOpenAI,
		Name:    "chatgpt",
		Enabled: true,
		BaseURL: "https://chatgpt.example.com/backend-api/codex",
	})
	require.NoError(t, err)
	require.Equal(t, "chatgpt", provider.Name)
	return provider
}

func useScriptedDeviceFlow(api *coderd.API, fake *scriptedDeviceExchanger) {
	api.AIDeviceGrants.ClientFactory(func(_ database.AIProviderType, _ string) coderd.AIDeviceGrantExchanger {
		return fake
	})
}

func findKeyConfig(configs []codersdk.UserAIProviderKeyConfig, providerID uuid.UUID) *codersdk.UserAIProviderKeyConfig {
	for i := range configs {
		if configs[i].Provider.ID == providerID {
			return &configs[i]
		}
	}
	return nil
}

func TestUserAIDeviceGrants(t *testing.T) {
	t.Parallel()

	t.Run("AuthorizeThenSaveThroughUserKeys", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		adminClient, api := newChatClientWithAPI(t)
		firstUser := coderdtest.CreateFirstUser(t, adminClient.Client)
		memberClientRaw, _ := coderdtest.CreateAnotherUser(t, adminClient.Client, firstUser.OrganizationID)
		memberClient := codersdk.NewExperimentalClient(memberClientRaw)

		provider := createChatGPTProvider(t, adminClient)
		useScriptedDeviceFlow(api, &scriptedDeviceExchanger{
			userCode: "ABCD-1234",
			interval: 5,
			polls: []coderd.AIDevicePollOutcome{
				coderd.AIDeviceGrantPending(),
				coderd.AIDeviceGrantComplete("test-auth-code", "test-verifier"),
			},
			exchangeToken: "test-device-access-token",
		})

		configs, err := memberClient.ListUserAIProviderKeyConfigs(ctx, "me")
		require.NoError(t, err)
		cfg := findKeyConfig(configs, provider.ID)
		require.NotNil(t, cfg)
		require.True(t, cfg.DeviceFlowSupported, "chatgpt provider must advertise device flow")
		require.False(t, cfg.HasUserAPIKey)

		grant, err := memberClient.InitiateUserAIDeviceGrant(ctx, "me", provider.ID)
		require.NoError(t, err)
		require.Equal(t, "ABCD-1234", grant.UserCode)
		require.NotEmpty(t, grant.VerificationURI)
		require.NotContains(t, grant.VerificationURI, "test-device-access-token")
		require.GreaterOrEqual(t, grant.ExpiresIn, 890)
		require.LessOrEqual(t, grant.ExpiresIn, 15*60)
		require.Equal(t, 5, grant.PollInterval)
		require.True(t, grant.StoresAccessTokenOnly)
		require.False(t, grant.RefreshSupported)
		require.NotEmpty(t, grant.ReauthMessage)

		pending, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusPending, pending.Status)
		require.Empty(t, pending.APIKey, "pending polls must not carry key material")

		authorized, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.Status)
		require.Equal(t, "test-device-access-token", authorized.APIKey)
		require.True(t, authorized.StoresAccessTokenOnly)
		require.False(t, authorized.RefreshSupported)
		require.NotEmpty(t, authorized.ReauthMessage)

		// The dashboard saves through the existing user-keys endpoint;
		// save/remove behavior is preserved end to end.
		saved, err := memberClient.UpsertUserAIProviderKey(ctx, "me", provider.ID, codersdk.CreateUserAIProviderKeyRequest{APIKey: authorized.APIKey})
		require.NoError(t, err)
		require.True(t, saved.HasUserAPIKey)

		configs, err = memberClient.ListUserAIProviderKeyConfigs(ctx, "me")
		require.NoError(t, err)
		require.True(t, findKeyConfig(configs, provider.ID).HasUserAPIKey)

		require.NoError(t, memberClient.DeleteUserAIProviderKey(ctx, "me", provider.ID))
		configs, err = memberClient.ListUserAIProviderKeyConfigs(ctx, "me")
		require.NoError(t, err)
		require.False(t, findKeyConfig(configs, provider.ID).HasUserAPIKey)
	})

	t.Run("WrongUserIsolation", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		adminClient, api := newChatClientWithAPI(t)
		firstUser := coderdtest.CreateFirstUser(t, adminClient.Client)
		memberClientRaw, memberUser := coderdtest.CreateAnotherUser(t, adminClient.Client, firstUser.OrganizationID)
		otherClientRaw, _ := coderdtest.CreateAnotherUser(t, adminClient.Client, firstUser.OrganizationID)
		memberClient := codersdk.NewExperimentalClient(memberClientRaw)
		otherClient := codersdk.NewExperimentalClient(otherClientRaw)

		provider := createChatGPTProvider(t, adminClient)
		useScriptedDeviceFlow(api, &scriptedDeviceExchanger{
			userCode:      "ABCD-1234",
			interval:      5,
			exchangeToken: "test-device-access-token",
		})

		// Another user cannot mint into this user's slot.
		_, err := otherClient.InitiateUserAIDeviceGrant(ctx, memberUser.ID.String(), provider.ID)
		requireSDKError(t, err, http.StatusNotFound)

		grant, err := memberClient.InitiateUserAIDeviceGrant(ctx, "me", provider.ID)
		require.NoError(t, err)

		// Another user cannot poll or cancel the grant.
		_, err = otherClient.GetUserAIDeviceGrant(ctx, memberUser.ID.String(), provider.ID, grant.GrantID)
		requireSDKError(t, err, http.StatusNotFound)
		_, err = otherClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		requireSDKError(t, err, http.StatusNotFound)
		requireSDKError(t, otherClient.CancelUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID), http.StatusNotFound)

		// The owner's grant is untouched by the probes.
		pending, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusPending, pending.Status)
	})

	t.Run("Cancel", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		adminClient, api := newChatClientWithAPI(t)
		firstUser := coderdtest.CreateFirstUser(t, adminClient.Client)
		memberClientRaw, _ := coderdtest.CreateAnotherUser(t, adminClient.Client, firstUser.OrganizationID)
		memberClient := codersdk.NewExperimentalClient(memberClientRaw)

		provider := createChatGPTProvider(t, adminClient)
		useScriptedDeviceFlow(api, &scriptedDeviceExchanger{
			userCode:      "ABCD-1234",
			interval:      5,
			exchangeToken: "test-device-access-token",
		})

		grant, err := memberClient.InitiateUserAIDeviceGrant(ctx, "me", provider.ID)
		require.NoError(t, err)
		require.NoError(t, memberClient.CancelUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID))

		canceled, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusCanceled, canceled.Status)
		require.Empty(t, canceled.APIKey)

		requireSDKError(t,
			memberClient.CancelUserAIDeviceGrant(ctx, "me", provider.ID, uuid.New()),
			http.StatusNotFound,
		)
	})

	t.Run("ProviderExpired", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		adminClient, api := newChatClientWithAPI(t)
		firstUser := coderdtest.CreateFirstUser(t, adminClient.Client)
		memberClientRaw, _ := coderdtest.CreateAnotherUser(t, adminClient.Client, firstUser.OrganizationID)
		memberClient := codersdk.NewExperimentalClient(memberClientRaw)

		provider := createChatGPTProvider(t, adminClient)
		useScriptedDeviceFlow(api, &scriptedDeviceExchanger{
			userCode:      "ABCD-1234",
			interval:      5,
			polls:         []coderd.AIDevicePollOutcome{coderd.AIDeviceGrantExpired()},
			exchangeToken: "test-device-access-token",
		})

		grant, err := memberClient.InitiateUserAIDeviceGrant(ctx, "me", provider.ID)
		require.NoError(t, err)
		expired, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusExpired, expired.Status)
		require.Empty(t, expired.APIKey)
		require.NotEmpty(t, expired.ReauthMessage, "expired polls still surface the re-auth path")
	})

	t.Run("UnsupportedProvider", func(t *testing.T) {
		t.Parallel()

		ctx := testutil.Context(t, testutil.WaitLong)
		adminClient, api := newChatClientWithAPI(t)
		firstUser := coderdtest.CreateFirstUser(t, adminClient.Client)
		memberClientRaw, _ := coderdtest.CreateAnotherUser(t, adminClient.Client, firstUser.OrganizationID)
		memberClient := codersdk.NewExperimentalClient(memberClientRaw)

		plain, err := adminClient.CreateAIProvider(ctx, codersdk.CreateAIProviderRequest{
			Type:    codersdk.AIProviderTypeOpenAI,
			Name:    "plain-api-keys",
			Enabled: true,
			BaseURL: "https://api.openai.example.com/v1",
		})
		require.NoError(t, err)
		useScriptedDeviceFlow(api, &scriptedDeviceExchanger{userCode: "ABCD-1234", interval: 5})

		configs, err := memberClient.ListUserAIProviderKeyConfigs(ctx, "me")
		require.NoError(t, err)
		require.False(t, findKeyConfig(configs, plain.ID).DeviceFlowSupported)

		sdkErr := requireSDKError(t, func() error {
			_, err := memberClient.InitiateUserAIDeviceGrant(ctx, "me", plain.ID)
			return err
		}(), http.StatusUnprocessableEntity)
		require.Equal(t, "Device sign-in is not supported for this provider.", sdkErr.Message)
	})
}
