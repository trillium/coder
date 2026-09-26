package coderd_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
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
	exchangeGrant coderd.AIDeviceTokenGrant
}

func (f *scriptedDeviceExchanger) RequestDeviceCode(_ context.Context) (deviceAuthID, userCode string, intervalSeconds int, err error) {
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

// scriptedAccessJWT mints an unsigned throwaway JWT carrying the ChatGPT
// account claim. It is invalid as a credential and never leaves the test.
func scriptedAccessJWT(t testing.TB, accountID string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
}

// scriptedTokenGrant builds the full exchange shape around a throwaway JWT
// access token plus a static refresh token.
func scriptedTokenGrant(t testing.TB, accountID string) coderd.AIDeviceTokenGrant {
	t.Helper()
	return coderd.AIDeviceTokenGrant{
		AccessToken:  scriptedAccessJWT(t, accountID),
		RefreshToken: "test-device-refresh-token", // #nosec G101 -- test fixture, not a credential.
		ExpiresIn:    3600,
	}
}

func (f *scriptedDeviceExchanger) ExchangeAuthorizationCode(_ context.Context, _, _ string) (coderd.AIDeviceTokenGrant, error) {
	return f.exchangeGrant, nil
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
			exchangeGrant: scriptedTokenGrant(t, "acct-outer-test"),
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
		require.NotContains(t, grant.VerificationURI, "eyJhbGciOiJub25lIn0")
		require.GreaterOrEqual(t, grant.ExpiresIn, 890)
		require.LessOrEqual(t, grant.ExpiresIn, 15*60)
		require.Equal(t, 5, grant.PollInterval)
		require.False(t, grant.StoresAccessTokenOnly)
		require.True(t, grant.RefreshSupported)
		require.NotEmpty(t, grant.ReauthMessage)

		pending, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusPending, pending.Status)
		require.Empty(t, pending.APIKey, "pending polls must not carry key material")

		authorized, err := memberClient.GetUserAIDeviceGrant(ctx, "me", provider.ID, grant.GrantID)
		require.NoError(t, err)
		require.Equal(t, codersdk.AIDeviceGrantStatusAuthorized, authorized.Status)
		require.Empty(t, authorized.APIKey, "server-persisted grants carry no key material")
		require.False(t, authorized.StoresAccessTokenOnly)
		require.True(t, authorized.RefreshSupported)
		require.NotEmpty(t, authorized.ReauthMessage)
		authorizedJSON, err := json.Marshal(authorized) // #nosec G117 -- test asserts the refresh token is absent from this payload.
		require.NoError(t, err)
		require.NotContains(t, string(authorizedJSON), "test-device-refresh-token", "refresh token never leaves the server")

		// Server-side custody: the credential reached the user key row on
		// approval with no dashboard PUT.
		configs, err = memberClient.ListUserAIProviderKeyConfigs(ctx, "me")
		require.NoError(t, err)
		cfg = findKeyConfig(configs, provider.ID)
		require.NotNil(t, cfg)
		require.True(t, cfg.HasUserAPIKey)
		require.NotNil(t, cfg.OAuthExpiry, "expiry visible on the keys page")
		require.WithinDuration(t, time.Now().Add(time.Hour), *cfg.OAuthExpiry, 5*time.Minute)
		require.True(t, cfg.RefreshSupported, "refresh state advertised once the refresher ships")
		require.False(t, cfg.ReauthRequired)

		memberUser, err := memberClientRaw.User(ctx, "me")
		require.NoError(t, err)
		row, err := api.Database.GetUserAIProviderKeyByProviderID(dbauthz.AsSystemRestricted(ctx), database.GetUserAIProviderKeyByProviderIDParams{
			UserID:       memberUser.ID,
			AIProviderID: provider.ID,
		})
		require.NoError(t, err)
		require.Equal(t, scriptedAccessJWT(t, "acct-outer-test"), row.APIKey)
		require.True(t, row.OAuthRefreshToken.Valid, "refresh token persisted server-side")
		require.Equal(t, "test-device-refresh-token", row.OAuthRefreshToken.String)
		require.True(t, row.OAuthExpiry.Valid, "expiry persisted from expires_in")
		require.WithinDuration(t, time.Now().Add(time.Hour), row.OAuthExpiry.Time, 5*time.Minute)
		require.True(t, row.AccountID.Valid)
		require.Equal(t, "acct-outer-test", row.AccountID.String)

		// A pasted/static replace still saves through the user-keys
		// endpoint and drops OAuth state by definition.
		saved, err := memberClient.UpsertUserAIProviderKey(ctx, "me", provider.ID, codersdk.CreateUserAIProviderKeyRequest{APIKey: scriptedAccessJWT(t, "acct-static-replace")})
		require.NoError(t, err)
		require.True(t, saved.HasUserAPIKey)
		row, err = api.Database.GetUserAIProviderKeyByProviderID(dbauthz.AsSystemRestricted(ctx), database.GetUserAIProviderKeyByProviderIDParams{
			UserID:       memberUser.ID,
			AIProviderID: provider.ID,
		})
		require.NoError(t, err)
		require.False(t, row.OAuthRefreshToken.Valid, "static replace clears OAuth state")
		require.False(t, row.OAuthExpiry.Valid)

		configs, err = memberClient.ListUserAIProviderKeyConfigs(ctx, "me")
		require.NoError(t, err)
		cfg = findKeyConfig(configs, provider.ID)
		require.NotNil(t, cfg)
		require.True(t, cfg.HasUserAPIKey)
		require.Nil(t, cfg.OAuthExpiry)
		require.False(t, cfg.RefreshSupported)
		require.False(t, cfg.ReauthRequired, "static keys never prompt re-auth")

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
			exchangeGrant: scriptedTokenGrant(t, "acct-outer-test"),
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
			exchangeGrant: scriptedTokenGrant(t, "acct-outer-test"),
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
			exchangeGrant: scriptedTokenGrant(t, "acct-outer-test"),
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
