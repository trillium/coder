package chatd

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/x/chatd/chaterror"
	"github.com/coder/quartz"
	"github.com/coder/retry"
)

const (
	// aiProviderOAuthRefreshTimeout bounds one refresh exchange, per the
	// portable OAuth spec (DEFAULT_OAUTH_REFRESH_TIMEOUT_MS). Transient
	// retries share this budget: bounded attempts, no unbounded loop.
	aiProviderOAuthRefreshTimeout = 15 * time.Second

	// aiProviderOAuthMinimumValidity is the pre-expiry window: a token
	// expiring within this window is refreshed lazily per request, per
	// the spec (DEFAULT_OAUTH_MINIMUM_VALIDITY_MS).
	aiProviderOAuthMinimumValidity = 5 * time.Minute

	// aiProviderOAuthMaxAttempts bounds transient refresh retries within
	// one request. Terminal failures (invalid_grant) never retry.
	aiProviderOAuthMaxAttempts = 3

	// aiProviderOAuthRetryInitialBackoff is the starting wait between
	// transient refresh attempts.
	aiProviderOAuthRetryInitialBackoff = 250 * time.Millisecond

	// aiProviderOAuthRetryMaxBackoff caps the wait between transient
	// refresh attempts.
	aiProviderOAuthRetryMaxBackoff = 2 * time.Second

	// aiProviderOAuthLeaseTimeoutMS is the refresh lease hold: long enough
	// for the 15s exchange plus the persist, short enough that a crashed
	// holder blocks peers only briefly.
	aiProviderOAuthLeaseTimeoutMS = 60_000

	// aiProviderOAuthFailureReasonLimit caps failure text written to the
	// row, mirroring the externalauth precedent.
	aiProviderOAuthFailureReasonLimit = 400

	// userAIProviderKeyActiveLeaseConstraint indicates the lease could not
	// be acquired because something else holds an active lease. It mirrors
	// externalAuthLinkActiveLeaseConstraint: a RAISE EXCEPTION name, not a
	// table constraint.
	userAIProviderKeyActiveLeaseConstraint database.CheckConstraint = "user_ai_provider_key_active_lease"
)

// aiProviderOAuthConfig is the provider-generic shape for one subscription
// provider's refresh endpoint. ChatGPT is the first entry. Source of truth
// for these values is coderd/ai_provider_device_grants.go
// (aiDeviceGrantConfigForProvider); keep the two in sync. chatd cannot
// import the coderd package (it would cycle), so the three fork-only
// constants are repeated here.
type aiProviderOAuthConfig struct {
	clientID string
	tokenURL string
}

func aiProviderOAuthConfigForProvider(providerType database.AIProviderType, name string) (aiProviderOAuthConfig, bool) {
	if providerType == database.AIProviderTypeOpenai && name == "chatgpt" {
		return aiProviderOAuthConfig{
			clientID: "app_EMoamEEZ73f0CkXaXp7hrann",
			tokenURL: "https://auth.openai.com/oauth/token",
		}, true
	}
	return aiProviderOAuthConfig{}, false
}

// userAIProviderKeyOAuthStore is the narrow database surface the OAuth
// refresher needs. database.Store (and the dbcrypt wrapper) satisfy it;
// tests script it.
type userAIProviderKeyOAuthStore interface {
	GetUserAIProviderKeyByProviderID(ctx context.Context, params database.GetUserAIProviderKeyByProviderIDParams) (database.UserAIProviderKey, error)
	AcquireUserAIProviderKeyRefreshLease(ctx context.Context, params database.AcquireUserAIProviderKeyRefreshLeaseParams) (database.UserAIProviderKey, error)
	ReleaseUserAIProviderKeyRefreshLease(ctx context.Context, params database.ReleaseUserAIProviderKeyRefreshLeaseParams) error
	UpdateUserAIProviderKeyOAuth(ctx context.Context, params database.UpdateUserAIProviderKeyOAuthParams) (database.UserAIProviderKey, error)
}

