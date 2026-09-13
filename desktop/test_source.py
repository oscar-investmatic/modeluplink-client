import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("source", Path(__file__).with_name("source.py"))
source = importlib.util.module_from_spec(spec)
spec.loader.exec_module(source)


class SourceTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.git("init", "-q")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        (self.root / "desktop").mkdir()
        (self.root / "desktop/VERSION").write_text("1.2.3\n")
        (self.root / "desktop/source.py").write_text(Path(source.__file__).read_text())
        (self.root / ".gitignore").write_text("/dist/\n/.release/\n")
        self.git("add", ".")
        self.git("commit", "-qm", "Fixture")
        self.git("tag", "v1.2.3")

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True).strip()

    def test_clean_tag_identifies_exact_commit(self):
        result = source.identity(self.root, "v1.2.3")
        self.assertEqual(result["revision"], self.git("rev-parse", "HEAD"))
        self.assertTrue(result["source_url"].endswith(result["revision"]))

    def test_refuses_untracked_source(self):
        (self.root / "extra.go").write_text("package extra\n")
        with self.assertRaisesRegex(ValueError, "untracked"):
            source.identity(self.root)

    def test_refuses_modified_source(self):
        (self.root / "desktop/VERSION").write_text("1.2.4\n")
        with self.assertRaisesRegex(ValueError, "Commit"):
            source.identity(self.root)

    def test_refuses_tag_for_different_version(self):
        self.git("tag", "v1.2.4")
        with self.assertRaisesRegex(ValueError, "must agree"):
            source.identity(self.root, "v1.2.4")

    def test_refuses_moved_checkout(self):
        (self.root / "new.go").write_text("package new\n")
        self.git("add", ".")
        self.git("commit", "-qm", "New source")
        with self.assertRaisesRegex(ValueError, "must agree"):
            source.identity(self.root, "v1.2.3")

    def test_refuses_parent_repository_identity(self):
        with self.assertRaisesRegex(ValueError, "own Git repository"):
            source.identity(self.root / "desktop")

    def test_custom_output_keeps_goreleaser_directory_empty(self):
        for _ in range(2):
            subprocess.run(
                [
                    sys.executable,
                    str(self.root / "desktop/source.py"),
                    "--tag",
                    "v1.2.3",
                    "--output",
                    ".release/source.json",
                ],
                cwd=self.root,
                check=True,
                capture_output=True,
            )
        info = json.loads((self.root / ".release/source.json").read_text())
        self.assertEqual(info["revision"], self.git("rev-parse", "HEAD"))
        self.assertFalse((self.root / "dist").exists())
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_default_output_is_preserved_for_desktop_packaging(self):
        subprocess.run(
            [sys.executable, str(self.root / "desktop/source.py"), "--tag", "v1.2.3"],
            cwd=self.root,
            check=True,
            capture_output=True,
        )
        info = json.loads((self.root / "dist/source.json").read_text())
        self.assertEqual(info["revision"], self.git("rev-parse", "HEAD"))
