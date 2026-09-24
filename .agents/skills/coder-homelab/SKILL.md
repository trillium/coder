---
name: coder-homelab
description: "Operate Trillium's homelab Coder deployment on lnx (tailnet-only coderd, Docker workspaces, Community features). Use for orientation, provisioning, services, templates, workspaces, modules, troubleshooting, and architecture discovery. Inspect-before-mutate; existing Macs are persistent infrastructure."
---

# Coder homelab operations

Deployment facts: `homelab/AGENTS.md`. Upstream fresh-install flow:
`.agents/skills/coder-setup/SKILL.md` (do not apply it to lnx — the
deployment already exists; orient, do not reinstall).

## 1. Read-only orientation (do this first, every session)

No approval needed — these change nothing:

```sh
curl -s -m 10 http://lnx.hippo-tilapia.ts.net:7080/api/v2/buildinfo
coder templates list        # templates on lnx
coder list                  # workspaces on lnx
coder show <owner>/<name>   # one workspace: resources, health, versions
coder users list            # who exists
ssh -o BatchMode=yes trillium@lnx 'docker ps --format "{{.Names}} {{.Image}} {{.Status}}"'
ssh -o BatchMode=yes trillium@lnx 'docker inspect coder --format "{{.Config.Image}} restart={{.HostConfig.RestartPolicy.Name}}"'
```

Expected baseline (2026-09-24): server `v2.37.1-devel+70321bfd78`,
template `docker-test:auspicious_cole75`, workspaces `coder-inspect` and
`deepseek-test` Started and healthy. Any deviation is a discovery —
record it in `homelab/AGENTS.md` (facts) or below (procedures).

Readable-over-SSH host paths: `/home/trillium/coder-templates/<template>/`
(template working copies pushed to lnx). Never read
`/home/trillium/.coder-admin-token` or `~/.coder-first-admin-pass` —
host-only secrets, mode 600, not needed for CLI work from an authed box.

## 2. Services on lnx (know what you are looking at)

`docker ps` on lnx shows Coder and non-Coder containers side by side:

- `coder` — the control plane. The only container this skill manages.
- `coder-<owner>-<workspace>` — workspace containers, owned by template
  lifecycle (stop/start/delete via `coder`, never `docker rm` directly).
- `pihole`, `open_crm-try-*`, `coder-admin-proof1` — unrelated or
  historical. Do not touch without a brief that names them.

## 3. Templates

Canonical sources live in `homelab/templates/<name>/`; the host working
copy at `/home/trillium/coder-templates/<name>/` is what gets pushed.
Keep the two in sync — the README in each template dir names the
direction that wins.

Read-only inspect of any template version without touching the host copy:

```sh
T="$(mktemp -d)/<name>" && coder templates pull <name> "$T"
diff "$T/main.tf" homelab/templates/<name>/main.tf   # fork drift?
diff "$T/main.tf" examples/templates/docker/main.tf  # upstream drift?
```

Push a new version (from a shell authed to lnx, host copy current):

```sh
coder templates push <name> -d /home/trillium/coder-templates/<name> \
  --message "reason for the change" --yes
coder list   # existing workspaces stay on their version until updated
```

Rules: one change per push with a `--message`; `coder templates create`
is deprecated/broken for this flow — use `push`. Required list params
with an obvious "none" take `[]` (e.g. `--parameter 'jetbrains_ides=[]'`);
never put secrets in `terraform.tfvars` or plain `--variable` values.

## 4. Workspaces

```sh
coder create <name> --template <template> --parameter '...' --yes
coder start|stop|restart <owner>/<name>
coder delete <owner>/<name>   # confirm lifecycle ownership in the template README first
coder ssh <owner>.<name>      # quote remote paths: bare ~ expands on the jump host
```

Home volumes (`/home/coder`) persist across stop/start; anything outside
home is ephemeral on rebuild. A successful build is not enough — wait for
the agent to reach `ready`/healthy. `coder-inspect` is the canary: exercise
risky template changes against a scratch workspace before touching real ones.

## 5. Modules and registry

Templates compose versioned registry modules (see upstream
`examples/templates/` and `coder/skills` templates skill on demand).
Prefer upstream modules unmodified; when homelab needs a fork delta
(bridge-DNS fix, admin tooling), keep the delta minimal, commented with
*why*, and recorded in the template README so it survives rebases onto
newer upstream starters.

## 6. macOS fleet

- CLI on MacBook/minis is installed via Homebrew and authed to lnx;
  minis use file sessions (`CODER_USE_KEYRING=false`, keychain is
  unreachable over SSH — exit 36 means exactly this).
- iOS is browser-only: dashboard URL over Tailscale, web terminal and
  code-server workspace apps. No CLI/Desktop on iOS.
- Do NOT enroll Macs as External Workspaces without explicit approval:
  the Premium/Early Access gate is unconfirmed and enrollment changes
  shared deployment state plus machine runtime. Full rollout record:
  task `task-i4umg`.

## 7. Troubleshooting (observed, not theorized)

- **Workspace stuck `connecting`:** bridge containers cannot resolve
  tailnet DNS — the template must map `coder_access_host` to
  `host-gateway` (the `docker-test` fork delta). Check the template has
  the `host` block before anything else.
- **CLI/server version warning:** expected on lnx (devel server, no
  downloadable client). Only exact-match-sensitive ops are affected.
- **`coder login` hangs over SSH:** macOS keyring unreachable + first-user
  `cli-auth` prompt need a TTY. Use API-first setup
  (`POST /api/v2/users/first`, `/api/v2/users/login`) with
  `CODER_USE_KEYRING=false` + `CODER_SESSION_TOKEN`; never let the CLI
  prompt over a dead stdin (kill -9 the hanger).
- **404 on `/bin/coder-darwin-arm64`:** the devel server publishes no
  client binaries. Use brew stable/tap and accept the delta.
- **`coder ssh` runs in the wrong home:** quote remote paths.

## 8. Discovery discipline

New durable facts go to `homelab/AGENTS.md`; new procedures here;
infrastructure definitions into `homelab/templates/`; deployment-incident
knowledge back to brain (`brain-3vp70` family). State the verification
command and date with every claim — "verified 2026-09-24 via `docker
inspect coder`" beats "should be".
