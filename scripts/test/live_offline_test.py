import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class OfflineTest(unittest.TestCase):
    def test_runner_excludes_live_tests_and_inherited_credentials(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "go"
            program = '''import os, re, sys
from pathlib import Path
assert not any(key.startswith("VNGCLOUD_") for key in os.environ)
assert not Path(".env").exists()
assert not (Path.home() / ".vngcloud").exists()
assert Path("testdata/storage/project_list_empty.json").is_file()
assert Path("livetest/live_write_storage_project_test.go").is_file()
pattern = sys.argv[sys.argv.index("-test.run") + 1]
for name in ("TestIAMUserHarnessGoogleCleanupPrivacy", "TestBucketEncryptionInventoryGuards", "TestLegacyWriteRequiresOwnGate"):
    assert re.search(pattern, name), name
for name in ("TestLiveWrite", "TestLiveWriteStorageBucket", "TestLive", "TestUnexpectedMutation"):
    assert not re.search(pattern, name), name
print("offline synthetic checks ran")
'''
            binary.write_text(f'''#!{sys.executable}
from pathlib import Path
import sys
path = Path(sys.argv[sys.argv.index("-o") + 1])
path.write_text({(f'#!{sys.executable}' + chr(10) + program)!r})
path.chmod(0o700)
''')
            binary.chmod(0o700)
            result = subprocess.run([sys.executable, "-I", str(ROOT / "scripts/test-live-offline.py")],
                                    cwd=ROOT, env=dict(os.environ, VNGCLOUD_LIVE_WRITE="1",
                                                       VNGCLOUD_ACCESS_TOKEN="synthetic-token",
                                                       PATH=temp + os.pathsep + os.environ["PATH"]),
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("offline synthetic checks ran", result.stdout)


if __name__ == "__main__":
    unittest.main()
