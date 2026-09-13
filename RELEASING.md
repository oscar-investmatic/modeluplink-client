# Releasing clients

All future client releases originate in this repository. A private security
branch is temporary; publish its fix with the corresponding release. Do not
manually export a second client implementation from the hosted service repository.

1. Update `desktop/VERSION` and GUI packaging metadata. Commit all source and
   dependency changes. Run the client CI and native acceptance checks.
2. Tag that commit `v<desktop/VERSION>`. The release workflow rejects a mismatched
   version/tag or dirty checkout, including untracked source files.
3. CI builds CLI archives as a draft release, records source identity and hashes,
   signs the checksum manifest, and attests the archives. The draft is published
   as a prerelease only after these operations pass. Promote after acceptance.
4. Native installers need their own platform builds, signatures, acceptance
   evidence, and artifact attestations from the same source commit. The tag
   workflow publishes CLI archives only. The separate Flatpak candidate workflow
   builds and attests its own bundle, as described below. Neither workflow attests
   a DMG, DEB, or EXE.
5. The website should reference the resulting release artifacts and source commit.
   Promotion must verify downloaded hashes and signatures before changing links.
   Never rebuild an already accepted artifact during website publication.

`desktop/source.py` verifies the checkout before packaging and records identity
in `dist/source.json`. Packaging embeds its revision in the helper and GUI.
Flatpak preparation exports exact Git blobs and vendors the committed dependency
lockfile; the SDK/runtime versions are recorded separately by the build environment.
They are not yet pinned to immutable OSTree commits, so this is not a reproducible
build claim. Native code signing and timestamps also need a defined comparison
procedure before making such a claim.

## Verify a CLI release

Download the archive, `checksums.txt`, and `checksums.txt.sigstore.json` from the
same public release. In the download directory, verify the archive's checksum and
then the signature (replace VERSION with the selected version):

```sh
sha256sum --ignore-missing --check checksums.txt
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/oscar-investmatic/modeluplink-client/.github/workflows/release.yml@refs/tags/vVERSION' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify modeluplink_VERSION_linux_amd64.tar.gz \
  --repo oscar-investmatic/modeluplink-client \
  --signer-workflow oscar-investmatic/modeluplink-client/.github/workflows/release.yml
```

Inspect the attestation's source revision and compare it with the release tag and
`modeluplink version --json`. A checksum on its own proves integrity only relative
to that checksum file. Neither signatures nor source publication replace review.

## Native acceptance

Exercise real sign-in and authenticated remote streaming inference; keyring lock
and recovery; model selection; connection limits; permission denial and recovery;
Start/Stop; window close/reopen; login/logout/reboot; network loss; update from the
previous version; and uninstall. Run Linux checks in graphical VM sessions on
Ubuntu GNOME, Fedora KDE, and openSUSE KDE. Sharing need not survive logout.
Keep lab addresses and credentials outside this repository.

## Publish an accepted Flatpak

Run **Signed Flatpak candidate** (`flatpak.yml`) on the committed `main` revision
intended for release. It builds from clean public source, records SDK/runtime
commits, signs `flatpak-checksums.txt` with GitHub OIDC, attests the actual bundle,
and uploads a workflow artifact. It does not publish a release or change the site.

Download that run's `flatpak-x86_64-<commit>` artifact. Check the run's source
revision against `flatpak-source.json`, and verify:

```sh
sha256sum --check flatpak-checksums.txt
cosign verify-blob \
  --bundle flatpak-checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/oscar-investmatic/modeluplink-client/.github/workflows/flatpak.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  flatpak-checksums.txt
gh attestation verify modeluplink_VERSION_x86_64.flatpak \
  --repo oscar-investmatic/modeluplink-client \
  --signer-workflow oscar-investmatic/modeluplink-client/.github/workflows/flatpak.yml
```

Run native acceptance on these exact downloaded bytes. Match the installed
`modeluplink version --json` revision and the app OSTree commit to the recorded
identity. Test both the graphical update prompt and the completed restart while
sharing; a stopped connection must remain stopped. Legacy previews without the
handover method must show usable manual recovery instructions.

After acceptance and client CI pass for that revision, tag the same commit and
attach the accepted bundle, `flatpak-source.json`, `flatpak-checksums.txt`, and
its Sigstore bundle to that release. Keep the acceptance report with its source
commit and bundle hash. Verify the published downloads again before promoting
the release or changing the website. Do not rebuild during promotion. The bundle
is distributed directly; this process does not constitute a Flathub listing.
