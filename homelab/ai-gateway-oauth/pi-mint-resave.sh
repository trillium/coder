#!/bin/sh
# pi-mint-resave.sh - re-save any Pi-held credential as your Coder BYOK user
# key. Generalizes byok-resave-chatgpt.sh across Pi minters.
#
# Flow: Pi (the minter, with silent refresh where supported) exports a
# fresh credential -> PUT /api/v2/users/me/ai-provider-keys/<uuid> ->
# verified with GET. Run AS YOURSELF: mints from YOUR Pi store, saves to
# YOUR Coder user. Never run with another user's Pi store or session.
#
# Usage:
#   ./pi-mint-resave.sh --pi-provider NAME --coder-provider NAME|UUID
#       [--mode api-key|bearer-token] [--min-expiry DURATION]
#       [--host URL] [--check]
#
#   --mode api-key      uses `pi auth print-api-key` (api_key authType)
#   --mode bearer-token uses `pi auth print-bearer-token` (oauth authType)
#   --min-expiry only applies to bearer-token mode (default 24h).
#   --check dry-runs everything except the PUT.
#
# Known-good mappings (verified 2026-09-25, metadata only):
#   openai-codex/bearer-token -> chatgpt
#   opencode-go/api-key        -> opencode-go (key currently dead upstream;
#                                 path stays wired so renewal lights it up)
#
# Secret hygiene: credential lives only in a shell variable, never echoed,
# never logged, unset on exit (trap). Logs carry byte length only.
# Never run with 'sh -x' / 'set -x'.
set -eu

HOST="${CODER_HOST:-http://lnx:7080}"
PI_PROVIDER=""
CODER_REF=""
MODE=""
MIN_EXPIRY="24h"
CHECK=0
MAX_KEY_BYTES=10240 # server rejects api_key > 10 KB with 400

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --pi-provider) PI_PROVIDER="$2"; shift 2 ;;
    --coder-provider) CODER_REF="$2"; shift 2 ;;
    --mode) MODE="$2"; shift 2 ;;
    --min-expiry) MIN_EXPIRY="$2"; shift 2 ;;
    --check) CHECK=1; shift ;;
    -h|--help)
      sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "usage: $0 --pi-provider NAME --coder-provider NAME|UUID [--mode api-key|bearer-token] [--min-expiry DURATION] [--host URL] [--check]" >&2; exit 2 ;;
  esac
done

[ -n "$PI_PROVIDER" ] || { echo "error: --pi-provider required" >&2; exit 2; }
[ -n "$CODER_REF" ] || { echo "error: --coder-provider required" >&2; exit 2; }
if [ -z "$MODE" ]; then
  MODE="$(pi auth check --provider "$PI_PROVIDER" --json --no-refresh 2>/dev/null \
    | jq -r 'if .authType == "api_key" then "api-key" else "bearer-token" end')"
  echo "mode auto-detected: $MODE"
fi
case "$MODE" in
  api-key|bearer-token) ;;
  *) echo "error: --mode must be api-key or bearer-token" >&2; exit 2 ;;
esac

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

case "$CODER_REF" in
  ????????-????-????-????-????????????) PROVIDER_ID="$CODER_REF" ;;
  *)
    PROVIDER_ID="$(api GET /api/v2/ai/providers | jq -r --arg n "$CODER_REF" \
      '[.[] | select(.name == $n)] | .[0].id // empty')"
    [ -n "$PROVIDER_ID" ] || {
      echo "error: no provider named '$CODER_REF' (create the shell first)" >&2
      exit 1
    }
    ;;
esac
info="$(api GET /api/v2/ai/providers | jq -c --arg id "$PROVIDER_ID" \
  '[.[] | select(.id == $id)] | .[0] // empty')"
[ -n "$info" ] || { echo "error: provider id $PROVIDER_ID not found" >&2; exit 1; }
echo "provider: $(echo "$info" | jq -r '{name, type, base_url, enabled} | to_entries | map("\(.key)=\(.value)") | join(" ")')"

pi_status="$(pi auth check --provider "$PI_PROVIDER" --json --no-refresh 2>/dev/null \
  | jq -r '{authType, provider, status} | to_entries | map("\(.key)=\(.value)") | join(" ")')"
echo "pi minter: $pi_status"

if [ "$CHECK" -eq 1 ]; then
  echo "check mode: provider resolves, minter ready; PUT skipped (no state changed)"
  exit 0
fi

if [ "$MODE" = "api-key" ]; then
  TOKEN="$(pi auth print-api-key --provider "$PI_PROVIDER")"
else
  TOKEN="$(pi auth print-bearer-token --provider "$PI_PROVIDER" --min-expiry "$MIN_EXPIRY")"
fi
[ -n "$TOKEN" ] || { echo "error: Pi returned an empty credential" >&2; exit 1; }
bytes="$(printf '%s' "$TOKEN" | wc -c)"
echo "exported credential: ${bytes} bytes (value never logged)"
if [ "$bytes" -gt "$MAX_KEY_BYTES" ]; then
  echo "error: credential ${bytes} bytes exceeds server 10 KB limit; refusing PUT" >&2
  exit 1
fi
case "$TOKEN" in
  ''|*[[:space:]]*)
    echo "error: credential empty or has leading/trailing whitespace; refusing PUT" >&2
    exit 1
    ;;
esac

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
