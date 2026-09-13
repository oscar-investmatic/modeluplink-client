import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest

from candidate import TARGETS, digest, verify


class CandidateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for target in TARGETS:
            artifact = self.root / (target + '.bin')
            artifact.write_bytes(b'candidate fixture')
            (self.root / (target + '.json')).write_text(json.dumps({
                'format': 1, 'publishable': False, 'target': target,
                'version': '0.3.0', 'commit': 'a' * 40,
                'files': [{'name': artifact.name, 'sha256': digest(artifact)}]}))

    def test_complete_set(self):
        with contextlib.redirect_stdout(io.StringIO()):
            verify(self.root)

    def test_mixed_commit_rejected(self):
        path = self.root / 'windows-amd64.json'
        value = json.loads(path.read_text())
        value['commit'] = 'b' * 40
        path.write_text(json.dumps(value))
        with self.assertRaisesRegex(ValueError, 'different versions or commits'):
            verify(self.root)

    def test_missing_platform_rejected(self):
        (self.root / 'linux-arm64.json').unlink()
        with self.assertRaisesRegex(ValueError, 'Missing targets'):
            verify(self.root)

    def test_modified_binary_rejected(self):
        (self.root / 'macos-arm64.bin').write_bytes(b'changed')
        with self.assertRaisesRegex(ValueError, 'changed artifact'):
            verify(self.root)
