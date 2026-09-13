#!/usr/bin/env bash
# Run in a logged-in test desktop, with an explicit loopback runtime tunnel.
# This checks the packaged helper + sandbox, not public relay/sign-in acceptance.
set -euo pipefail
# SSH may be ready before the desktop. Do not activate portals during greeter startup.
systemctl --user is-active --quiet graphical-session.target || {
  echo "Log into the graphical desktop and wait for it to finish starting first." >&2
  exit 1
}
umask 077
output="${1:?evidence directory required}"
probe="${2:?path to separately built keyring-probe required}"
mkdir -p "$output" "$HOME/.var/app/com.modeluplink.app/data"
flatpak info --show-permissions com.modeluplink.app > "$output/permissions.txt"
flatpak info com.modeluplink.app > "$output/app.txt"
flatpak info org.freedesktop.Platform//25.08 > "$output/runtime.txt"
canary="$(mktemp "$HOME/.modeluplink-flatpak-canary.XXXXXX")"
trap 'rm -f "$canary" "$HOME/.var/app/com.modeluplink.app/data/keyring-probe"' EXIT
printf 'sandbox isolation test\n' > "$canary"
# Expand XDG paths inside the sandbox, not in the host shell.
# shellcheck disable=SC2016
flatpak run --command=sh com.modeluplink.app -c 'test ! -e "$1" && test -w "$XDG_CONFIG_HOME" && test -w "$XDG_DATA_HOME"' sh "$canary"
printf 'Host home file inaccessible; app XDG directories writable.\n' > "$output/isolation.txt"
install -m755 "$probe" "$HOME/.var/app/com.modeluplink.app/data/keyring-probe"
# Expand XDG paths inside the sandbox, not in the host shell.
# shellcheck disable=SC2016
flatpak run --command=sh com.modeluplink.app -c 'exec "$XDG_DATA_HOME/keyring-probe"' > "$output/keyring.json"
flatpak run --command=modeluplink com.modeluplink.app _desktop > "$output/source.json" <<'JSON'
{"action":"test_source","local_url":"http://127.0.0.1:11434/v1","model":"gemma3:1b"}
JSON
python3 - "$output/source.json" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    result = json.load(f)
assert result.get('ready') is True, result
assert result['source']['status'] == 'ready', result
print('Real inference through the packaged helper passed (not public relay E2E).')
PY