// aiProviderOAuthNeedsRefresh reports whether the row carries OAuth state
// due for a lazy refresh: a present refresh token plus a known expiry
// inside the validity window. NULL refresh or NULL expiry means today's
// static-secret behavior: no refresh attempted.
func aiProviderOAuthNeedsRefresh(key database.UserAIProviderKey, now time.Time) bool {
	if !key.OAuthRefreshToken.Valid || strings.TrimSpace(key.OAuthRefreshToken.String) == "" {
		return false
	}
	if !key.OAuthExpiry.Valid {
		return false
	}
	return !now.Add(aiProviderOAuthMinimumValidity).Before(key.OAuthExpiry.Time)
}

// refreshUserAIProviderKeyIfNeeded is the lazy per-request refresh gate.
// Rows without OAuth state pass through unchanged (today's behavior). Rows
// due for refresh go through the lease + singleflight path. There is no
// background sweeper: refresh is lazy per-request only.
func (p *Server) refreshUserAIProviderKeyIfNeeded(
	ctx context.Context,
	provider database.AIProvider,
	key database.UserAIProviderKey,
) (database.UserAIProviderKey, error) {
	now := time.Now()
	if p.clock != nil {
		now = p.clock.Now()
	}
	if !aiProviderOAuthNeedsRefresh(key, now) {
		return key, nil
	}
	config, ok := aiProviderOAuthConfigForProvider(provider.Type, provider.Name)
	if p.oauthRefreshTestConfig != nil {
		// Test seam: scripted token endpoint.
		config, ok = *p.oauthRefreshTestConfig, true
	}
	if !ok {
		return key, nil
	}
	refreshKey := provider.ID.String() + ":" + key.UserID.String()
	ch := p.aiProviderOAuthRefreshGroup.DoChan(refreshKey, func() (any, error) {
		// Detach so a canceled request does not cancel peers sharing the
		// refresh, mirroring the externalauth refresher. The deadline
		// covers the exchange plus the persist.
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aiProviderOAuthRefreshTimeout+10*time.Second)
		defer cancel()
		return doRefreshUserAIProviderKeyOAuth(refreshCtx, p.db, p.oauthRefreshHTTPClientOrDefault(), p.clock, p.logger, config, provider, key)
	})
	select {
	case result := <-ch:
		if refreshed, ok := result.Val.(database.UserAIProviderKey); ok {
			return refreshed, result.Err
		} else if result.Err == nil {
			return key, xerrors.Errorf("got invalid type from AI provider token refresh: %T", result.Val)
		}
		return key, result.Err
	case <-ctx.Done():
		return key, ctx.Err()
	}
}

// oauthRefreshHTTPClientOrDefault returns the configured refresh client,
// defaulting to a 15s-timeout client. Tests override the field with an
// httptest-backed client.
func (p *Server) oauthRefreshHTTPClientOrDefault() *http.Client {
	if p.oauthRefreshHTTPClient != nil {
		return p.oauthRefreshHTTPClient
	}
	return &http.Client{Timeout: aiProviderOAuthRefreshTimeout}
}

// aiProviderOAuthRefreshedTokens is one successful exchange: the rotated
// triple plus the re-derived account. An empty RefreshToken means the
// provider issued access-only material: the stored refresh is kept.
type aiProviderOAuthRefreshedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	AccountID    string
}

