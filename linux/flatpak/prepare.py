#!/usr/bin/env python3
"""Export the clean client commit and vendor its locked dependencies."""
import datetime
import hashlib
import importlib.util
from pathlib import Path
import shutil
import subprocess

root = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("source", root / "desktop/source.py")
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)
info = source.identity()
output = root / "dist/flatpak/source"
if output.exists():
    shutil.rmtree(output)
output.mkdir(parents=True)
# Read exact Git blobs. Local ignored files and private history cannot enter.
paths = subprocess.check_output(["git", "ls-tree", "-rz", "--name-only", "HEAD"], cwd=root).split(b"\0")
for raw in paths:
    if not raw:
        continue
    path = raw.decode()
    target = output / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(subprocess.check_output(["git", "show", "HEAD:" + path], cwd=root))
(output / "SOURCE_REVISION").write_text(info["revision"] + "\n")
(output / "BUILD_ID").write_text(hashlib.sha256(info["revision"].encode()).hexdigest() + "\n")
(output / "BUILD_DATE").write_text(datetime.datetime.fromtimestamp(info["source_date_epoch"], datetime.timezone.utc).date().isoformat() + "\n")
subprocess.run(["go", "mod", "vendor", "-o", str(output / "vendor")], cwd=root, check=True)
print(output)
print("Source:", info["source_url"])
