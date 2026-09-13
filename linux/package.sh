#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
revision="${MODELUPLINK_REVISION:-$(python3 desktop/source.py --field revision)}"
[[ "$revision" =~ ^[0-9a-f]{40}$ ]] || { echo "Invalid source revision" >&2; exit 1; }
version="${MODELUPLINK_VERSION:-$(cat desktop/VERSION)}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Use a numeric version' >&2; exit 1; }
[[ "$(go env GOOS)" == linux ]] || { echo 'Run on Linux, or use linux/build.sh from macOS.' >&2; exit 1; }
arch="$(go env GOARCH)"
case "$arch" in amd64|arm64) ;; *) echo 'Only amd64 and arm64 are supported' >&2; exit 1;; esac
output="$PWD/dist/linux"
mkdir -p "$output"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
root="$stage/package"
app="$root/usr/lib/modeluplink"
mkdir -p "$app" "$root/usr/bin" "$root/usr/share/applications" "$root/usr/share/metainfo" "$root/usr/share/keyrings" "$root/etc/apt/sources.list.d" "$root/DEBIAN"
mkdir -p "$root/usr/share/doc/modeluplink"
cp LICENSE "$root/usr/share/doc/modeluplink/copyright"
cp NOTICE "$root/usr/share/doc/modeluplink/NOTICE"
for px in 64 128 256 512; do mkdir -p "$root/usr/share/icons/hicolor/${px}x${px}/apps"; done
command -v convert >/dev/null || { echo 'ImageMagick (convert) is required for icon sizes' >&2; exit 1; }
release_date="${SOURCE_DATE_EPOCH:+$(date -u -d "@$SOURCE_DATE_EPOCH" +%F)}"
release_date="${release_date:-$(date -u +%F)}"
CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "-s -w -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$revision -X github.com/oscar-investmatic/modeluplink-client/pkg/agent.Version=$version" -o "$app/modeluplink" ./cmd/modeluplink
CGO_ENABLED=1 go build -mod=readonly -trimpath -ldflags "-s -w -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$revision -X main.Version=$version" -o "$app/modeluplink-app" ./cmd/modeluplink-app
ln -s ../lib/modeluplink/modeluplink-app "$root/usr/bin/modeluplink-app"
# The source icon is 512 px; App Center and desktop shells pick the hicolor size they need.
for px in 64 128 256 512; do
  convert cmd/modeluplink-app/icon.png -resize "${px}x${px}" "$root/usr/share/icons/hicolor/${px}x${px}/apps/com.modeluplink.app.png"
done
# AppStream metadata drives the App Center listing: display name, icon, developer, license, links, release date.
cat > "$root/usr/share/metainfo/com.modeluplink.app.metainfo.xml" <<METAINFO
<?xml version="1.0" encoding="UTF-8"?>
<component type="desktop-application">
  <id>com.modeluplink.app</id>
  <name>Model Uplink</name>
  <summary>Your local AI model, reachable from anywhere</summary>
  <developer_name>Model Uplink</developer_name>
  <metadata_license>CC0-1.0</metadata_license>
  <project_license>Apache-2.0</project_license>
  <description>
    <p>Model Uplink gives the Ollama model running on this computer a permanent, secure HTTPS address that works in any app that speaks the OpenAI API. Sign in with an emailed code, pick a model, and copy your address and key. Traffic is encrypted end to end; the relay never sees a prompt.</p>
    <p>The connection keeps running as a background service after you close the window. Keep the computer awake and online.</p>
  </description>
  <launchable type="desktop-id">com.modeluplink.app.desktop</launchable>
  <provides><binary>modeluplink-app</binary><binary>modeluplink</binary></provides>
  <url type="homepage">https://modeluplink.com/</url>
  <url type="help">https://modeluplink.com/quickstart/</url>
  <update_contact>contact@modeluplink.com</update_contact>
  <categories><category>Network</category><category>Utility</category></categories>
  <keywords><keyword>ollama</keyword><keyword>llm</keyword><keyword>openai</keyword><keyword>api</keyword><keyword>local ai</keyword></keywords>
  <content_rating type="oars-1.1"/>
  <branding><color type="primary" scheme_preference="light">#a8ff4f</color><color type="primary" scheme_preference="dark">#090e0b</color></branding>
  <releases>
    <release version="$version" date="$release_date"/>
  </releases>
</component>
METAINFO
cat > "$root/usr/share/applications/com.modeluplink.app.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=Model Uplink
Comment=Connect your models with a secure URL
Exec=/usr/bin/modeluplink-app
Icon=com.modeluplink.app
Terminal=false
Categories=Network;Utility;
StartupWMClass=Model Uplink
DESKTOP
# Updates arrive through the signed apt repository: the package registers the
# source and the archive key so Software Updater and `apt upgrade` see new versions.
cp linux/modeluplink-archive-keyring.gpg "$root/usr/share/keyrings/modeluplink-archive-keyring.gpg"
cat > "$root/etc/apt/sources.list.d/modeluplink.sources" <<'SOURCES'
Types: deb
URIs: https://modeluplink.com/apt
Suites: stable
Components: main
Architectures: amd64 arm64
Signed-By: /usr/share/keyrings/modeluplink-archive-keyring.gpg
SOURCES
printf '/etc/apt/sources.list.d/modeluplink.sources\n' > "$root/DEBIAN/conffiles"
cat > "$root/DEBIAN/control" <<CONTROL
Package: modeluplink
Version: $version
Architecture: $arch
Maintainer: Model Uplink <contact@modeluplink.com>
Section: net
Priority: optional
Homepage: https://modeluplink.com/
Installed-Size: $(du -sk "$root/usr" | cut -f1)
Depends: libc6 (>= 2.36), libgl1, libx11-6, libxcursor1, libxrandr2, libxinerama1, libxi6, libxxf86vm1, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0, xdg-utils
Recommends: gnome-keyring | kwallet6, libpam-systemd
Description: Share local models through an authenticated HTTPS endpoint
 Native desktop interface with email-code sign-in, model selection and
 background sharing. Requires a desktop session with systemd user services.
CONTROL
chmod -R go-w "$root"
dpkg-deb --root-owner-group --build "$root" "$output/modeluplink_${version}_linux_${arch}.deb"
portable="$stage/ModelUplink"
mkdir -p "$portable"
cp "$app/modeluplink" "$app/modeluplink-app" "$portable/"
cp linux/README.md "$portable/README.md"
cp LICENSE NOTICE "$portable/"
tar -czf "$output/modeluplink-desktop_${version}_linux_${arch}.tar.gz" -C "$stage" ModelUplink
(cd "$output"; sha256sum "modeluplink_${version}_linux_${arch}.deb" "modeluplink-desktop_${version}_linux_${arch}.tar.gz" > "checksums_${arch}.txt")
echo "Built Linux $arch packages in $output"
