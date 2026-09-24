# `coder-setup`: upstream skill provenance + homelab gap analysis

## Provenance

`SKILL.md`, `agents/`, `references/`, `scripts/` are vendored verbatim from
the official open-source skill set at `github.com/coder/skills`
(`skills/setup/`), MIT licensed, as fetched 2026-09-24. Only `assets/`
(binary icons) was omitted. Do not edit the vendored files for homelab
reasons — re-fetch from upstream to update, so the diff stays reviewable.
Homelab adaptations live here, in `coder-homelab`, and in `homelab/`.

This directory satisfies inbox-1i2m.1 (install and use Coder's official
setup skill) in fork-local form: a global `npx skills add coder/skills
--global` install writes outside any repo and would not travel with the
config repo, so the bootstrap workspace carries the skill instead. Humans
who want it globally can still run that command.

## When to use which skill

- **New deployment from scratch** (no coderd yet): `coder-setup` drives —
  Discover, Install, Start, Admin, Starter template, Workspace, Hand off.
- **This homelab (lnx already runs coderd)**: `coder-homelab` drives.
  Reach for `coder-setup` only for its phase checklists and
  `references/troubleshooting.md`, never its install phases.

## Gap analysis: upstream assumptions vs homelab (2026-09-24)

1. **Fresh install assumed; we orient.** Setup phases 2-4 (install,
   start, first admin) must NOT run against lnx — coderd, users, and the
   template already exist. Running them would create a second deployment
   or clobber CLI auth. The skill's own guard ("confirm before replacing
   existing Coder state") is the backstop; the homelab rule is stronger:
   inspect-before-mutate, no reinstall path.
2. **Auth: device flow vs existing accounts.** Upstream prefers GitHub
   device flow for fresh deployments. lnx uses email/password accounts
   (`admin`, `trillium`, `trillium-admin`, `trillium-web`); the device-flow
   scripts are retained for future fresh hosts (e.g. minis), not lnx.
3. **Access URL: tunnel vs tailnet.** Upstream quick-start defaults to a
   `*.try.coder.app` tunnel or a public domain. Homelab is tailnet-only
   (`http://lnx.hippo-tilapia.ts.net:7080`), which is exactly why the
   `coder_access_host` bridge-DNS fix exists in `homelab/templates/`.
4. **Devel server, no client binaries.** Upstream install/verify assumes a
   stable release with downloadable clients. lnx runs
   `v2.37.1-devel+70321bfd78`; expect version-mismatch warnings and use the
   closest documented client. Do not "fix" this by hand-copying binaries.
5. **Aligned: no trial, telemetry untouched, health checks.** The skill's
   `--first-user-trial=false`, "do not disable telemetry on the user's
   behalf", and `/healthz` readiness rules match homelab constraints
   (Community-first) and should be preserved in any future fresh setup.
6. **Useful upstream practices adopted:** starter-template push flow with
   explicit required params (`jetbrains_ides=[]` pattern), secret-variable
   discipline (never `terraform.tfvars`/plain `--variable`), wait-for-agent
   `ready` (not just build success), state dir for generated files.
7. **Troubleshooting overlap confirmed:** upstream `references/`
   troubleshooting covers embedded-Postgres-on-ARM (mini2's Rosetta
   requirement, brain-89mf1) and tailnet DNS rebind (same family as our
   bridge-DNS fix). Read it before diagnosing from scratch.
