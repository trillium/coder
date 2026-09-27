# dev-credentials — composed the Coder way.
#
# Shape:
#   parameters (dashboard-pickable per workspace)
#     └─ repo ............ which project this disposable box opens (default: trillium/displayd)
#   registry modules (upstream-owned, version-pinned)
#     ├─ code-server ..... browser IDE
#     └─ git-clone ....... repo checkout, skips if present
#   local modules (homelab-owned, versioned with the template)
#     └─ agentic-tools ... git/gh/python/Pillow/node/opencode/pi, dashboard-visible
#   platform (thin, identical across templates)
#     └─ docker container + home volume + bridge-DNS fix
#
# Auth is intentionally NOT a template input: it is exported post-create via
# homelab/bin/coder-auth-export.sh (stopgap until a secret store supersedes it).

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
  description = "Hostname of CODER_ACCESS_URL as seen by workspaces. Mapped to the Docker host gateway so the agent can reach coderd."
  type        = string
}

variable "docker_socket" {
  default     = ""
  description = "(Optional) Docker socket URI"
  type        = string
}

provider "docker" {
  host = var.docker_socket != "" ? var.docker_socket : null
}

data "coder_provisioner" "me" {}
data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

# Per-project selection, picked in the dashboard (or --parameter) at create
# time. One disposable workspace per project; the template itself never changes.
data "coder_parameter" "repo" {
  name         = "repo"
  display_name = "Git repository"
  description  = "Which project this box opens. Defaults to displayd."
  default      = "https://github.com/trillium/displayd.git"
  mutable      = true
}

resource "coder_agent" "main" {
  arch           = data.coder_provisioner.me.arch
  os             = "linux"
  startup_script = <<-EOT
    set -e
    if [ ! -f ~/.init_done ]; then
      cp -rT /etc/skel ~
      touch ~/.init_done
    fi
  EOT

  env = {
    GIT_AUTHOR_NAME     = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
    GIT_AUTHOR_EMAIL    = "${data.coder_workspace_owner.me.email}"
    GIT_COMMITTER_NAME  = coalesce(data.coder_workspace_owner.me.full_name, data.coder_workspace_owner.me.name)
    GIT_COMMITTER_EMAIL = "${data.coder_workspace_owner.me.email}"
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

module "agentic-tools" {
  source   = "./modules/agentic-tools"
  agent_id = coder_agent.main.id
}

module "git-clone" {
  source   = "registry.coder.com/coder/git-clone/coder"
  version  = "~> 1.0"
  agent_id = coder_agent.main.id
  url      = data.coder_parameter.repo.value
}

module "code-server" {
  count    = data.coder_workspace.me.start_count
  source   = "registry.coder.com/coder/code-server/coder"
  version  = "~> 1.0"
  agent_id = coder_agent.main.id
  order    = 1
}

resource "docker_volume" "home_volume" {
  name = "coder-${data.coder_workspace.me.id}-home"
  lifecycle {
    ignore_changes = all
  }
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
    label = "coder.workspace_name_at_creation"
    value = data.coder_workspace.me.name
  }
}

resource "docker_container" "workspace" {
  count      = data.coder_workspace.me.start_count
  image      = "codercom/example-base:ubuntu"
  name       = "coder-${data.coder_workspace_owner.me.name}-${lower(data.coder_workspace.me.name)}"
  hostname   = data.coder_workspace.me.name
  entrypoint = ["sh", "-c", replace(coder_agent.main.init_script, "/localhost|127\\.0\\.0\\.1/", "host.docker.internal")]
  env        = ["CODER_AGENT_TOKEN=${coder_agent.main.token}"]
  host {
    host = "host.docker.internal"
    ip   = "host-gateway"
  }
  host {
    host = var.coder_access_host
    ip   = "host-gateway"
  }
  volumes {
    container_path = "/home/coder"
    volume_name    = docker_volume.home_volume.name
    read_only      = false
  }
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
