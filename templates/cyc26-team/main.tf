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
  EOT

  # The owner's identity reaches the startup script as plain variables, not
  # GIT_AUTHOR_* / GIT_COMMITTER_*: those would take precedence over the
  # per-session identity each teammate sets (README, "Per-teammate git
  # identity").
  #
  # Deliberately absent: CLAUDE_CODE_OAUTH_TOKEN. It reaches the agent from
  # the workspace owner's `claude-code-oauth-token` Coder user secret
  # (homelab/claude-code-auth/RUNBOOK.md), so it never enters this template,
  # Terraform state, the image, or git.
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

# Toolchain: git, gh, python3 from apt (re-run each start, because the
# container filesystem is rebuilt on every start); Node 22 LTS and pnpm in
# the persistent home volume so restarts only re-link them.
resource "coder_script" "toolchain" {
  agent_id           = coder_agent.main.id
  display_name       = "Install toolchain"
  icon               = "/icon/code.svg"
  run_on_start       = true
  start_blocks_login = true
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"
    export DEBIAN_FRONTEND=noninteractive

    # System packages. apt runs only when something is missing; gh comes
    # from GitHub's apt repository, which Ubuntu does not carry.
    pkgs=""
    for pair in git:git curl:curl jq:jq python3:python3 rg:ripgrep; do
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
      $SUDO apt-get update -qq
      # shellcheck disable=SC2086
      $SUDO apt-get install -y -qq $pkgs
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

    export npm_config_update_notifier=false
    command -v pnpm >/dev/null 2>&1 || npm install -g pnpm

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

    echo "git $(git --version | cut -d' ' -f3), gh $(gh --version | head -1 | cut -d' ' -f3), python $(python3 --version | cut -d' ' -f2), node $(node --version), pnpm $(pnpm --version)"
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
  script             = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail

    SUDO=""
    [ "$(id -u)" = 0 ] || SUDO="sudo"

    export PATH="$HOME/.local/bin:$PATH"
    if ! command -v claude >/dev/null 2>&1; then
      curl -fsSL https://claude.ai/install.sh | bash
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
