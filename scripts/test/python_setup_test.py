import importlib.util
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("workflows", ROOT / "scripts/test-ci-workflows.py")
workflows = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(workflows)


class PythonSetupTest(unittest.TestCase):
    def test_missing_dependency_cannot_fall_back_to_another_interpreter(self):
        def run(command, **kwargs):
            return subprocess.CompletedProcess(command, 1 if command[0] == sys.executable else 0)

        with mock.patch.object(workflows.subprocess, "run", side_effect=run):
            with self.assertRaises(RuntimeError) as caught:
                workflows.parser_python()
        self.assertEqual(str(caught.exception),
                         "Workflow checks require Python 3.11+ with tomllib and PyYAML")


if __name__ == "__main__":
    unittest.main()
