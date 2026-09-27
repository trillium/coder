package chaterror

import (
	"github.com/google/uuid"
)

// ReauthRequiredError signals the user's saved OAuth credential for one AI
// provider is dead (terminal refresh failure, e.g. invalid_grant): the user
// must sign in again through the device-code flow. It carries provider
// identity and the device-code re-initiation path so the UI can render
// exactly one re-auth prompt per provider. It never carries key material.
//
// The saved key row is kept: only the refresh state is cleared, so the
// user is blocked on this provider until re-auth, never silently moved to
// a different credential.
type ReauthRequiredError struct {
	ProviderID   uuid.UUID
	ProviderName string
	// Cause is a short operator-safe failure summary (e.g. "invalid_grant").
	Cause string
}

func (e *ReauthRequiredError) Error() string {
	provider := e.ProviderName
	if provider == "" {
		provider = e.ProviderID.String()
	}
	if e.Cause != "" {
		return "ai provider credential expired (" + e.Cause + "): sign in again for " + provider
	}
	return "ai provider credential expired: sign in again for " + provider
}

// DeviceGrantInitiatePath returns the API path that starts a fresh
// device-code sign-in for this provider and user, for UI affordances.
func (e *ReauthRequiredError) DeviceGrantInitiatePath(userID uuid.UUID) string {
	return "/api/v2/users/" + userID.String() + "/ai-provider-keys/" + e.ProviderID.String() + "/device-grants"
}
