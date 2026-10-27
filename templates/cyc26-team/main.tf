terraform {
  required_providers {
    coder = {
      source = "coder/coder"
    }
    docker = {
      source = "kreuzwerker/docker"
    }
  }
}

variable "coder_access_host" {
  default     = "lnx.hippo-tilapia.ts.net"
  description = "Hostname of CODER_ACCESS_URL as seen by workspaces. It is mapped to the Docker host gateway so workspace agents can reach coderd on this standalone host."
  type        = string
}

variable "pi_version" {
  default     = "1.4.0"
  description = "Pinned npm version of the Pi coding agent CLI (@earendil-works/pi-coding-agent)."
  type        = string
}

variable "opencode_version" {
  default     = "1.19.4"
  description = "Pinned npm version of OpenCode (opencode-ai). Keep it on a 1.x release."
  type        = string
}

variable "inference_proxy_url" {
  default     = "http://host.docker.internal:8787"
  description = "Base URL of the cyc26 inference proxy (homelab/cyc26/inference-proxy) as seen from the workspace. The proxy holds the real Anthropic, ChatGPT and OpenCode credentials; the workspace only ever holds a per-teammate proxy pass."
  type        = string
}

variable "tailscale_auth_key" {
  default     = ""
  description = "Tailscale auth key the workspace uses to join the tailnet as its own tagged node. Supply it at push time (`--variable tailscale_auth_key=...`), never commit it. Use a tagged (tag:cyc26-dev), non-reusable, pre-approved key. It is only used on the first join; the node state then lives in the home volume. Empty means the workspace starts without Tailscale."
  sensitive   = true
  type        = string
}

variable "tailscale_hostname" {
  default     = "cyc26-dev"
  description = "Tailnet machine name of the workspace node. Teammates reach it as <name>.<tailnet>.ts.net."
  type        = string
}

variable "tailscale_version" {
  default     = "1.104.2"
  description = "Pinned version of the static Tailscale build installed into the home volume (pkgs.tailscale.com/stable). Needs a release whose `tailscale up` reads --auth-key=file:."
  type        = string
}

variable "code_server_version" {
  default     = "4.141.1"
  description = "Pinned version of the password-protected code-server served on the tailnet node."
  type        = string
}

variable "code_server_tailnet_port" {
  default     = 8443
  description = "Port the tailnet node serves code-server on. The tailnet policy in homelab/cyc26/RUNBOOK.md allows shared users to reach this port and SSH only."
  type        = number
}

variable "docker_socket" {
  default     = ""
  description = "(Optional) Docker socket URI"
  type        = string
}

provider "docker" {
  # Defaulting to null if the variable is an empty string lets us have an optional variable without having to set our own default
  host = var.docker_socket != "" ? var.docker_socket : null
}

data "coder_provisioner" "me" {}
data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