// doRefreshUserAIProviderKeyOAuth acquires the row lease and refreshes the
// credential, persisting the rotated triple BEFORE use. Transient failures
// retry bounded with no prompt and preserve the credential; a terminal
// failure (invalid_grant) records the reason, NULLs the refresh token
// (down-path to static-secret behavior), keeps the saved key, and returns
// a typed re-auth error. It never falls back to another credential.
func doRefreshUserAIProviderKeyOAuth(
	ctx context.Context,
	store userAIProviderKeyOAuthStore,
	httpClient *http.Client,
	clock quartz.Clock,
	logger slog.Logger,
	config aiProviderOAuthConfig,
	provider database.AIProvider,
	key database.UserAIProviderKey,
) (database.UserAIProviderKey, error) {
	leased, err := acquireUserAIProviderKeyLease(ctx, store, key)
	if err != nil {
		return key, err
	}
	// The lease is always released; the persist-before-use update lands
	// before this runs.
	defer func() {
		if !leased.RefreshLeaseExpiresAt.Valid {
			return
		}
		if releaseErr := store.ReleaseUserAIProviderKeyRefreshLease(ctx, database.ReleaseUserAIProviderKeyRefreshLeaseParams{
			UserID:                key.UserID,
			AIProviderID:          key.AIProviderID,
			RefreshLeaseExpiresAt: leased.RefreshLeaseExpiresAt,
		}); releaseErr != nil {
			logger.Warn(ctx, "failed to release AI provider key refresh lease",
				slog.F("user_id", key.UserID), slog.F("ai_provider_id", key.AIProviderID),
				slog.Error(releaseErr))
		}
	}()

	// Another replica refreshed while we waited for the lease: use its row.
	if oauthRefreshTokenChanged(key, leased) {
		if leased.OauthRefreshFailureReason.Valid && strings.TrimSpace(leased.OauthRefreshFailureReason.String) != "" && !leased.OAuthRefreshToken.Valid {
			return leased, reauthRequiredError(provider, leased.OauthRefreshFailureReason.String)
		}
		return leased, nil
	}

	tokens, err := exchangeUserAIProviderRefreshToken(ctx, httpClient, clock, config, leased.OAuthRefreshToken.String)
	if err != nil {
		var terminal *oauthTerminalRefreshError
		if xerrors.As(err, &terminal) {
			return recordUserAIProviderTerminalFailure(ctx, store, provider, leased, terminal.reason)
		}
		return recordUserAIProviderTransientFailure(ctx, store, provider, leased, logger, err)
	}

	return persistUserAIProviderRefreshedTokens(ctx, store, provider, leased, tokens)
}

// oauthRefreshTokenChanged reports whether the leased row's refresh state
// moved since the gate snapshot: another replica persisted meanwhile.
func oauthRefreshTokenChanged(snapshot, leased database.UserAIProviderKey) bool {
	return leased.OAuthRefreshToken.String != snapshot.OAuthRefreshToken.String ||
		leased.OAuthRefreshToken.Valid != snapshot.OAuthRefreshToken.Valid
}

// acquireUserAIProviderKeyLease takes the row lease, waiting out a peer's
// active lease with the externalauth backoff shape (50ms to 500ms) until
// the context gives up. Acquiring also re-reads the row, which is how a
// waiter's snapshot learns about a peer's refresh.
func acquireUserAIProviderKeyLease(
	ctx context.Context,
	store userAIProviderKeyOAuthStore,
	key database.UserAIProviderKey,
) (database.UserAIProviderKey, error) {
	r := retry.New(50*time.Millisecond, 500*time.Millisecond)
	for {
		leased, err := store.AcquireUserAIProviderKeyRefreshLease(ctx, database.AcquireUserAIProviderKeyRefreshLeaseParams{
			AIProviderID: key.AIProviderID,
			UserID:       key.UserID,
			TimeoutMs:    aiProviderOAuthLeaseTimeoutMS,
		})
		switch {
		case database.IsCheckViolation(err, userAIProviderKeyActiveLeaseConstraint):
			if !r.Wait(ctx) {
				return key, ctx.Err()
			}
		case err != nil:
			return key, xerrors.Errorf("acquire AI provider key refresh lease: %w", err)
		default:
			return leased, nil
		}
	}
}

// oauthTerminalRefreshError is a terminal refresh failure: the grant is
// dead (invalid_grant) and must never be retried.
type oauthTerminalRefreshError struct {
	reason string
}

func (e *oauthTerminalRefreshError) Error() string {
	return "terminal AI provider token refresh failure: " + e.reason
}

