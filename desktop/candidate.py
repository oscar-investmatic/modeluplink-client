#!/usr/bin/env python3
"""Record unsigned build candidates, or verify a complete downloaded candidate set.

This is deliberately not the website's publication manifest. Signing and human
acceptance remain separate, required release gates.
"""

import argparse
import hashlib
import json
import re
from pathlib import Path

import source

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("macos-arm64", "linux-amd64", "linux-arm64", "windows-amd64")


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def record(target):
    version = (ROOT / "desktop/VERSION").read_text().strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("Invalid desktop/VERSION")
    commit = source.identity(ROOT)["revision"]
    if target.startswith("linux-"):
        arch = target.removeprefix("linux-")
        names = [
            f"linux/modeluplink_{version}_linux_{arch}.deb",
            f"linux/modeluplink-desktop_{version}_linux_{arch}.tar.gz",
        ]
        signing = "APT repository signing required for distribution"
    elif target == "macos-arm64":
        names = [f"macos/modeluplink_{version}_darwin_arm64.adhoc.zip"]
        signing = "ad-hoc only; Developer ID and notarization required for distribution"
    else:
        names = [f"windows/modeluplink_{version}_windows_amd64.unsigned.exe"]
        signing = "unsigned; Authenticode required for distribution"
    files = [{"name": Path(name).name, "sha256": digest(ROOT / "dist" / name)} for name in names]
    manifest = {
        "format": 1,
        "version": version,
        "commit": commit,
        "target": target,
        "files": files,
        "signing": signing,
        "hardware_acceptance": "pending",
        "publishable": False,
    }
    output = ROOT / "dist/candidates" / f"{target}.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(manifest, indent=2) + "\n")
    print(output)


def verify(directory):
    found = {}
    for path in directory.rglob("*.json"):
        if path.name not in {f"{t}.json" for t in TARGETS}:
            continue
        manifest = json.loads(path.read_text())
        target = manifest["target"]
        if target in found or target not in TARGETS:
            raise ValueError(f"Unexpected or duplicate target: {target}")
        if manifest.get("format") != 1 or manifest.get("publishable") is not False:
            raise ValueError("Not a build candidate manifest")
        for file in manifest["files"]:
            matches = list(directory.rglob(file["name"]))
            if len(matches) != 1 or digest(matches[0]) != file["sha256"]:
                raise ValueError(f"Missing, duplicate or changed artifact: {file['name']}")
        found[target] = manifest
    if set(found) != set(TARGETS):
        raise ValueError(f"Missing targets: {sorted(set(TARGETS) - set(found))}")
    origins = {(m["version"], m["commit"]) for m in found.values()}
    if len(origins) != 1:
        raise ValueError("Candidates were built from different versions or commits")
    version, commit = origins.pop()
    print(
        f"All four candidates match version {version}, commit {commit}, and their SHA-256 hashes. Hardware acceptance and release signing remain required."
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", choices=(*TARGETS, "verify"))
    parser.add_argument("--directory", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    if args.target == "verify":
        verify(args.directory)
    else:
        record(args.target)
