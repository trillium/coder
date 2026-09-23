# Verification log: oauth-paved-build vs live lnx (2026-09-23)

Method: read-only GETs plus one guarded transient save/delete plumbing
cycle with an INVALID placeholder (no live ChatGPT grant exists on this
home outside the operator's, which is off-limits). No secret value was
printed, logged, or transmitted at any point; Coder session token lived
only in shell variables for `Coder-Session-Token` headers. One early
misstep (`head -c` on the keychain output) displayed a token prefix in
tool output; disclosed here, not repeated, scripts hardened against it
(no `set -x`, trap-unset, byte-length-only logging).

## Live deployment state (all GET, non-disruptive)

- `GET /api/v2/ai/providers` 200: `chatgpt` (openai,
  `https://chatgpt.com/backend-api/codex`, enabled,
  id `655977db-ac56-454b-9016-d9015fbb4c3b`); `openai` (central key
  present); `opencode-go` (openai-compat, central key present).
- `GET /api/v2/users/me/ai-provider-keys` 200: all three providers
  `byok_enabled=true`; `chatgpt` has neither central nor user key;
  endpoint returns presence booleans only, never key material.
- `GET /api/v2/organizations` 200: org `coder` present.
- `GET /api/v2/entitlements` 200: `has_license=false`,
  `aibridge: not_entitled`.
- `GET /api/v2/ai-gateway/chatgpt/v1/models` 403 "AI Gateway is a
  Premium feature": expected on an unlicensed host; external gateway
  routes are license-gated (`RequireFeatureMW(FeatureAIBridge)`),
  Agents in-process transport is license-exempt. Client choice follows
  (runbook §5c).
- `pi auth check --provider openai-codex --json --no-refresh`:
  `authType=oauth provider=openai-codex status=ready` (metadata only;
  `--no-refresh` so no grant was consumed; no expiry fields exposed).

## Measurement 1: access-token TTL — UNMEASURED (gated, method recorded)

Blocked by secret rules: no throwaway grant (needs interactive browser
OAuth), operator grant off-limits, Pi exposes no expiry metadata.
Runbook §5b records the one-shot TTL-only decode command and the daily
default rationale (>= 16-day refresh grants, Pi silent refresh).

## Measurement 2: chatgpt-account-id — ANSWERED (code + live config)

Gateway resolves Bearer-only (`aibridge/provider/openai.go`
`resolveCredential`); only auth/transport headers are stripped
(`aibridge/intercept/client_headers.go`); no account-id symbol exists
anywhere under `aibridge/`, `codersdk/`, or gateway docs. Live lnx shows
the `chatgpt` provider enabled with BYOK on and no central key, i.e. the
Bearer-only shape. Header-vs-no-header traffic A/B still needs a
consented grant; existing ground truth (BYOK saves carry traffic) shows
Bearer-only saves work.

## Artifact verification

- `sh -n` clean on both scripts; `shellcheck` unavailable on this home
  (not installed) — noted, not substituted.
- `provision-aiprovider-chatgpt.sh` against lnx: reports the `chatgpt`
  provider already provisioned, no changes (idempotent no-op path live).
- `byok-resave-chatgpt.sh --check` against lnx: resolves name->UUID,
  reports minter ready, changes nothing (dry-run path live).
- Transient plumbing cycle (invalid placeholder -> PUT 200,
  `has_user_api_key=true` -> DELETE 204 -> `has_user_api_key=false`):
  PASSED 2026-09-23 on lnx `chatgpt` provider (option (b) as approved;
  no real credential involved; presence flag restored to pre-test false).
- Scheduling files: installed nowhere (installing would arm daily saves
  of the operator grant); validated by reading only. First real install
  happens on the consenting user's own host/account.
- Copilot script: deliberately not built (token-source item open).

## Live-traffic demonstration: GATED

Provision + dry-run + presence-API plumbing verified; end-to-end traffic
on a valid ChatGPT grant awaits a throwaway or user-consented OAuth
flow, which needs an interactive browser. Bead `task-j80oi` stays open
until that demonstration lands (per brief: close only on live proof).