// exchangeUserAIProviderRefreshToken performs the refresh exchange with
// bounded transient retries. Only invalid_grant is terminal; every other
// failure (network, 5xx, 429, malformed success) is transient and preserved
// for retry.
func exchangeUserAIProviderRefreshToken(
	ctx context.Context,
	httpClient *http.Client,
	clock quartz.Clock,
	config aiProviderOAuthConfig,
	refreshToken string,
) (*aiProviderOAuthRefreshedTokens, error) {
	backoff := retry.New(aiProviderOAuthRetryInitialBackoff, aiProviderOAuthRetryMaxBackoff)
	var lastErr error
	for attempt := 0; attempt < aiProviderOAuthMaxAttempts; attempt++ {
		if attempt > 0 {
			if !backoff.Wait(ctx) {
				return nil, lastErr
			}
		}
		tokens, terminal, err := postUserAIProviderRefresh(ctx, httpClient, clock, config, refreshToken)
		if terminal != nil {
			return nil, terminal
		}
		if err == nil {
			return tokens, nil
		}
		lastErr = err
		// A terminal classification can also arrive wrapped: stop at once.
		var termCheck *oauthTerminalRefreshError
		if xerrors.As(err, &termCheck) {
			return nil, err
		}
	}
	return nil, lastErr
}

// postUserAIProviderRefresh performs one refresh POST. It returns either
// tokens, a terminal error (never retry), or a transient error (retry
// within the caller's bound).
func postUserAIProviderRefresh(
	ctx context.Context,
	httpClient *http.Client,
	clock quartz.Clock,
	config aiProviderOAuthConfig,
	refreshToken string,
) (*aiProviderOAuthRefreshedTokens, *oauthTerminalRefreshError, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {config.clientID},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, xerrors.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, xerrors.Errorf("post refresh request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, xerrors.Errorf("read refresh response: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		return parseUserAIProviderRefreshSuccess(clock, body)
	}
	if providerErrorIsInvalidGrant(resp.StatusCode, body) {
		return nil, &oauthTerminalRefreshError{reason: "invalid_grant"}, nil
	}
	return nil, nil, xerrors.Errorf("refresh failed with status %d: %s", resp.StatusCode, truncateOAuthFailureDetail(body))
}

// parseUserAIProviderRefreshSuccess decodes a 200 refresh response. A
// missing access token is transient (provider glitch, retry); a missing
// refresh keeps the stored one (rotation best-effort); a missing or
// non-positive expires_in persists NULL expiry, which disables further
// refresh until the next successful rotation carries one. Account id is
// re-derived from the new JWT when possible and carried otherwise: the
// column is informational until header injection is measured, so a changed
// token shape must not strand the request.
func parseUserAIProviderRefreshSuccess(clock quartz.Clock, body []byte) (*aiProviderOAuthRefreshedTokens, *oauthTerminalRefreshError, error) {
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    any    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, xerrors.Errorf("decode refresh response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, nil, xerrors.New("refresh response missing access token")
	}
	tokens := &aiProviderOAuthRefreshedTokens{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
	}
	if expiresIn := aiDeviceRefreshExpiresInSeconds(payload.ExpiresIn); expiresIn > 0 {
		tokens.ExpiresAt = clock.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	if accountID, err := aiDeviceRefreshAccountIDFromJWT(payload.AccessToken); err == nil {
		tokens.AccountID = accountID
	}
	return tokens, nil, nil
}

// providerErrorIsInvalidGrant reports the terminal refresh failure: HTTP
// 400 carrying the invalid_grant code. Only this code NULLs the refresh
// token; every other failure preserves the credential for retry.
func providerErrorIsInvalidGrant(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), "invalid_grant")
}

