package chatd //nolint:testpackage // Exercises unexported refresh helpers.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/x/chatd/chaterror"
	"github.com/coder/coder/v2/testutil"
	"github.com/coder/quartz"
)

// All key material below is throwaway and invalid. These tests never touch
// live credentials or a real subscription provider.

// refreshTestJWT mints an unsigned throwaway JWT carrying the ChatGPT
// account claim.
func refreshTestJWT(t testing.TB, accountID string) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
}

func refreshTestProvider() database.AIProvider {
	return database.AIProvider{
		ID:      uuid.New(),
		Type:    database.AIProviderTypeOpenai,
		Name:    "chatgpt",
		Enabled: true,
	}
}

func refreshTestKey(userID, providerID uuid.UUID, accessToken, refreshToken string, expiry time.Time) database.UserAIProviderKey {
	key := database.UserAIProviderKey{
		ID:           uuid.New(),
		UserID:       userID,
		AIProviderID: providerID,
		APIKey:       accessToken,
		CreatedAt:    time.Now().Add(-time.Hour),
		UpdatedAt:    time.Now().Add(-time.Hour),
	}
	if refreshToken != "" {
		key.OAuthRefreshToken = sql.NullString{String: refreshToken, Valid: true}
	}
	if !expiry.IsZero() {
		key.OAuthExpiry = sql.NullTime{Time: expiry, Valid: true}
	}
	return key
}

// fakeOAuthKeyStore is an in-memory userAIProviderKeyOAuthStore with real
// lease semantics: acquiring while a live lease holds fails with the
// check-violation the SQL function raises.
type fakeOAuthKeyStore struct {
	mu           sync.Mutex
	rows         map[uuid.UUID]map[uuid.UUID]database.UserAIProviderKey
	now          time.Time
	acquireCalls int
	updateCalls  int
	releaseCalls int
	lastUpdate   database.UpdateUserAIProviderKeyOAuthParams
}

func newFakeOAuthKeyStore(now time.Time) *fakeOAuthKeyStore {
	return &fakeOAuthKeyStore{
		rows: map[uuid.UUID]map[uuid.UUID]database.UserAIProviderKey{},
		now:  now,
	}
}

func (f *fakeOAuthKeyStore) seed(key database.UserAIProviderKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	byUser, ok := f.rows[key.UserID]
	if !ok {
		byUser = map[uuid.UUID]database.UserAIProviderKey{}
		f.rows[key.UserID] = byUser
	}
	byUser[key.AIProviderID] = key
}

func (f *fakeOAuthKeyStore) get(userID, providerID uuid.UUID) (database.UserAIProviderKey, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[userID][providerID]
	return row, ok
}

func (f *fakeOAuthKeyStore) GetUserAIProviderKeyByProviderID(_ context.Context, params database.GetUserAIProviderKeyByProviderIDParams) (database.UserAIProviderKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[params.UserID][params.AIProviderID]
	if !ok {
		return database.UserAIProviderKey{}, sql.ErrNoRows
	}
	return row, nil
}

func (f *fakeOAuthKeyStore) AcquireUserAIProviderKeyRefreshLease(_ context.Context, params database.AcquireUserAIProviderKeyRefreshLeaseParams) (database.UserAIProviderKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls++
	row, ok := f.rows[params.UserID][params.AIProviderID]
	if !ok {
		return database.UserAIProviderKey{}, sql.ErrNoRows
	}
	if row.RefreshLeaseExpiresAt.Valid && row.RefreshLeaseExpiresAt.Time.After(f.now) {
		return database.UserAIProviderKey{}, &pq.Error{Code: "23514", Constraint: string(userAIProviderKeyActiveLeaseConstraint)}
	}
	row.RefreshLeaseExpiresAt = sql.NullTime{Time: f.now.Add(time.Duration(params.TimeoutMs) * time.Millisecond), Valid: true}
	f.rows[params.UserID][params.AIProviderID] = row
	return row, nil
}

func (f *fakeOAuthKeyStore) ReleaseUserAIProviderKeyRefreshLease(_ context.Context, params database.ReleaseUserAIProviderKeyRefreshLeaseParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	row := f.rows[params.UserID][params.AIProviderID]
	if row.RefreshLeaseExpiresAt.Valid && row.RefreshLeaseExpiresAt.Time.Equal(params.RefreshLeaseExpiresAt.Time) {
		row.RefreshLeaseExpiresAt = sql.NullTime{}
		f.rows[params.UserID][params.AIProviderID] = row
	}
	return nil
}

