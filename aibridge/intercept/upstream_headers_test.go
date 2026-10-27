package intercept_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/aibridge/intercept"
	"github.com/coder/coder/v2/codersdk"
)

func TestChatIDPlaceholderMatchesSDK(t *testing.T) {
	t.Parallel()
	// The placeholder is defined in both codersdk (admin-facing settings)
	// and intercept (gateway hot path) because aibridge does not import
	// codersdk. This test pins the two definitions together so a rename on
	// either side fails loudly instead of silently breaking
	// per-conversation upstream routing.
	require.Equal(t, codersdk.ChatIDPlaceholder, intercept.ChatIDPlaceholder)
}

func TestApplyUpstreamHeaders(t *testing.T) {
	t.Parallel()

	t.Run("empty configuration leaves headers untouched", func(t *testing.T) {
		t.Parallel()
		dst := http.Header{"Authorization": {"Bearer sk-key"}}
		intercept.ApplyUpstreamHeaders(dst, nil, http.Header{"X-Coder-Chat-Id": {"chat-1"}}, "zen")
		require.Equal(t, http.Header{"Authorization": {"Bearer sk-key"}}, dst)
	})

	t.Run("literal headers are set", func(t *testing.T) {
		t.Parallel()
		dst := http.Header{}
		intercept.ApplyUpstreamHeaders(dst,
			map[string]string{
				"x-opencode-session": "session-123",
				"X-Custom-Two":       "second",
			},
			http.Header{}, "zen")
		assert.Equal(t, "session-123", dst.Get("X-Opencode-Session"))
		assert.Equal(t, "second", dst.Get("X-Custom-Two"))
	})

	t.Run("configured headers override forwarded client headers", func(t *testing.T) {
		t.Parallel()
		dst := http.Header{"X-Opencode-Session": {"client-value"}}
		intercept.ApplyUpstreamHeaders(dst,
			map[string]string{"x-opencode-session": "admin-value"},
			http.Header{}, "zen")
		assert.Equal(t, "admin-value", dst.Get("X-Opencode-Session"))
	})

	t.Run("placeholder resolves to the conversation id", func(t *testing.T) {
		t.Parallel()
		dst := http.Header{}
		intercept.ApplyUpstreamHeaders(dst,
			map[string]string{"x-opencode-session": "prefix-" + intercept.ChatIDPlaceholder},
			http.Header{"X-Coder-Chat-Id": {"chat-abc"}}, "zen")
		assert.Equal(t, "prefix-chat-abc", dst.Get("X-Opencode-Session"))
	})

	t.Run("placeholder falls back to a stable uuid without a conversation id", func(t *testing.T) {
		t.Parallel()
		first := http.Header{}
		intercept.ApplyUpstreamHeaders(first,
			map[string]string{"x-opencode-session": intercept.ChatIDPlaceholder},
			http.Header{}, "zen")
		second := http.Header{}
		intercept.ApplyUpstreamHeaders(second,
			map[string]string{"x-opencode-session": intercept.ChatIDPlaceholder},
			nil, "zen")
		got := first.Get("X-Opencode-Session")
		require.NotEmpty(t, got)
		// The fallback is a UUID so upstreams that validate session shape
		// accept it, and it is stable across requests and restarts.
		require.Len(t, got, 36)
		assert.Equal(t, got, second.Get("X-Opencode-Session"))
		other := http.Header{}
		intercept.ApplyUpstreamHeaders(other,
			map[string]string{"x-opencode-session": intercept.ChatIDPlaceholder},
			nil, "other-provider")
		assert.NotEqual(t, got, other.Get("X-Opencode-Session"), "fallback must differ per provider")
	})

	t.Run("nil client headers are safe", func(t *testing.T) {
		t.Parallel()
		dst := http.Header{}
		intercept.ApplyUpstreamHeaders(dst,
			map[string]string{"X-A": "b"},
			nil, "zen")
		assert.Equal(t, "b", dst.Get("X-A"))
	})
}
