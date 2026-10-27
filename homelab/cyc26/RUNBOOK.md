# Commit Your Code 2026 team workspace

Operator runbook for the shared Coder workspace used by the cyc26 cohort team
(project-mcik, six-week project, about five teammates). Coder is
`http://lnx.hippo-tilapia.ts.net:7080`, tailnet-only, community edition (no
license). Template source: `templates/cyc26-team/`. Credential proxy:
`homelab/cyc26/inference-proxy/`. Teammates reach the workspace as its own
shared tailnet node, with no Coder account (see "Teammate access: the shared
tailnet node"); Coder accounts remain as an alternative.

## Current state

- Template `cyc26-team` is pushed to the server.
- Workspace `admin/cyc26-team` exists and is running. It has git, gh, python3,
  tmux, Node 22, pnpm, Claude Code, Pi, OpenCode 1.x, and code-server. It has
  no auto-stop schedule.
- The captain's credentials (Claude setup token, ChatGPT login, OpenCode key)
  are held by the inference proxy on lnx, not by the workspace. The workspace
  has only the proxy URL and a per-teammate pass. Design, routes and limits:
  `homelab/cyc26/inference-proxy/README.md`. No teammate accounts exist yet.
- The tailnet node (`cyc26-dev`) is defined in the template but not enabled
  yet: it needs the policy, auth key and template push in "Teammate access: the
  shared tailnet node".

## Credentials: the inference proxy

Everyone connected to the workspace is the same Unix user, so anything in the
environment or home directory is visible to all of them. So the workspace never
holds a real credential. A container on lnx (`cyc26-proxy`, bound to the Docker
bridge address only) holds them and the harnesses call it with a pass:

```text
Claude Code / Pi / OpenCode  --pass-->  cyc26-proxy (lnx)  --real credential-->  Anthropic / OpenAI / OpenCode
```

The template points each harness at the proxy (`coder_script.inference_proxy`)
and installs `cyc26-pass`, which prints the caller's pass. Nothing real is in
the template, Terraform state, image, agent environment, code-server, process
arguments or git. The old `claude-code-oauth-token` and `opencode-go-api-key`
Coder user secrets and the `openai` entry of `~/.pi/agent/auth.json` are not
used and should not exist (they would put a real credential back in every
session).

### Issue a pass (captain, on lnx)

```sh
ssh trillium@100.81.88.113
docker exec cyc26-proxy node passctl.mjs add ada --cap 2000   # prints the pass once; cap optional
```

Send the pass to the teammate over a private channel. The teammate keeps it in
their own shell profile, not in a command line or the repo:

```sh
# teammate's machine: ~/.zshrc or ~/.bashrc
export CODER_SSH_ENV="CYC26_PASS=<pass>"   # coder ssh forwards it into the session
```

For sessions that cannot take a session variable (code-server, web terminal),
put it in `~/.cyc26/pass` in the workspace (`mkdir -p ~/.cyc26 && chmod 700
~/.cyc26`, then write the file with mode 600). Everyone in the workspace can
read that file, so prefer the session variable. Teammates on the tailnet node
(no `coder ssh`) get a per-person file instead: see "Give a teammate their AI
pass".

### Revoke, offboard, audit

```sh
docker exec cyc26-proxy node passctl.mjs revoke ada     # stops on the next request
docker exec cyc26-proxy node passctl.mjs list
docker exec cyc26-proxy node passctl.mjs usage [ada] [--day 2026-10-26]
```

Add `docker exec cyc26-proxy node passctl.mjs revoke <name>` to offboarding. To
stop all AI use at once (for example the no-AI first weeks), revoke every pass
or `docker stop cyc26-proxy`; `docker start cyc26-proxy` restores it.

### Rotate a real credential

```sh
claude setup-token                                            # browser; copy the token
homelab/cyc26/inference-proxy/set-credential.sh anthropic     # hidden prompt
homelab/cyc26/inference-proxy/set-credential.sh opencode      # hidden prompt
homelab/cyc26/inference-proxy/set-credential.sh openai <file> # Pi auth.json from a fresh login
```

No workspace restart is needed; the proxy reads the file on the next request.
Revoking at the source (Claude account settings, OpenCode console, ChatGPT
sessions) plus `set-credential.sh` with the new value closes the loop. The
Claude setup token lasts one year (rotate in about 11 months). The ChatGPT
login refreshes itself inside the proxy. Details and the ChatGPT caveat:
`homelab/cyc26/inference-proxy/README.md`.

### Redeploy the proxy

`homelab/cyc26/inference-proxy/deploy.sh` rebuilds and restarts the container;
credentials, passes and usage on `/srv/cyc26-proxy/data` are kept. The
container restarts with Docker (`unless-stopped`).

## Cohort rule: no AI assistants in the first two weeks

The harnesses are installed but do nothing without a pass. Do not issue a pass
until the team may use AI assistants. If one is out and the team has not
reached week 3, revoke it (see above).

## Verify

```sh
coder ssh admin/cyc26-team -- 'node --version; pnpm --version; gh --version | head -1; claude --version; pi --version; opencode --version; tmux -V'
# harness checks need a pass in the session
CODER_SSH_ENV="CYC26_PASS=<pass>" coder ssh admin/cyc26-team -- 'claude -p "say pong" </dev/null'
# no real credential in the session: the count is 0 and auth.json is absent
coder ssh admin/cyc26-team -- 'env | grep -c "^CLAUDE_CODE_OAUTH_TOKEN=\|^OPENCODE_API_KEY=\|^ANTHROPIC_API_KEY="; ls ~/.pi/agent/auth.json 2>&1'
```

Always put a bound on waiting: `timeout 300 coder ssh admin/cyc26-team --wait yes -- true`.
`--wait yes` blocks until every startup script has finished. If it times out,
the script logs are on the workspace page ("Startup Scripts").

## Teammate access: the shared tailnet node

The workspace container joins your tailnet as its own tagged node
(`cyc26-dev`, `tag:cyc26-dev`) and you share only that node with each
teammate. A teammate needs a free Tailscale account that accepts the share.
They need no Coder account and get no route to `lnx`, the Coder UI, or other
templates. They connect with Tailscale SSH, VS Code Remote-SSH, or code-server
in the browser. Template side: `templates/cyc26-team/README.md`, "Tailnet node".

Trade-offs: you keep the workspace running (it is a normal Coder workspace you
start and stop); everyone is the same Unix user, `coder`; and the AI pass
arrives as a file in the workspace instead of through `coder ssh`.

Status: the template scripts were checked with `tofu validate`, shellcheck and
a stubbed dry run. Nothing below has been run against a real tailnet. Steps
that depend on Tailscale console or policy behavior are marked **untested**.

### 1. Tailnet policy (captain, admin console, Access controls)

Merge these entries into the policy file, replacing the example emails with
the Tailscale login of each teammate (the account that accepts the share, which
can differ from the email you invite). The policy lets the team group reach the
node on SSH (22) and code-server (8443) only, and nothing else. **Untested.**

```json
{
  "groups": {
    "group:cyc26-team": ["ada@example.com", "bo@example.com"]
  },
  "tagOwners": {
    "tag:cyc26-dev": ["autogroup:admin"]
  },
  "grants": [
    {
      "src": ["group:cyc26-team"],
      "dst": ["tag:cyc26-dev"],
      "ip": ["tcp:22", "tcp:8443"]
    }
  ],
  "ssh": [
    {
      "action": "accept",
      "src": ["group:cyc26-team"],
      "dst": ["tag:cyc26-dev"],
      "users": ["coder"]
    }
  ],
  "tests": [
    {
      "src": "ada@example.com",
      "accept": ["tag:cyc26-dev:22", "tag:cyc26-dev:8443"],
      "deny": ["tag:cyc26-dev:80", "100.81.88.113:7080", "100.81.88.113:22"]
    },
    {
      "src": "tag:cyc26-dev",
      "deny": ["100.81.88.113:7080", "100.81.88.113:22"]
    }
  ],
  "sshTests": [
    {
      "src": "ada@example.com",
      "dst": ["tag:cyc26-dev"],
      "accept": ["coder"],
      "deny": ["root"]
    }
  ]
}
```

The `tests` and `sshTests` blocks make the console refuse to save a policy that
breaks these properties; keep them and add every teammate's email to the first
one. `100.81.88.113` is `lnx`.

Two things to check before you save:

- **Default allow-all rule.** A fresh tailnet ships with `"src": ["*"],
  "dst": ["*"]`. That rule would let the workspace node reach every machine on
  your tailnet (teammates have `sudo` in the container and could use it), and
  Tailscale warns it also applies to invited users. Replace it with a rule
  scoped to your own users and devices, for example `{"src":
  ["autogroup:member"], "dst": ["*"], "ip": ["*"]}`, and add explicit rules for
  any tagged machine that relied on `*`. The `tests` entry for `tag:cyc26-dev`
  above fails the save if the node can still reach `lnx`.
- **Shared users.** `autogroup:shared` matches everyone you shared a node with
  and avoids listing emails in grants. Whether it is accepted as an SSH `src`
  is **untested**, so the SSH rule uses the group.

### 2. Auth key (captain, admin console, Settings, Keys)

Generate an auth key with: Reusable **off**, Ephemeral **off**, Pre-approved
**on** (only shown when device approval is enabled), Tags `tag:cyc26-dev`,
and a short expiry (one day is enough). Keep Ephemeral off: an ephemeral node
is deleted whenever the workspace stops, and the saved node state would then
have nothing to rejoin. The key is used once, on the first start; after that
the node state in `/home/coder` is enough. Copy the key now, it is shown once.
**Untested.**

### 3. Give the workspace the key (captain)

The key is a sensitive template variable. Pass it in a mode-600 variables file,
not on a command line, and delete the file:

```sh
( umask 077
  read -rs -p 'auth key: ' TS_KEY; echo
  printf 'tailscale_auth_key: "%s"\n' "$TS_KEY" > cyc26.vars.yaml )
coder templates push cyc26-team --directory templates/cyc26-team \
  --variables-file cyc26.vars.yaml --yes
rm -f cyc26.vars.yaml; unset TS_KEY
coder update admin/cyc26-team
```

Never commit the file or the key. Check that the node joined (**untested**):

```sh
timeout 300 coder ssh admin/cyc26-team --wait yes -- 'tailscale status | head -3'
```

The Machines page should now list `cyc26-dev` with `tag:cyc26-dev`. If it does
not, read "Join tailnet" in the workspace page's Startup Scripts, and
`/tmp/tailscaled.log` in the workspace. Once the node is up, push the template
again with `tailscale_auth_key: ""` (in the same kind of variables file) so the
spent key is no longer stored with the template version. Whether Coder reuses
the previous value when the variable is omitted is untested, so set it
explicitly. With no key and no saved node state the workspace simply starts
without Tailscale.

Optional: enable HTTPS in the admin console (DNS, Enable HTTPS) so code-server
is served at `https://cyc26-dev.hippo-tilapia.ts.net:8443` with a real
certificate. Without it the start script falls back to a plain TCP forward
(encrypted by WireGuard, but browsers treat `http://` as an insecure context,
which limits some code-server features). Tailscale may also ask an admin to
enable Serve the first time; the URL appears in the "Join tailnet" log.
**Untested.**

### 4. Code-server password (captain)

Code-server on the tailnet node has its own password, generated on first start
in the workspace volume and never in the template:

```sh
coder ssh admin/cyc26-team -- "grep '^password:' ~/.config/cyc26-code-server/config.yaml"
```

Send it to each teammate over a private channel. To rotate it, delete
`~/.config/cyc26-code-server/config.yaml` and stop and start the workspace.
Rotate it when someone leaves.

### 5. Share the node, one person at a time (captain)

1. Add the teammate's Tailscale login email to `group:cyc26-team` in the policy
   (step 1).
2. Admin console, Machines, `cyc26-dev`, `...`, Share. Invite by email or copy
   the invite link and send it. Share only this machine.
3. Tell the teammate to accept with a Tailscale account of their own (a free
   personal account is enough) and to use the email you put in the group.
   Check the accepted identity under Users before relying on it.

Shared machines can receive connections but cannot start any, and the teammate
sees only the machine you shared. **Untested** for a tagged machine; Tailscale
documents that sharing strips the tag in the teammate's tailnet but does not
say it is blocked.

### 6. What the teammate does

```sh
# 1. Install Tailscale, sign in, accept the share (once).
# 2. Shell, using Tailscale SSH (no key, no Coder account):
tailscale ssh coder@cyc26-dev.hippo-tilapia.ts.net
#    plain ssh also works while Tailscale is running:
ssh coder@cyc26-dev.hippo-tilapia.ts.net
# 3. Per session, set your own commit identity and pass selector:
eval "$(cyc26-me 'Ada Lovelace' ada@example.com)"
export CYC26_USER=ada          # only once the captain has issued you a pass
```

The short name `cyc26-dev` does not resolve from a teammate's tailnet; use the
full name above or the node's `100.x` address. The login user is `coder`,
because that is the workspace's Unix user and the SSH rule maps to it.

VS Code Remote-SSH: add to `~/.ssh/config` and connect to `cyc26`:

```text
Host cyc26
    HostName cyc26-dev.hippo-tilapia.ts.net
    User coder
```

Browser: `https://cyc26-dev.hippo-tilapia.ts.net:8443` (`http://` if HTTPS is
not enabled), then the code-server password. **Untested**, including Remote-SSH
over Tailscale SSH.

### Give a teammate their AI pass

The pass model is unchanged (`cyc26-proxy` holds the real credentials, each
person has a revocable pass). Without `coder ssh` there is no
`CODER_SSH_ENV`, so a teammate's pass is a file in the workspace:
`~/.cyc26/passes/<handle>`, selected per session with `export CYC26_USER=<handle>`
(`cyc26-pass` reads `$CYC26_PASS`, then that file, then `~/.cyc26/pass`).

Issue the pass on `lnx` (see "Issue a pass"), then drop it into the workspace
from your own session without the pass touching a command line or shell
history:

```sh
coder ssh admin/cyc26-team          # or: tailscale ssh coder@cyc26-dev.hippo-tilapia.ts.net
umask 077; mkdir -p ~/.cyc26/passes
read -rsp 'pass for ada: ' P; echo; printf %s "$P" > ~/.cyc26/passes/ada; unset P
```

Same rule as before: do not do this until week 3 of the cohort ("no AI for the
first two weeks"). The harnesses do nothing without a pass. Teammates can read
each other's pass files (same Unix user) but not the real credentials.

### Revoke a person

```sh
# on lnx: stop their AI use immediately
docker exec cyc26-proxy node passctl.mjs revoke ada
# in the workspace
coder ssh admin/cyc26-team -- 'rm -f ~/.cyc26/passes/ada'
```

Then remove them from `group:cyc26-team` in the policy (this ends their SSH and
code-server access), and revoke their share: Machines, `cyc26-dev`, Share, the
user's menu, Revoke invite. Rotate the code-server password (step 4) if they
knew it. Whether open SSH sessions drop when the policy changes is
**untested**; to be certain, stop and start the workspace.

### Rotate the node's auth key

The key is only used to join. Rotate when you rebuild the node (workspace or
volume deleted, node removed or expired) or if a key leaked before it was used:

1. Settings, Keys: revoke the old key. If the node should rejoin from scratch,
   Machines, `cyc26-dev`, Remove.
2. Generate a new tagged key (step 2).
3. Push the template with it and `coder update admin/cyc26-team` (step 3). A
   node whose saved state is no longer valid logs in again with the new key.

A leaked key that has been used once is already spent; one that has not been
used can add a node tagged `tag:cyc26-dev`, so revoke it and expire keys
quickly. Anyone in the workspace can read what the agent was given, which is
why the key is non-reusable and short-lived.

## Alternative: teammates with Coder accounts

This path gives each teammate a Coder user and `coder ssh`. The tailnet node above
needs neither. Without a license there are no custom roles or template RBAC, but workspace
sharing works: the organization setting `shareable_workspace_owners` is
`everyone` and nothing in the sharing code checks entitlements. So each
teammate gets their own Coder user and the captain shares the one workspace
with the `use` role (SSH, apps, start, stop; no delete or share). Per-person
users keep who-did-what visible in Coder and let you remove one person
without touching the others. A single shared team login would not.

Teammates share one Unix user, so they can see each other's passes but not the
real credentials (see "Credentials: the inference proxy"). Revoke a pass on
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

Version mismatch warnings are expected (server is a dev build). Put the pass the
captain gave you in `CODER_SSH_ENV` first (see "Issue a pass"). The workspace
page also offers code-server and a web terminal. Set your own commit identity
per session so commits carry your name and not the owner's: see
`templates/cyc26-team/README.md`, "Per-teammate git identity".

### Offboard a teammate

```sh
coder sharing remove admin/cyc26-team --user <handle>
coder users suspend <handle>
```

## Update the template

```sh
coder templates push cyc26-team --directory templates/cyc26-team --yes
coder update admin/cyc26-team
```

Updating rebuilds the container; `/home/coder` is a volume and is kept.
Do not `docker rm` workspace containers or volumes by hand.