resource "coder_agent" "main" {
  arch = data.coder_provisioner.me.arch
  os   = "linux"

  startup_script = <<-EOT
    set -e

    # Prepare user home with default files on first start.
    if [ ! -f ~/.init_done ]; then
      cp -rT /etc/skel ~
      touch ~/.init_done
    fi

    # Teammates share this one home directory, so ~/.gitconfig only carries
    # a fallback identity (the workspace owner's, set once). A per-person
    # identity comes from the session environment, which outranks it: see
    # `coder ssh -e` and cyc26-me below.
    git config --global --get user.name >/dev/null || git config --global user.name "$CYC26_OWNER_GIT_NAME"
    git config --global --get user.email >/dev/null || git config --global user.email "$CYC26_OWNER_GIT_EMAIL"
    git config --global init.defaultBranch main
    git config --global pull.rebase false

    # Print export lines that make commits in the current shell carry one
    # teammate's identity: eval "$(cyc26-me 'Ada Lovelace' ada@example.com)"
    sudo tee /usr/local/bin/cyc26-me >/dev/null <<'ME_EOF'
#!/bin/sh
[ $# -eq 2 ] || { echo "usage: eval \"\$(cyc26-me 'Full Name' email@example.com)\"" >&2; exit 2; }
for v in GIT_AUTHOR_NAME GIT_COMMITTER_NAME; do printf "export %s='%s'\n" "$v" "$1"; done
for v in GIT_AUTHOR_EMAIL GIT_COMMITTER_EMAIL; do printf "export %s='%s'\n" "$v" "$2"; done
ME_EOF
    sudo chmod 755 /usr/local/bin/cyc26-me

    # Print the caller's inference proxy pass. Harness configs call this, so a
    # pass is never written into a config file. Order: $CYC26_PASS from the
    # session (`CODER_SSH_ENV=CYC26_PASS=... coder ssh ...`), then
    # ~/.cyc26/passes/$CYC26_USER (Tailscale SSH sessions, one file per
    # teammate), then ~/.cyc26/pass for sessions that cannot set either
    # (code-server, web terminal).
    sudo tee /usr/local/bin/cyc26-pass >/dev/null <<'PASS_EOF'
#!/bin/sh
if [ -n "$CYC26_PASS" ]; then printf %s "$CYC26_PASS"
elif [ -n "$CYC26_USER" ]; then
  case "$CYC26_USER" in
    */* | .*) echo "cyc26-pass: invalid CYC26_USER" >&2; exit 1 ;;
  esac
  if [ -r "$HOME/.cyc26/passes/$CYC26_USER" ]; then cat "$HOME/.cyc26/passes/$CYC26_USER"
  else echo "cyc26-pass: no pass in ~/.cyc26/passes/$CYC26_USER" >&2; exit 1; fi
elif [ -r "$HOME/.cyc26/pass" ]; then cat "$HOME/.cyc26/pass"
else echo "cyc26-pass: set CYC26_PASS or CYC26_USER, or put your pass in ~/.cyc26/pass" >&2; exit 1; fi
PASS_EOF
    sudo chmod 755 /usr/local/bin/cyc26-pass
  EOT

  # The owner's identity reaches the startup script as plain variables, not
  # GIT_AUTHOR_* / GIT_COMMITTER_*: those would take precedence over the
  # per-session identity each teammate sets (README, "Per-teammate git
  # identity").
  #
  # Deliberately absent: every real credential (Anthropic setup token, ChatGPT
  # login, OpenCode key) and every proxy pass. The credentials live only in the
  # inference proxy on the Docker host; a teammate's pass arrives in their own
  # session (cyc26-pass), so none of it enters this template, Terraform state,
  # the image, the agent environment, or git. See homelab/cyc26/RUNBOOK.md.
  env = {
    CYC26_OWNER_GIT_NAME  = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
    CYC26_OWNER_GIT_EMAIL = "${data.coder_workspace_owner.me.email}"
  }

  metadata {
    display_name = "CPU Usage"
    key          = "0_cpu_usage"
    script       = "coder stat cpu"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "RAM Usage"
    key          = "1_ram_usage"
    script       = "coder stat mem"
    interval     = 10
    timeout      = 1
  }

  metadata {
    display_name = "Home Disk"
    key          = "3_home_disk"
    script       = "coder stat disk --path $${HOME}"
    interval     = 60
    timeout      = 1
  }
}

# Points Claude Code, Pi and OpenCode at the inference proxy. Config files are
# merged with jq, so anything a teammate added is kept; the pass itself is never
# written (the harnesses call cyc26-pass). Non-blocking: it waits for jq from the
# toolchain script, and a failure here only leaves the harnesses unconfigured.
resource "coder_script" "inference_proxy" {
  agent_id           = coder_agent.main.id
  display_name       = "Configure inference proxy"
  icon               = "/icon/code.svg"
  run_on_start       = true
  start_blocks_login = false
  timeout            = 400
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    for _ in $(seq 1 60); do command -v jq >/dev/null && break; sleep 5; done
    command -v jq >/dev/null || { echo "jq never appeared; harnesses not pointed at the proxy" >&2; exit 1; }

    PROXY='${var.inference_proxy_url}'

    # merge FILE JQ_FILTER: apply the filter to FILE (created if missing), atomically.
    merge() {
      local file="$1" filter="$2" tmp
      mkdir -p "$(dirname "$file")"
      [ -s "$file" ] || echo '{}' > "$file"
      tmp="$(mktemp "$file.XXXXXX")"
      jq --arg p "$PROXY" "$filter" "$file" > "$tmp" && mv "$tmp" "$file"
    }

    # Claude Code: send API calls to the proxy, with the pass from cyc26-pass.
    merge ~/.claude/settings.json '.apiKeyHelper = "/usr/local/bin/cyc26-pass" | .env = ((.env // {}) + {ANTHROPIC_BASE_URL: ($p + "/anthropic")})'

    # Pi: its built-in openai (ChatGPT) and opencode providers, base URL and key swapped.
    merge ~/.pi/agent/models.json '.providers = ((.providers // {}) + {
      openai:        {baseUrl: ($p + "/openai/v1"),      apiKey: "!/usr/local/bin/cyc26-pass"},
      opencode:      {baseUrl: ($p + "/opencode/v1"),    apiKey: "!/usr/local/bin/cyc26-pass"},
      "opencode-go": {baseUrl: ($p + "/opencode/go/v1"), apiKey: "!/usr/local/bin/cyc26-pass"}
    })'

    # OpenCode: Zen and Go, key from the environment (the opencode wrapper exports it).
    merge ~/.config/opencode/opencode.json '.provider = ((.provider // {}) + {
      opencode:      {options: {baseURL: ($p + "/opencode/v1"),    apiKey: "{env:CYC26_PASS}"}},
      "opencode-go": {options: {baseURL: ($p + "/opencode/go/v1"), apiKey: "{env:CYC26_PASS}"}}
    })'

    echo "Claude Code, Pi and OpenCode point at $PROXY"
  EOT
}

# Toolchain: git, gh, python3, tmux from apt (re-run each start, because the
# container filesystem is rebuilt on every start); Node 22 LTS and pnpm in the
# persistent home volume so restarts only re-link them. Every network step is
# bounded (curl --max-time, `timeout`), and the script has a hard timeout, so a
# stalled connection cannot hold the workspace in "starting".
resource "coder_script" "toolchain" {
  agent_id           = coder_agent.main.id
  display_name       = "Install toolchain"
  icon               = "/icon/code.svg"
  run_on_start       = true
  start_blocks_login = true
  timeout            = 900
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"
    export DEBIAN_FRONTEND=noninteractive
    # Bounded network access for every step below.
    curl() { command curl --connect-timeout 15 --max-time 300 --retry 3 "$@"; }

    # System packages. apt runs only when something is missing; gh comes
    # from GitHub's apt repository, which Ubuntu does not carry.
    pkgs=""
    for pair in git:git curl:curl jq:jq python3:python3 tmux:tmux rg:ripgrep; do
      command -v "$${pair%%:*}" >/dev/null 2>&1 || pkgs="$pkgs $${pair##*:}"
    done
    dpkg -s python3-venv >/dev/null 2>&1 || pkgs="$pkgs python3-venv"
    dpkg -s python3-pip >/dev/null 2>&1 || pkgs="$pkgs python3-pip"
    dpkg -s ca-certificates >/dev/null 2>&1 || pkgs="$pkgs ca-certificates"
    if ! command -v gh >/dev/null 2>&1; then
      $SUDO mkdir -p -m 755 /etc/apt/keyrings
      curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg |
        $SUDO tee /etc/apt/keyrings/githubcli-archive-keyring.gpg >/dev/null
      $SUDO chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
      echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" |
        $SUDO tee /etc/apt/sources.list.d/github-cli.list >/dev/null
      pkgs="$pkgs gh"
    fi
    if [ -n "$pkgs" ]; then
      $SUDO timeout 300 apt-get update -qq
      # shellcheck disable=SC2086
      $SUDO timeout 600 apt-get install -y -qq $pkgs
    fi

    # Node 22 LTS into the home volume, verified against the published
    # SHASUMS. Skipped when a Node 22 is already installed.
    node_home="$HOME/.local/node"
    have_major=""
    [ -x "$node_home/bin/node" ] && have_major="$("$node_home/bin/node" -p 'process.versions.node.split(".")[0]')"
    if [ "$have_major" != "22" ]; then
      case "$(uname -m)" in
        x86_64) node_arch=x64 ;;
        aarch64 | arm64) node_arch=arm64 ;;
        *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
      esac
      base=https://nodejs.org/dist/latest-v22.x
      tmp="$(mktemp -d)"
      trap 'rm -rf "$tmp"' EXIT
      curl -fsSL "$base/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
      tarball="$(awk -v suffix="-linux-$node_arch.tar.gz" 'index($2, suffix) { print $2 }' "$tmp/SHASUMS256.txt")"
      curl -fsSL "$base/$tarball" -o "$tmp/$tarball"
      (cd "$tmp" && grep " $tarball\$" SHASUMS256.txt | sha256sum -c -)
      rm -rf "$node_home"
      mkdir -p "$node_home"
      tar -xzf "$tmp/$tarball" -C "$node_home" --strip-components=1
    fi
    export PATH="$node_home/bin:$HOME/.local/bin:$PATH"

    export npm_config_update_notifier=false npm_config_fetch_timeout=30000 npm_config_fetch_retries=5
    command -v pnpm >/dev/null 2>&1 || timeout 300 npm install -g pnpm

    # Login shells: put the home-volume tools on PATH.
    for rc in ~/.profile ~/.bashrc; do
      grep -qs 'cyc26-team toolchain' "$rc" 2>/dev/null ||
        printf '\n# cyc26-team toolchain\nexport PATH="$HOME/.local/node/bin:$HOME/.local/bin:$PATH"\n' >> "$rc"
    done

    # Non-login sessions (`coder ssh <ws> -- cmd`, the web terminal's exec)
    # do not read those files, so also link the tools into /usr/local/bin.
    for bin in node npm npx pnpm pnpx; do
      [ -e "$node_home/bin/$bin" ] && $SUDO ln -sf "$node_home/bin/$bin" "/usr/local/bin/$bin"
    done

    echo "git $(git --version | cut -d' ' -f3), gh $(gh --version | head -1 | cut -d' ' -f3), python $(python3 --version | cut -d' ' -f2), node $(node --version), pnpm $(pnpm --version), tmux $(tmux -V | cut -d' ' -f2)"
  EOT
}

# Claude Code, installed so it is ready; it is signed in only when the
# workspace owner has stored the `claude-code-oauth-token` user secret.
# Without that secret `claude` reports not logged in, which is the intended
# state during the cohort's no-AI weeks.
resource "coder_script" "claude_code" {
  agent_id           = coder_agent.main.id
  display_name       = "Install Claude Code"
  icon               = "/icon/claude.svg"
  run_on_start       = true
  start_blocks_login = true
  timeout            = 600
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"

    export PATH="$HOME/.local/bin:$PATH"
    if ! command -v claude >/dev/null 2>&1; then
      curl -fsSL --connect-timeout 15 --max-time 120 --retry 3 https://claude.ai/install.sh | timeout 300 bash
    fi
    $SUDO ln -sf "$HOME/.local/bin/claude" /usr/local/bin/claude

    # Skip the first-run onboarding prompt so the env token is used directly.
    if [ ! -f ~/.claude.json ]; then
      printf '{"hasCompletedOnboarding":true}\n' > ~/.claude.json
      chmod 600 ~/.claude.json
    fi

    claude --version
  EOT
}

# Pi and OpenCode, pinned to the template's versions. Install only: no
# credentials or provider config is provisioned here. These are conveniences,
# so they never block login, a failure here only logs a warning, and each
# install is bounded and retried (a stalled registry connection once hung a
# bare `npm install` for over ten minutes).
resource "coder_script" "agent_clis" {
  agent_id           = coder_agent.main.id
  display_name       = "Install Pi and OpenCode"
  icon               = "/icon/code.svg"
  run_on_start       = true
  start_blocks_login = false
  timeout            = 900
  script             = <<-EOT
    #!/usr/bin/env bash
    set -uo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"

    # Needs the Node and pnpm from "Install toolchain", whose last step links
    # pnpm into /usr/local/bin; wait for that, bounded.
    for _ in $(seq 1 150); do
      [ -e /usr/local/bin/pnpm ] && break
      sleep 2
    done
    export PATH="$HOME/.local/node/bin:$PATH"
    export npm_config_update_notifier=false npm_config_fetch_timeout=30000 npm_config_fetch_retries=5

    failed=0
    for spec in "@earendil-works/pi-coding-agent@${var.pi_version}" "opencode-ai@${var.opencode_version}"; do
      if npm ls -g --depth=0 "$spec" >/dev/null 2>&1; then
        continue
      fi
      ok=0
      for attempt in 1 2 3; do
        if timeout --kill-after=10 180 npm install -g --no-audit --no-fund "$spec"; then
          ok=1
          break
        fi
        echo "attempt $attempt to install $spec failed" >&2
      done
      [ "$ok" = 1 ] || { echo "WARNING: could not install $spec" >&2; failed=1; }
    done

    # Non-login sessions (`coder ssh <ws> -- cmd`) need these on the default PATH.
    [ -e "$HOME/.local/node/bin/pi" ] && $SUDO ln -sf "$HOME/.local/node/bin/pi" /usr/local/bin/pi

    # OpenCode reads its proxy key from the environment, so a wrapper exports the
    # caller's pass (see cyc26-pass) instead of a config file holding it.
    if [ -e "$HOME/.local/node/bin/opencode" ]; then
      $SUDO tee /usr/local/bin/opencode >/dev/null <<'OC_EOF'
#!/bin/sh
CYC26_PASS="$(cyc26-pass 2>/dev/null || true)"
export CYC26_PASS
exec "$HOME/.local/node/bin/opencode" "$@"
OC_EOF
      $SUDO chmod 755 /usr/local/bin/opencode
    fi

    for bin in pi opencode; do
      printf '%s ' "$bin"
      "$bin" --version 2>&1 | head -1 || true
    done
    exit "$failed"
  EOT
}

# Tailnet node: runs tailscaled inside the workspace container so teammates
# reach this one container (and nothing else on lnx) through Tailscale node
# sharing, with no Coder account. Userspace networking needs no NET_ADMIN or
# /dev/net/tun. The node joins tagged (tag:cyc26-dev) with Tailscale SSH, which
# lands teammates as the workspace's normal user; the node state lives in the
# home volume, so the auth key is only consumed on the first join. With no key
# and no saved state the script exits cleanly and the workspace runs without
# Tailscale. The key is passed to `tailscale up` through a root-only file, never
# an argument, and is not echoed. See homelab/cyc26/RUNBOOK.md.
resource "coder_script" "tailscale" {
  agent_id           = coder_agent.main.id
  display_name       = "Join tailnet"
  icon               = "/icon/code.svg"
  run_on_start       = true
  start_blocks_login = false
  timeout            = 600
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"
    curl() { command curl --connect-timeout 15 --max-time 300 --retry 3 "$@"; }

    TS_VERSION='${var.tailscale_version}'
    TS_HOSTNAME='${var.tailscale_hostname}'
    TS_AUTHKEY='${var.tailscale_auth_key}'
    TS_PORT='${var.code_server_tailnet_port}'
    # Loopback port of the password-protected code-server ("Serve code-server
    # on the tailnet"); the tailnet port above forwards to it.
    CS_LOCAL_PORT=13338

    state_dir="$HOME/.local/state/tailscale"
    state_file="$state_dir/tailscaled.state"
    sock=/var/run/tailscale/tailscaled.sock
    ts_home="$HOME/.local/tailscale"

    # The state dir is root-only (it holds the node key), so test as root.
    if [ -z "$TS_AUTHKEY" ] && ! $SUDO test -s "$state_file"; then
      echo "No Tailscale auth key and no saved node state; starting without Tailscale."
      exit 0
    fi

    # Static build into the home volume, verified against the published
    # checksum. Skipped when the pinned version is already installed.
    if [ "$("$ts_home/tailscale" version 2>/dev/null | head -1)" != "$TS_VERSION" ]; then
      case "$(uname -m)" in
        x86_64) ts_arch=amd64 ;;
        aarch64 | arm64) ts_arch=arm64 ;;
        *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
      esac
      base=https://pkgs.tailscale.com/stable
      tarball="tailscale_$${TS_VERSION}_$${ts_arch}.tgz"
      tmp="$(mktemp -d)"
      trap 'rm -rf "$tmp"' EXIT
      curl -fsSL "$base/$tarball" -o "$tmp/$tarball"
      echo "$(curl -fsSL "$base/$tarball.sha256")  $tarball" > "$tmp/$tarball.sha256"
      (cd "$tmp" && sha256sum -c "$tarball.sha256")
      rm -rf "$ts_home"
      mkdir -p "$ts_home"
      tar -xzf "$tmp/$tarball" -C "$ts_home" --strip-components=1
    fi
    for bin in tailscale tailscaled; do
      $SUDO ln -sf "$ts_home/$bin" "/usr/local/bin/$bin"
    done

    ts() { $SUDO tailscale --socket="$sock" "$@"; }
    backend_state() {
      ts status --json 2>/dev/null | grep -o '"BackendState": *"[A-Za-z]*"' | cut -d'"' -f4 || true
    }

    # Start the daemon unless this script is re-running against a live one.
    if [ -z "$(backend_state)" ]; then
      mkdir -p "$HOME/.local/state"
      $SUDO mkdir -p -m 700 "$state_dir"
      $SUDO mkdir -p /var/run/tailscale
      $SUDO setsid nohup tailscaled --tun=userspace-networking \
        --state="$state_file" --socket="$sock" </dev/null >/tmp/tailscaled.log 2>&1 &
    fi

    state=""
    for _ in $(seq 1 30); do
      state="$(backend_state)"
      case "$state" in Running | NeedsLogin | Stopped | NeedsMachineAuth) break ;; esac
      sleep 1
    done

    up_node() {
      ts up --reset --ssh --hostname="$TS_HOSTNAME" --advertise-tags=tag:cyc26-dev --timeout=60s "$@"
    }
    case "$state" in
      NeedsLogin)
        if [ -z "$TS_AUTHKEY" ]; then
          echo "Tailscale node needs a new login but no auth key was supplied; push the template with tailscale_auth_key." >&2
          exit 1
        fi
        keyfile="$state_dir/authkey"
        trap '$SUDO rm -f "$keyfile"; rm -rf "$${tmp:-}"' EXIT
        (umask 077; printf %s "$TS_AUTHKEY" | $SUDO tee "$keyfile" >/dev/null)
        up_node --auth-key="file:$keyfile"
        $SUDO rm -f "$keyfile"
        ;;
      Running | Stopped | NeedsMachineAuth)
        up_node
        ;;
      *)
        echo "tailscaled did not become ready; log follows" >&2
        tail -n 20 /tmp/tailscaled.log >&2 || true
        exit 1
        ;;
    esac

    # Forward the tailnet port to the local code-server. HTTPS (a certificate
    # for the node's MagicDNS name) needs HTTPS enabled for the tailnet; when it
    # is not, fall back to a plain TCP forward, which is still encrypted by
    # WireGuard but is not a secure context for the browser. Both are bounded
    # because `serve` can wait on an admin to enable the feature.
    if $SUDO timeout 30 tailscale --socket="$sock" serve --bg --https="$TS_PORT" "http://127.0.0.1:$CS_LOCAL_PORT"; then
      echo "code-server is served over HTTPS on tailnet port $TS_PORT"
    else
      ts serve reset || true
      $SUDO timeout 30 tailscale --socket="$sock" serve --bg --tcp="$TS_PORT" "tcp://127.0.0.1:$CS_LOCAL_PORT"
      echo "HTTPS is not enabled for the tailnet; code-server is served as plain TCP on tailnet port $TS_PORT"
    fi

    echo "Tailscale node $TS_HOSTNAME is up: $(ts ip -4 | head -1)"
  EOT
}

# Password-protected code-server for teammates on the tailnet. It is separate
# from the Coder-proxied code-server module below (no auth, reachable only
# through Coder): this one listens on loopback and is exposed by `tailscale
# serve`, so it carries its own password. The password is generated on first
# start into a mode-600 config in the home volume and never leaves the
# workspace; the captain reads it there and hands it out. Skipped, like the
# tailnet node, when there is no auth key and no saved node state.
resource "coder_script" "code_server_tailnet" {
  agent_id           = coder_agent.main.id
  display_name       = "Serve code-server on the tailnet"
  icon               = "/icon/code.svg"
  run_on_start       = true
  start_blocks_login = false
  timeout            = 600
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"

    CS_VERSION='${var.code_server_version}'
    TS_AUTHKEY='${var.tailscale_auth_key}'
    # Must match CS_LOCAL_PORT in "Join tailnet".
    CS_LOCAL_PORT=13338
    state_file="$HOME/.local/state/tailscale/tailscaled.state"

    if [ -z "$TS_AUTHKEY" ] && ! $SUDO test -s "$state_file"; then
      echo "No Tailscale node for this workspace; not starting the tailnet code-server."
      exit 0
    fi

    export PATH="$HOME/.local/bin:$PATH"
    if [ "$(code-server --version 2>/dev/null | cut -d' ' -f1)" != "$CS_VERSION" ]; then
      tmp="$(mktemp -d)"
      trap 'rm -rf "$tmp"' EXIT
      # The install script of the pinned release, not the moving copy on code-server.dev.
      curl -fsSL --connect-timeout 15 --max-time 120 --retry 3 \
        "https://raw.githubusercontent.com/coder/code-server/v$CS_VERSION/install.sh" -o "$tmp/install.sh"
      timeout 600 sh "$tmp/install.sh" --method standalone --prefix "$HOME/.local" --version "$CS_VERSION"
    fi

    cfg_dir="$HOME/.config/cyc26-code-server"
    cfg="$cfg_dir/config.yaml"
    if [ ! -s "$cfg" ]; then
      ( umask 077
        mkdir -p "$cfg_dir"
        password="$(head -c 24 /dev/urandom | base64 | tr -d '/+=\n')"
        printf 'bind-addr: 127.0.0.1:%s\nauth: password\npassword: %s\ncert: false\n' \
          "$CS_LOCAL_PORT" "$password" > "$cfg" )
    fi

    if ! (exec 3<>"/dev/tcp/127.0.0.1/$CS_LOCAL_PORT") 2>/dev/null; then
      nohup setsid code-server --config "$cfg" \
        --user-data-dir "$HOME/.local/share/cyc26-code-server" \
        --disable-telemetry --disable-update-check /home/coder \
        </dev/null >/tmp/cyc26-code-server.log 2>&1 &
    fi
    echo "code-server $CS_VERSION listens on 127.0.0.1:$CS_LOCAL_PORT; password is in $cfg"
  EOT
}

# See https://registry.coder.com/modules/coder/code-server
module "code-server" {
  count  = data.coder_workspace.me.start_count
  source = "registry.coder.com/coder/code-server/coder"

  # This ensures that the latest non-breaking version of the module gets downloaded, you can also pin the module version to prevent breaking changes in production.
  version = "~> 1.0"

  agent_id = coder_agent.main.id
  folder   = "/home/coder"
  order    = 1
}

resource "docker_volume" "home_volume" {
  name = "coder-${data.coder_workspace.me.id}-home"
  # Protect the volume from being deleted due to changes in attributes.
  lifecycle {
    ignore_changes = all
  }
  # Add labels in Docker to keep track of orphan resources.
  labels {
    label = "coder.owner"
    value = data.coder_workspace_owner.me.name
  }
  labels {
    label = "coder.owner_id"
    value = data.coder_workspace_owner.me.id
  }
  labels {
    label = "coder.workspace_id"
    value = data.coder_workspace.me.id
  }
  # This field becomes outdated if the workspace is renamed but can
  # be useful for debugging or cleaning out dangling volumes.
  labels {
    label = "coder.workspace_name_at_creation"
    value = data.coder_workspace.me.name
  }
}

resource "docker_container" "workspace" {
  count = data.coder_workspace.me.start_count
  image = "codercom/example-base:ubuntu"
  # Uses lower() to avoid Docker restriction on container names.
  name = "coder-${data.coder_workspace_owner.me.name}-${lower(data.coder_workspace.me.name)}"
  # Hostname makes the shell more user friendly: coder@my-workspace:~$
  hostname = data.coder_workspace.me.name
  # Use the docker gateway if the access URL is 127.0.0.1
  entrypoint = ["sh", "-c", replace(coder_agent.main.init_script, "/localhost|127\\.0\\.0\\.1/", "host.docker.internal")]
  env        = ["CODER_AGENT_TOKEN=${coder_agent.main.token}"]
  host {
    host = "host.docker.internal"
    ip   = "host-gateway"
  }
  # Let the workspace agent reach coderd via the deployment access URL.
  # Plain bridge containers cannot resolve tailnet names, so map the
  # access-URL host to the Docker host gateway (coder publishes 7080
  # on 0.0.0.0, so host-gateway:7080 reaches coderd).
  host {
    host = var.coder_access_host
    ip   = "host-gateway"
  }
  volumes {
    container_path = "/home/coder"
    volume_name    = docker_volume.home_volume.name
    read_only      = false
  }

  # Add labels in Docker to keep track of orphan resources.
  labels {
    label = "coder.owner"
    value = data.coder_workspace_owner.me.name
  }
  labels {
    label = "coder.owner_id"
    value = data.coder_workspace_owner.me.id
  }
  labels {
    label = "coder.workspace_id"
    value = data.coder_workspace.me.id
  }
  labels {
    label = "coder.workspace_name"
    value = data.coder_workspace.me.name
  }
}
