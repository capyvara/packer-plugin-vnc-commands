# packer-plugin-vnc-commands

A Packer provisioner that sends standard boot-command expressions and coordinate-based mouse clicks over VNC at any point in a build. It connects from the machine running Packer, independently of the builder's communicator.

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
| `username` | `""` | A supplied username selects Apple account authentication (security type 30). Use the Mac account's short name and its account password. No fallback to other authentication methods. |
| `password` | `""` | Mac account password when `username` is supplied; otherwise a classic VNC password. With both credentials empty, selects unauthenticated VNC. Apple credentials must each fit in 63 bytes and contain no NUL; checked after interpolation. |
| `commands` | Required | Nonempty list of strings with nonempty combined text, concatenated without separators. |
| `key_interval` | `"100ms"` | Positive Go duration between input events, including key-down/key-up and mouse movement/button-down/button-up. |
| `connect_timeout` | `"30s"` | Positive Go duration bounding TCP connection and the complete VNC handshake together. |
| `preview` | `false` | Serve an observe-only browser preview on `127.0.0.1` at an automatically assigned port. Always prints its URL when enabled. |
| `preview_auto_open` | `false` | Open the preview URL in the Packer host's default browser. Requires `preview = true`. If opening fails, prints a message and continues. |
| `recording_path` | `""` | Save the screen to this local MJPEG AVI file; must end in `.avi`. Empty disables recording. Creates parent directories and refuses to overwrite an existing file. Relative paths use the Packer process's working directory. |
| `frame_rate` | `5` | Preview and recording frames per second, from 1 to 60. Also controls screen-update requests for mouse resize tracking when capture is disabled. |

Packer's common provisioner options also apply, including `timeout`, `only`, `except`, and `pause_before`. Set `timeout` to bound the entire provisioner, including typing and waits. Zero and negative durations are invalid.

HCL supports ordinary variables (`var.vnc_password`) and runtime builder values (`build.Host`). Packer prepares the provisioner again when builder values are available. Legacy JSON supports user expressions such as ``{{user `vnc_password`}}`` and runtime expressions such as `{{.Host}}` in `host`, `username`, `password`, `commands`, and `recording_path`.

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

## macOS account authentication

Enable **System Settings > General > Sharing > Screen Sharing** and allow the Mac account to connect. You can leave **“VNC viewers may control screen with password” disabled**. Supply the account's short username and login password:

```hcl
provisioner "vnc-commands" {
  host     = build.Host
  username = var.macos_username
  password = var.macos_password
  commands = ["<wait10s>", "echo ready<enter>", "<wait2s>"]
}
```

Declare the password variable with `sensitive = true`. Authentication does not prepare or focus the graphical session: keep the desired account logged in with the target application focused before typing. Supplying a username requires Apple authentication, even if the server also offers classic VNC or unauthenticated access. Omitting `username` preserves classic VNC password behavior. This is a local macOS account, not an Apple ID.

## Screen preview and recording

Enable either feature or both:

```hcl
provisioner "vnc-commands" {
  host     = build.Host
  username = var.macos_username
  password = var.macos_password
  commands = ["<wait10s>", "echo ready<enter>", "<wait2s>"]

  preview           = true
  preview_auto_open = false
  recording_path    = "artifacts/vnc-session.avi"
  frame_rate        = 5
}
```

When `preview` is enabled, Packer prints `VNC preview (observe only): http://127.0.0.1:PORT/TOKEN/`, whether or not `preview_auto_open` is enabled. Open that URL on the machine running Packer. With `preview_auto_open = true`, the plugin also opens the default browser using `open` on macOS, `xdg-open` on Linux, or `rundll32.exe` on Windows. The preview has no keyboard, mouse, or clipboard input endpoint. Closing a viewer does not stop provisioning. Multiple viewers can connect; slow viewers do not block capture or typing. The relative `stream` URL serves multipart MJPEG for external consumers.

Capture uses the existing shared VNC connection, so it adds no second login. It requests a full screen first, then incremental changed rectangles. Commands start after the first screen arrives; `connect_timeout` separately bounds that initial screen wait. The requested frame rate is a sampling target; available screen updates depend on the server and network. During static screens and waits, recording repeats the latest frame to preserve elapsed time. JPEG quality is 85, and recording needs no FFmpeg or guest software.

