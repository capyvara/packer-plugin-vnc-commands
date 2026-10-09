#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mode=${1:?expected snapshot or release}
export PACKER_PLUGIN_API_VERSION
PACKER_PLUGIN_API_VERSION=$(bin/packer-plugin-vnc-commands describe | python3 -c 'import json,sys; print(json.load(sys.stdin)["api_version"])')
goreleaser check
case "$mode" in
  snapshot)
    goreleaser release --snapshot --clean
    python3 scripts/verify-packaging.py dist
    ;;
  release)
    tag=$(git describe --tags --exact-match)
    [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Release needs a stable version tag" >&2; exit 1; }
    [[ -z $(git status --porcelain) ]] || { echo "Release needs a clean checkout" >&2; exit 1; }
    # Validate every artifact before GoReleaser uploads anything.
    goreleaser release --clean --skip=publish
    python3 scripts/verify-packaging.py dist
    # GoReleaser OSS does not resume publication. Rebuild the same version
    # with the same deterministic flags, now enabling publication.
    goreleaser release --clean
    ;;
  *) echo "Unknown release mode: $mode" >&2; exit 1 ;;
esac
