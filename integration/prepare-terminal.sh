#!/bin/bash
set -euo pipefail
sudo_cmd() { printf '%s\n' "$MACHINE_PASSWORD" | sudo -S -p '' "$@"; }
# Keep the base's Screen Sharing service; only configure legacy password access.
sudo_cmd /System/Library/CoreServices/RemoteManagement/ARDAgent.app/Contents/Resources/kickstart \
  -configure -clientopts -setvnclegacy -vnclegacy yes -setvncpw -vncpw "$VNC_PASSWORD"
sudo_cmd launchctl kickstart -k system/com.apple.screensharing
[[ $(stat -f '%Su' /dev/console) == $(id -un) ]] || { echo "The SSH user needs a logged-in graphical session" >&2; exit 1; }
rm -f /tmp/vnc-commands-marker
osascript <<'APPLESCRIPT'
with timeout of 30 seconds
  tell application "Terminal"
    activate
    do script "clear"
  end tell
end timeout
APPLESCRIPT
sleep 2
