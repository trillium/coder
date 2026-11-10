package intercept

import (
	"net/http"
	"net/textproto"
	"strings"

	"github.com/google/uuid"
)

// ChatIDPlaceholder is the template token an admin may embed in a custom
// upstream header value to get per-conversation stability (e.g.
// x-opencode-session: {{chat_id}} for OpenCode Zen routing). It must match
// codersdk.ChatIDPlaceholder; it is redefined here because aibridge does
// not import codersdk.
const ChatIDPlaceholder = "{{chat_id}}"

// chatIDHeader names the conversation header Coder Agents sends on gateway
// requests. It must match chatprovider.HeaderCoderChatID; it is redefined
// here because aibridge cannot import chatd (coderd imports aibridge).
const chatIDHeader = "X-Coder-Chat-Id"

// ApplyUpstreamHeaders sets admin-configured custom headers on dst, the
// outbound upstream request headers. Configured headers win over any
// same-named header forwarded from the client. dst must be non-nil; a nil
// clientHeaders reads as absent.
//
// A value embedding ChatIDPlaceholder resolves per request to the caller's
// conversation ID (chatIDHeader) when reachable, so upstreams that key
// routing off a session header see a stable value per conversation. When
// the conversation ID is absent, the placeholder resolves to a
// deployment-stable UUID derived from the provider name, so requests still
// carry a consistent session value across gateway restarts. Values without
// the placeholder are sent verbatim.
func ApplyUpstreamHeaders(dst http.Header, configured map[string]string, clientHeaders http.Header, providerName string) {
	if len(configured) == 0 {
		return
	}
	chatID := strings.TrimSpace(clientHeaders.Get(chatIDHeader))
	for name, value := range configured {
		resolved := value
		if strings.Contains(resolved, ChatIDPlaceholder) {
			session := chatID
			if session == "" {
				session = fallbackSessionID(providerName)
			}
			resolved = strings.ReplaceAll(resolved, ChatIDPlaceholder, session)
		}
		dst.Set(textproto.CanonicalMIMEHeaderKey(name), resolved)
	}
}

// fallbackSessionID derives a deployment-stable session UUID from the
// provider name. It is deterministic (SHA-1 over a fixed namespace) so the
// value survives gateway restarts and reloads, unlike a random UUID minted
// per process.
func fallbackSessionID(providerName string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("coder:aibridge:upstream-headers:"+providerName)).String()
}
