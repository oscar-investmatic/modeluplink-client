#!/usr/bin/env python3
"""Verify a clean canonical checkout before packaging; write its source identity."""

import argparse
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = "https://github.com/oscar-investmatic/modeluplink-client"


def git(root, *args):
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def identity(root=ROOT, tag=None):
    if Path(git(root, "rev-parse", "--show-toplevel")).resolve() != root.resolve():
        raise ValueError("Client must have its own Git repository")
    if git(root, "status", "--porcelain", "--untracked-files=all"):
        raise ValueError("Commit source changes and untracked files before packaging")
    revision = git(root, "rev-parse", "HEAD")
    if not re.fullmatch(r"[a-f0-9]{40}", revision):
        raise ValueError("Invalid source revision")
    version = (root / "desktop/VERSION").read_text().strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("Invalid desktop version")
    if tag and (tag != "v" + version or git(root, "rev-parse", tag + "^{commit}") != revision):
        raise ValueError("Release tag, checked-out commit and desktop version must agree")
    return {
        "format": 1,
        "version": version,
        "revision": revision,
        "repository": REPOSITORY,
        "source_url": REPOSITORY + "/tree/" + revision,
        "source_date_epoch": int(git(root, "show", "-s", "--format=%ct", "HEAD")),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag")
    parser.add_argument("--field", choices=("revision", "source_url", "source_date_epoch"))
    parser.add_argument("--output", type=Path, default=Path("dist/source.json"))
    args = parser.parse_args()
    info = identity(tag=args.tag)
    output = ROOT / args.output
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(info, indent=2) + "\n")
    print(info[args.field] if args.field else json.dumps(info))


if __name__ == "__main__":
    main()