// truncateOAuthFailureDetail caps failure text recorded or returned, so a
// proxied HTML error page cannot flood the row or the logs.
func truncateOAuthFailureDetail(body []byte) string {
	detail := strings.TrimSpace(string(body))
	if len(detail) > aiProviderOAuthFailureReasonLimit {
		detail = detail[:aiProviderOAuthFailureReasonLimit]
	}
	if detail == "" {
		return "empty response body"
	}
	return detail
}

// recordUserAIProviderTransientFailure records the reason under the lease
// with tokens untouched, preserving the credential for retry. The caller
// proceeds with the stale access token: same credential, single attempt,
// never a different credential and never unauthenticated.
func recordUserAIProviderTransientFailure(
	ctx context.Context,
	store userAIProviderKeyOAuthStore,
	provider database.AIProvider,
	leased database.UserAIProviderKey,
	logger slog.Logger,
	refreshErr error,
) (database.UserAIProviderKey, error) {
	reason := truncateOAuthFailureDetail([]byte(refreshErr.Error()))
	logger.Warn(ctx, "AI provider token refresh failed transiently, keeping credential for retry",
		slog.F("user_id", leased.UserID), slog.F("ai_provider_id", leased.AIProviderID),
		slog.F("reason", reason))
	updated, err := store.UpdateUserAIProviderKeyOAuth(ctx, database.UpdateUserAIProviderKeyOAuthParams{
		UserID:                    leased.UserID,
		AIProviderID:              leased.AIProviderID,
		APIKey:                    leased.APIKey,
		ApiKeyKeyID:               leased.ApiKeyKeyID,
		OAuthRefreshToken:         leased.OAuthRefreshToken,
		OAuthRefreshTokenKeyID:    leased.OAuthRefreshTokenKeyID,
		OAuthExpiry:               leased.OAuthExpiry,
		AccountID:                 leased.AccountID,
		OAuthExtra:                leased.OAuthExtra,
		OauthRefreshFailureReason: sql.NullString{String: reason, Valid: true},
		RefreshLeaseExpiresAt:     leased.RefreshLeaseExpiresAt,
	})
	if err != nil {
		return leased, xerrors.Errorf("record transient refresh failure: %w", refreshErr)
	}
	// Best effort: the caller's request proceeds with the stale token.
	// A 401 from upstream then surfaces through the normal error path.
	return updated, refreshErr
}

// recordUserAIProviderTerminalFailure takes the down-path: NULL the dead
// refresh token, record the reason, keep the saved key row, and return the
// typed re-auth signal. Never retries, never falls back.
func recordUserAIProviderTerminalFailure(
	ctx context.Context,
	store userAIProviderKeyOAuthStore,
	provider database.AIProvider,
	leased database.UserAIProviderKey,
	reason string,
) (database.UserAIProviderKey, error) {
	updated, err := store.UpdateUserAIProviderKeyOAuth(ctx, database.UpdateUserAIProviderKeyOAuthParams{
		UserID:       leased.UserID,
		AIProviderID: leased.AIProviderID,
		APIKey:       leased.APIKey,
		ApiKeyKeyID:  leased.ApiKeyKeyID,
		// The grant is dead: NULL it so the row returns to static-secret
		// behavior instead of retrying forever.
		OAuthRefreshToken:      sql.NullString{},
		OAuthRefreshTokenKeyID: sql.NullString{},
		OAuthExpiry:            leased.OAuthExpiry,
		AccountID:              leased.AccountID,
		OAuthExtra:             leased.OAuthExtra,
		OauthRefreshFailureReason: sql.NullString{
			String: truncateOAuthFailureDetail([]byte(reason)),
			Valid:  true,
		},
		RefreshLeaseExpiresAt: leased.RefreshLeaseExpiresAt,
	})
	if err != nil {
		return leased, xerrors.Errorf("record terminal refresh failure: %w", err)
	}
	return updated, reauthRequiredError(provider, reason)
}

