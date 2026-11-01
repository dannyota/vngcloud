"""Credential loading tests use only temporary fake values."""

import contextlib
import importlib.util
import io
import os
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True

SPEC = importlib.util.spec_from_file_location(
    "browser_creds", Path(__file__).resolve().parents[1] / "browser-creds.py"
)
creds = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(creds)


class LoadingTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "credentials"

    def write(self, text, mode=0o600):
        self.path.write_text(text, encoding="utf-8")
        self.path.chmod(mode)

    def test_profile_mapping_and_literal_percent(self):
        self.write("[fake]\nroot_email = fake@example.invalid\n"
                   "username = fake-user\npassword = 100%fake\n"
                   "totp_secret = FAKE\n")
        self.assertEqual(creds.load_profile(self.path, "fake"), {
            "VNGCLOUD_ROOT_EMAIL": "fake@example.invalid",
            "VNGCLOUD_USERNAME": "fake-user",
            "VNGCLOUD_PASSWORD": "100%fake",
            "VNGCLOUD_TOTP_SECRET": "FAKE",
        })

    def test_missing_section_and_keys(self):
        self.write("[fake]\nusername = fake-user\n")
        result = creds.load_profile(self.path, "fake")
        self.assertEqual(result["VNGCLOUD_PASSWORD"], "")
        self.assertEqual(result["VNGCLOUD_USERNAME"], "fake-user")
        self.assertTrue(all(v == "" for v in
                            creds.load_profile(self.path, "absent").values()))

    def test_permission_refusal(self):
        for mode in (0o640, 0o604):
            with self.subTest(mode=mode):
                self.write("[fake]\npassword = fake-secret\n", mode)
                with self.assertRaisesRegex(ValueError, "private credentials"):
                    creds.load_profile(self.path, "fake")

    def test_missing_file(self):
        with self.assertRaises(ValueError) as caught:
            creds.load_profile(self.path, "fake")
        self.assertEqual(str(caught.exception),
                         f"cannot read private credentials file: {self.path}")

    def test_parse_error_does_not_leak(self):
        self.write("fake-secret without section\n")
        error = io.StringIO()
        with contextlib.redirect_stderr(error):
            result = creds.main(["--profile", "fake",
                                 "--credentials", str(self.path)])
        self.assertEqual(result, 2)
        self.assertEqual(error.getvalue(),
                         f"cannot parse credentials file: {self.path}\n")
        self.assertNotIn("fake-secret", error.getvalue())

    def test_default_section_is_isolated(self):
        self.write("[DEFAULT]\nroot_email = fake@example.invalid\n"
                   "username = fake-user\npassword = fake-password\n"
                   "totp_secret = FAKE\n[limited]\n")
        self.assertTrue(all(v == "" for v in
                            creds.load_profile(self.path, "limited").values()))
        self.assertEqual(creds.load_profile(self.path, "DEFAULT")[
            "VNGCLOUD_USERNAME"], "fake-user")

    def test_empty_sections_are_isolated(self):
        expected = {
            "VNGCLOUD_ROOT_EMAIL": "fake@example.invalid",
            "VNGCLOUD_USERNAME": "fake-user",
            "VNGCLOUD_PASSWORD": "fake-password",
            "VNGCLOUD_TOTP_SECRET": "FAKE",
        }
        for header in ("[]", "[ ]"):
            with self.subTest(header=header):
                self.write(header + "\nroot_email = fake@example.invalid\n"
                           "username = fake-user\npassword = fake-password\n"
                           "totp_secret = FAKE\n[limited]\n")
                self.assertEqual(creds.load_profile(self.path, "limited"),
                                 dict.fromkeys(expected, ""))
                self.assertEqual(creds.load_profile(self.path, ""), expected)

    def test_bom_and_cli_grammar(self):
        self.write("\ufeff# comment\n; comment\n[ fake ]\n"
                   " PASSWORD = first\npassword = last=100% ; # literal\n"
                   "[fake]\n  username = fake-user\n")
        result = creds.load_profile(self.path, "fake")
        self.assertEqual(result["VNGCLOUD_PASSWORD"], "last=100% ; # literal")
        self.assertEqual(result["VNGCLOUD_USERNAME"], "fake-user")

    def test_cli_grammar_rejects_invalid_lines(self):
        for text in ("[fake] trailing\n", "[fake]\npassword: fake-secret\n",
                     "[fake]\npassword = ok\n continuation\n",
                     "[fake]\n = fake-secret\n"):
            with self.subTest(text=text):
                self.write(text)
                with self.assertRaisesRegex(ValueError, "cannot parse"):
                    creds.load_profile(self.path, "fake")

    def test_fifo_refused_promptly(self):
        os.mkfifo(self.path, 0o600)
        code = ("import importlib.util,sys; "
                "s=importlib.util.spec_from_file_location('creds',sys.argv[1]); "
                "m=importlib.util.module_from_spec(s); s.loader.exec_module(m); "
                "sys.exit(m.main(['--profile','fake','--credentials',sys.argv[2]]))")
        result = subprocess.run(
            [sys.executable, "-I", "-B", "-c", code,
             str(Path(creds.__file__)), str(self.path)],
            capture_output=True, text=True, timeout=2)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertEqual(result.stderr,
                         f"cannot read private credentials file: {self.path}\n")

    def test_make_profile_is_literal_data(self):
        bindir = Path(self.temp.name) / "bin"
        bindir.mkdir()
        stub = bindir / "python3"
        stub.write_text('#!/bin/sh\nprintf "%s\\0" "$@" > "$ARGV_FILE"\n')
        stub.chmod(0o700)
        marker = Path(self.temp.name) / "executed"
        argv_file = Path(self.temp.name) / "argv"
        profile = ('fake"; touch ' + str(marker) + '; # $(touch ' +
                   str(marker) + ') `touch ' + str(marker) + '`')
        env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
                   ARGV_FILE=str(argv_file), PROFILE=profile)
        root = Path(__file__).resolve().parents[2]
        for command in (["make", "browser-creds"],
                        ["make", "browser-creds", "PROFILE=" + profile]):
            with self.subTest(command=command):
                result = subprocess.run(command, cwd=root, env=env,
                                        capture_output=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(argv_file.read_bytes().split(b"\0")[:-1],
                                 [b"scripts/browser-creds.py", b"--profile",
                                  profile.encode()])
                self.assertFalse(marker.exists())

    def test_env_unchanged(self):
        self.write('export VNGCLOUD_USERNAME = "fake-user"\n'
                   "VNGCLOUD_PASSWORD='100%fake'\n# ignored\ninvalid\n")
        self.assertEqual(creds.load_env(self.path), {
            "VNGCLOUD_USERNAME": "fake-user",
            "VNGCLOUD_PASSWORD": "100%fake",
        })


if __name__ == "__main__":
    unittest.main()
