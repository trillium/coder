# Template `bootstrap-admin` — self description (NOT yet pushed to lnx)

- **Intended workload:** the first workspace: Coder administration and
  config-as-code. Coder CLI, Git-backed config repo checkout (`~/config`),
  Terraform tooling, docs/runbooks, and the coder-homelab skill. A new
  agent starting here orients to the deployment instead of inferring it
  from an empty shell.
- **Infrastructure type:** Docker container on the lnx host, built from the
  same upstream `docker` starter base as `docker-test`, plus the
  `coder_access_host` bridge-DNS fix and an admin startup script.
- **Persistence / lifecycle ownership:** persistent home volume at
  `/home/coder` (survives stop/start; outside-home is ephemeral). Managed
  by Coder lifecycle only — never `docker rm`. The config repo working
  copy inside is a convenience clone; the fork is canonical.
- **Capabilities:** everything `docker-test` provides (terminal/SSH,
  code-server, CPU/RAM/disk metadata, Git identity env) plus preinstalled
  `git`, `gh`, `terraform`, `node` runtime deps, pinned Coder CLI
  (v2.36.6, closest documented client to the devel server — mismatch
  warning expected), and optional `config_repo_url` / `dotfiles_uri` params.
- **Safety constraints:** owner/admin use only — this workspace holds a
  live `coder` CLI session against the control plane. Community-only
  features; no secrets in params; startup script is best-effort and must
  never block agent startup (package failures fall through).
- **Status:** pushed to lnx 2026-09-24 (version `encouraging_yost24`) and
  live as workspace `trillium-admin/bootstrap-admin` (Started,
  agent healthy; git/gh/terraform/coder CLI verified, startup script
  completed via `~/.bootstrap_done`). Host working copy
  `/home/trillium/coder-templates/bootstrap-admin/` is md5-in-sync with
  this dir. To land future versions: sync this dir to that host path,
  then `coder templates push bootstrap-admin --directory <dir with
  identical content> --yes --message "reason"` from any shell authed
  to lnx (no `coder` CLI on lnx itself, so push from an authed Mac).

## Relationship to `docker-test`

Same base image, same DNS fix, same volume conventions. If the base needs
a change, make it in both dirs (or promote a shared module later) and note
it here. `docker-test` remains the canary for control-plane experiments;
`bootstrap-admin` is where the resulting procedures get written down.
