#!/usr/bin/env bash
set -euo pipefail
version="$(cat desktop/VERSION)"
build_id="$(cat BUILD_ID)"
revision="$(cat SOURCE_REVISION)"
[[ "$revision" =~ ^[0-9a-f]{40}$ ]]
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$build_id" =~ ^[0-9a-f]{64}$ ]]
export GOCACHE="$PWD/.go-cache"
mkdir -p /app/bin /app/share/applications /app/share/metainfo /app/share/icons/hicolor/512x512/apps
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$revision -X github.com/oscar-investmatic/modeluplink-client/pkg/agent.Version=$version -X github.com/oscar-investmatic/modeluplink-client/internal/flatpak.BuildID=$build_id" -o /app/bin/modeluplink ./cmd/modeluplink
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$revision -X main.Version=$version -X github.com/oscar-investmatic/modeluplink-client/internal/flatpak.BuildID=$build_id" -o /app/bin/modeluplink-app ./cmd/modeluplink-app
install -m755 linux/flatpak/modeluplink-flatpak /app/bin/
install -m644 linux/flatpak/com.modeluplink.app.desktop /app/share/applications/
install -m644 cmd/modeluplink-app/icon.png /app/share/icons/hicolor/512x512/apps/com.modeluplink.app.png
sed -e "s/@VERSION@/$version/g" -e "s/@DATE@/$(cat BUILD_DATE)/g" linux/flatpak/com.modeluplink.app.metainfo.xml > /app/share/metainfo/com.modeluplink.app.metainfo.xml
desktop-file-validate /app/share/applications/com.modeluplink.app.desktop
ldd /app/bin/modeluplink-app > dependencies.txt
if grep -q 'not found' dependencies.txt; then cat dependencies.txt; exit 1; fi
