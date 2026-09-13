#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
output="$(mktemp -d "${TMPDIR:-/tmp}/modeluplink-swift-tests.XXXXXX")"
trap 'rm -rf "$output"' EXIT
for suite in HelperStream DesktopContract; do
  xcrun swiftc -module-cache-path "${TMPDIR:-/tmp}/modeluplink-swift-cache" \
    macos/Sources/ModelUplink/Bridge.swift macos/Sources/ModelUplink/Model.swift \
    "macos/Tests/${suite}Tests.swift" -o "$output/$suite"
  "$output/$suite" internal/desktopcontract/fixtures.json
done