func (f *fakeOAuthKeyStore) UpdateUserAIProviderKeyOAuth(_ context.Context, params database.UpdateUserAIProviderKeyOAuthParams) (database.UserAIProviderKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	f.lastUpdate = params
	row := f.rows[params.UserID][params.AIProviderID]
	if params.RefreshLeaseExpiresAt.Valid {
		if !row.RefreshLeaseExpiresAt.Valid || !row.RefreshLeaseExpiresAt.Time.Equal(params.RefreshLeaseExpiresAt.Time) {
			return database.UserAIProviderKey{}, sql.ErrNoRows
		}
	}
	row.APIKey = params.APIKey
	row.ApiKeyKeyID = params.ApiKeyKeyID
	row.OAuthRefreshToken = params.OAuthRefreshToken
	row.OAuthRefreshTokenKeyID = params.OAuthRefreshTokenKeyID
	row.OAuthExpiry = params.OAuthExpiry
	row.AccountID = params.AccountID
	row.OAuthExtra = params.OAuthExtra
	row.OauthRefreshFailureReason = params.OauthRefreshFailureReason
	row.UpdatedAt = f.now
	f.rows[params.UserID][params.AIProviderID] = row
	return row, nil
}

// scriptRefreshEndpoint serves one scripted token endpoint. Each handler
// controls status, body, and counts hits.
type scriptRefreshEndpoint struct {
	t      testing.TB
	server *httptest.Server
	hits   atomic.Int32
	mu     sync.Mutex
	script []scriptedRefreshResponse
}

type scriptedRefreshResponse struct {
	status int
	body   string
}

func newScriptRefreshEndpoint(t testing.TB, script ...scriptedRefreshResponse) *scriptRefreshEndpoint {
	t.Helper()
	e := &scriptRefreshEndpoint{t: t, script: script}
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.t.Helper()
		hit := int(e.hits.Add(1))
		require.Equal(e.t, http.MethodPost, r.Method)
		require.NoError(e.t, r.ParseForm())
		require.Equal(e.t, "refresh_token", r.Form.Get("grant_type"))
		require.NotEmpty(e.t, r.Form.Get("refresh_token"))
		require.NotEmpty(e.t, r.Form.Get("client_id"))
		e.mu.Lock()
		resp := e.script[len(e.script)-1]
		if hit <= len(e.script) {
			resp = e.script[hit-1]
		}
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(e.server.Close)
	return e
}

func refreshSuccessBody(t testing.TB, accessToken, refreshToken string, expiresIn int) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    expiresIn,
	})
	require.NoError(t, err)
	return string(raw)
}

func refreshTestFixture(t testing.TB, now time.Time) (context.Context, *fakeOAuthKeyStore, database.AIProvider, database.UserAIProviderKey) {
	t.Helper()
	ctx := testutil.Context(t, testutil.WaitLong)
	provider := refreshTestProvider()
	key := refreshTestKey(uuid.New(), provider.ID, refreshTestJWT(t, "acct-old"), "refresh-old", now.Add(-time.Minute))
	store := newFakeOAuthKeyStore(now)
	store.seed(key)
	return ctx, store, provider, key
}

func TestAIProviderOAuthNeedsRefresh(t *testing.T) {
	t.Parallel()
	now := time.Now()
	userID, providerID := uuid.New(), uuid.New()

	static := refreshTestKey(userID, providerID, "static-key", "", time.Time{})
	require.False(t, aiProviderOAuthNeedsRefresh(static, now), "NULL refresh is today's behavior")

	unknownExpiry := refreshTestKey(userID, providerID, "token", "refresh", time.Time{})
	require.False(t, aiProviderOAuthNeedsRefresh(unknownExpiry, now), "NULL expiry attempts no refresh")

	fresh := refreshTestKey(userID, providerID, "token", "refresh", now.Add(time.Hour))
	require.False(t, aiProviderOAuthNeedsRefresh(fresh, now))

	due := refreshTestKey(userID, providerID, "token", "refresh", now.Add(4*time.Minute))
	require.True(t, aiProviderOAuthNeedsRefresh(due, now), "inside the 5-minute window")

	expired := refreshTestKey(userID, providerID, "token", "refresh", now.Add(-time.Minute))
	require.True(t, aiProviderOAuthNeedsRefresh(expired, now))

	blank := refreshTestKey(userID, providerID, "token", "  ", now.Add(-time.Minute))
	blank.OAuthRefreshToken = sql.NullString{String: "  ", Valid: true}
	require.False(t, aiProviderOAuthNeedsRefresh(blank, now), "blank refresh is not OAuth state")
}

