package chaterror_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/x/chatd/chaterror"
	"github.com/coder/coder/v2/codersdk"
)

func TestClassifyReauthRequired(t *testing.T) {
	t.Parallel()

	providerID := uuid.New()
	reauth := &chaterror.ReauthRequiredError{
		ProviderID:   providerID,
		ProviderName: "chatgpt",
		Cause:        "invalid_grant",
	}

	classified := chaterror.Classify(reauth)
	require.Equal(t, codersdk.ChatErrorKindReauthRequired, classified.Kind)
	require.Equal(t, "chatgpt", classified.Provider)
	require.False(t, classified.Retryable)
	require.Equal(t, 401, classified.StatusCode)
	require.Contains(t, classified.Message, "Sign in again")

	// The signal survives wrapping: whatever the chatd error surface adds
	// around it, the typed classification wins over generic auth parsing.
	wrapped := xerrors.Errorf("chat turn failed: %w", reauth)
	classified = chaterror.Classify(wrapped)
	require.Equal(t, codersdk.ChatErrorKindReauthRequired, classified.Kind)
	require.False(t, classified.Retryable)

	// Explicit classification round-trips through the surface helper.
	roundTripped := chaterror.Classify(chaterror.WithClassification(
		xerrors.New("boom"),
		chaterror.ClassifiedError{Kind: codersdk.ChatErrorKindReauthRequired, Provider: "chatgpt"},
	))
	require.Equal(t, codersdk.ChatErrorKindReauthRequired, roundTripped.Kind)

	// The re-initiation path names the provider's device-grant route.
	userID := uuid.New()
	require.Contains(t, reauth.DeviceGrantInitiatePath(userID), providerID.String())
	require.Contains(t, reauth.DeviceGrantInitiatePath(userID), "device-grants")
}