// persistUserAIProviderRefreshedTokens writes the rotated triple BEFORE
// use and clears any past failure: refresh rotates both tokens, so writing
// after the request risks losing the new refresh token on a crash and
// permanently breaking the credential.
func persistUserAIProviderRefreshedTokens(
	ctx context.Context,
	store userAIProviderKeyOAuthStore,
	provider database.AIProvider,
	leased database.UserAIProviderKey,
	tokens *aiProviderOAuthRefreshedTokens,
) (database.UserAIProviderKey, error) {
	refreshToken := leased.OAuthRefreshToken
	refreshTokenKeyID := leased.OAuthRefreshTokenKeyID
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		// The store (dbcrypt) encrypts when the digest is empty; pass the
		// plaintext through with a cleared key id, mirroring the
		// externalauth update path.
		refreshToken = sql.NullString{String: tokens.RefreshToken, Valid: true}
		refreshTokenKeyID = sql.NullString{}
	}
	expiry := sql.NullTime{}
	if !tokens.ExpiresAt.IsZero() {
		expiry = sql.NullTime{Time: tokens.ExpiresAt, Valid: true}
	}
	accountID := leased.AccountID
	if strings.TrimSpace(tokens.AccountID) != "" {
		accountID = sql.NullString{String: tokens.AccountID, Valid: true}
	}
	updated, err := store.UpdateUserAIProviderKeyOAuth(ctx, database.UpdateUserAIProviderKeyOAuthParams{
		UserID:                    leased.UserID,
		AIProviderID:              leased.AIProviderID,
		APIKey:                    tokens.AccessToken,
		ApiKeyKeyID:               sql.NullString{},
		OAuthRefreshToken:         refreshToken,
		OAuthRefreshTokenKeyID:    refreshTokenKeyID,
		OAuthExpiry:               expiry,
		AccountID:                 accountID,
		OAuthExtra:                leased.OAuthExtra,
		OauthRefreshFailureReason: sql.NullString{},
		RefreshLeaseExpiresAt:     leased.RefreshLeaseExpiresAt,
	})
	if err != nil {
		return leased, xerrors.Errorf("persist refreshed AI provider tokens: %w", err)
	}
	return updated, nil
}

// reauthRequiredError builds the typed re-auth signal for a dead OAuth
// credential: provider identity plus the device-code re-initiation path.
func reauthRequiredError(provider database.AIProvider, reason string) *chaterror.ReauthRequiredError {
	return &chaterror.ReauthRequiredError{
		ProviderID:   provider.ID,
		ProviderName: provider.Name,
		Cause:        reason,
	}
}

// aiDeviceRefreshAccountIDFromJWT re-derives the provider account id from a
// fresh access JWT. It mirrors aiDeviceAccountIDFromJWT in
// coderd/ai_provider_device_grants.go (chatd cannot import coderd); a
// derivation failure here is non-fatal (the stored account is kept) because
// the column is informational until header injection is measured, while the
// Bearer token itself still works.
func aiDeviceRefreshAccountIDFromJWT(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", xerrors.New("access token is not a JWT")
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", xerrors.Errorf("decode access token payload: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return "", xerrors.Errorf("decode access token claims: %w", err)
	}
	namespace, ok := payload["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return "", xerrors.New("access token JWT missing account claim namespace")
	}
	accountID, ok := namespace["chatgpt_account_id"].(string)
	if !ok || strings.TrimSpace(accountID) == "" {
		return "", xerrors.New("access token JWT missing chatgpt_account_id")
	}
	return accountID, nil
}

// aiDeviceRefreshExpiresInSeconds normalizes the provider expires_in field
// into whole seconds. It mirrors aiDeviceExpiresInSeconds in
// coderd/ai_provider_device_grants.go; non-positive or unparseable values
// report unknown (0).
func aiDeviceRefreshExpiresInSeconds(raw any) int {
	switch v := raw.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case float32:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case json.Number:
		if parsed, err := v.Int64(); err == nil && parsed > 0 {
			return int(parsed)
		}
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &parsed); err == nil && parsed > 0 {
			return int(parsed)
		}
	}
	return 0
}
