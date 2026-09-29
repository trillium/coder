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
    if [ -n "${var.ollama_ip_override}" ]; then
      sed -i '/${var.ollama_hostname}/d' /etc/hosts || true
      echo "${var.ollama_ip_override} ${var.ollama_hostname}" >> /etc/hosts
    fi

    # Local-model environment for every login shell (agent env below covers
    # the Coder agent process itself; SSH sessions source profile.d).
    cat > /etc/profile.d/ollama-local.sh <<PROFILE_EOF
    export OLLAMA_HOST="${var.ollama_base_url}"
    export OPENAI_BASE_URL="${var.ollama_base_url}/v1"
    export OPENAI_API_KEY="ollama"
    export ANTHROPIC_BASE_URL="${var.ollama_base_url}"
    export ANTHROPIC_AUTH_TOKEN="ollama"
    PROFILE_EOF
    chmod 644 /etc/profile.d/ollama-local.sh
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
