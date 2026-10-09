#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

unformatted=$(gofmt -l main.go provisioner/vnccommands)
if [[ -n "$unformatted" ]]; then
  echo "Go files need formatting: $unformatted" >&2
  exit 1
fi
packer fmt -check -recursive examples integration
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
cp provisioner/vnccommands/config.hcl2spec.go "$temporary/schema.go"
mise run generate
cmp "$temporary/schema.go" provisioner/vnccommands/config.hcl2spec.go
go vet ./...
mise run test
mise run build
(cd bin && go run github.com/hashicorp/packer-plugin-sdk/cmd/packer-sdc@v0.6.12 plugin-check packer-plugin-vnc-commands)
python3 scripts/verify-metadata.py bin/packer-plugin-vnc-commands
# No use of the developer's normal plugin directory or remote downloads.
export PACKER_PLUGIN_PATH="$temporary/plugins"
packer plugins install --path bin/packer-plugin-vnc-commands github.com/capyvara/vnc-commands
packer validate examples/basic.pkr.hcl
packer validate examples/legacy.json
