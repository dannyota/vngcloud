#!/usr/bin/env python3
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class ToolsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "scripts").mkdir()
        shutil.copytree(ROOT / ".github", self.root / ".github")
        for name in ("tools-outdated.sh", "check-workflow-pins.py"):
            shutil.copyfile(ROOT / "scripts" / name, self.root / "scripts" / name)
        for name in (".tool-versions", "go.mod"):
            shutil.copyfile(ROOT / name, self.root / name)
        pins = dict(line.split() for line in (ROOT / ".tool-versions").read_text().splitlines())
        self.responses = {
            "https://go.dev/dl/?mode=json": [{"version": "go" + pins["golang"]}],
            "https://proxy.golang.org/golang.org/x/vuln/@latest": {"Version": "v" + pins["govulncheck"]},
        }
        for repo, pin in (("golangci/golangci-lint", "golangci-lint"), ("gitleaks/gitleaks", "gitleaks")):
            self.responses[f"https://api.github.com/repos/{repo}/releases/latest"] = {"tag_name": "v" + pins[pin]}
        for path in (ROOT / ".github/workflows").glob("*.yml"):
            for action, version in re.findall(r"uses: ([^@ ]+)@[0-9a-f]{40} # v([0-9.]+)", path.read_text()):
                repo = "/".join(action.split("/")[:2])
                if repo == "github/codeql-action":
                    self.responses[f"https://api.github.com/repos/{repo}/releases?per_page=100"] = [{"tag_name": "v" + version}]
                else:
                    self.responses[f"https://api.github.com/repos/{repo}/releases/latest"] = {"tag_name": "v" + version}
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.stub("curl", f'''#!{sys.executable}
import json, os, sys
token = "synthetic_tool_token"
assert all(token not in arg for arg in sys.argv), "token in curl argv"
url = sys.argv[-1]
if url.startswith("https://api.github.com/") and os.environ.get("EXPECT_AUTH"):
    config = sys.stdin.read()
    assert 'header = "Authorization: Bearer ' + token + '"' in config
    assert "GH_TOKEN" not in os.environ
print(json.dumps(json.loads(os.environ["RESPONSES"])[url]))
''')
        self.stub("go", '#!/bin/sh\nexit "${GO_STATUS:-0}"\n')

    def stub(self, name, content):
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o700)

    def run_script(self, **overrides):
        env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                   GH_TOKEN="", RESPONSES=json.dumps(self.responses))
        env.update(overrides)
        return subprocess.run(["bash", "-x", "scripts/tools-outdated.sh"],
                              cwd=self.root, env=env, capture_output=True,
                              text=True, timeout=10)

    def test_authenticated_token_uses_private_stdin_without_tracing(self):
        result = self.run_script(GH_TOKEN="synthetic_tool_token", EXPECT_AUTH="1")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("synthetic_tool_token", result.stdout + result.stderr)
        self.assertIn("all pins current", result.stdout)

    def test_failed_module_inventory_cannot_succeed(self):
        result = self.run_script(GO_STATUS="19")
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("all pins current", result.stdout)

    def test_failed_action_inventory_cannot_succeed(self):
        (self.root / "scripts/check-workflow-pins.py").write_text("raise SystemExit(19)\n")
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("all pins current", result.stdout)

    def test_unauthenticated_and_outdated_pins(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.responses["https://api.github.com/repos/gitleaks/gitleaks/releases/latest"] = {"tag_name": "v99.0.0"}
        result = self.run_script()
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("outdated: gitleaks", result.stdout)


if __name__ == "__main__":
    unittest.main()
