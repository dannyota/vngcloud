import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class InventoryTest(unittest.TestCase):
    def test_git_inventory_failures_are_fatal(self):
        for pattern in ("*.md", "*.go"):
            with self.subTest(pattern=pattern), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                (root / "scripts").mkdir()
                shutil.copyfile(ROOT / "scripts/check-lengths.sh", root / "scripts/check-lengths.sh")
                binary = root / "bin"
                binary.mkdir()
                git = binary / "git"
                git.write_text('#!/bin/sh\nfor arg do\n  if [ "$arg" = "$FAIL_PATTERN" ]; then exit 19; fi\ndone\n')
                git.chmod(0o700)
                result = subprocess.run(["bash", "scripts/check-lengths.sh"], cwd=root,
                                        env=dict(os.environ, FAIL_PATTERN=pattern,
                                                 PATH=str(binary) + os.pathsep + os.environ["PATH"]),
                                        text=True, capture_output=True, timeout=5)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertNotIn("check-lengths: ok", result.stdout)


if __name__ == "__main__":
    unittest.main()
