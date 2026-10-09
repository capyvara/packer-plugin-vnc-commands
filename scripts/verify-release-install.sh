#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
export PACKER_PLUGIN_PATH="$temporary/plugins"
sed 's/version = "~> 0.1"/version = "= 0.1.0"/' examples/basic.pkr.hcl > "$temporary/release.pkr.hcl"
packer init "$temporary/release.pkr.hcl"
packer validate "$temporary/release.pkr.hcl"
plugin=$(find "$PACKER_PLUGIN_PATH" -type f -name 'packer-plugin-vnc-commands_v0.1.0_x*' ! -name '*SHA256SUM')
[[ -n "$plugin" ]]
python3 scripts/verify-metadata.py "$plugin"
