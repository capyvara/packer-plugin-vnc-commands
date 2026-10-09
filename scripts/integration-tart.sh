#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]] || { echo "Tart integration needs macOS on Apple Silicon" >&2; exit 1; }

export PKR_VAR_base_vm=${TART_BASE_VM:-macos-base}
export PKR_VAR_ssh_username=${TART_SSH_USERNAME:-admin}
export PKR_VAR_ssh_password=${TART_SSH_PASSWORD:-admin}
export PKR_VAR_vnc_password=${TART_VNC_PASSWORD:-vnctest}
export PKR_VAR_vnc_auth=${TART_VNC_AUTH:-vnc}
export PKR_VAR_debug_pause=${TART_DEBUG_PAUSE:-false}
# Always generate the output name here; callers cannot override the cleanup target.
export PKR_VAR_test_vm="vnc-commands-test-$(uuidgen | tr '[:upper:]' '[:lower:]')"
tart get "$PKR_VAR_base_vm" >/dev/null
if tart get "$PKR_VAR_test_vm" >/dev/null 2>&1; then
  echo "Refusing to use an existing VM: $PKR_VAR_test_vm" >&2
  exit 1
fi
temporary=$(mktemp -d)
cleanup() {
  if tart get "$PKR_VAR_test_vm" >/dev/null 2>&1; then
    tart stop "$PKR_VAR_test_vm" >/dev/null 2>&1 || true
    tart delete "$PKR_VAR_test_vm"
  fi
  rm -rf "$temporary"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export PACKER_PLUGIN_PATH="$temporary/plugins"
packer plugins install --path bin/packer-plugin-vnc-commands github.com/capyvara/vnc-commands
# init queries GitHub for development plugins even when installed locally.
# Bootstrap only the released Tart dependency before validating the full build.
cat > "$temporary/tart-plugin.pkr.hcl" <<'HCL'
packer {
  required_plugins {
    tart = {
      source  = "github.com/cirruslabs/tart"
      version = "= 1.21.0"
    }
  }
}
HCL
packer init "$temporary/tart-plugin.pkr.hcl"
packer validate integration/tart.pkr.hcl
packer build -on-error=cleanup integration/tart.pkr.hcl
