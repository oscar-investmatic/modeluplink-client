#!/usr/bin/env python3
"""Disposable local lifecycle fixture, not an account or public-relay E2E test.

Run only in a test VM. All control/relay connections target closed loopback port 9.
Launch the app/helper with MODELUPLINK_CONFIG_DIR set to the printed directory.
"""

import json
import os
import shutil
import sys
from pathlib import Path

root = Path.home() / ".var/app/com.modeluplink.app/config/lifecycle-lab"
config = root / "config.json"
if len(sys.argv) != 2:
    raise SystemExit("Usage: lifecycle-fixture.py prepare|check-stopped|cleanup")
action = sys.argv[1]
if action == "prepare" and root.exists() and not (root / ".test-fixture").exists():
    raise SystemExit("Refusing to replace a non-test profile")
if action == "prepare":
    os.umask(0o077)
    (root / "endpoints").mkdir(parents=True, exist_ok=True)
    endpoint = {
        "id": "ep_flatpak_lab",
        "slug": "flatpak-lab",
        "engine": "openai_compatible",
        "runtime_ownership": "external",
        "control_url": "http://127.0.0.1:9",
        "relay_url": "ws://127.0.0.1:9/v1/relay/connect",
        "url": "https://flatpak-lab.invalid",
        "agent_token": "local-test-placeholder",
        "upstream_url": "http://127.0.0.1:11434/v1",
        "shared_models": ["gemma3:1b"],
        "stopped": False,
    }
    config.write_text(
        json.dumps(
            {
                "flatpak_lab_fixture": True,
                "startup_disabled": True,
                "account_token": "local-test-placeholder",
                "control_url": "http://127.0.0.1:9",
                "endpoints": {"flatpak-lab": endpoint},
            }
        )
    )
    (root / "endpoints/flatpak-lab.json").write_text(json.dumps(endpoint))
    (root / ".test-fixture").touch(mode=0o600)
    print(root)
elif action in ("check-stopped", "cleanup"):
    # The helper rewrites known config fields, so keep the marker separately.
    if not (root / ".test-fixture").exists():
        raise SystemExit("Fixture marker is required")
    cfg = json.loads(config.read_text())
    endpoint = json.loads((root / "endpoints/flatpak-lab.json").read_text())
    if not (
        cfg.get("startup_disabled")
        and cfg["endpoints"]["flatpak-lab"].get("stopped")
        and endpoint.get("stopped")
    ):
        raise SystemExit("Disable autostart and Stop before cleaning up the profile")
    if action == "cleanup":
        shutil.rmtree(root)
    print(json.dumps({"check": "local lifecycle fixture stopped state", "passed": True}))
else:
    raise SystemExit("Use prepare, check-stopped, or cleanup")
