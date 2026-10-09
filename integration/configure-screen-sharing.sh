#!/bin/bash
set -euo pipefail
sudo_cmd() { printf '%s\n' "$MACHINE_PASSWORD" | sudo -S -p '' "$@"; }
dscl /Local/Default -authonly "$(id -un)" "$MACHINE_PASSWORD"
sudo_cmd defaults write /Library/Preferences/com.apple.RemoteManagement VNCAlwaysStartOnConsole -bool true
sudo_cmd /System/Library/CoreServices/RemoteManagement/ARDAgent.app/Contents/Resources/kickstart \
  -configure -access -on -users "$(id -un)" -privs -all
case "$VNC_AUTH" in
  apple)
    sudo_cmd /System/Library/CoreServices/RemoteManagement/ARDAgent.app/Contents/Resources/kickstart \
      -configure -clientopts -setvnclegacy -vnclegacy no
    ;;
  vnc)
    sudo_cmd /System/Library/CoreServices/RemoteManagement/ARDAgent.app/Contents/Resources/kickstart \
      -configure -clientopts -setvnclegacy -vnclegacy yes -setvncpw -vncpw "$VNC_PASSWORD"
    ;;
  *) echo "VNC_AUTH must be vnc or apple" >&2; exit 1 ;;
esac
sudo_cmd launchctl kickstart -k system/com.apple.screensharing
[[ $(stat -f '%Su' /dev/console) == $(id -un) ]] || { echo "The SSH user needs a logged-in graphical session" >&2; exit 1; }
caffeinate -disu -t 180 >/dev/null 2>&1 &
