#!/usr/bin/env bash
set -euo pipefail
# Launch the actual packaged GUI/helper with an empty local profile. This checks
# startup and dynamic linkage only; it does not simulate a successful connection.
package_root="${1:?package root}"
profile="$(mktemp -d)"
trap 'rm -rf "$profile"' EXIT
mkdir -m 700 "$profile/runtime"
export XDG_RUNTIME_DIR="$profile/runtime"
set +e
MODELUPLINK_CONFIG_DIR="$profile" timeout --signal=TERM 8s \
 dbus-run-session -- xvfb-run -a "$package_root/usr/lib/modeluplink/modeluplink-app" > "$profile/startup.log" 2>&1
status=$?
set -e
cat "$profile/startup.log"
[[ "$status" == 124 ]] || { echo "GUI exited before timeout: $status" >&2; exit 1; }
if grep -E 'panic:|fatal error:|error while loading shared libraries' "$profile/startup.log"; then
 exit 1
fi
echo 'Packaged GUI and helper stayed running under Linux X11.'
