#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
release_version=${1:-$(git describe --tags --abbrev=0)}
release_version=${release_version#v}
[[ "$release_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Expected a stable release version" >&2; exit 1; }
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
export PACKER_PLUGIN_PATH="$temporary/plugins"
sed "s/version = \"~> 0.1\"/version = \"= $release_version\"/" examples/basic.pkr.hcl > "$temporary/release.pkr.hcl"
packer init "$temporary/release.pkr.hcl"
packer validate "$temporary/release.pkr.hcl"
plugin=$(find "$PACKER_PLUGIN_PATH" -type f -name "packer-plugin-vnc-commands_v${release_version}_x*" ! -name '*SHA256SUM')
[[ -n "$plugin" ]]
python3 scripts/verify-metadata.py "$plugin"
