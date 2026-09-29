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

locals {
  username = data.coder_workspace_owner.me.name
}

variable "docker_socket" {
  default     = ""
  description = "(Optional) Docker socket URI"
  type        = string
}

variable "coder_access_host" {
  default     = "lnx.hippo-tilapia.ts.net"
  description = "Hostname of CODER_ACCESS_URL as seen by workspaces. It is mapped to the Docker host gateway so workspace agents can reach coderd on this standalone host."
  type        = string
}

variable "ollama_base_url" {
  default     = "http://macbook.hippo-tilapia.ts.net:11434"
  description = "MacBook Ollama base URL (no trailing slash) as seen from the workspace container. Plain HTTP, so no TLS/SNI concern, but the hostname must resolve inside the container (see ollama_hostname / ollama_ip_override)."
  type        = string
}

variable "ollama_hostname" {
  default     = "macbook.hippo-tilapia.ts.net"
  description = "Tailnet hostname embedded in ollama_base_url. Pinned to ollama_ip_override inside the container because bridge containers cannot resolve tailnet DNS names."
  type        = string
}

variable "ollama_ip_override" {
  default     = "100.74.138.74"
  description = "Tailnet IPv4 of the Ollama host, pinned for ollama_hostname in the container (Docker host block plus /etc/hosts entry at startup). Empty string disables the pin and falls back to container DNS. Refresh with `tailscale ip -4` on the MacBook when the smoke test reports the hostname resolving elsewhere or not at all."
  type        = string
}

variable "anthropic_api_key" {
  default     = ""
  description = "(Optional) Real Anthropic API key. Only set this to reach api.anthropic.com with genuine Claude credentials; leave empty for local-Ollama mode. Prefer a Coder secret over a plaintext parameter value."
  type        = string
  sensitive   = true
}

provider "docker" {
  # Defaulting to null if the variable is an empty string lets us have an optional variable without having to set our own default
  host = var.docker_socket != "" ? var.docker_socket : null
}

data "coder_provisioner" "me" {}
data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

