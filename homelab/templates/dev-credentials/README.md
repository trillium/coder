# Template `dev-credentials` — LIVE on lnx (proves disposable-box loop)

Intended workload: displayd authorization work with a full agentic setup —
Python + Pillow, `opencode`, `pi`, GitHub access, repo checked out ready to edit/test.

Status: LIVE. Active versions on lnx; default repo is `trillium/displayd` (https, so the
first-start clone works with token auth — no SSH keys needed). The auth
story is export-based, not baked-in (see below).

## What this fork adds over `docker-test`

- Same base image, same `coder_access_host` bridge-DNS fix, same
  home-volume conventions.
- Two placeholder secret slots: `github_token` and `npm_token`.
  Both are `sensitive = true`, `ephemeral` where supported, never
  echoed, never written to tfvars.
- Startup script writes credentials to the standard homes
  (`gh auth`, `~/.npmrc`) best-effort and must never block agent
  startup.

## Auth: durable export from the MacBook (the actual ask)

Secrets are never baked into the template and never committed. Flow:

1. Create the workspace (pass a GitHub token once for the first-start
   clone, or clone later by hand — repo stays follow-on config).
2. From the MacBook, run `homelab/bin/coder-auth-export.sh
   <owner/workspace>` — pushes gh (hosts + config), opencode auth, and
   pi auth byte-for-byte over the existing `coder ssh` session, 0600 at
   rest. Values never print, never touch git.
3. Agents (dashboard chat + workspace shells) get working `gh`, opencode,
   and pi with API keys cleanly available from the standard config paths.

Home volumes persist across stop/start, so the export survives day-to-day
use — re-run it after rebuild/delete. This is the stopgap until a proper
secret store supersedes it.

The repo does NOT have to be baked in. `config_repo_url` defaults to empty
and can be passed at `coder create --parameter` time, or you can `git clone`
inside the workspace later. Same for secrets — add the variable now, fill the
value at workspace creation, rotate without rebuilding the template.

Current defaults target displayd (`git@github.com:trillium/displayd.git`,
Python + Pillow) plus `opencode` + `pi`. Replace the two placeholder secrets
with the real set.
3. Edit here in the fork, copy dir to `/home/trillium/coder-templates/dev-credentials/`
   on lnx, review `diff` there.
4. `coder templates push dev-credentials --directory /home/trillium/coder-templates/dev-credentials --yes --message "<reason>"`
   from a shell authed to lnx.
5. `coder templates pull` into scratch and diff back to confirm no drift.
6. `coder create <name> --template dev-credentials --yes`, wait for
   agent `ready`, verify clone + auth + build.

Safety: Community-only features. No secrets in params, tfvars, or
`--variable` values on the command line history — pass secrets via
Coder's secret-variable UI/flags at workspace creation. Startup
script never runs with `set -x`.
