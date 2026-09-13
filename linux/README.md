# Model Uplink for Linux

Connect a local model server to Model Uplink using the desktop application.
The client source is Apache-2.0; LICENSE and NOTICE accompany this package.
Source: https://github.com/oscar-investmatic/modeluplink-client

For Debian/Ubuntu install the DEB with `sudo apt install ./modeluplink_VERSION_linux_ARCH.deb`.
The package registers the signed Model Uplink APT repository for updates.
For the portable archive, extract it and run `./modeluplink-app` from its folder;
keep the companion `modeluplink` helper beside it. Both editions require the
native libraries declared by linux/package.sh and a graphical login session.

Use an existing OpenAI-compatible model server, or the managed Ollama setup where
supported. Keep the server and this computer running while sharing. Stop sharing
before uninstalling. Closing the window may leave sharing running; signing out
may stop it. API credentials use the desktop keyring.

Use Settings → View source to inspect the exact packaged client revision.
`./modeluplink version --json` reports the helper revision. Current website
installers predating 0.4.0 are not attributed to this repository retroactively.
See RELEASING.md in the source repository for build and verification details.
