# Template `cyc26-team`

Shared workspace for the Commit Your Code 2026 cohort team (project-mcik).
One captain-owned workspace on the lnx Docker host, used by about five
teammates. Derived from `templates/docker-lnx-server`, which is untouched.

- **Infrastructure:** one Docker container per workspace on lnx, provisioned by
  coderd's built-in provisioner. Tailnet only.
- **Persistence:** one volume at `/home/coder` (repos, `~/.local`, Claude Code
  state). The container itself is rebuilt on every start; `/usr` changes are
  not kept, so the start scripts re-run each time.
- **Tools:** git, gh, python3 (with venv and pip), ripgrep, jq from apt; Node 22
  LTS (checksum-verified tarball) and pnpm in `~/.local`; Claude Code via the
  native installer; code-server on the workspace page. Node, pnpm, and claude
  are linked into `/usr/local/bin` so `coder ssh <ws> -- <cmd>` finds them.
  The stack of the cohort app is deliberately not chosen here.
- **Claude Code auth:** `CLAUDE_CODE_OAUTH_TOKEN` comes from the workspace
  owner's `claude-code-oauth-token` Coder user secret. Nothing in this template,
  the image, Terraform state, or git holds it, and without the secret `claude`
  reports not logged in. Mechanism: `homelab/claude-code-auth/RUNBOOK.md`.
  Operator steps: `homelab/cyc26/RUNBOOK.md`.

## Why a user secret and not a host env file

The question was whether a secret owned by the workspace owner reaches other
Coder users who connect to the same workspace. It does. The server builds
the agent manifest from the secrets of `workspace.OwnerID`
(`coderd/agentapi/manifest.go`), and the agent applies them to every session
it serves (`agent/agent.go`), whoever connected. There is no per-connecting-user
step, and `TestAgent_Session_SecretInjection` exercises that shared path.
That keeps the single-token property with no extra moving parts, so no
bind-mounted env file or team user is needed. The consequence is that
anyone with access to the workspace can read the token (`printenv`); that is
inherent in sharing a workspace "with my keys", and revocation (below) is the
control.

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
| `docker_socket` | `""` | Optional Docker socket URI. |

## Push

```sh
coder templates push cyc26-team --directory templates/cyc26-team --yes
```

Existing workspaces pick up a new version with `coder update admin/cyc26-team`.
