# Template `gnhf-agent-runner` — self description

- **Intended workload:** overnight/agent-runner workspaces that run
  [gnhf](https://www.npmjs.com/package/gnhf) inside a minimal Linux
  container while doing inference against MacBook-hosted Ollama. No local
  Ollama and no model weights in the workspace.
- **Infrastructure type:** Docker containers on the lnx host, provisioned
  by coderd's built-in provisioner. No cloud, no Kubernetes.
- **Persistence / lifecycle ownership:** one Docker volume persisted at
  `/home/coder` (agent CLI configs live under `$HOME`) plus a second
  volume at `/workspace` for scratch repos and agent runs. Both survive
  stop/start; everything else is ephemeral across rebuilds. Workspace
  containers are owned by Coder lifecycle, manage with
  `coder start|stop|delete`, never `docker rm`.
- **Capabilities:** terminal/SSH via workspace agent, CPU/RAM/disk
  metadata, Git identity env preset from owner profile, `gnhf-smoke-test`
  and `gnhf-run-local` on `PATH`.
- **Safety constraints:** Community-only features; tailnet-only access URL;
  no secrets in params, use Coder secret variables for
  `anthropic_api_key`. `qwen3:32b` is the default model for a reason: do
  not retune the template to another model family without asking.
- **Fork delta vs upstream `docker` starter:** `coder_access_host`
  variable plus a `host-gateway` block (same fix as
  `homelab/templates/docker-test`), the Ollama URL variables and
  container host pin below, a `/workspace` volume, local-model env/config
  provisioning, and the two helpers. No code-server/JetBrains modules:
  this template is terminal-first and barebones on purpose.

## How workspaces reach Ollama

Two facts combine here:

1. Ollama is plain HTTP, so there is no TLS/SNI trap, only name
   resolution. Bridge containers on the Coder host do NOT resolve
   tailnet hostnames (a container resolves
   `macbook.hippo-tilapia.ts.net` to a public wildcard address and
   hangs; the host is fine). The template therefore pins the Ollama
   hostname twice: a Docker `host` block
   (`ollama_hostname` -> `ollama_ip_override`) and a matching `/etc/hosts`
   entry written at startup (via in-place rewrite, since `sed -i` cannot
   rename onto the mounted hosts file).
2. The MacBook's Ollama must actually listen on its tailnet interface.
   Verified 2026-09-29: `ollama serve` on the MacBook binds localhost
   only, so the tailnet IP refuses port 11434. Until that changes
   (e.g. `OLLAMA_HOST=0.0.0.0` in Ollama's environment plus a restart),
   no workspace can connect and `gnhf-smoke-test` will fail at
   `GET /api/tags reachable` with `Connection refused`. That failure is
   the diagnostic, not a template bug.

Keep `ollama_ip_override` fresh: 100.x tailnet IPs are stable per device
but not guaranteed. Refresh with `tailscale ip -4` on the MacBook and
push a new template version when it changes.

## Variables

| Name | Default | Notes |
|---|---|---|
| `docker_socket` | `""` | Optional Docker socket URI, same as starter. |
| `coder_access_host` | `lnx.hippo-tilapia.ts.net` | Access-URL host mapped to host-gateway so the agent reaches coderd. |
| `ollama_base_url` | `http://macbook.hippo-tilapia.ts.net:11434` | Ollama base URL as seen from the container. Rendered into env, pi and opencode configs. |
| `ollama_hostname` | `macbook.hippo-tilapia.ts.net` | Hostname pinned inside the container. |
| `ollama_ip_override` | `100.74.138.74` | Tailnet IPv4 for the pin. Empty string disables pinning (container DNS). |
| `anthropic_api_key` | `""` (sensitive) | Real Claude credentials, opt-in only. Local mode needs no key. |

Exported into every workspace (agent env plus `/etc/profile.d/ollama-local.sh`
for SSH shells): `OLLAMA_HOST`, `OPENAI_BASE_URL` (`<base>/v1`),
`OPENAI_API_KEY=ollama`, `ANTHROPIC_BASE_URL` (`<base>`),
`ANTHROPIC_AUTH_TOKEN=ollama`.

Configured at startup (write-once; the smoke test fails if they drift
from `OLLAMA_HOST`, e.g. after the URL param changes, until the stale
file is removed and the workspace restarted):

- `~/.pi/agent/models.json`: an `ollama-local` provider
  (`openai-completions` against `<base>/v1`, dummy key) exposing
  `ollama-local/qwen3:32b`, `qwen3-coder:30b`, `qwen3:8b`, `gemma3:1b`,
  so `pi --list-models` shows `ollama-local/*`.
- `~/.config/opencode/opencode.json`: `ollama` provider
  (`@ai-sdk/openai-compatible`, `<base>/v1`) with qwen3 model entries
  and permissive (`"*": allow`) agent-runner permissions.
- Claude Code needs no config file: `ANTHROPIC_BASE_URL` plus
  `ANTHROPIC_AUTH_TOKEN=ollama` point it at Ollama's Anthropic-compatible
  API (Ollama >= v0.14.0). Run `claude --model qwen3-coder:30b`.

Installed at startup (each guarded by command presence, so restarts are
fast): `gnhf` (`npm install -g gnhf`), `pi`
(`@earendil-works/pi-coding-agent`), `claude`
(`@anthropic-ai/claude-code`), `opencode` (official installer, symlinked
into `/usr/local/bin`), plus `git curl jq ripgrep fd-find
ca-certificates openssh-client` and Node 22 LTS when node is missing or
older than 18.

## Helpers

- `gnhf-smoke-test`: checks every CLI exists, the local-model env is
  set, the Ollama hostname resolves, `/api/tags` and `/v1/models`
  respond, `pi --list-models` shows `ollama-local/*`, both config files
  match `OLLAMA_HOST`, and `opencode --version` / `claude --version`
  launch. Prints `PASS`/`FAIL` per check, exits nonzero on any failure.
- `gnhf-run-local [--agent NAME] [--model MODEL] [--max-iterations N] "<objective>"`:
  foreground gnhf run defaulting to `--agent pi
  --model ollama-local/qwen3:32b --max-iterations 10`. Refuses dirty git
  trees and non-repos, enforces a positive-integer iteration cap, then
  `exec`s gnhf directly, never wrapped in `timeout`, so the TUI keeps
  its terminal.

## Push a new version (from lnx)

The deployment CLI is an exact-version copy of the server binary:

```sh
export CODER_URL=http://localhost:7080
export CODER_SESSION_TOKEN=$(cat ~/.coder-admin-token)
/home/trillium/coder-cli templates push \
  --directory /home/trillium/coder-templates/gnhf-agent-runner --yes \
  --message "reason for the change"
```

Keep the working copy at `/home/trillium/coder-templates/gnhf-agent-runner`
in sync with `templates/gnhf-agent-runner/` here, the same way
`docker-test` syncs with `homelab/templates/docker-test`.

## Validation (new workspace)

```sh
gnhf-smoke-test
pi --list-models | grep ollama-local/
mkdir -p /workspace/scratch && cd /workspace/scratch
git init && git commit -q --allow-empty -m init
gnhf-run-local --max-iterations 1 "add a HELLO file containing hi"
```

opencode and claude local-Ollama paths are covered up to CLI launch by
the smoke test; a full agentic run through either is not part of
acceptance. If either path fails in a workspace, record the exact
blocker here rather than silently dropping the check.
