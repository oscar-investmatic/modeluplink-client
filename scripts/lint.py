#!/usr/bin/env python3
"""Run the publication lint checks without modifying source files."""

import argparse
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GOIMPORTS = "golang.org/x/tools/cmd/goimports@v0.44.1-0.20260420230617-19499e7caabc"
STATICCHECK = "honnef.co/go/tools/cmd/staticcheck@v0.8.1"
ACTIONLINT = "github.com/rhysd/actionlint/cmd/actionlint@v1.7.7"


def run(*args):
    subprocess.run(args, cwd=ROOT, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--swift-only", action="store_true")
    args = parser.parse_args()
    if args.swift_only:
        run("xcrun", "swift-format", "lint", "--strict", "--recursive", "macos")
        return

    paths = (
        subprocess.check_output(
            ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
            cwd=ROOT,
        )
        .decode()
        .split("\0")
    )
    go_files = sorted({p for p in paths if p.endswith(".go") and not p.endswith(".pb.go")})
    unformatted = subprocess.check_output(
        ["go", "run", GOIMPORTS, "-l", *go_files], cwd=ROOT, text=True
    )
    if unformatted:
        raise SystemExit("Run goimports on these files:\n" + unformatted)
    run("go", "run", STATICCHECK, "-tags", "ci", "./...")
    run(sys.executable, "-m", "ruff", "check", "desktop", "linux/flatpak", "scripts")
    run(sys.executable, "-m", "ruff", "format", "--check", "desktop", "linux/flatpak", "scripts")
    shell_files = sorted(p for p in paths if p.endswith(".sh"))
    run("shellcheck", *shell_files, "linux/flatpak/modeluplink-flatpak")
    run(
        "go",
        "run",
        ACTIONLINT,
        "-pyflakes=",
        *sorted(str(p.relative_to(ROOT)) for p in (ROOT / ".github/workflows").glob("*.yml")),
    )
    run("git", "diff", "--check")
    print("Client lint checks passed.")


if __name__ == "__main__":
    main()
