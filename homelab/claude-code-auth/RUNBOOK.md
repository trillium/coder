# Claude Code subscription auth in lnx workspaces

Workspaces from `templates/docker-lnx-server` install `claude` at start and
read `CLAUDE_CODE_OAUTH_TOKEN` from a Coder user secret. The token never enters
Terraform state, the image, or git.

## Why this shape

- `claude setup-token` mints a one-year, inference-only OAuth token. It is not
  saved anywhere; Claude Code reads it only from `CLAUDE_CODE_OAUTH_TOKEN`
  ([authentication docs](https://code.claude.com/docs/en/authentication#generate-a-long-lived-token)).
- Do not copy `~/.claude.json`, `~/.claude/.credentials.json`, or Keychain
  entries. Those hold rotating refresh tokens that must have one consumer.
- A Coder user secret with `--env` injects the value into every workspace
  session you own, with no template parameter or Terraform state involved.

## One-time setup (captain)

1. Run `claude setup-token` on a machine with a browser. Approve in the browser and copy the token.
2. Run `coder login http://lnx.hippo-tilapia.ts.net:7080` if the CLI is signed out.
3. Run `homelab/claude-code-auth/coder-claude-token-push.sh` and paste the token at the hidden prompt.
   (macOS clipboard: `homelab/claude-code-auth/coder-claude-token-push.sh --from-clipboard`.)
4. Push the updated template so new workspaces install `claude`:
   `coder templates push docker-lnx-server --directory templates/docker-lnx-server --yes`.
5. Restart the workspace: `coder restart <owner/workspace> --yes`. Secrets load when the agent starts.

Preview without touching anything: add `--dry-run`. Steps 3 and 5 in one go:
`homelab/claude-code-auth/coder-claude-token-push.sh --verify <owner/workspace>`.

## Verify

1. Run `coder ssh <owner/workspace> -- claude /status`. It should show the OAuth token as the login method and your subscription.
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
- Revoke: `coder secret delete claude-code-oauth-token`, then revoke the token in claude.ai settings.