Recording works with `preview = false`. Each AVI contains only this provisioner's execution, including its command waits; the file is indexed and closed on completion, error, timeout, and cancellation. Capture and recording failures fail the provisioner. Choose a distinct recording path for each step or run: existing files are never overwritten. Classic AVI files are limited to 4 GiB; reaching that limit fails the step and finalizes the frames already saved. No audio is captured.

The preview adapts to desktop resolution changes. AVI retains the initial dimensions and fits resized screens into that frame with black borders. Capture accepts screens up to 16 megapixels and 16384 pixels per side. The preview server closes when the provisioner ends. Screen images can include anything visible in the guest, including text typed by the commands; treat preview URLs and recordings accordingly.

## Command semantics

Keyboard commands use the Packer SDK parser, validator, and VNC driver; the plugin adds mouse expressions. List boundaries add no spaces, newlines, waits, or modifier releases; even a token can span two strings. Supported expressions include:

- Literal text and special keys: `<enter>`, `<return>`, `<esc>`, `<tab>`, `<bs>`, `<del>`, `<spacebar>`, `<insert>`, `<home>`, `<end>`, `<menu>`, `<pageUp>`, `<pageDown>`, `<up>`, `<down>`, `<left>`, `<right>`, and `<f1>` through `<f12>`.
- Modifiers: `<leftAlt>`, `<rightAlt>`, `<leftCtrl>`, `<rightCtrl>`, `<leftShift>`, `<rightShift>`, `<leftSuper>`, `<rightSuper>`, `<leftCommand>`, `<rightCommand>`, `<leftOption>`, and `<rightOption>`.
- Holds and releases: `<leftCtrlOn>c<leftCtrlOff>` or `<aOn><wait1s><aOff>`. Explicitly release keys you hold; the plugin does not add releases.
- Waits: `<wait>` (one second), `<wait5>`, `<wait10>`, or arbitrary positive durations such as `<wait300ms>`, `<wait2s>`, and `<wait1m20s>`.
- Mouse clicks: `<click 400,300>` for the left button and `<rightClick 400,300>` for the right button. Each coordinate accepts integer pixels or a percentage, for example `<rightClick 50%,75%>` or `<click 400,75%>`. Mouse command names are case insensitive; whitespace around coordinates is optional.

Special key names and On/Off suffixes are case insensitive; `wait` is lowercase. Unknown or malformed keyboard tokens can be treated as literal text by the SDK, so spelling matters. Recognized mouse commands with missing or invalid coordinates fail validation instead of being typed. The SDK synthesizes Shift for uppercase letters and shifted punctuation. Text entry follows its key mapping; arbitrary Unicode and non-US guest layouts are not guaranteed.

### Mouse coordinates

```hcl
commands = [
  "<wait10s>",
  "<click 400,300>",
  "<rightClick 50%,75%><wait1s><esc>",
  "<leftCtrlOn><click 25%,50%><leftCtrlOff>",
  "<wait2s>",
]
```

Coordinates refer to the VNC server's framebuffer: `(0,0)` is the top-left, X increases rightward, and Y increases downward. They are independent of the preview's displayed size and macOS logical points. Pixel values must be integers from 0 to 65535 and fall within the current framebuffer. Percentages accept decimals from `0%` to `100%`; each axis maps with `round(percent / 100 * (dimension - 1))`, so `100%` targets the last pixel.

Percentages resolve at the start of each click using the latest dimensions received from the server. DesktopSize updates are tracked even without preview or recording; mouse-only sessions discard pixel payloads without rendering images. Resize support depends on the server advertising changes, and a resize concurrent with a click can briefly make its coordinates stale. Percentage coordinates follow the framebuffer size, not UI elements that move independently.

A click moves the pointer, presses the selected button, then releases it at the same coordinates, with `key_interval` after each event. Held keyboard modifiers remain active across clicks. On an interrupted button hold the plugin attempts a release, but delivery cannot be guaranteed after a disconnect. Mouse expressions support the same HCL and legacy JSON interpolation as other commands; malformed rendered coordinates fail before connecting. A bare `<rightClick>` requires coordinates and is invalid.

