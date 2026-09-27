# Shared local module: agentic toolchain for disposable dev workspaces.
# Consumed by path reference (source = "./modules/agentic-tools") so every
# template gets the same toolset, versioned with the template itself.
# Registry modules cover the generic parts (code-server, git-clone);
# only homelab-specific tooling lives here.

terraform {
  required_providers {
    coder = {
      source = "coder/coder"
    }
  }
}

variable "agent_id" {
  description = "ID of the workspace agent to attach setup scripts to."
  type        = string
}

# Phase 1: toolchain. Async (never blocks login), idempotent via marker
# file, dashboard-visible with logs. Paths verified 2026-09-25 on dev-proof:
# opencode installer -> ~/.opencode/bin; npm globals -> ~/.local (no sudo);
# Ubuntu pip needs --break-system-packages.
resource "coder_script" "tools" {
  agent_id     = var.agent_id
  display_name = "Dev tools"
  icon         = "/icon/tools.svg"
  run_on_start = true
  script       = <<-EOT
    #!/bin/sh
    set +e
    MARKER="$HOME/.agentic_done_v4"
    if [ -f "$MARKER" ]; then
      echo "tools already installed, skipping"
      exit 0
    fi
    sudo apt-get update >/dev/null 2>&1
    sudo apt-get install -y git gh python3 python3-pip curl ca-certificates >/dev/null 2>&1
    python3 -m pip install --user --break-system-packages 'Pillow>=9.0' >/dev/null 2>&1
    if ! command -v node >/dev/null 2>&1; then
      curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - >/dev/null 2>&1
      sudo apt-get install -y nodejs >/dev/null 2>&1
    fi
    if ! command -v opencode >/dev/null 2>&1; then
      curl -fsSL https://opencode.ai/install | bash >/dev/null 2>&1
    fi
    if ! command -v pi >/dev/null 2>&1; then
      npm config set prefix "$HOME/.local" >/dev/null 2>&1
      npm install -g @earendil-works/pi-coding-agent >/dev/null 2>&1
    fi
    if ! command -v pnpm >/dev/null 2>&1; then
      npm install -g pnpm >/dev/null 2>&1
    fi
    if ! command -v bun >/dev/null 2>&1; then
      curl -fsSL https://bun.sh/install | bash >/dev/null 2>&1
    fi
    grep -q '.opencode/bin' "$HOME/.bashrc" 2>/dev/null || echo 'export PATH="$HOME/.opencode/bin:$HOME/.local/bin:$HOME/.bun/bin:$PATH"' >> "$HOME/.bashrc"
    # Non-interactive shells (agents, ssh one-shots) never read .bashrc:
    # publish the same PATH system-wide so tools resolve everywhere.
    printf 'export PATH="$HOME/.opencode/bin:$HOME/.local/bin:$HOME/.bun/bin:$PATH"\n' | sudo tee /etc/profile.d/agentic-tools.sh >/dev/null 2>&1 || true
    # Bulletproof PATH: non-interactive shells (ssh one-shots, agent exec)
    # read neither .bashrc nor /etc/profile.d. /usr/local/bin is on the
    # default PATH everywhere, so link the user-local toolbins there.
    for d in "$HOME/.opencode/bin" "$HOME/.local/bin" "$HOME/.bun/bin"; do
      if [ -d "$d" ]; then
        for b in "$d"/*; do
          [ -x "$b" ] || continue
          sudo ln -sf "$b" /usr/local/bin/ 2>/dev/null || true
        done
      fi
    done
    # Readiness manifest: one file saying what is installed and authed.
    # Agents read this instead of probing. Best-effort JSON.
    {
      printf '{\n  "generated": "%s",\n' "$(date -u +%FT%TZ)"
      printf '  "tools": {\n'
      first=1
      for t in git gh python3 node npm opencode pi pnpm bun; do
        if p=$(command -v "$t" 2>/dev/null); then
          v=$("$t" --version 2>/dev/null | head -n 1 | tr -d '"' | cut -c1-64)
          [ "$first" = 1 ] || printf ',\n'
          printf '    "%s": {"path": "%s", "version": "%s"}' "$t" "$p" "$v"
          first=0
        fi
      done
      printf '\n  },\n'
      if gh auth status >/dev/null 2>&1; then g=true; else g=false; fi
      if python3 -c "import PIL" >/dev/null 2>&1; then pl=true; else pl=false; fi
      printf '  "gh_logged_in": %s,\n  "pillow": %s\n}\n' "$g" "$pl"
    } > "$HOME/toolchain.json" 2>/dev/null || true
    touch "$MARKER"
    echo "toolchain ready"
  EOT
}
