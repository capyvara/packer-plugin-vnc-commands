#!/bin/bash
set -euo pipefail
for ((attempt = 0; attempt < 30; attempt++)); do
  if [[ -f /tmp/vnc-commands-marker ]]; then
    printf '%s' 'vnc-commands-ok' | cmp - /tmp/vnc-commands-marker
    echo "Verified exact VNC marker contents over SSH"
    exit 0
  fi
  sleep 1
done
echo "VNC marker did not appear within 30 seconds" >&2
exit 1
