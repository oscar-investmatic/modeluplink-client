#!/usr/bin/env python3
"""Exercise tagged CLI packaging in a disposable clone, without publishing."""

import hashlib
import json
import os
import subprocess
import tarfile
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def run(*args, cwd=ROOT, **kwargs):
    return subprocess.run(args, cwd=cwd, check=True, **kwargs)


def main():
    if subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT):
        raise SystemExit("Commit changes before checking release packaging")
    with tempfile.TemporaryDirectory(prefix="modeluplink-release-check-") as temp:
        checkout = Path(temp) / "client"
        run("git", "clone", "--quiet", "--no-hardlinks", str(ROOT), str(checkout))
        revision = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=checkout, text=True
        ).strip()
        version = (checkout / "desktop/VERSION").read_text().strip()
        tag = "v" + version
        # Only this disposable clone receives a local tag for the commit under test.
        run("git", "tag", "--force", tag, revision, cwd=checkout)
        run(
            "git",
            "remote",
            "set-url",
            "origin",
            "https://github.com/oscar-investmatic/modeluplink-client.git",
            cwd=checkout,
        )
        env = {**os.environ, "GORELEASER_CURRENT_TAG": tag}
        for key in ("GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITEA_TOKEN"):
            env.pop(key, None)
        run("goreleaser", "release", "--clean", "--skip=publish", cwd=checkout, env=env)
        dist = checkout / "dist"
        archives = sorted(dist.glob("*.tar.gz"))
        expected = {
            f"modeluplink_{version}_{platform}.tar.gz"
            for platform in ("linux_amd64", "linux_arm64", "darwin_arm64")
        }
        if {archive.name for archive in archives} != expected:
            raise SystemExit("Missing or unexpected CLI release archives")
        for archive in archives:
            with tarfile.open(archive) as bundle:
                info = json.load(bundle.extractfile("source.json"))
                if info["revision"] != revision or info["version"] != version:
                    raise SystemExit(f"Incorrect source identity in {archive.name}")
                for name in ("modeluplink", "README.md", "LICENSE", "NOTICE"):
                    bundle.getmember(name)
        checksums = {}
        for line in (dist / "checksums.txt").read_text().splitlines():
            digest, name = line.split(maxsplit=1)
            checksums[name] = digest
            if hashlib.sha256((dist / name).read_bytes()).hexdigest() != digest:
                raise SystemExit(f"Checksum mismatch: {name}")
        if not expected.issubset(checksums):
            raise SystemExit("Release checksums do not cover all CLI archives")
        if len(list(dist.glob("*.sbom.*"))) != len(archives):
            raise SystemExit("Missing archive SBOMs")
        print("Release packaging passed: three archives, source records, checksums and SBOMs.")


if __name__ == "__main__":
    main()
