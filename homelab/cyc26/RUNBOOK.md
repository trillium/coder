# Commit Your Code 2026 team workspace

Operator runbook for the shared Coder workspace used by the cyc26 cohort team
(project-mcik, six-week project, about five teammates). Coder is
`http://lnx.hippo-tilapia.ts.net:7080`, tailnet-only, community edition (no
license). Template source: `templates/cyc26-team/`. Token mechanism:
`homelab/claude-code-auth/RUNBOOK.md`.

## Current state

- Template `cyc26-team` is pushed to the server.
- Workspace `admin/cyc26-team` exists and is running. It has git, gh, python3,
  Node 22, pnpm, Claude Code, and code-server. It has no auto-stop schedule.
- The `claude-code-oauth-token` secret does not exist yet, so `claude` reports
  not logged in. No teammate accounts exist yet.

## Cohort rule: no AI assistants in the first two weeks

Claude Code is installed but unauthenticated. Do not run the token step until
the team may use AI assistants. If the token was stored early, take it back with
`coder secret delete claude-code-oauth-token` and
`coder restart admin/cyc26-team --yes`.

## Captain: deliver the Claude token (the one human step)

Run on a machine with a browser (the MacBook), signed in to Coder as `admin`
(the workspace owner; the secret only reaches workspaces its user owns):

```sh
coder login http://lnx.hippo-tilapia.ts.net:7080   # if signed out; use the admin account
coder whoami                                        # must say admin
claude setup-token                                  # approve in the browser, copy the token
homelab/claude-code-auth/coder-claude-token-push.sh --verify admin/cyc26-team
```

Paste the token at the script's hidden prompt. (`--from-clipboard` reads it
from the macOS clipboard instead; piping `claude setup-token` straight into the
script is untested because the command is interactive.) Never use the Keychain
"Claude Code-credentials" entry or `~/.claude*` files; they hold a rotating
refresh token that must have one consumer. The setup-token is a separate,
one-year, non-rotating token.

`--verify` restarts the workspace and runs `claude -p hi` in it.

## Verify

```sh
coder ssh admin/cyc26-team -- claude auth status   # loggedIn: true once the secret exists
coder ssh admin/cyc26-team -- 'node --version; pnpm --version; gh --version | head -1; claude --version'
```

## Teammate access

Without a license there are no custom roles or template RBAC, but workspace
sharing works: the organization setting `shareable_workspace_owners` is
`everyone` and nothing in the sharing code checks entitlements. So each
teammate gets their own Coder user and the captain shares the one workspace
with the `use` role (SSH, apps, start, stop; no delete or share). Per-person
users keep who-did-what visible in Coder and let you remove one person
without touching the others. A single shared team login would not.

Teammates see the owner's token (`printenv`) while connected; revoke on
offboarding or when the project ends.

### Onboard a teammate (captain, as admin)

```sh
coder users create --username <handle> --email <email> --full-name "<Full Name>"
# answer the prompts; pick a password login and a one-time password
coder sharing add admin/cyc26-team --user <handle>:use
coder sharing status admin/cyc26-team
```

Add several at once with `--user ada:use,bo:use`. Send the teammate the
password out of band and ask them to change it at first login (dashboard,
Account, Security).

### Reach the tailnet-only server

Do not expose Coder publicly. Share the `lnx` node with each teammate through
Tailscale node sharing (admin console, Machines, `lnx`, Share). Each teammate
needs a Tailscale account of their own and accepts the invite. Then restrict
what the shared node exposes to Coder only: in the tailnet policy file, allow
users outside your tailnet (Tailscale's `autogroup:shared` selector) to reach
`lnx` on `tcp:7080` and nothing else, so SSH and Docker on the host stay
private. Confirm the selector name in the policy editor; this runbook was not
tested against the Tailscale console.

Check from the teammate's machine:

```sh
curl -s http://lnx.hippo-tilapia.ts.net:7080/api/v2/buildinfo
```

If the name does not resolve, use the node's tailnet IP (`100.81.88.113`)
instead.

### Teammate first run

```sh
# install the CLI (brew install coder, or https://coder.com/docs/install)
coder login http://lnx.hippo-tilapia.ts.net:7080
coder ssh admin/cyc26-team
```

Version mismatch warnings are expected (server is a dev build). The workspace
page also offers code-server and a web terminal. Set your own commit identity
per session so commits carry your name and not the owner's: see
`templates/cyc26-team/README.md`, "Per-teammate git identity".

### Offboard a teammate

```sh
coder sharing remove admin/cyc26-team --user <handle>
coder users suspend <handle>
```

## Rotate the Claude token

Before the one-year expiry (calendar about 11 months after minting):

```sh
claude setup-token
homelab/claude-code-auth/coder-claude-token-push.sh --verify admin/cyc26-team
```

The script updates the existing secret; `--verify` restarts the workspace.

## Revoke

```sh
coder secret delete claude-code-oauth-token
coder restart admin/cyc26-team --yes
```

Then revoke the token in the Claude account settings. The restart matters: a
running agent keeps the value it loaded at start.

## Update the template

```sh
coder templates push cyc26-team --directory templates/cyc26-team --yes
coder update admin/cyc26-team
```

Updating rebuilds the container; `/home/coder` is a volume and is kept.
Do not `docker rm` workspace containers or volumes by hand.
