#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
arch="${1:-amd64}"
case "$arch" in amd64|arm64) ;; *) echo 'Usage: linux/build.sh [amd64|arm64]' >&2; exit 1;; esac
revision="$(python3 desktop/source.py --field revision)"
mkdir -p dist/linux
docker buildx build --platform "linux/$arch" --file linux/Dockerfile \
  --build-arg "MODELUPLINK_REVISION=$revision" \
  --build-arg "MODELUPLINK_VERSION=${MODELUPLINK_VERSION:-$(cat desktop/VERSION)}" \
  --output type=local,dest=dist/linux .
