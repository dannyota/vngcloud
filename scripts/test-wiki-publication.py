#!/usr/bin/env python3

import base64
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
SHA = "0123456789abcdef" * 2 + "01234567"
OTHER = "abcdef0123456789" * 2 + "abcdef01"
RUN_URL = "https://github.com/example/project/actions/runs/123/attempts/2"
GIT_STUB = r'''import base64
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
token = os.environ["STUB_TOKEN"]
encoded = base64.b64encode(("x-access-token:" + token).encode()).decode()
trace = Path(os.environ["STUB_TRACE"])
def record(kind, **fields):
    with trace.open("a") as stream:
        stream.write(json.dumps(dict(kind=kind, **fields)) + "\n")
if any(token in arg or encoded in arg for arg in args):
    record("credential-in-argument")
while args and args[0] in ("-C", "-c"):
    args = args[2:]
command = args[0]
if command == "rev-parse":
    record(command)
    print(os.environ["STUB_HEAD"][:7] if "--short" in args else os.environ["STUB_HEAD"])
elif command == "ls-remote":
    if args != ["ls-remote", "origin", "refs/heads/master"]:
        sys.exit("unexpected master query")
    count = sum(json.loads(line)["kind"] == command for line in trace.read_text().splitlines()) if trace.exists() else 0
    record(command)
    masters = json.loads(os.environ["STUB_MASTERS"])
    master = masters[min(count, len(masters) - 1)]
    if master == "error":
        sys.exit(1)
    if master:
        print(master + "\trefs/heads/master")
elif command == "clone":
    record(command)
    if os.environ.get("STUB_FAILURE") == command:
        sys.exit(token + " " + encoded)
    (Path(args[-1]) / ".git").mkdir(parents=True)
elif command == "add":
    record(command)
elif command == "diff":
    if "--cached" in args:
        sys.exit(0 if os.environ.get("STUB_UNCHANGED") else 1)
    print("1 file changed")
elif command == "commit":
    if os.environ.get("STUB_UNCHANGED") and "--allow-empty" not in args:
        sys.exit(1)
    record(command, message=args[args.index("-m") + 1])
elif command == "push":
    record(command)
    if os.environ.get("STUB_FAILURE") == command:
        sys.exit(token + " " + encoded)
else:
    sys.exit("unexpected git command")
'''


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        scripts = self.root / "scripts"
        scripts.mkdir()
        shutil.copyfile(ROOT / "scripts/wiki-sync.sh", scripts / "wiki-sync.sh")
        pages = self.root / "docs/wiki"
        pages.mkdir(parents=True)
        (pages / "Home.md").write_text("[Guide](Guide.md)\n")
        (pages / "Guide.md").write_text("[Home](Home.md#start)\n")
        binaries = self.root / "bin"
        binaries.mkdir()
        git = binaries / "git"
        git.write_text(f"#!{sys.executable}\n" + GIT_STUB)
        git.chmod(0o700)
        self.trace = self.root / "trace"
        self.token = "synthetic-publication-credential"
        self.encoded = base64.b64encode(("x-access-token:" + self.token).encode()).decode()
        self.env = dict(os.environ, PATH=str(binaries) + ":" + os.environ["PATH"],
                        GH_TOKEN=self.token, GITHUB_REPOSITORY="example/project",
                        WIKI_SOURCE_SHA=SHA, WIKI_SOURCE_RUN_URL=RUN_URL,
                        STUB_HEAD=SHA, STUB_MASTERS=json.dumps([SHA, SHA]),
                        STUB_TRACE=str(self.trace), STUB_TOKEN=self.token,
                        TMPDIR=str(self.root))
        for key in ("STUB_FAILURE", "STUB_UNCHANGED", "BASH_ENV", "ENV"):
            self.env.pop(key, None)

    def run_sync(self, *args):
        result = subprocess.run(["bash", str(self.root / "scripts/wiki-sync.sh"), *args],
                                env=self.env, capture_output=True, text=True, timeout=10)
        output = result.stdout + result.stderr
        self.assertNotIn(self.token, output)
        self.assertNotIn(self.encoded, output)
        for path in self.root.rglob("*"):
            if path.is_file():
                content = path.read_text()
                self.assertNotIn(self.token, content, str(path))
                self.assertNotIn(self.encoded, content, str(path))
        return result

    def events(self, kind):
        records = [json.loads(line) for line in self.trace.read_text().splitlines()] if self.trace.exists() else []
        self.assertFalse(any(record["kind"] == "credential-in-argument" for record in records))
        return [record for record in records if record["kind"] == kind]

    def assert_rejected(self, clones=0):
        result = self.run_sync()
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.events("clone")), clones)
        self.assertEqual(self.events("push"), [])

    def test_missing_source_sha_stops_before_clone(self):
        self.env.pop("WIKI_SOURCE_SHA")
        self.assert_rejected()

    def test_malformed_source_sha_stops_before_clone(self):
        for value in ("", SHA[:39], SHA + "0", SHA.upper(), "g" * 40, SHA + "\n"):
            with self.subTest(sha=value):
                self.env["WIKI_SOURCE_SHA"] = value
                self.assert_rejected()

    def test_mismatched_checkout_stops_before_clone(self):
        self.env["STUB_HEAD"] = OTHER
        self.assert_rejected()

    def test_stale_master_stops_before_clone(self):
        self.env["STUB_MASTERS"] = json.dumps([OTHER])
        self.assert_rejected()

    def test_unavailable_master_stops_before_clone(self):
        for value in ("", "error"):
            with self.subTest(master=value):
                self.env["STUB_MASTERS"] = json.dumps([value])
                self.assert_rejected()

    def test_master_moving_stops_before_push(self):
        self.env["STUB_MASTERS"] = json.dumps([SHA, OTHER])
        self.assert_rejected(clones=1)

    def test_current_master_pushes_once_with_source_and_run(self):
        result = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.events("push")), 1)
        self.assertEqual(len(self.events("ls-remote")), 2)
        messages = self.events("commit")
        self.assertEqual(len(messages), 1)
        self.assertIn(SHA, messages[0]["message"])
        self.assertIn(RUN_URL, messages[0]["message"])

    def test_run_url_is_optional(self):
        self.env.pop("WIKI_SOURCE_RUN_URL")
        result = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.events("push")), 1)
        self.assertIn(SHA, self.events("commit")[0]["message"])

    def test_authenticated_failures_keep_credentials_private(self):
        for command in ("clone", "push"):
            with self.subTest(command=command):
                if self.trace.exists():
                    self.trace.unlink()
                self.env["STUB_FAILURE"] = command
                result = self.run_sync()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(len(self.events("push")), int(command == "push"))

    def test_unchanged_pages_record_current_source(self):
        self.env["STUB_UNCHANGED"] = "1"
        result = self.run_sync()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.events("commit")), 1)
        self.assertIn(SHA, self.events("commit")[0]["message"])
        self.assertEqual(len(self.events("push")), 1)

    def test_preview_needs_no_identity_or_credentials(self):
        for key in ("GH_TOKEN", "WIKI_SOURCE_SHA", "WIKI_SOURCE_RUN_URL"):
            self.env.pop(key, None)
        result = self.run_sync("--dry-run")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        render = Path(result.stdout.strip().split(" into ")[-1])
        self.assertEqual((render / "Home.md").read_text(), "[Guide](Guide)\n")
        self.assertEqual((render / "Guide.md").read_text(), "[Home](Home#start)\n")
        self.assertFalse(self.trace.exists())


if __name__ == "__main__":
    unittest.main()
