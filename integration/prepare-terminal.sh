#!/bin/bash
set -euo pipefail
for ((attempt = 0; attempt < 30; attempt++)); do
  [[ $(stat -f '%Su' /dev/console) == $(id -un) ]] && break
  sleep 1
done
[[ $(stat -f '%Su' /dev/console) == $(id -un) ]] || { echo "The SSH user needs a logged-in graphical session" >&2; exit 1; }
console_state=$(ioreg -n Root -d1 | sed -n '/IOConsoleUsers/p')
if [[ "$console_state" == *'"CGSSessionScreenIsLocked"=Yes'* ]]; then
  echo "Unlock the graphical session before running the integration" >&2
  exit 1
fi
rm -f /tmp/vnc-commands-marker
# Launch a fresh interactive shell through LaunchServices. This avoids
# AppleEvent automation permissions for SSH-launched osascript processes.
session=$(mktemp -d /tmp/vnc-commands-terminal.XXXXXX)
cat > "$session/session.bashrc" <<'BASHRC'
set -o emacs
clear
touch /tmp/vnc-commands-terminal-ready
BASHRC
cat > "$session/session.command" <<'COMMAND'
#!/bin/zsh
export INPUTRC=/dev/null
exec /bin/bash --noprofile --rcfile "$(dirname "$0")/session.bashrc" -i
COMMAND
chmod +x "$session/session.command"
rm -f /tmp/vnc-commands-terminal-ready
open -a Terminal "$session/session.command"
for ((attempt = 0; attempt < 30; attempt++)); do
  if [[ -f /tmp/vnc-commands-terminal-ready ]]; then
    open -a Terminal
    sleep 2
    exit 0
  fi
  sleep 1
done
echo "Terminal did not start its interactive test session within 30 seconds" >&2
exit 1
