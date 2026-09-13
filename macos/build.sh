#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
version="${MODELUPLINK_VERSION:-$(cat desktop/VERSION)}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Use a numeric release version' >&2; exit 1; }
revision="$(python3 desktop/source.py --field revision)"
identity="${MODELUPLINK_SIGNING_IDENTITY:--}"
preview="${MODELUPLINK_PREVIEW:-0}"
if [[ "$preview" != '0' && "$preview" != '1' ]]; then
  echo 'MODELUPLINK_PREVIEW must be 0 or 1' >&2; exit 1
fi
if [[ "$preview" == '1' && "$identity" != '-' ]]; then
  echo 'Local preview settings cannot be included in a Developer ID build' >&2; exit 1
fi
app="$PWD/dist/macos/Model Uplink.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -mod=readonly \
  -ldflags "-s -w -X github.com/oscar-investmatic/modeluplink-client/pkg/agent.Version=$version -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$revision" \
  -o "$app/Contents/Resources/modeluplink" ./cmd/modeluplink
# DEVELOPER_DIR may select the separately installed Command Line Tools for
# local development; release builds use a fully configured Xcode toolchain.
xcrun swiftc -O -target arm64-apple-macosx14.0 \
  -module-cache-path "${TMPDIR:-/tmp}/modeluplink-swift-cache" \
  macos/Sources/ModelUplink/*.swift -o "$app/Contents/MacOS/ModelUplink"
xcrun swift -module-cache-path "${TMPDIR:-/tmp}/modeluplink-swift-cache" macos/icon.swift "$PWD/dist/macos/AppIcon.iconset"
iconutil -c icns "$PWD/dist/macos/AppIcon.iconset" -o "$app/Contents/Resources/AppIcon.icns"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Model Uplink</string>
<key>CFBundleDisplayName</key><string>Model Uplink</string>
<key>CFBundleIdentifier</key><string>com.modeluplink.mac</string>
<key>CFBundleExecutable</key><string>ModelUplink</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$version</string>
<key>ModelUplinkSourceURL</key><string>https://github.com/oscar-investmatic/modeluplink-client/tree/$revision</string>
<key>CFBundleVersion</key><string>$version</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>LSArchitecturePriority</key><array><string>arm64</string></array>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSPrincipalClass</key><string>NSApplication</string>
</dict></plist>
PLIST
if [[ "$preview" == '1' ]]; then
  /usr/libexec/PlistBuddy -c 'Add :ModelUplinkPreviewControlURL string http://127.0.0.1:8098' "$app/Contents/Info.plist"
  /usr/libexec/PlistBuddy -c 'Add :ModelUplinkPreviewConfigDirectory string /tmp/modeluplink-native-preview' "$app/Contents/Info.plist"
fi
if [[ "$identity" == '-' ]]; then
  codesign --force --sign - "$app/Contents/Resources/modeluplink"
  codesign --force --sign - "$app"
  echo "Local development app only: $app"
else
  codesign --force --options runtime --timestamp --sign "$identity" "$app/Contents/Resources/modeluplink"
  codesign --force --options runtime --timestamp --sign "$identity" "$app"
  echo "Developer ID signed app: $app"
fi
codesign --verify --deep --strict "$app"
