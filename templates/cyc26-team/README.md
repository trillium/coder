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

## Credential isolation

All teammates run as the same Unix user in the same home, so anything the
workspace holds is readable by every teammate. A Coder user secret of the
workspace owner does not help: it reaches every session
(`coderd/agentapi/manifest.go` loads the secrets of `workspace.OwnerID` and
`agent/agent.go` applies them to whoever connects). Real credentials therefore
live only in the proxy container on lnx.

- **Pass:** `cyc26-pass` (installed in `/usr/local/bin`) prints the caller's
  pass from `$CYC26_PASS` (set per session with `CODER_SSH_ENV`, not in a
  command line) or `~/.cyc26/pass`. Claude Code (`apiKeyHelper`) and Pi
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
| `docker_socket` | `""` | Optional Docker socket URI. |
| `pi_version` | `1.4.0` | npm version of `@earendil-works/pi-coding-agent`. |
| `opencode_version` | `1.19.4` | npm version of `opencode-ai`. Keep it on 1.x. |

## Push

```sh
coder templates push cyc26-team --directory templates/cyc26-team --yes
```

Existing workspaces pick up a new version with `coder update admin/cyc26-team`.
