import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class SelectionTest(unittest.TestCase):
    def test_billing_recipe_cannot_select_another_mutation(self):
        makefile = (ROOT / "Makefile").read_text()
        recipe = re.search(r"(?m)^live-write:.*\n\t@?(.*)$", makefile).group(1)
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "go"
            binary.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$ARGV_FILE"\n')
            binary.chmod(0o700)
            arguments = root / "arguments"
            result = subprocess.run(["bash", "-c", recipe], cwd=ROOT,
                                    env=dict(os.environ, ARGV_FILE=str(arguments),
                                             PATH=temp + os.pathsep + os.environ["PATH"]),
                                    capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            args = arguments.read_text().splitlines()
        pattern = args[args.index("-run") + 1]
        names = [name for path in (ROOT / "livetest").glob("*.go")
                 for name in re.findall(r"^func (TestLiveWrite\w*)\(", path.read_text(), re.M)]
        self.assertEqual([name for name in names if re.search(pattern, name)], ["TestLiveWrite"])


if __name__ == "__main__":
    unittest.main()
