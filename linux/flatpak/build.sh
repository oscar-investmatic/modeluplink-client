#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
[[ "$(uname -s)" == Linux ]] || { echo 'Run inside a Linux build VM.' >&2; exit 1; }
test -s dist/flatpak/source/BUILD_ID || { echo 'Run python3 linux/flatpak/prepare.py first, then copy the build context.' >&2; exit 1; }
flatpak-builder --force-clean --repo=dist/flatpak/repo dist/flatpak/build linux/flatpak/com.modeluplink.app.yml
version="$(cat dist/flatpak/source/desktop/VERSION)"
arch="$(flatpak --default-arch)"
artifact="dist/flatpak/modeluplink_${version}_${arch}.flatpak"
flatpak build-bundle --runtime-repo=https://dl.flathub.org/repo/flathub.flatpakrepo dist/flatpak/repo "$artifact" com.modeluplink.app
sha256sum "$artifact" > "$artifact.sha256"
echo "$artifact"
