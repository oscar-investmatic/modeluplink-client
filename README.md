# Model Uplink clients

Desktop apps and CLI for connecting a local model server to Model Uplink. Connect
an OpenAI-compatible server, choose the models to share, and provide remote access
through a stable URL. Your model server runs on your computer.

This repository contains the macOS Swift app, Linux and Windows Fyne app, CLI,
local agent, key storage, TLS handling, model integrations, wire protocol, tests,
and packaging scripts. The hosted account service, relay, billing, and website
are maintained separately. Running these clients against Model Uplink requires
an account and is subject to the same service limits as the official app.

## Build and test

Use the Go version in `go.mod` (CI pins 1.27.1):

```sh
go build -mod=readonly -trimpath -o dist/modeluplink ./cmd/modeluplink
go test -race -tags ci ./...
./dist/modeluplink version --json
```

The `ci` tag allows GUI logic tests without opening windows. It does not test
native desktop integration. [CLI instructions](docs/cli-release/README.md).

Native packaging requires a clean commit in this repository, including newly
created files. Commit local changes before packaging your own build.

- macOS arm64: `bash macos/test.sh`, then `bash macos/build.sh` (Xcode/Command Line Tools).
- Linux: `bash linux/build.sh amd64` or `arm64` (Docker with buildx).
- Windows amd64: `./windows/build.ps1 -Unsigned` (Go, MinGW, Inno Setup 6, Python).
- Flatpak: [build and sandbox details](linux/flatpak/README.md).

Local macOS builds use ad-hoc signatures. Windows `-Unsigned` installers have no
publisher signature. Production desktop distribution also requires platform
signing and the acceptance checks described in [RELEASING.md](RELEASING.md).
`macos/package.sh` signs and notarizes a release DMG and requires Developer ID
and notarytool credentials; it is not the local development build command.
See [docs/architecture.md](docs/architecture.md) for the main code and trust boundaries.

## Source and releases

Client changes and contributions belong here. The repository split is in progress:
backend compatibility has been tested against the extracted `pkg/` and `proto/`
packages, but the hosted-service cutover has not been completed. After cutover,
the private service will consume a pinned public revision of these packages. Wire changes must remain compatible with
supported client versions; generated protocol code is committed with its schema.

Packaged clients link to their source commit from Settings. `modeluplink version
--json` reports the helper version, revision, modification state, and source URL.
The configured CLI release workflow produces checksums and CI build attestations;
no release has run from this repository yet. Desktop installer attestations are
still pending. We have not demonstrated byte-for-byte reproducibility across machines.

Version 0.4.0 is the upcoming public-client release. Older website installers and
the earlier Flatpak preview predate this repository and are not claimed to have
been built from its commits. Source links identify source, not an independent audit.

## Contributions and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).
The client source is licensed under [Apache-2.0](LICENSE). Third-party
dependencies and model runtimes retain their own licenses. See [NOTICE](NOTICE).
