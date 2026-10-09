# packer-plugin-vnc-commands

A Packer provisioner that types standard boot-command expressions over VNC at any point in a build. It connects from the machine running Packer, independently of the builder's communicator.

## Installation

Add the plugin to your HCL template, then run `packer init .`:

```hcl
packer {
  required_plugins {
    vnc-commands = {
      source  = "github.com/capyvara/vnc-commands"
      version = "~> 0.1"
    }
  }
}
```

The source deliberately omits `packer-plugin-`; the public repository is [capyvara/packer-plugin-vnc-commands](https://github.com/capyvara/packer-plugin-vnc-commands). For legacy JSON templates, install first with `packer plugins install github.com/capyvara/vnc-commands`.

## Configuration

| Field | Default | Description |
| --- | --- | --- |
| `host` | Required | Nonempty IP address or DNS name of the VNC server. Use an unbracketed IPv6 address such as `::1`; address construction uses `net.JoinHostPort`. |
| `port` | `5900` | TCP port from 1 to 65535. |
| `password` | `""` | Standard VNC password. Empty selects unauthenticated VNC; a supplied password requires password authentication. |
| `commands` | Required | Nonempty list of strings with nonempty combined text, concatenated without separators. |
| `key_interval` | `"100ms"` | Positive Go duration between each key event, including key-down and key-up. |
| `connect_timeout` | `"30s"` | Positive Go duration bounding TCP connection and the complete VNC handshake together. |

Packer's common provisioner options also apply, including `timeout`, `only`, `except`, and `pause_before`. Set `timeout` to bound the entire provisioner, including typing and waits. Zero and negative durations are invalid.

HCL supports ordinary variables (`var.vnc_password`) and runtime builder values (`build.Host`). Packer prepares the provisioner again when builder values are available. Legacy JSON supports user expressions such as ``{{user `vnc_password`}}`` and runtime expressions such as `{{.Host}}` in `host`, `password`, and `commands`.

## Between shell provisioners

Prepare the guest's graphical session and focus the destination application before typing. VNC sends key events, so the guest's focus and keyboard layout determine their effect.

```hcl
build {
  sources = ["source.tart-cli.macos"]

  provisioner "shell" {
    inline = ["open -a Terminal"]
  }

  provisioner "vnc-commands" {
    host            = build.Host
    port            = 5900
    password        = var.vnc_password
    commands        = ["<wait10s>", "echo ready > /tmp/vnc-ready<enter>", "<wait2s>"]
    key_interval    = "100ms"
    connect_timeout = "30s"
    timeout         = "2m"
  }

  provisioner "shell" {
    inline = ["test \"$(cat /tmp/vnc-ready)\" = ready"]
  }
}
```

On macOS, Screen Sharing's “Your screen is being controlled” notification can temporarily take keyboard focus when a VNC client connects. Start `commands` with `<wait10s>` to let it disappear before sending keys. This wait happens after the VNC handshake; Packer's `pause_before` happens before connecting and cannot cover the notification. A trailing `<wait2s>` gives the guest time to process the final keys before the disconnect notification appears. Adjust these waits for your guest.

See [examples/basic.pkr.hcl](examples/basic.pkr.hcl) and [examples/legacy.json](examples/legacy.json). The null-builder examples are for validation; building requires a real SSH/VNC guest.

## Command semantics

Commands use the Packer SDK parser, validator, and VNC driver. List boundaries add no spaces, newlines, waits, or modifier releases; even a token can span two strings. Supported expressions include:

- Literal text and special keys: `<enter>`, `<return>`, `<esc>`, `<tab>`, `<bs>`, `<del>`, `<spacebar>`, `<insert>`, `<home>`, `<end>`, `<menu>`, `<pageUp>`, `<pageDown>`, `<up>`, `<down>`, `<left>`, `<right>`, and `<f1>` through `<f12>`.
- Modifiers: `<leftAlt>`, `<rightAlt>`, `<leftCtrl>`, `<rightCtrl>`, `<leftShift>`, `<rightShift>`, `<leftSuper>`, `<rightSuper>`, `<leftCommand>`, `<rightCommand>`, `<leftOption>`, and `<rightOption>`.
- Holds and releases: `<leftCtrlOn>c<leftCtrlOff>` or `<aOn><wait1s><aOff>`. Explicitly release keys you hold; the plugin does not add releases.
- Waits: `<wait>` (one second), `<wait5>`, `<wait10>`, or arbitrary positive durations such as `<wait300ms>`, `<wait2s>`, and `<wait1m20s>`.

Special key names and On/Off suffixes are case insensitive; `wait` is lowercase. Unknown or malformed tokens can be treated as literal text by the SDK, so spelling matters. The SDK synthesizes Shift for uppercase letters and shifted punctuation. Text entry follows its key mapping; arbitrary Unicode and non-US guest layouts are not guaranteed.

This plugin does not implement OCR, mouse operations, endpoint discovery, SSH tunnels, or builder-specific extensions such as Tart's `<click 'text'>` and `<wait 'text'>`. Connect to a known, reachable VNC endpoint. It never automatically reconnects or replays commands after an error.

## Protocol, authentication, and logging

The pinned `mitchellh/go-vnc` client negotiates **RFB 3.8** and supports only None (security type 1) and classic VNC password authentication (type 2). It does not implement TLS, VeNCrypt, Apple account authentication, or RFB 3.3/3.7. Classic VNC passwords use only the first eight bytes. Connections are shared, preserving other VNC clients, and are closed on success, error, timeout, or cancellation. Disconnects interrupt timed waits; cancellation also interrupts long key intervals and blocked socket operations.

Normal plugin status messages omit passwords and typed commands. **Packer/SDK debug logs can expose typed characters**, and Packer can log configuration or shell setup. Avoid enabling `PACKER_LOG` when typing secrets. The transport is unencrypted; use an appropriately trusted network or an externally managed tunnel.

## Development

Install [Mise](https://mise.jdx.dev/), then run `mise install`. Tool versions are pinned in `mise.toml`: Go 1.26.8, GoReleaser 2.18.3, and Packer 1.16.1. Tart 2.40.1 is scoped to the local integration task. SDK tooling always runs from v0.6.12; `go-vnc` is pinned in `go.mod`. The SDK's required `go-cty` compatibility replacement is also pinned there. Python 3 and Bash are used by verification scripts.

| Task | Purpose |
| --- | --- |
| `mise run generate` | Regenerate the checked-in HCL schema. |
| `mise run fmt` | Format Go and HCL files. |
| `mise run check` | Formatting, schema consistency, vet, race tests, metadata, isolated Packer HCL/JSON validation. |
| `mise run test` | Race-enabled local RFB server and configuration tests. |
| `mise run build` | Build `bin/packer-plugin-vnc-commands` with a development version. |
| `mise run install` | Install the development binary into the normal Packer plugin directory. |
| `mise run integration:tart` | Run the disposable macOS VM integration. |
| `mise run release:snapshot` | Validate ZIP packaging for all six platforms without publication. |
| `mise run release` | Publish a clean stable version tag with GoReleaser. |
| `mise run release:verify-install` | Install v0.1.0 with `packer init` in an isolated directory and validate it. |

## Local Tart integration

Requires macOS on Apple Silicon, a cached stopped `macos-base` VM, SSH with password authentication, an unlocked graphical session for the SSH user, and working Screen Sharing configured on the base. The default guest credentials are `admin`/`admin`. The test opens a fresh Terminal session through LaunchServices and waits for it to start.

```sh
mise run integration:tart
# Pause before each VNC step to inspect focus and modal dialogs:
TART_DEBUG_PAUSE=true mise run integration:tart
# Optional overrides:
TART_BASE_VM=another-cached-base TART_SSH_USERNAME=admin TART_SSH_PASSWORD=admin mise run integration:tart
```

The task generates a unique test VM name and lets Packer clone the base. It opens a Tart window, keeps the display awake, retains Screen Sharing, and configures classic VNC password access and control privileges on the clone. A shell step prepares Terminal; the VNC step waits ten seconds after connecting, types the marker command, then waits two seconds before disconnecting. The final shell step verifies exact marker bytes over SSH with a 30-second deadline. The task's trap deletes only its generated test VM. The base is unchanged. `TART_VNC_PASSWORD` overrides the VNC test password (default `vnctest`); it is a disposable test credential. This integration runs locally, without a dedicated CI runner.

## Releases

CI runs `mise run check` and `mise run release:snapshot` on pushes to `main` and pull requests. Before a release, also pass the local Tart integration and wait for CI on the exact commit. Then:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The tag workflow checks the code, builds and validates packaging before publication, and uses GoReleaser to publish ZIPs for Linux, macOS, and Windows on amd64/arm64. Packer API version comes from the built plugin's `describe` metadata. ZIPs and enclosed binaries follow `packer-plugin-vnc-commands_vVERSION_xAPI_OS_ARCH` (Windows binaries append `.exe`), with `packer-plugin-vnc-commands_vVERSION_SHA256SUMS` covering the archives. The workflow verifies the published release through `packer init`; run `mise run release:verify-install` locally too. For a manual release, set `GITHUB_TOKEN` and run `mise run release` from a clean tagged checkout.

MIT licensed; see [LICENSE](LICENSE).