func TestAIProviderOAuthRefreshSuccess(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	clock.Set(now)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-new"), "refresh-new", 3600)},
	)
	client := endpoint.server.Client()

	refreshed, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t),
		aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}, provider, key)
	require.NoError(t, err)
	require.Equal(t, int32(1), endpoint.hits.Load(), "one exchange, no retry on success")

	// Persist-before-use: the store holds the rotated triple and the
	// returned row carries it.
	stored, ok := store.get(key.UserID, key.AIProviderID)
	require.True(t, ok)
	require.Equal(t, refreshed.APIKey, stored.APIKey)
	require.Contains(t, refreshed.APIKey, "eyJhbGciOiJub25lIn0")
	require.Equal(t, "refresh-new", stored.OAuthRefreshToken.String)
	require.True(t, stored.OAuthExpiry.Valid)
	require.WithinDuration(t, now.Add(time.Hour), stored.OAuthExpiry.Time, 5*time.Second)
	require.Equal(t, "acct-new", stored.AccountID.String, "account re-derived from the new JWT")
	require.False(t, stored.OauthRefreshFailureReason.Valid, "success clears failure bookkeeping")
	require.False(t, stored.RefreshLeaseExpiresAt.Valid, "lease released")
	require.Equal(t, 1, store.updateCalls)
}

func TestAIProviderOAuthRefreshTwoRotations(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	clock.Set(now)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-r1"), "refresh-r1", 3600)},
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-r2"), "refresh-r2", 3600)},
	)
	client := endpoint.server.Client()
	config := aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}

	first, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t), config, provider, key)
	require.NoError(t, err)
	require.Equal(t, "refresh-r1", first.OAuthRefreshToken.String)

	// The second rotation chains off the first rotation's refresh token:
	// indefinitely is proven as >=2 successive rotations.
	secondSnapshot, err := store.GetUserAIProviderKeyByProviderID(ctx, database.GetUserAIProviderKeyByProviderIDParams{
		UserID: key.UserID, AIProviderID: key.AIProviderID,
	})
	require.NoError(t, err)
	secondSnapshot.OAuthExpiry = sql.NullTime{Time: now.Add(-time.Second), Valid: true}
	second, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t), config, provider, secondSnapshot)
	require.NoError(t, err)
	require.Equal(t, "refresh-r2", second.OAuthRefreshToken.String)
	require.Equal(t, int32(2), endpoint.hits.Load())
}

func TestAIProviderOAuthRefreshTerminalInvalidGrant(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 400, body: `{"error": "invalid_grant", "error_description": "refresh token revoked"}`},
	)
	client := endpoint.server.Client()

	updated, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t),
		aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}, provider, key)
	require.Error(t, err)
	require.Equal(t, int32(1), endpoint.hits.Load(), "terminal failures never retry")

	// Typed re-auth signal, never silent breakage.
	var reauth *chaterror.ReauthRequiredError
	require.ErrorAs(t, err, &reauth)
	require.Equal(t, provider.ID, reauth.ProviderID)
	require.Equal(t, "chatgpt", reauth.ProviderName)
	require.Contains(t, reauth.DeviceGrantInitiatePath(key.UserID), provider.ID.String())

	// Down-path: refresh NULLed, saved key kept, reason recorded.
	require.False(t, updated.OAuthRefreshToken.Valid, "dead refresh token is cleared")
	require.False(t, updated.OAuthRefreshTokenKeyID.Valid)
	require.Equal(t, key.APIKey, updated.APIKey, "saved key is kept")
	require.True(t, updated.OauthRefreshFailureReason.Valid)
	require.Contains(t, updated.OauthRefreshFailureReason.String, "invalid_grant")
	stored, ok := store.get(key.UserID, key.AIProviderID)
	require.True(t, ok)
	require.False(t, stored.OAuthRefreshToken.Valid)
	require.False(t, aiProviderOAuthNeedsRefresh(stored, now), "down-path row returns to static behavior")
}

func TestAIProviderOAuthRefreshTransientRetriesBounded(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 500, body: `upstream blip`},
		scriptedRefreshResponse{status: 502, body: `still blipping`},
		scriptedRefreshResponse{status: 503, body: `always blipping`},
		scriptedRefreshResponse{status: 500, body: `never recovers`},
	)
	client := endpoint.server.Client()

	updated, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t),
		aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}, provider, key)
	require.Error(t, err)
	var reauth *chaterror.ReauthRequiredError
	require.NotErrorAs(t, err, &reauth, "transient failures never prompt re-auth")
	require.Equal(t, int32(aiProviderOAuthMaxAttempts), endpoint.hits.Load(), "transient retries are bounded")

	// Credential preserved for retry: same tokens, reason recorded.
	require.Equal(t, "refresh-old", updated.OAuthRefreshToken.String)
	require.Equal(t, key.APIKey, updated.APIKey)
	require.True(t, updated.OauthRefreshFailureReason.Valid, "transient reason recorded, no prompt")
	require.True(t, aiProviderOAuthNeedsRefresh(updated, now.Add(time.Hour)), "credential preserved for retry")
}

