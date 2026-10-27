# cyc26 inference proxy

A small credential-holding proxy for the cyc26 team workspace. The real
Anthropic, ChatGPT and OpenCode credentials live here, on lnx, outside every
workspace container. A workspace holds only the proxy base URL and a
per-teammate pass. Claude Code, Pi and OpenCode send their API calls to the
proxy; it checks the pass, drops it, attaches the real credential, forwards the
request, and returns only the upstream response.

Why: every teammate runs as the same Unix user in the shared workspace, so
anything in the environment or the home directory is readable by all of them.
A pass can be revoked in seconds and shows who used what. The real tokens are
never in that environment.

I looked for an existing proxy (LiteLLM, Portkey, claude-code-router, and
similar). None covers Anthropic subscription tokens, the ChatGPT OAuth login
with a rotating refresh token, and OpenCode keys with per-pass revoke, without
a database service and a large dependency tree. This is about 300 lines with no
dependencies (Node 22 built-ins).

## Layout

| Path | Purpose |
|---|---|
| `server.mjs` | The proxy. |
| `passctl.mjs` | Issue, revoke, list passes and summarize usage (run inside the container). |
| `deploy.sh` | Build and (re)start the container on lnx. Holds no credentials. |
| `set-credential.sh` | Store or rotate one real credential on lnx. |
| `Dockerfile` | `node:22-alpine`, runs as uid 10001. |

On lnx: code in `/srv/cyc26-proxy/src`, state in `/srv/cyc26-proxy/data`
(owned by uid 10001, mode 700): `credentials.json`, `passes.json` (hashes
only), `usage.jsonl`.

## Routes

The workspace uses `http://host.docker.internal:8787` (the template maps it to
the Docker host gateway, `172.17.0.1`).

| Prefix | Upstream | Credential attached |
|---|---|---|
| `/anthropic/*` | `https://api.anthropic.com/*` | `Authorization: Bearer <setup token>` plus the `oauth-2025-04-20` beta flag |
| `/openai/*` | `https://api.openai.com/*` | `Authorization: Bearer <ChatGPT access token>`; the proxy is the only refresher |
| `/opencode/*` | `https://opencode.ai/zen/*` (Zen at `/opencode/v1`, Go at `/opencode/go/v1`) | `Authorization: Bearer <OpenCode key>` |
| `/healthz` | none | none |

The pass is accepted as `Authorization: Bearer` or `x-api-key` and removed
before forwarding. Cookies, forwarding headers and `chatgpt-account-id` are
dropped. Paths that would leave the provider's API prefix are refused.

## Network exposure

The container publishes `172.17.0.1:8787` only (the `docker0` gateway). That
address is not on the tailnet or the LAN, so nothing off lnx can reach it
(checked from the MacBook over both the tailnet name and IP: refused). UFW
allows `172.16.0.0/12` to that port. Any container on lnx's default bridge can
connect, but without a valid pass it gets `401`. The container runs read-only,
with all capabilities dropped, `no-new-privileges`, 256 MB and 100 PIDs.

## Operate

```sh
# deploy or update the code (credentials and passes are kept)
homelab/cyc26/inference-proxy/deploy.sh

# passes (run on lnx; "ssh trillium@100.81.88.113" first)
docker exec cyc26-proxy node passctl.mjs add ada --cap 2000   # prints the pass once; cap is optional, requests per UTC day
docker exec cyc26-proxy node passctl.mjs list
docker exec cyc26-proxy node passctl.mjs revoke ada           # effective on the next request
docker exec cyc26-proxy node passctl.mjs usage [name] [--day 2026-10-26]
```

`usage` counts requests, errors and best-effort token totals per day, pass and
provider from `usage.jsonl` (rotated at 20 MB to `usage.jsonl.1`). Request and
response bodies are never logged. Container logs (`docker logs cyc26-proxy`)
carry the same JSON lines.

Rotate or set a credential (hidden prompt; the value never appears in a
command line):

```sh
claude setup-token                                          # on a machine with a browser
homelab/cyc26/inference-proxy/set-credential.sh anthropic
homelab/cyc26/inference-proxy/set-credential.sh opencode
homelab/cyc26/inference-proxy/set-credential.sh openai <file>   # "openai" entry of a Pi auth.json
```

For `openai`, log in to Pi with a fresh agent directory so no other machine
shares the refresh token: `PI_CODING_AGENT_DIR=$(mktemp -d) pi`, then `/login`,
pick the ChatGPT subscription, and pass `<dir>/auth.json` to the script. Delete
that directory afterwards. The ChatGPT refresh token rotates: after the proxy
takes it over, every other copy of that login (a laptop's Pi, the workspace's
old `auth.json`) stops working once the proxy refreshes. That is intended; the
proxy is the single refresher. A failed refresh logs the status only and the
proxy answers `502` until a fresh login is stored.

## Limits and residual risk

- A pass proves a teammate to the proxy, not to the shared Unix user. Another
  teammate in the same workspace can read your pass from `/proc/<pid>/environ`
  or `~/.cyc26/pass`. Revoke it and issue a new one if that matters. What they
  cannot get is the real credential.
- Anyone holding a valid pass can spend the captain's subscriptions through
  the proxy. Use `--cap`, watch `usage`, and revoke at offboarding.
- The Anthropic route attaches a Claude subscription token, which Anthropic
  accepts only from Claude Code. Pi and OpenCode are not routed to it.
- Only the three listed providers are proxied. The workspace can still reach
  the internet, so a teammate can call other services with their own keys.
