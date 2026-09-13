#!/bin/bash
# Run only after signing with an Apple Developer ID Application certificate.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${MODELUPLINK_SIGNING_IDENTITY:?Set a Developer ID Application identity}"
: "${MODELUPLINK_NOTARY_PROFILE:?Set the notarytool Keychain profile name}"
[[ "$MODELUPLINK_SIGNING_IDENTITY" != '-' ]] || exit 1
version="${MODELUPLINK_VERSION:-$(cat desktop/VERSION)}"
bash macos/build.sh
app="$PWD/dist/macos/Model Uplink.app"
archive="$PWD/dist/macos/ModelUplink-notary.zip"
ditto -c -k --keepParent "$app" "$archive"
xcrun notarytool submit "$archive" --keychain-profile "$MODELUPLINK_NOTARY_PROFILE" --wait
xcrun stapler staple "$app"
xcrun stapler validate "$app"
spctl --assess --type execute --verbose "$app"
staging="$(mktemp -d "${TMPDIR:-/tmp}/modeluplink-dmg.XXXXXX")"
trap 'rm -rf "$staging"' EXIT
ditto "$app" "$staging/Model Uplink.app"
ln -s /Applications "$staging/Applications"
dmg="$PWD/dist/macos/modeluplink_${version}_darwin_arm64.dmg"
hdiutil create -volname 'Model Uplink' -srcfolder "$staging" -ov -format UDZO "$dmg"
codesign --force --timestamp --sign "$MODELUPLINK_SIGNING_IDENTITY" "$dmg"
xcrun notarytool submit "$dmg" --keychain-profile "$MODELUPLINK_NOTARY_PROFILE" --wait
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
shasum -a 256 "$dmg"
echo "Ready for release review: $dmg"
