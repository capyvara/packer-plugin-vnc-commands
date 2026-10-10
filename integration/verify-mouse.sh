#!/bin/bash
set -euo pipefail
expected=$'left-down\nleft-up\nright-down\nright-up\nleft-down\nleft-up\n'
for ((attempt = 0; attempt < 30; attempt++)); do
  if printf '%s' "$expected" | cmp -s - /tmp/vnc-commands-mouse-marker; then
    echo "Verified exact left/right mouse button events over SSH"
    kill "$(cat /tmp/vnc-commands-mouse-ready)"
    exit 0
  fi
  sleep 1
done
echo "Mouse button events did not match within 30 seconds" >&2
cat /tmp/vnc-commands-mouse-marker
exit 1
