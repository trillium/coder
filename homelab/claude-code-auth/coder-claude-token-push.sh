#!/bin/sh
# coder-claude-token-push.sh - store a Claude Code subscription token as a
# Coder user secret so every workspace you own gets CLAUDE_CODE_OAUTH_TOKEN.
#
# Usage: coder-claude-token-push.sh [--dry-run] [--from-clipboard] [--verify <owner/workspace>]
#
# Token source (first match; the value is never printed or put in argv):
#   1. stdin, when piped:   pbpaste | coder-claude-token-push.sh
#   2. --from-clipboard     reads pbpaste (macOS)
#   3. hidden prompt        paste the token from `claude setup-token`
#
# Creates the secret `claude-code-oauth-token`, or updates it when it
# exists (use the same command to rotate). Running workspaces pick the
# value up on their next agent start, so restart them afterwards.
# --verify restarts <owner/workspace> and runs `claude -p hi` inside it.
# See RUNBOOK.md.
set -eu

SECRET_NAME="claude-code-oauth-token"
ENV_NAME="CLAUDE_CODE_OAUTH_TOKEN"

DRY_RUN=0
CLIP=0
VERIFY=""
while [ $# -gt 0 ]; do
	case "$1" in
	--dry-run) DRY_RUN=1 ;;
	--from-clipboard) CLIP=1 ;;
	--verify)
		shift
		[ $# -gt 0 ] || {
			echo "error: --verify needs <owner/workspace>" >&2
			exit 1
		}
		VERIFY="$1"
		;;
	-h | --help)
		sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "error: unknown argument: $1" >&2
		exit 1
		;;
	esac
	shift
done

command -v coder >/dev/null 2>&1 || {
	echo "error: coder CLI not found" >&2
	exit 1
}

# Fails when signed out; prints nothing sensitive. Skipped for --dry-run so
# the token handling can be exercised offline.
if [ "$DRY_RUN" = 0 ] && ! coder whoami >/dev/null 2>&1; then
	echo "error: coder CLI is signed out; run: coder login <access-url>" >&2
	exit 1
fi

# User secrets belong to the Coder user the CLI is signed in as, and a
# workspace only receives its owner's secrets. Name that user, and refuse
# --verify against a workspace someone else owns.
if [ "$DRY_RUN" = 0 ]; then
	ME="$(coder users show me -o json 2>/dev/null | sed -n 's/.*"username": *"\([^"]*\)".*/\1/p' | head -1)"
	echo "storing the secret for Coder user: ${ME:-unknown}" >&2
	if [ -n "$VERIFY" ] && [ "${VERIFY%%/*}" != "$ME" ]; then
		echo "error: $VERIFY is owned by '${VERIFY%%/*}' but the CLI is signed in as '$ME'; sign in as the workspace owner (coder login)" >&2
		exit 1
	fi
fi

umask 077
TMP="$(mktemp "${TMPDIR:-/tmp}/claude-token.XXXXXX")"
trap 'rm -f "$TMP"' EXIT INT TERM

if [ "$CLIP" = 1 ]; then
	pbpaste >"$TMP"
elif [ ! -t 0 ]; then
	cat >"$TMP"
else
	printf "Paste token from claude setup-token (hidden): " >&2
	stty -echo
	IFS= read -r line || true
	stty echo
	printf '\n' >&2
	printf '%s' "$line" >"$TMP"
	line=""
fi

# Strip whitespace/newlines, then sanity-check the shape without echoing it.
tr -d '[:space:]' <"$TMP" >"$TMP.clean" && mv "$TMP.clean" "$TMP"
if [ ! -s "$TMP" ]; then
	echo "error: empty token" >&2
	exit 1
fi
if ! grep -q '^sk-ant-oat' "$TMP"; then
	echo "error: value does not look like a claude setup-token token (expected prefix sk-ant-oat)" >&2
	exit 1
fi

if [ "$DRY_RUN" = 0 ] && coder secret list "$SECRET_NAME" >/dev/null 2>&1; then
	ACTION=update
else
	ACTION=create
fi

if [ "$DRY_RUN" = 1 ]; then
	echo "would run: coder secret $ACTION $SECRET_NAME --env $ENV_NAME  (value on stdin, not printed)"
	[ -z "$VERIFY" ] || echo "would restart $VERIFY and run: claude -p hi"
	exit 0
fi

if [ "$ACTION" = create ]; then
	coder secret create "$SECRET_NAME" --env "$ENV_NAME" \
		--description "Claude Code subscription token (claude setup-token)" <"$TMP"
else
	coder secret update "$SECRET_NAME" --env "$ENV_NAME" <"$TMP"
fi
echo "secret $SECRET_NAME ${ACTION}d; restart workspaces to pick it up"

if [ -n "$VERIFY" ]; then
	coder restart "$VERIFY" --yes
	coder ssh "$VERIFY" -- 'claude -p hi'
fi
