# Runbook: ChatGPT Pro subscription via Coder AI Gateway (homelab paved path)

Scope: homelab Coder (verified against `lnx`, org `coder`). ChatGPT first;
Copilot is gated on its open token-source item (see §7). Fork-local only:
never PR these scripts or this doc upstream.

## 0. How the path works (30 seconds)

Coder persists only the OAuth **access token** as your BYOK user key for
the `chatgpt` provider; there is no refresh-grant column and no server
refresh. The minters (Pi, Codex CLI) already silent-refresh, so the paved
path is **expiry-notify + one-command re-save**, not a server daemon:

1. `provision-aiprovider-chatgpt.sh` (admin, once per deployment).
2. `byok-resave-chatgpt.sh` (per user, daily via scheduler of choice).
3. Verify via presence booleans; rotate/revoke with re-save / DELETE.

Always act **as yourself**: the re-save script mints from YOUR Pi grant
and writes to YOUR Coder user. Never run it with another user's Pi store
or Coder session, and never print a token (scripts log byte-length only).

## 1. Provision (admin, once)

```sh
./provision-aiprovider-chatgpt.sh [--host URL]   # default http://lnx:7080
```

Idempotent: creates `openai/chatgpt` at
`https://chatgpt.com/backend-api/codex` (enabled, no central keys) when
absent; PATCHes `base_url`/`enabled` drift when present; otherwise a
verified no-op. Refuses to repurpose a same-named provider of another
type. Live on lnx: id `655977db-ac56-454b-9016-d9015fbb4c3b`, enabled,
BYOK on, no central key.

Auth: `$CODER_SESSION_TOKEN`, else macOS keychain
(`coder-lnx-7080-session` / `coder-owner`).

## 2. Save (per user, first time and daily)

```sh
./byok-resave-chatgpt.sh --check     # dry run: resolves provider, checks minter, changes nothing
./byok-resave-chatgpt.sh             # export from Pi -> PUT -> verified GET
```

What it does: `pi auth print-bearer-token --provider openai-codex
--min-expiry 24h` (Pi refreshes server-side when nearer expiry, so the
saved token is fresh) -> asserts <= 10 KB and no surrounding whitespace
(server 400s otherwise) -> `PUT /api/v2/users/me/ai-provider-keys/<uuid>`
`{"api_key": ...}` -> verifies `has_user_api_key=true` via GET.

Failure meanings (from `coderd/exp_chats.go`): 403 = BYOK disabled on the
deployment; 404 = provider missing (run §1); 400 = key too large/blank;
provider-disabled = precondition error (re-run §1).

## 3. Schedule (per user)

Pick one file from `scheduling/`, replace `HOMELAB_BIN` with your
checkout path, install **as yourself**:

- macOS: `com.coder.byok-resave-chatgpt.plist` -> `~/Library/LaunchAgents/`,
  `launchctl bootstrap gui/$UID ...` (daily 06:15).
- systemd host: `byok-resave-chatgpt.systemd.example` -> split into
  `~/.config/systemd/user/*.service|*.timer`, `enable --now ...timer`.
- cron fallback: `cron.example` line via `crontab -e` (never as root).

Start daily; §5 explains why daily is the safe default until TTL is
measured with a consented grant. Do NOT enable the schedule with another
user's credentials present: the job saves whatever Pi grant the local
user holds.

## 4. Verify end to end (no valid token needed for plumbing)

```sh
./provision-aiprovider-chatgpt.sh            # expect: already provisioned
./byok-resave-chatgpt.sh --check             # expect: resolves + minter ready, no state change
curl -s -H "Coder-Session-Token: $CS" $HOST/api/v2/users/me/ai-provider-keys \
  | jq '.[] | {name: .provider.name, has_user_api_key}'
```

Full-traffic proof additionally needs a user-consented ChatGPT grant
(see §5): save, then exercise the provider through **Coder Agents**
(the license-exempt in-process path on unlicensed homelab hosts).

## 5. Measurements

### 5a. `chatgpt-account-id`: NOT required (recorded 2026-09-23)

The gateway has no account-id concept: `OpenAI.resolveCredential`
(`aibridge/provider/openai.go`) resolves Bearer-BYOK-or-central-pool and
nothing else, and `PrepareClientHeaders`
(`aibridge/intercept/client_headers.go`) strips exactly `Authorization`,
`X-Api-Key` (+ hop-by-hop/proxy/firewall headers) while every other
client header passes through. So a BYOK Bearer token suffices; a
`chatgpt-account-id` header is optional passthrough, and per-user account
ids cannot come from admin `UpstreamHeaders` (static values + chat-ID
placeholder only). Live-config corroboration on lnx: `chatgpt` provider
enabled, no central key, BYOK enabled. Header-present-vs-absent live
traffic comparison remains gated on a user-consented grant; ground truth
(BYOK-saved tokens carry traffic on lnx) already shows Bearer-only saves
work.

### 5b. Access-token TTL: UNMEASURED, gated (daily cadence is the safe default)

Not measured: decoding `exp` needs token material, and the only grants on
this home are the operator's (off-limits; throwaway-grant OAuth needs an
interactive browser). Pi metadata exposes no expiry fields
(`pi auth check --json` yields only authType/provider/status). Structural
bounds: Codex refresh grants live >= 16 days (scout); Pi silent-refreshes
via `--min-expiry`. Daily re-save therefore outruns any plausible
access-token TTL while the refresh grant is healthy.

To measure with a consented (throwaway or own) grant, print ONLY the TTL:

```sh
t=$(pi auth print-bearer-token --provider openai-codex); exp=$(printf '%s' "$t" | cut -d. -f2 | base64 -d 2>/dev/null | jq -r .exp); unset t; echo "ttl_hours=$(( (exp - $(date +%s)) / 3600 ))"
```

Tighten the schedule only on that evidence.

### 5c. License gate (recorded 2026-09-23, affects client choice)

lnx has no Coder license (`aibridge: not_entitled`), so the external
`/api/v2/ai-gateway/*` routes 403 (`RequireFeatureMW(FeatureAIBridge)`);
the chatd/Agents in-process transport is license-exempt
(`coderd/coderd.go`: `aiGatewayHandler`). Consequence: on unlicensed
homelab hosts, consume ChatGPT-through-gateway via **Coder Agents**, not
Codex CLI pointed at the external route. Direct Codex CLI
(`requires_openai_auth=true`, `base_url=.../api/v2/ai-gateway/chatgpt/v1`)
needs the premium entitlement.

## 6. Rotate / revoke

- Rotate: just re-run `./byok-resave-chatgpt.sh` (PUT is create-or-replace).
- Revoke: `curl -X DELETE -H "Coder-Session-Token: $CS" \
  $HOST/api/v2/users/me/ai-provider-keys/<provider-uuid>` (204), then
  confirm `has_user_api_key=false` via the §4 GET. Coder never returns
  key material, only presence booleans.
- Compromised Pi grant: rotate at the source (Pi/Codex re-login), then
  re-save; treat any pasted token as exposed to the agent plane.

## 7. Copilot (gated, not built)

Same script shape applies with the minter/export swapped, but the token
source is unconfirmed: `gh auth login` yields a github.com token, which
is not necessarily a Copilot-API token. Confirm whether the Copilot CLI
device flow or Pi's `github-copilot` oauth entry is the documented
source before writing `byok-resave-copilot.sh` and its runbook section.