This plugin does not implement OCR, dragging, scrolling, endpoint discovery, SSH tunnels, or builder-specific extensions such as Tart's `<click 'text'>` and `<wait 'text'>`. Connect to a known, reachable VNC endpoint. It never automatically reconnects or replays commands after an error.

## Protocol, authentication, and logging

The pinned `mitchellh/go-vnc` client negotiates **RFB 3.8**, including from Apple's `RFB 003.889` banner. It supports None (security type 1), classic VNC password authentication (type 2), and the plugin's Apple Remote Desktop account authentication (type 30). Apple authentication exchanges Diffie-Hellman keys and encrypts the credentials with AES-128-ECB using MD5 of the fixed-width shared secret, as required by that protocol. Classic VNC passwords use only the first eight bytes; Apple account passwords use the complete supplied value, up to 63 bytes. TLS, VeNCrypt, and RFB 3.3/3.7 are not implemented. Connections are shared, preserving other VNC clients, and are closed on success, error, timeout, or cancellation. Disconnects interrupt timed waits; cancellation also interrupts long key intervals and blocked socket operations.

Normal plugin status messages omit account credentials and typed commands. **Packer/SDK debug logs can expose typed characters**, and Packer can log configuration or shell setup. Avoid enabling `PACKER_LOG` when typing secrets. Apple authentication encrypts the credentials but does not authenticate the server or encrypt subsequent traffic, including typed keys. Use an appropriately trusted network or an externally managed tunnel.

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
| `mise run release:verify-install` | Install the release for the latest local tag with `packer init` in an isolated directory and validate it. |

## Local Tart integration

Requires macOS on Apple Silicon, a cached stopped `macos-base` VM, SSH with password authentication, an unlocked graphical session for the SSH user, working Screen Sharing configured on the base, and Swift/AppKit development tools in the guest. The default guest credentials are `admin`/`admin`. The test opens a fresh Terminal session through LaunchServices and waits for it to start, then compiles a native window to verify mouse buttons.

```sh
mise run integration:tart
# Authenticate as the Mac account with classic VNC password access disabled:
TART_VNC_AUTH=apple mise run integration:tart
# Pause before each VNC step to inspect focus and modal dialogs:
TART_DEBUG_PAUSE=true mise run integration:tart
# Optional overrides:
TART_BASE_VM=another-cached-base TART_SSH_USERNAME=admin TART_SSH_PASSWORD=admin mise run integration:tart
```

The task generates a unique test VM name and lets Packer clone the base. It opens a Tart window, keeps the display awake, retains Screen Sharing, and configures control privileges on the clone. By default it enables classic VNC password access; `TART_VNC_AUTH=apple` disables that access and authenticates with the SSH user's Mac account credentials. A shell step prepares Terminal; the VNC step waits ten seconds after connecting, types the marker command, then waits two seconds before disconnecting. A shell step verifies exact marker bytes over SSH with a 30-second deadline. A second VNC step sends pixel and percentage clicks to the native test window with capture disabled; SSH verifies the exact left/right button-down and button-up sequence. Both steps support a debugging breakpoint. The task's trap deletes only its generated test VM. The base is unchanged. `TART_VNC_PASSWORD` overrides the classic VNC test password (default `vnctest`); it is a disposable test credential. This integration runs locally, without a dedicated CI runner.

## Releases

CI runs `mise run check` and `mise run release:snapshot` on pushes to `main` and pull requests. Before a release, also pass the local Tart integration and wait for CI on the exact commit. Then:

```sh
git tag v0.3.0
git push origin v0.3.0
```

The tag workflow checks the code, builds and validates packaging before publication, and uses GoReleaser to publish ZIPs for Linux, macOS, and Windows on amd64/arm64. Packer API version comes from the built plugin's `describe` metadata. ZIPs and enclosed binaries follow `packer-plugin-vnc-commands_vVERSION_xAPI_OS_ARCH` (Windows binaries append `.exe`), with `packer-plugin-vnc-commands_vVERSION_SHA256SUMS` covering the archives. The workflow verifies the published release through `packer init`; run `mise run release:verify-install` locally too. For a manual release, set `GITHUB_TOKEN` and run `mise run release` from a clean tagged checkout.

MIT licensed; see [LICENSE](LICENSE).
