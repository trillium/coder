#!/usr/bin/env bash
# Store or rotate one real credential in the inference proxy on lnx. The value is
# read at a hidden prompt (or from a file for openai), travels over ssh stdin, and
# is never an argument, a log line or an env var. The proxy picks it up on the
# next request.
#
# Usage:
#   set-credential.sh anthropic [ssh-target]            claude setup-token value
#   set-credential.sh opencode  [ssh-target]            OpenCode Zen/Go API key
#   set-credential.sh openai <pi-auth.json> [ssh-target]  "openai" entry of a Pi auth.json
#
# Default ssh-target: trillium@100.81.88.113
set -euo pipefail

provider="${1:-}"
case "$provider" in
anthropic | opencode)
	target="${2:-trillium@100.81.88.113}"
	read -rsp "$provider credential (hidden): " value
	echo >&2
	[ -n "$value" ] || {
		echo "empty value" >&2
		exit 1
	}
	;;
openai)
	file="${2:?usage: set-credential.sh openai <pi-auth.json> [ssh-target]}"
	target="${3:-trillium@100.81.88.113}"
	value="$(cat "$file")"
	;;
*)
	echo "usage: set-credential.sh anthropic|opencode|openai ..." >&2
	exit 2
	;;
esac

# shellcheck disable=SC2016
remote_py='
import json, os, sys
prov, raw = sys.argv[1], sys.stdin.read().strip()
p = "/srv/cyc26-proxy/data/credentials.json"
d = json.load(open(p)) if os.path.exists(p) else {}
if prov == "anthropic":
    d["anthropic"] = {"token": raw}
elif prov == "opencode":
    d["opencode"] = {"apiKey": raw}
else:
    a = json.loads(raw)
    a = a.get("openai", a)
    d["openai"] = {k: a[k] for k in ("access", "refresh", "expires", "clientId")}
tmp = p + ".new"
fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
os.write(fd, json.dumps(d, indent=2).encode() + b"\n")
os.close(fd)
os.chown(tmp, 10001, 10001)
os.rename(tmp, p)
print("updated", prov)
'
printf '%s' "$value" | ssh -o BatchMode=yes "$target" "sudo -n python3 -c $(printf '%q' "$remote_py") $provider"
