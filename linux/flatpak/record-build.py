#!/usr/bin/env python3
"""Record the actual Flatpak inputs and collect files for signed publication."""

import hashlib
import json
import shutil
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parents[2]
source = json.loads((root / "dist/source.json").read_text())
arch = subprocess.check_output(["flatpak", "--default-arch"], text=True).strip()
artifact = root / "dist/flatpak" / f"modeluplink_{source['version']}_{arch}.flatpak"
source["architecture"] = arch
with artifact.open("rb") as handle:
    source["bundle_sha256"] = hashlib.file_digest(handle, "sha256").hexdigest()
source["app_commit"] = subprocess.check_output(
    [
        "ostree",
        "--repo=" + str(root / "dist/flatpak/repo"),
        "rev-parse",
        f"app/com.modeluplink.app/{arch}/master",
    ],
    text=True,
).strip()
source["sdk_inputs"] = {
    name: subprocess.check_output(
        ["flatpak", "info", "--show-commit", f"{name}/{arch}/25.08"], text=True
    ).strip()
    for name in (
        "org.freedesktop.Platform",
        "org.freedesktop.Sdk",
        "org.freedesktop.Sdk.Extension.golang",
    )
}
release = root / "dist/flatpak/release"
release.mkdir(parents=True, exist_ok=False)
shutil.copyfile(artifact, release / artifact.name)
(release / "flatpak-source.json").write_text(json.dumps(source, indent=2) + "\n")
manifest = []
for path in sorted(release.iterdir()):
    with path.open("rb") as handle:
        digest = hashlib.file_digest(handle, "sha256").hexdigest()
    manifest.append(f"{digest}  {path.name}\n")
(release / "flatpak-checksums.txt").write_text("".join(manifest))