func TestAIProviderOAuthRefreshTransientThenSuccess(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	clock.Set(now)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 500, body: `blip`},
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-retry"), "refresh-retry", 3600)},
	)
	client := endpoint.server.Client()

	refreshed, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t),
		aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}, provider, key)
	require.NoError(t, err)
	require.Equal(t, int32(2), endpoint.hits.Load())
	require.Equal(t, "refresh-retry", refreshed.OAuthRefreshToken.String)
	require.False(t, refreshed.OauthRefreshFailureReason.Valid)
}

func TestAIProviderOAuthRefreshLeaseContentionTimeout(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-x"), "refresh-x", 3600)},
	)
	client := endpoint.server.Client()

	// Another replica holds the lease past our deadline.
	_, err := store.AcquireUserAIProviderKeyRefreshLease(context.Background(), database.AcquireUserAIProviderKeyRefreshLeaseParams{
		AIProviderID: key.AIProviderID, UserID: key.UserID, TimeoutMs: 60_000,
	})
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, testutil.IntervalMedium)
	defer cancel()
	_, err = doRefreshUserAIProviderKeyOAuth(waitCtx, store, client, clock, testutil.Logger(t),
		aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}, provider, key)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int32(0), endpoint.hits.Load(), "no exchange without the lease")
}

func TestAIProviderOAuthRefreshObservesPeerRefresh(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx, store, provider, key := refreshTestFixture(t, now)
	clock := quartz.NewMock(t)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-winner"), "refresh-winner", 3600)},
	)
	client := endpoint.server.Client()
	config := aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}

	// A racing replica refreshes first; our snapshot is stale.
	winner, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t), config, provider, key)
	require.NoError(t, err)
	require.Equal(t, "refresh-winner", winner.OAuthRefreshToken.String)

	observed, err := doRefreshUserAIProviderKeyOAuth(ctx, store, client, clock, testutil.Logger(t), config, provider, key)
	require.NoError(t, err)
	require.Equal(t, "refresh-winner", observed.OAuthRefreshToken.String)
	require.Equal(t, int32(1), endpoint.hits.Load(), "stale snapshot reuses the peer's refresh, no second exchange")
}

func TestAIProviderOAuthRefreshSingleflightCollapse(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ctx := testutil.Context(t, testutil.WaitLong)
	provider := refreshTestProvider()
	key := refreshTestKey(uuid.New(), provider.ID, refreshTestJWT(t, "acct-old"), "refresh-old", now.Add(-time.Minute))
	clock := quartz.NewMock(t)
	clock.Set(now)
	endpoint := newScriptRefreshEndpoint(t,
		scriptedRefreshResponse{status: 200, body: refreshSuccessBody(t, refreshTestJWT(t, "acct-shared"), "refresh-shared", 3600)},
	)

	ctrl := gomock.NewController(t)
	mockStore := dbmock.NewMockStore(ctrl)
	leased := key
	leased.RefreshLeaseExpiresAt = sql.NullTime{Time: now.Add(time.Minute), Valid: true}
	mockStore.EXPECT().AcquireUserAIProviderKeyRefreshLease(gomock.Any(), gomock.Any()).Return(leased, nil).Times(1)
	mockStore.EXPECT().UpdateUserAIProviderKeyOAuth(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, params database.UpdateUserAIProviderKeyOAuthParams) (database.UserAIProviderKey, error) {
			leased.APIKey = params.APIKey
			leased.OAuthRefreshToken = params.OAuthRefreshToken
			leased.OAuthExpiry = params.OAuthExpiry
			leased.AccountID = params.AccountID
			return leased, nil
		}).Times(1)
	mockStore.EXPECT().ReleaseUserAIProviderKeyRefreshLease(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	server := &Server{
		db:                     mockStore,
		logger:                 testutil.Logger(t),
		clock:                  clock,
		oauthRefreshHTTPClient: endpoint.server.Client(),
	}
	// Point the (unexported) endpoint at the scripted server by injecting
	// the provider through the gate with a test-only config override.
	server.oauthRefreshTestConfig = &aiProviderOAuthConfig{clientID: "test-client", tokenURL: endpoint.server.URL}

	const callers = 10
	var wg sync.WaitGroup
	results := make([]database.UserAIProviderKey, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = server.refreshUserAIProviderKeyIfNeeded(ctx, provider, key)
		}(i)
	}
	wg.Wait()
	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, "refresh-shared", results[i].OAuthRefreshToken.String)
	}
	require.Equal(t, int32(1), endpoint.hits.Load(), "N concurrent callers collapse into one exchange")
}
