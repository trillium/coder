# Template `cyc26-team`

Shared workspace for the Commit Your Code 2026 cohort team (project-mcik).
One captain-owned workspace on the lnx Docker host, used by about five
teammates. Derived from `templates/docker-lnx-server`, which is untouched.

- **Infrastructure:** one Docker container per workspace on lnx, provisioned by
  coderd's built-in provisioner. Tailnet only.
- **Persistence:** one volume at `/home/coder` (repos, `~/.local`, Claude Code
  state). The container itself is rebuilt on every start; `/usr` changes are
  not kept, so the start scripts re-run each time.
- **Tools:** git, gh, python3 (with venv and pip), ripgrep, jq, tmux from apt;
  Node 22 LTS (checksum-verified tarball) and pnpm in `~/.local`; Claude Code
  via the native installer; the Pi coding agent CLI and OpenCode, each pinned
  to an npm version (`pi_version`, `opencode_version`; OpenCode stays on 1.x);
  code-server on the workspace page. Node, pnpm, claude, pi, and opencode are
  linked into `/usr/local/bin` so `coder ssh <ws> -- <cmd>` finds them. The
  base image already has gcc/make, ssh, unzip, vim, wget, and htop. tmux is
  there so a shared workspace keeps long-running sessions across SSH drops.
  The stack of the cohort app is deliberately not chosen here.
- **Credentials:** none in the workspace. Claude Code, Pi and OpenCode send
  their API calls to the inference proxy on lnx
  (`homelab/cyc26/inference-proxy/`), which holds the real credentials; the
  workspace has only the proxy URL and each teammate's pass.
- **Startup is bounded:** every network step has a timeout and each script a
  hard `timeout`. Pi and OpenCode install in their own non-blocking script
  that retries and only warns on failure, so a stalled registry connection
  cannot keep the workspace from becoming ready.
- **Harness configuration:** `coder_script.inference_proxy` merges, with `jq`,
  the proxy settings into `~/.claude/settings.json` (`ANTHROPIC_BASE_URL`,
  `apiKeyHelper`), `~/.pi/agent/models.json` (`openai`, `opencode`,
  `opencode-go` base URLs, key from `cyc26-pass`) and
  `~/.config/opencode/opencode.json`. It keeps anything else in those files.
  Operator steps: `homelab/cyc26/RUNBOOK.md`.

## Tailnet node

Teammates do not need Coder accounts. `coder_script.tailscale` runs
`tailscaled` inside the workspace container in userspace-networking mode (no
`NET_ADMIN` or `/dev/net/tun`) and joins the captain's tailnet as its own node:
`tailscale up --ssh --hostname=<tailscale_hostname> --advertise-tags=tag:cyc26-dev`.
The captain shares only that node with each teammate (Tailscale node sharing),
so a teammate with a free Tailscale account can:

- `tailscale ssh coder@cyc26-dev.<tailnet>.ts.net` (Tailscale SSH lands as the
  workspace user `coder`; VS Code Remote-SSH uses the same host and user), or
- open `https://cyc26-dev.<tailnet>.ts.net:8443`, a code-server with its own
  password (a plain TCP forward when HTTPS is not enabled for the tailnet).

and has no route to `lnx`, the Coder UI, or other templates.

- **Installed in the home volume:** a pinned static Tailscale build
  (`tailscale_version`, verified against the published SHA-256) and, when the
  node is enabled, a pinned code-server (`code_server_version`). `tailscale` and
  `tailscaled` are linked into `/usr/local/bin`.
- **State:** `~/.local/state/tailscale` (root-only) holds the node identity, so
  the auth key is consumed only on the first join and a restart rejoins as the
  same node. Use a non-ephemeral key: an ephemeral node is deleted when the
  workspace stops.
- **Auth key:** the sensitive variable `tailscale_auth_key`, supplied at push
  time (`--variables-file`) and never committed. The script hands it to
  `tailscale up` through a root-only file (not an argument) and removes it. With
  no key and no saved state the script exits and the workspace starts without
  Tailscale. Anything the agent is given is readable by every teammate (same
  Unix user), so use a tagged, non-reusable, short-lived key and clear the
  variable after the first join.