resource "coder_agent" "main" {
  arch           = data.coder_provisioner.me.arch
  os             = "linux"
  startup_script = <<-EOT
    set -e

    # Prepare user home with default files on first start.
    if [ ! -f ~/.init_done ]; then
      cp -rT /etc/skel ~
      touch ~/.init_done
    fi

    # Persistent scratch area for agent runs (backed by workspace_volume).
    mkdir -p /workspace

    # Pin the Ollama tailnet hostname: bridge containers resolve it to a
    # public wildcard address (or not at all), so the Docker host block in
    # this template plus this entry are what make OLLAMA_HOST reachable.
    # The Docker host block covers name resolution for most lookups; the
    # /etc/hosts entry is belt and braces for resolvers that bypass it.
    # NOTE: sed -i cannot touch /etc/hosts inside a container (rename onto
    # a bind mount fails), so filter through a temp file and rewrite the
    # mounted file in place with cat instead.
    if [ -n "${var.ollama_ip_override}" ]; then
      grep -v -F "${var.ollama_hostname}" /etc/hosts > /etc/hosts.gnhf-tmp || true
      cat /etc/hosts.gnhf-tmp > /etc/hosts
      rm -f /etc/hosts.gnhf-tmp
      echo "${var.ollama_ip_override} ${var.ollama_hostname}" >> /etc/hosts
    fi

    # Local-model environment for every login shell (agent env below covers
    # the Coder agent process itself; SSH sessions source profile.d).
    # Inner heredoc terminators sit at column 0 on purpose: POSIX sh only
    # recognises an unindented terminator for << (non-dash) heredocs.
    # Inner heredocs use quoted delimiters so the runtime shell writes
    # them literally; Terraform still expands variable references inside
    # at provision time.
    cat > /etc/profile.d/ollama-local.sh <<'PROFILE_EOF'
export OLLAMA_HOST="${var.ollama_base_url}"
export OPENAI_BASE_URL="${var.ollama_base_url}/v1"
export OPENAI_API_KEY="ollama"
export ANTHROPIC_BASE_URL="${var.ollama_base_url}"
export ANTHROPIC_AUTH_TOKEN="ollama"
PROFILE_EOF
    chmod 644 /etc/profile.d/ollama-local.sh

    # Base dev/runtime tooling. Guarded so workspace restarts stay fast:
    # apt runs only when something is actually missing.
    export DEBIAN_FRONTEND=noninteractive
    apt_pkgs=""
    command -v git >/dev/null 2>&1 || apt_pkgs="$apt_pkgs git"
    command -v curl >/dev/null 2>&1 || apt_pkgs="$apt_pkgs curl ca-certificates"
    command -v jq >/dev/null 2>&1 || apt_pkgs="$apt_pkgs jq"
    command -v rg >/dev/null 2>&1 || apt_pkgs="$apt_pkgs ripgrep"
    command -v fd >/dev/null 2>&1 || apt_pkgs="$apt_pkgs fd-find"
    command -v ssh >/dev/null 2>&1 || apt_pkgs="$apt_pkgs openssh-client"
    if [ -n "$apt_pkgs" ]; then
      apt-get update
      # shellcheck disable=SC2086
      apt-get install -y $apt_pkgs
    fi

    # Node 22 LTS for the agent CLIs (all four install from npm or a
    # Node-based installer). Refresh only when node is missing or older
    # than 18, which every CLI here requires.
    node_major=""
    if command -v node >/dev/null 2>&1; then
      node_major=$(node -p 'process.versions.node.split(".")[0]')
    fi
    if [ -z "$node_major" ] || [ "$node_major" -lt 18 ]; then
      curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
      apt-get install -y nodejs
    fi

    # Agent CLIs, from each tool's documented install path. Guarded by
    # command presence so they survive restarts without re-downloading.
    export npm_config_update_notifier=false
    command -v gnhf >/dev/null 2>&1 || npm install -g gnhf
    command -v pi >/dev/null 2>&1 || npm install -g @earendil-works/pi-coding-agent
    command -v claude >/dev/null 2>&1 || npm install -g @anthropic-ai/claude-code
    if ! command -v opencode >/dev/null 2>&1; then
      curl -fsSL https://opencode.ai/install | bash
      if [ -f "$HOME/.opencode/bin/opencode" ]; then
        ln -sf "$HOME/.opencode/bin/opencode" /usr/local/bin/opencode
      fi
    fi

    # pi: expose the MacBook models as ollama-local/* so `pi --list-models`
    # and `gnhf --agent pi --model ollama-local/...` work out of the box.
    # Written once; gnhf-smoke-test fails if the rendered baseUrl ever
    # drifts from OLLAMA_HOST (e.g. after the template URL param changes).
    pi_models="$HOME/.pi/agent/models.json"
    if [ ! -f "$pi_models" ]; then
      mkdir -p "$(dirname "$pi_models")"
      cat > "$pi_models" <<'PI_JSON_EOF'
{
  "providers": {
    "ollama-local": {
      "baseUrl": "${var.ollama_base_url}/v1",
      "api": "openai-completions",
      "apiKey": "ollama",
      "models": [
        { "id": "qwen3:32b" },
        { "id": "qwen3-coder:30b" },
        { "id": "qwen3:8b" },
        { "id": "gemma3:1b" }
      ]
    }
  }
}
PI_JSON_EOF
    fi

    # opencode: provider entry against OLLAMA_HOST/v1 with qwen3 entries
    # and permissive agent-runner permissions. Written once, same drift
    # rule as pi above (checked by gnhf-smoke-test).
    opencode_json="$HOME/.config/opencode/opencode.json"
    if [ ! -f "$opencode_json" ]; then
      mkdir -p "$(dirname "$opencode_json")"
      cat > "$opencode_json" <<'OPENCODE_JSON_EOF'
{
  "$schema": "https://opencode.ai/config.json",
  "permission": {
    "*": "allow"
  },
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Ollama (local)",
      "options": {
        "baseURL": "${var.ollama_base_url}/v1"
      },
      "models": {
        "qwen3:32b": { "name": "Qwen3 32B" },
        "qwen3-coder:30b": { "name": "Qwen3 Coder 30B" },
        "qwen3:8b": { "name": "Qwen3 8B" }
      }
    }
  }
}
OPENCODE_JSON_EOF
    fi
  EOT

  # These environment variables allow you to make Git commits right away after creating a
  # workspace. Note that they take precedence over configuration defined in ~/.gitconfig!
  # You can remove this block if you'd prefer to configure Git manually or using
  # dotfiles. (see docs/dotfiles.md)
  env = merge(
    {
      GIT_AUTHOR_NAME      = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
      GIT_AUTHOR_EMAIL     = "${data.coder_workspace_owner.me.email}"
      GIT_COMMITTER_NAME   = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
      GIT_COMMITTER_EMAIL  = "${data.coder_workspace_owner.me.email}"
      OLLAMA_HOST          = var.ollama_base_url
      OPENAI_BASE_URL      = "${var.ollama_base_url}/v1"
      OPENAI_API_KEY       = "ollama"
      ANTHROPIC_BASE_URL   = var.ollama_base_url
      ANTHROPIC_AUTH_TOKEN = "ollama"
    },
    # Real Claude credentials are opt-in only; when set they ride along but
    # ANTHROPIC_BASE_URL above still points at local Ollama until unset.
    var.anthropic_api_key != "" ? { ANTHROPIC_API_KEY = var.anthropic_api_key } : {}
  )

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

  metadata {
    display_name = "Workspace Disk"
    key          = "4_workspace_disk"
    script       = "coder stat disk --path /workspace"
    interval     = 60
    timeout      = 1
  }
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

resource "docker_volume" "workspace_volume" {
  name = "coder-${data.coder_workspace.me.id}-workspace"
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
  # Same tailnet-DNS problem, different peer: pin the Ollama hostname to
  # its tailnet IP so OLLAMA_HOST resolves inside the container. Ollama is
  # plain HTTP so there is no TLS/SNI trap, only name resolution.
  dynamic "host" {
    for_each = var.ollama_ip_override != "" ? [var.ollama_ip_override] : []
    content {
      host = var.ollama_hostname
      ip   = host.value
    }
  }
  volumes {
    container_path = "/home/coder"
    volume_name    = docker_volume.home_volume.name
    read_only      = false
  }
  volumes {
    container_path = "/workspace"
    volume_name    = docker_volume.workspace_volume.name
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
