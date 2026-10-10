#!/bin/bash
set -euo pipefail
session=$(mktemp -d /tmp/vnc-commands-mouse.XXXXXX)
bundle="$session/MouseTest.app"
mkdir -p "$bundle/Contents/MacOS"
cp /tmp/vnc-commands-mouse-test.swift "$session/main.swift"
cat > "$bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>MouseTest</string>
<key>CFBundleIdentifier</key><string>com.capyvara.vnc-commands.mouse-test</string>
<key>CFBundleName</key><string>VNC Mouse Test</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>NSPrincipalClass</key><string>NSApplication</string>
</dict></plist>
PLIST
# The base image must have Swift/AppKit development tools for this fixture.
xcrun swiftc -module-cache-path "$session/module-cache" "$session/main.swift" -o "$bundle/Contents/MacOS/MouseTest"
rm -f /tmp/vnc-commands-mouse-ready
: > /tmp/vnc-commands-mouse-marker
open "$bundle"
for ((attempt = 0; attempt < 30; attempt++)); do
  if [[ -f /tmp/vnc-commands-mouse-ready ]]; then
    echo "Native mouse test window is ready"
    exit 0
  fi
  sleep 1
done
echo "Mouse test window did not start within 30 seconds" >&2
exit 1