- **Code-server:** `coder_script.code_server_tailnet` runs a second
  code-server on `127.0.0.1:13338` with `auth: password`; `tailscale serve`
  forwards `code_server_tailnet_port` to it. The password is generated on first
  start into `~/.config/cyc26-code-server/config.yaml` (mode 600) and is not in
  the template. The Coder-proxied code-server below is unchanged.
- **Reach:** the tailnet policy decides what shared users and the node can
  reach, not the template. Because teammates have `sudo`, a policy that lets
  `tag:cyc26-dev` initiate connections would let them reach your other nodes.
  The policy and every captain step are in `homelab/cyc26/RUNBOOK.md`.

Not verified against a real tailnet: the scripts passed `tofu validate`,
shellcheck, and a stubbed dry run only.

## Credential isolation

All teammates run as the same Unix user in the same home, so anything the
workspace holds is readable by every teammate. A Coder user secret of the
workspace owner does not help: it reaches every session
(`coderd/agentapi/manifest.go` loads the secrets of `workspace.OwnerID` and
`agent/agent.go` applies them to whoever connects). Real credentials therefore
live only in the proxy container on lnx.

- **Pass:** `cyc26-pass` (installed in `/usr/local/bin`) prints the caller's
  pass from `$CYC26_PASS` (set per session with `CODER_SSH_ENV`, not in a
  command line), then `~/.cyc26/passes/$CYC26_USER` (a teammate on the tailnet
  node, who has no `coder ssh`, runs `export CYC26_USER=ada`; the captain drops
  the file in over SSH), then `~/.cyc26/pass`. Claude Code (`apiKeyHelper`) and Pi
  (`!/usr/local/bin/cyc26-pass`) call it; the `opencode` wrapper in
  `/usr/local/bin` exports it for OpenCode. No config file holds a pass.
- **Not in the template:** no credential or pass in the template, Terraform
  state, image, agent environment, code-server, process arguments or git.
- **Residual:** teammates can read each other's passes (same Unix user) but not
  the real credentials; a pass is revocable per person.

## Per-teammate git identity

All teammates run as the same Unix user in the same home, so `~/.gitconfig`
holds only the owner's fallback identity (set once). A teammate sets their
own identity per session; session environment outranks `~/.gitconfig`:

```sh
# from the teammate's machine (SSH sessions)
coder ssh admin/cyc26-team \
  -e GIT_AUTHOR_NAME="Ada Lovelace",GIT_AUTHOR_EMAIL=ada@example.com,GIT_COMMITTER_NAME="Ada Lovelace",GIT_COMMITTER_EMAIL=ada@example.com

# inside any terminal (web terminal, code-server), per shell
eval "$(cyc26-me 'Ada Lovelace' ada@example.com)"
```

Check with `git var GIT_AUTHOR_IDENT` before committing.

## Variables

| Name | Default | Notes |
|---|---|---|
| `coder_access_host` | `lnx.hippo-tilapia.ts.net` | Access-URL host mapped to the Docker host gateway so the agent reaches coderd. |
| `inference_proxy_url` | `http://host.docker.internal:8787` | Inference proxy as seen from the workspace (`host.docker.internal` is the Docker host gateway). |
| `tailscale_auth_key` | `""` | Sensitive. Tagged, non-reusable Tailscale auth key, supplied at push time. Empty starts the workspace without Tailscale. |
| `tailscale_hostname` | `cyc26-dev` | Tailnet machine name of the workspace node. |
| `tailscale_version` | `1.104.2` | Static Tailscale build from `pkgs.tailscale.com/stable`. |
| `code_server_version` | `4.141.1` | code-server served on the tailnet node. |
| `code_server_tailnet_port` | `8443` | Tailnet port forwarded to that code-server; keep it in sync with the tailnet policy. |
| `docker_socket` | `""` | Optional Docker socket URI. |
| `pi_version` | `1.4.0` | npm version of `@earendil-works/pi-coding-agent`. |
| `opencode_version` | `1.19.4` | npm version of `opencode-ai`. Keep it on 1.x. |

## Push

```sh
coder templates push cyc26-team --directory templates/cyc26-team --yes
```

To enable the tailnet node, add `--variables-file <file>` with
`tailscale_auth_key` (RUNBOOK, "Give the workspace the key").

Existing workspaces pick up a new version with `coder update admin/cyc26-team`.
