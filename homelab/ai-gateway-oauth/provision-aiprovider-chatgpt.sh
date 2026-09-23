#!/bin/sh
# provision-aiprovider-chatgpt.sh - idempotent admin provisioning of the
# ChatGPT subscription provider on a homelab Coder deployment.
#
# Ensures exactly one provider: type=openai name=chatgpt
# base_url=https://chatgpt.com/backend-api/codex, enabled, with NO central
# API keys (ChatGPT authenticates per-user ChatGPT OAuth tokens via BYOK,
# so BYOK must remain enabled). Creates it when absent; PATCHes drift
# (base_url / enabled) when present; otherwise a verified no-op.
#
# Usage:
#   ./provision-aiprovider-chatgpt.sh [--host URL]
#
# Auth: $CODER_SESSION_TOKEN, else the macOS keychain entry
# (service coder-lnx-7080-session, account coder-owner). Requires a Coder
# user able to manage AI providers (site owner on homelab).
# Never prints secret values; only names, ids, and booleans.
set -eu

HOST="${CODER_HOST:-http://lnx:7080}"
WANT_TYPE="openai"
WANT_NAME="chatgpt"
WANT_BASE="https://chatgpt.com/backend-api/codex"

usage() {
  echo "usage: $0 [--host URL]" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done

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
trap 'unset CS' EXIT INT TERM

api() { # method path [body-file]
  if [ $# -ge 3 ]; then
    curl -sf -H "Coder-Session-Token: $CS" -H 'Content-Type: application/json' \
      -X "$1" --data @"$3" "$HOST$2"
  else
    curl -sf -H "Coder-Session-Token: $CS" -X "$1" "$HOST$2"
  fi
}

providers="$(api GET /api/v2/ai/providers)"
echo "$providers" | jq -e . >/dev/null 2>&1 \
  || { echo "error: provider list is not JSON" >&2; exit 1; }

existing="$(echo "$providers" | jq -c --arg n "$WANT_NAME" '[.[] | select(.name == $n)] | .[0] // empty')"

if [ -z "$existing" ]; then
  echo "provider '$WANT_NAME' absent: creating"
  body="$(mktemp)"; trap 'rm -f "$body"; unset CS' EXIT INT TERM
  jq -n --arg t "$WANT_TYPE" --arg n "$WANT_NAME" --arg b "$WANT_BASE" \
    '{type: $t, name: $n, base_url: $b, enabled: true}' >"$body"
  created="$(api POST /api/v2/ai/providers "$body")"
  rm -f "$body"; trap 'unset CS' EXIT INT TERM
  echo "$created" | jq '{id, type, name, base_url, enabled}'
  echo "created provider '$WANT_NAME'"
  exit 0
fi

id="$(echo "$existing" | jq -r .id)"
type="$(echo "$existing" | jq -r .type)"
base="$(echo "$existing" | jq -r .base_url)"
enabled="$(echo "$existing" | jq -r .enabled)"
echo "provider '$WANT_NAME' exists: id=$id type=$type enabled=$enabled base_url=$base"

if [ "$type" != "$WANT_TYPE" ]; then
  echo "error: existing '$WANT_NAME' has type '$type' (want '$WANT_TYPE'); refusing to repurpose" >&2
  exit 1
fi

patch='{}'
[ "$base" != "$WANT_BASE" ] && patch="$(echo "$patch" | jq --arg b "$WANT_BASE" '. + {base_url: $b}')"
[ "$enabled" != "true" ] && patch="$(echo "$patch" | jq '. + {enabled: true}')"

if [ "$patch" = '{}' ]; then
  echo "already provisioned: no changes"
  exit 0
fi

echo "drift detected, patching: $patch"
body="$(mktemp)"; trap 'rm -f "$body"; unset CS' EXIT INT TERM
echo "$patch" >"$body"
updated="$(api PATCH "/api/v2/ai/providers/$id" "$body")"
rm -f "$body"; trap 'unset CS' EXIT INT TERM
echo "$updated" | jq '{id, type, name, base_url, enabled}'
echo "patched provider '$WANT_NAME' back to paved shape"
