# Template `docker-test` — self description

- **Intended workload:** general Linux development shells on lnx; the
  default starting point for container workspaces and the canary template
  for testing control-plane changes.
- **Infrastructure type:** Docker containers on the lnx host, provisioned
  by coderd's built-in provisioner. No cloud, no Kubernetes.
- **Persistence / lifecycle ownership:** each workspace gets a Docker
  volume persisted at `/home/coder` that survives stop/start. Everything
  outside home is ephemeral across rebuilds. Workspace containers are owned
  by Coder lifecycle — manage with `coder start|stop|delete`, never
  `docker rm`. Deleting a workspace deletes its containers; the home volume
  lifecycle follows the template — confirm before deleting anything keeping
  unique data.
- **Capabilities:** terminal/SSH via workspace agent, CPU/RAM metadata,
  `jetbrains_ides` multi-select param (pass `[]` for none), Git identity
  env preset from owner profile.
- **Safety constraints:** Community-only features; tailnet-only access URL;
  bridge-DNS fix required (see below) or agents stall at `connecting`;
  no secrets in params — use Coder secret variables.
- **Fork delta vs upstream `docker` starter:** `coder_access_host`
  variable (default `lnx.hippo-tilapia.ts.net`) plus a `host { host-gateway }`
  block on the workspace container, so the agent can download itself from
  coderd. Verify any time with:
  `diff homelab/templates/docker-test/main.tf examples/templates/docker/main.tf`
- **Version on lnx:** `auspicious_cole75` (pulled 2026-09-24; byte-identical
  to `main.tf` here).

## Sync: fork <-> lnx host working copy

Pushes to lnx run from `/home/trillium/coder-templates/docker-test/`
(SSH-reachable, host-only secrets stay out of git). Direction that wins:

1. Edit here in the fork.
2. Copy the dir to the host working copy, review `diff` there.
3. `coder templates push docker-test --directory
   /home/trillium/coder-templates/docker-test --yes --message "<reason>"`
   from a shell authed to lnx.
4. `coder templates pull` into scratch and diff back against this dir to
   confirm no drift.

Upstream starter README (prerequisites, image editing) is preserved in the
pulled `README.md` on the deployment; this file is the homelab overlay.
