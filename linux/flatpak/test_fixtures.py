"""Exercise fixture refusal paths with disposable directories, including under -O."""

import contextlib
import io
import json
import os
import runpy
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SCRIPT = Path(__file__).with_name("lifecycle-fixture.py")


class LifecycleFixtureTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.root = self.home / ".var/app/com.modeluplink.app/config/lifecycle-lab"
        previous_mask = os.umask(0o077)
        self.addCleanup(os.umask, previous_mask)

    def invoke(self, action):
        with (
            patch.object(Path, "home", return_value=self.home),
            patch.object(sys, "argv", [str(SCRIPT), action]),
            contextlib.redirect_stdout(io.StringIO()),
        ):
            runpy.run_path(str(SCRIPT), run_name="__main__")

    def test_prepare_refuses_existing_unmarked_directory(self):
        self.root.mkdir(parents=True)
        sentinel = self.root / "keep.txt"
        sentinel.write_text("existing data")
        with self.assertRaisesRegex(SystemExit, "non-test profile"):
            self.invoke("prepare")
        self.assertEqual(sentinel.read_text(), "existing data")

    def test_cleanup_refuses_missing_marker(self):
        self.invoke("prepare")
        (self.root / ".test-fixture").unlink()
        with self.assertRaisesRegex(SystemExit, "marker"):
            self.invoke("cleanup")
        self.assertTrue((self.root / "config.json").is_file())

    def test_cleanup_refuses_running_connection(self):
        self.invoke("prepare")
        with self.assertRaisesRegex(SystemExit, "Stop"):
            self.invoke("cleanup")
        self.assertTrue(self.root.is_dir())

    def test_cleanup_accepts_stopped_fixture(self):
        self.invoke("prepare")
        config_path = self.root / "config.json"
        cfg = json.loads(config_path.read_text())
        cfg["endpoints"]["flatpak-lab"]["stopped"] = True
        config_path.write_text(json.dumps(cfg))
        endpoint_path = self.root / "endpoints/flatpak-lab.json"
        endpoint = json.loads(endpoint_path.read_text())
        endpoint["stopped"] = True
        endpoint_path.write_text(json.dumps(endpoint))
        self.invoke("cleanup")
        self.assertFalse(self.root.exists())
