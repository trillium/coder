#!/usr/bin/env bash
# Build and (re)start the cyc26 inference proxy on lnx. Holds no credentials:
# seed them separately (see README.md). Safe to re-run; state in
# /srv/cyc26-proxy/data (credentials, passes, usage) is kept.
#
# Usage: deploy.sh [ssh-target]      default: trillium@100.81.88.113
set -euo pipefail

TARGET="${1:-trillium@100.81.88.113}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UID_PROXY=10001

ssh -o BatchMode=yes "$TARGET" 'sudo -n mkdir -p /srv/cyc26-proxy/src /srv/cyc26-proxy/data && sudo -n chown "$USER" /srv/cyc26-proxy/src'
scp -q "$HERE/Dockerfile" "$HERE/.dockerignore" "$HERE/server.mjs" "$HERE/passctl.mjs" "$TARGET:/srv/cyc26-proxy/src/"

ssh -o BatchMode=yes "$TARGET" bash -s -- "$UID_PROXY" <<'REMOTE'
set -euo pipefail
uid="$1"
sudo -n chown -R "$uid:$uid" /srv/cyc26-proxy/data
sudo -n chmod 700 /srv/cyc26-proxy/data
docker build -q -t cyc26-proxy:latest /srv/cyc26-proxy/src >/dev/null

# Bind only the docker0 gateway: reachable from containers on this host, not the tailnet or LAN.
gw="$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')"
docker rm -f cyc26-proxy >/dev/null 2>&1 || true
docker run -d --name cyc26-proxy --restart unless-stopped \
  --user "$uid:$uid" --read-only --tmpfs /tmp --cap-drop ALL \
  --security-opt no-new-privileges --memory 256m --pids-limit 100 \
  --log-opt max-size=10m --log-opt max-file=3 \
  -e BIND=0.0.0.0 -p "$gw:8787:8787" \
  -v /srv/cyc26-proxy/data:/data \
  cyc26-proxy:latest >/dev/null

probe() { docker run --rm --entrypoint wget cyc26-proxy:latest -qO- -T 5 "http://$gw:8787/healthz" 2>/dev/null; }
sleep 1
if ! probe >/dev/null; then
  echo "containers cannot reach $gw:8787; allowing docker ranges through ufw"
  sudo -n ufw allow from 172.16.0.0/12 to "$gw" port 8787 proto tcp comment cyc26-proxy
  probe >/dev/null
fi
echo "cyc26-proxy listening on $gw:8787 (reachable from containers on this host only)"
REMOTE
