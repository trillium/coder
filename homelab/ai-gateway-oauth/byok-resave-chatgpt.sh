#!/bin/sh
# byok-resave-chatgpt.sh - re-save your ChatGPT OAuth access token as your
# Coder BYOK user key for the chatgpt provider.
#
# Flow: Pi (the OAuth minter, with silent refresh) exports a fresh access
# token -> PUT /api/v2/users/me/ai-provider-keys/<provider-uuid> ->
# verified with GET. Run this AS YOURSELF: it mints from YOUR Pi agent
# store and saves to YOUR Coder user. Never run it with another user's
# Pi store or Coder session.
#
# Usage:
#   ./byok-resave-chatgpt.sh [--host URL] [--provider UUID|name]
#       [--min-expiry DURATION] [--check]
#
#   --provider defaults to name 'chatgpt' (resolved to UUID via the API).
#   --min-expiry (default 24h) is passed to Pi: Pi refreshes server-side
#     when the token would expire sooner, so the saved token is fresh.
#   --check dry-runs everything except the PUT: resolves the provider,
#     checks Pi credential readiness (metadata only), and reports what
#     would happen. Use --check to validate without touching state.
#
# Secret hygiene: the token lives only in a shell variable, is never
# echoed, never logged, and is unset on exit (trap). Logs carry only
# masked hints (byte length). Never run with 'sh -x' / 'set -x'.
set -eu

HOST="${CODER_HOST:-http://lnx:7080}"
PROVIDER_REF="chatgpt"
MIN_EXPIRY="24h"
CHECK=0
MAX_KEY_BYTES=10240 # server rejects api_key > 10 KB with 400

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --provider) PROVIDER_REF="$2"; shift 2 ;;
    --min-expiry) MIN_EXPIRY="$2"; shift 2 ;;
    --check) CHECK=1; shift ;;
    -h|--help)
      sed -n '2,24p' "$0"; exit 0 ;;
    *) echo "usage: $0 [--host URL] [--provider UUID|name] [--min-expiry DURATION] [--check]" >&2; exit 2 ;;
  esac
done

cleanup() {
  # shellcheck disable=SC2086
  unset TOKEN CS ${TMPBODY:+TMPBODY}
  [ -n "${TMPBODY:-}" ] && rm -f "$TMPBODY"
  return 0
}
trap cleanup EXIT INT TERM

session_token() {
  if [ -n "${CODER_SESSION_TOKEN:-}" ]; then
    printf '%s' "$CODER_SESSION_TOKEN"
    return 0
  fi
  if command -v security >/dev/null 2>&1; then
    security find-generic-password \
      -s coder-lnx-7080-session -a coder-owner -w 2>/dev/null
    return 0
  fi
  echo "error: no Coder session token (set CODER_SESSION_TOKEN)" >&2
  return 1
}

CS="$(session_token)"

api() { # method path [body-file]
  if [ $# -ge 3 ]; then
    curl -sf -H "Coder-Session-Token: $CS" -H 'Content-Type: application/json' \
      -X "$1" --data @"$3" "$HOST$2"
  else
    curl -sf -H "Coder-Session-Token: $CS" -X "$1" "$HOST$2"
  fi
}

# 1. Resolve provider ref -> UUID (accepts UUID directly or a name).
case "$PROVIDER_REF" in
  ????????-????-????-????-????????????) PROVIDER_ID="$PROVIDER_REF" ;;
  *)
    PROVIDER_ID="$(api GET /api/v2/ai/providers | jq -r --arg n "$PROVIDER_REF" \
      '[.[] | select(.name == $n)] | .[0].id // empty')"
    [ -n "$PROVIDER_ID" ] || {
      echo "error: no provider named '$PROVIDER_REF' (404 meaning: provider missing)" >&2
      exit 1
    }
    ;;
esac
info="$(api GET /api/v2/ai/providers | jq -c --arg id "$PROVIDER_ID" \
  '[.[] | select(.id == $id)] | .[0] // empty')"
[ -n "$info" ] || { echo "error: provider id $PROVIDER_ID not found" >&2; exit 1; }
echo "provider: $(echo "$info" | jq -r '{name, type, base_url, enabled} | to_entries | map("\(.key)=\(.value)") | join(" ")')"

# 2. Pi credential readiness (metadata only: no token material touched).
pi_status="$(pi auth check --provider openai-codex --json --no-refresh 2>/dev/null \
  | jq -r '{authType, provider, status} | to_entries | map("\(.key)=\(.value)") | join(" ")')"
echo "pi minter: $pi_status"

if [ "$CHECK" -eq 1 ]; then
  echo "check mode: provider resolves, minter ready; PUT skipped (no state changed)"
  exit 0
fi

# 3. Export a fresh access token from Pi (Pi refreshes when nearer expiry
#    than --min-expiry). This is the ONLY point token material exists,
#    and only in $TOKEN.
TOKEN="$(pi auth print-bearer-token --provider openai-codex --min-expiry "$MIN_EXPIRY")"
[ -n "$TOKEN" ] || { echo "error: Pi returned an empty token" >&2; exit 1; }
bytes="$(printf '%s' "$TOKEN" | wc -c)"
echo "exported token: ${bytes} bytes (value never logged)"
if [ "$bytes" -gt "$MAX_KEY_BYTES" ]; then
  echo "error: token ${bytes} bytes exceeds server 10 KB limit; refusing PUT" >&2
  exit 1
fi
case "$TOKEN" in
  ''|*[[:space:]]*)
    echo "error: token empty or has leading/trailing whitespace; refusing PUT" >&2
    exit 1
    ;;
esac

# 4. Save (create-or-replace) and verify presence (server returns booleans
#    only, never key material).
TMPBODY="$(mktemp)"
jq -n --arg k "$TOKEN" '{api_key: $k}' >"$TMPBODY"
unset TOKEN
if ! saved="$(api PUT "/api/v2/users/me/ai-provider-keys/$PROVIDER_ID" "$TMPBODY")"; then
  st=$?
  echo "error: PUT failed (exit $st). Meanings: 403 = BYOK disabled; 404 = provider missing; 400 = key too large/blank" >&2
  exit $st
fi
echo "save response: $(echo "$saved" | jq -c '{has_user_api_key: .has_user_api_key, byok_enabled: .byok_enabled}')"
present="$(api GET /api/v2/users/me/ai-provider-keys | jq -r --arg id "$PROVIDER_ID" \
  '[.[] | select(.provider.id == $id)] | .[0].has_user_api_key // false')"
[ "$present" = "true" ] || { echo "error: verify GET does not show a saved key" >&2; exit 1; }
echo "verified: user key present for provider $PROVIDER_ID"
