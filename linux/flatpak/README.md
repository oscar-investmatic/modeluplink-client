# Flatpak

This edition connects a separately installed OpenAI-compatible model server. It
does not install a model runtime or manage host services. It is not on Flathub yet.

Commit source changes, then prepare on a machine with the Go version in go.mod:

```sh
python3 linux/flatpak/prepare.py
```

Preparation refuses modified/untracked source and exports the exact client Git
commit. Both binaries embed its revision and use the same session build ID. Copy
`linux/flatpak` and `dist/flatpak/source` to a Linux build machine at the same
relative paths. Install Flatpak, flatpak-builder, and the Flathub remote, then:

```sh
flatpak install flathub org.freedesktop.Platform//25.08 \
  org.freedesktop.Sdk//25.08 org.freedesktop.Sdk.Extension.golang//25.08
bash linux/flatpak/build.sh
flatpak install --user ./dist/flatpak/modeluplink_0.4.0_x86_64.flatpak
flatpak run com.modeluplink.app
```

The build uses vendored Go dependencies without build network access. Record the
SDK/runtime OSTree commits with acceptance evidence: these branches can change,
so the pipeline does not currently promise reproducible bytes.

The app requests network, IPC, X11/XWayland, rendering devices, and Secret Service
access. It does not request host/home filesystem access or host process spawning.
Keys use the desktop keyring with no plaintext fallback. Profiles stay inside
`~/.var/app/com.modeluplink.app`; native profiles are not imported automatically.

Starting sharing requests Background portal permission. Closing the window keeps
active agents in the sandbox session. Stop waits for the agent to exit and leaves
an external model server running. Signing out may stop sharing. Login autostart is
off for new profiles; explicit Stop remains authoritative after automatic launch.

Installing a new bundle does not replace an already-running process. Opening the
updated package shows a restart window. Close the existing app window, then choose
Restart to update; active connections briefly disconnect and resume, while stopped
connections stay stopped. The old session releases its agents before the new one
starts. Choosing Later leaves the running version in place. Early preview builds
without this handover protocol show manual recovery instructions instead.

Test in a fully initialized graphical session. Unit tests do not establish portal,
keyring, login, or remote inference behavior. The earlier private preview passed
several distro checks, but its evidence does not certify this extracted revision.
See [native acceptance requirements](../../RELEASING.md).
