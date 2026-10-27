# Claude Code subscription auth in lnx workspaces

Workspaces from `templates/cyc26-team` (and `templates/docker-lnx-server`
once it installs `claude`) read `CLAUDE_CODE_OAUTH_TOKEN` from a Coder user
secret. The token never enters Terraform state, the image, or git.

The operator procedure for the cohort workspace is in
[`homelab/cyc26/RUNBOOK.md`](../cyc26/RUNBOOK.md); this file is the
reference for the mechanism.

## Why this shape

- `claude setup-token` mints a one-year, inference-only OAuth token. It is not
  saved anywhere; Claude Code reads it only from `CLAUDE_CODE_OAUTH_TOKEN`
  ([authentication docs](https://code.claude.com/docs/en/authentication#generate-a-long-lived-token)).
- Do not copy `~/.claude.json`, `~/.claude/.credentials.json`, or Keychain
  entries. Those hold rotating refresh tokens that must have one consumer.
- A Coder user secret with `--env` injects the value into every session of
  every workspace the secret's user owns, with no template parameter or
  Terraform state involved.
- The secret belongs to the Coder user that stored it, and a workspace gets
  only its owner's secrets. Anyone connected to that workspace (including
  users it is shared with) sees the owner's token, because the agent injects
  it into every session. Store it as the workspace owner.

## One-time setup (captain)

1. Run `claude setup-token` on a machine with a browser. Approve in the browser and copy the token.
2. Make sure the Coder CLI is signed in as the workspace owner:
   `coder login http://lnx.hippo-tilapia.ts.net:7080`, then `coder whoami`.
3. Run `homelab/claude-code-auth/coder-claude-token-push.sh` and paste the token at the hidden prompt.
   (macOS clipboard: `homelab/claude-code-auth/coder-claude-token-push.sh --from-clipboard`.)
   The script prints which Coder user it stores the secret for.
4. Restart the workspace: `coder restart <owner/workspace> --yes`. Secrets load when the agent starts.

Preview without touching anything: add `--dry-run`. Steps 3 and 4 in one go:
`homelab/claude-code-auth/coder-claude-token-push.sh --verify <owner/workspace>`
(it refuses a workspace owned by someone other than the signed-in user).

## Verify

1. Run `coder ssh <owner/workspace> -- claude auth status`. It should report `loggedIn: true` with the OAuth token as the method.
2. Run `coder ssh <owner/workspace> -- claude -p hi`. It should print a reply.

If `claude` is not found, the start script has not run yet; check the "Install
Claude Code" script log in the workspace page.

## Rotate (before the one-year expiry)

1. Run `claude setup-token` again and copy the new token.
2. Run `homelab/claude-code-auth/coder-claude-token-push.sh`. It updates the existing secret.
3. Restart each workspace. Calendar the rotation about 11 months after the token was minted.

## Notes

- `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` in the environment outranks the OAuth token. Keep them unset.
- The token cannot start Remote Control sessions or use claude.ai connectors. Local MCP servers work.
- The script checks for the `sk-ant-oat` prefix before storing. If Anthropic changes the format, edit that check.
- Revoke: `coder secret delete claude-code-oauth-token`, restart the workspace (the running agent keeps the old value until it restarts), then revoke the token in claude.ai settings.
