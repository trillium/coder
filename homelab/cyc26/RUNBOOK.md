# Commit Your Code 2026 team workspace

Operator runbook for the shared Coder workspace used by the cyc26 cohort team
(project-mcik, six-week project, about five teammates). Coder is
`http://lnx.hippo-tilapia.ts.net:7080`, tailnet-only, community edition (no
license). Template source: `templates/cyc26-team/`. Credential proxy:
`homelab/cyc26/inference-proxy/`.

## Current state

- Template `cyc26-team` is pushed to the server.
- Workspace `admin/cyc26-team` exists and is running. It has git, gh, python3,
  tmux, Node 22, pnpm, Claude Code, Pi, OpenCode 1.x, and code-server. It has
  no auto-stop schedule.
- The captain's credentials (Claude setup token, ChatGPT login, OpenCode key)
  are held by the inference proxy on lnx, not by the workspace. The workspace
  has only the proxy URL and a per-teammate pass. Design, routes and limits:
  `homelab/cyc26/inference-proxy/README.md`. No teammate accounts exist yet.

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
read that file, so prefer the session variable.

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

## Teammate access

Without a license there are no custom roles or template RBAC, but workspace
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
