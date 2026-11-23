import concurrent.futures
import contextlib
import http.client
import importlib.util
import io
import json
import os
import re
import subprocess
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest import mock

sys.dont_write_bytecode = True

SPEC = importlib.util.spec_from_file_location(
    "browser_creds", Path(__file__).resolve().parents[1] / "browser-creds.py"
)
creds = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(creds)


def git_ignore_stub(test, directory, root):
    binary = directory / "git-bin"
    binary.mkdir()
    git = binary / "git"
    git.write_text('#!/bin/sh\n'
                   '[ "$PWD" = "$BROWSER_TEST_GIT_ROOT" ] || exit 19\n'
                   '[ "$#" = 4 ] || exit 19\n'
                   '[ "$1" = check-ignore ] && [ "$2" = -q ] && [ "$3" = -- ] || exit 19\n'
                   'case "$4" in .playwright-mcp/*) ;; *) exit 19 ;; esac\n'
                   'printf "synthetic git diagnostic\\n" >&2\n'
                   'exit "${BROWSER_TEST_GIT_STATUS:-0}"\n')
    git.chmod(0o700)
    test.enterContext(mock.patch.dict(os.environ, {
        "PATH": str(binary) + os.pathsep + os.environ["PATH"],
        "BROWSER_TEST_GIT_ROOT": str(root),
        "BROWSER_TEST_GIT_STATUS": "0",
    }))


def synthetic_repository(test):
    temp = tempfile.TemporaryDirectory()
    test.addCleanup(temp.cleanup)
    root = Path(temp.name).resolve()
    scripts = root / "scripts"
    scripts.mkdir()
    template = Path(__file__).resolve().parents[1] / "browser-login.js"
    (scripts / "browser-login.js").write_text(template.read_text())
    test.enterContext(mock.patch.object(creds, "__file__", str(scripts / "browser-creds.py")))
    git_ignore_stub(test, root, root)
    return root


class LoadingTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "credentials"
        git_ignore_stub(self, Path(self.temp.name), Path(creds.__file__).resolve().parents[1])

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


class BrowserDirectoryTest(unittest.TestCase):
    def setUp(self):
        self.root = synthetic_repository(self)
        self.base = self.root / ".playwright-mcp"
        self.enterContext(mock.patch.object(creds, "load_env", return_value={}))

    def run_helper(self, inspect=lambda output: None):
        output, error = io.StringIO(), io.StringIO()
        with mock.patch.object(creds.http.server, "HTTPServer") as server, \
                mock.patch.object(creds.time, "monotonic", side_effect=(0, 0, 2)), \
                mock.patch.object(creds, "TIMEOUT_SECONDS", 1), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(error):
            server.return_value.__enter__.return_value.handle_request.side_effect = lambda: inspect(output)
            result = creds.main(["synthetic-input"])
        return result, output.getvalue(), error.getvalue(), server.called

    def inspect_script(self, output):
        script = Path(output.getvalue().split("browser script: ", 1)[1].splitlines()[0])
        self.assertEqual(script.parent.parent, self.base)
        self.assertEqual(self.base.stat().st_mode & 0o777, 0o700)
        self.assertEqual(script.parent.stat().st_mode & 0o777, 0o700)
        self.assertEqual(script.stat().st_mode & 0o777, 0o600)
        self.script = script

    def test_creates_private_browser_directory_and_removes_script_on_timeout(self):
        self.assertFalse(self.base.exists())
        _, _, error, called = self.run_helper(self.inspect_script)
        self.assertEqual(error, "")
        self.assertTrue(called)
        self.assertFalse(self.script.parent.exists())
        self.assertEqual(list(self.base.iterdir()), [])

    def test_unignored_browser_path_refuses_without_creating_anything(self):
        before = sorted(path.relative_to(self.root) for path in self.root.rglob("*"))
        for status in ("1", "128"):
            with self.subTest(status=status), \
                    mock.patch.dict(os.environ, {"BROWSER_TEST_GIT_STATUS": status}), \
                    mock.patch.object(creds.os, "mkdir", wraps=os.mkdir) as mkdir:
                result, output, error, called = self.run_helper()
                self.assertEqual(result, 2)
                self.assertEqual(output, "")
                self.assertEqual(error,
                                 "browser-creds: add .playwright-mcp/ to .git/info/exclude before browser login\n")
                self.assertFalse(called)
                mkdir.assert_not_called()
                creds.load_env.assert_not_called()
                self.assertEqual(sorted(path.relative_to(self.root) for path in self.root.rglob("*")), before)

    def test_failed_ignore_check_refuses_without_creating_anything(self):
        for error in (OSError("synthetic Git failure"), subprocess.TimeoutExpired("git", 5)):
            with self.subTest(error=type(error).__name__), \
                    mock.patch.object(subprocess, "run", side_effect=error), \
                    mock.patch.object(creds.os, "mkdir", wraps=os.mkdir) as mkdir:
                result, output, diagnostic, called = self.run_helper()
                self.assertEqual(result, 2)
                self.assertEqual(output, "")
                self.assertEqual(diagnostic,
                                 "browser-creds: add .playwright-mcp/ to .git/info/exclude before browser login\n")
                self.assertFalse(called)
                mkdir.assert_not_called()
                creds.load_env.assert_not_called()
                self.assertFalse(self.base.exists())

    def test_existing_owned_browser_directory_is_private(self):
        self.base.mkdir(mode=0o755)
        self.base.chmod(0o755)
        _, _, error, called = self.run_helper(self.inspect_script)
        self.assertEqual(error, "")
        self.assertTrue(called)
        self.assertFalse(self.script.parent.exists())

    def assert_refused(self):
        result, output, error, called = self.run_helper()
        self.assertEqual(result, 2)
        self.assertEqual(output, "")
        self.assertEqual(error, "browser-creds: cannot use private .playwright-mcp directory\n")
        self.assertFalse(called)

    def test_symlink_browser_directory_is_refused(self):
        for exists in (True, False):
            with self.subTest(target_exists=exists):
                target = self.root / ("existing" if exists else "missing")
                if exists:
                    target.mkdir(mode=0o755)
                    target.chmod(0o755)
                self.base.symlink_to(target, target_is_directory=True)
                try:
                    self.assert_refused()
                    if exists:
                        self.assertEqual(target.stat().st_mode & 0o777, 0o755)
                        self.assertEqual(list(target.iterdir()), [])
                    else:
                        self.assertFalse(target.exists())
                finally:
                    self.base.unlink()

    def test_browser_directory_owned_by_another_user_is_refused(self):
        self.base.mkdir(mode=0o755)
        self.base.chmod(0o755)
        with mock.patch.object(creds.os, "getuid", return_value=os.getuid() + 1):
            self.assert_refused()
        self.assertEqual(self.base.stat().st_mode & 0o777, 0o755)
        self.assertEqual(list(self.base.iterdir()), [])

    def test_non_directory_browser_path_is_refused(self):
        self.base.write_text("synthetic-blocker")
        self.assert_refused()
        self.assertEqual(self.base.read_text(), "synthetic-blocker")


class CapabilityTest(unittest.TestCase):
    def setUp(self):
        self.root = synthetic_repository(self)

    def test_two_helpers_use_independent_bound_ports(self):
        ready = threading.Event()
        servers = []
        server_type = creds.http.server.HTTPServer
        output = io.StringIO()
        fake = {"VNGCLOUD_PASSWORD": "synthetic-password",
                "VNGCLOUD_TOTP_SECRET": "JBSWY3DPEHPK3PXP"}

        def server(address, handler):
            self.assertEqual(address, ("127.0.0.1", 0))
            instance = server_type(address, handler)
            servers.append(instance)
            if len(servers) == 2:
                ready.set()
            return instance

        with mock.patch.object(creds, "load_env", return_value=fake), \
                mock.patch.object(creds.http.server, "HTTPServer", side_effect=server), \
                mock.patch.object(creds, "TIMEOUT_SECONDS", 3), \
                contextlib.redirect_stdout(output), \
                concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(creds.main, ["synthetic-input"]) for _ in range(2)]
            try:
                self.assertTrue(ready.wait(2), [f.exception() for f in futures if f.done()])
                for instance in servers:
                    instance.timeout = 0.1
                scripts = []
                for _ in range(200):
                    if output.getvalue().count("browser script: ") == 2:
                        break
                    threading.Event().wait(0.01)
                self.assertEqual(output.getvalue().count("browser script: "), 2)
                scripts = list((self.root / ".playwright-mcp").glob("*/browser-login.js"))
                self.assertEqual(len(scripts), 2)
                ports = {instance.server_port for instance in servers}
                self.assertEqual(len(ports), 2)
                for script in scripts:
                    text = script.read_text()
                    url = re.search(r'const helper = "(http://127\.0\.0\.1:(\d+))";', text)
                    token = re.search(r'const capability = "([^"]+)";', text)
                    self.assertIsNotNone(url)
                    self.assertIsNotNone(token)
                    port = int(url.group(2))
                    self.assertIn(port, ports)
                    ports.remove(port)
                    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
                    try:
                        connection.request("GET", "/creds", headers={"Authorization": "Bearer " + token.group(1)})
                        response = connection.getresponse()
                        self.assertEqual(response.status, 200)
                        body = json.loads(response.read())
                        self.assertEqual(body["password"], "synthetic-password")
                        self.assertNotEqual(body["next"], token.group(1))
                        connection.request("GET", "/totp", headers={"Authorization": "Bearer " + body["next"]})
                        response = connection.getresponse()
                        self.assertEqual(response.status, 200)
                        response.read()
                    finally:
                        connection.close()
                self.assertFalse(ports)
            finally:
                for future in futures:
                    future.result(timeout=5)

    def test_authentication_precedes_one_shot_consumption(self):
        self.run_session("synthetic-first-capability", "synthetic-old-capability")
        self.run_session("synthetic-capability", "synthetic-first-capability")

    def test_creds_capability_is_spent_by_first_request(self):
        status = self.run_session("synthetic-capability", "synthetic-old-capability")
        self.assertEqual(status["creds_again"], 403)
        self.assertEqual(status["totp_with_first"], 403)
        self.assertEqual(status["totp_wrong_next"], 403)
        self.assertEqual(status["totp_first_use"], 200)
        self.assertEqual(status["totp_with_creds"], 403)

    def test_helper_finishes_after_creds_without_totp(self):
        self.run_session("synthetic-capability", "synthetic-old-capability", totp_secret="", finish_after_creds=True)

    def run_session(self, capability, stale, totp_secret="JBSWY3DPEHPK3PXP", finish_after_creds=False):
        ready = threading.Event()
        servers = []
        server_type = creds.http.server.HTTPServer
        output = io.StringIO()
        fake = {"VNGCLOUD_ROOT_EMAIL": "fake@example.invalid",
                "VNGCLOUD_USERNAME": "fake-user",
                "VNGCLOUD_PASSWORD": "synthetic-password",
                "VNGCLOUD_TOTP_SECRET": totp_secret}
        next_capability = "synthetic-next-capability"
        result = {}

        def server(address, handler):
            instance = server_type(address, handler)
            servers.append(instance)
            ready.set()
            return instance

        def request(path, capability=None, host=None):
            port = servers[0].server_port
            headers = {"Host": host or f"127.0.0.1:{port}"}
            if capability is not None:
                headers["Authorization"] = "Bearer " + capability
            connection = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
            try:
                connection.request("GET", path, headers=headers)
                response = connection.getresponse()
                return response.status, response.read()
            finally:
                connection.close()

        with mock.patch.object(creds, "load_env", return_value=fake), \
                mock.patch.object(creds.http.server, "HTTPServer", side_effect=server), \
                mock.patch.object(creds, "TIMEOUT_SECONDS", 2), \
                mock.patch("secrets.token_urlsafe", side_effect=[capability, next_capability]), \
                contextlib.redirect_stdout(output), \
                concurrent.futures.ThreadPoolExecutor() as pool:
            future = pool.submit(creds.main, ["synthetic-input"])
            self.assertTrue(ready.wait(2))
            try:
                for path in ("/creds", "/totp"):
                    for token in (None, "wrong-capability", stale):
                        with self.subTest(path=path, token=token):
                            status, body = request(path, token)
                            self.assertEqual(status, 403)
                            self.assertNotIn(b"synthetic-password", body)
                script = Path(output.getvalue().split("browser script: ", 1)[1].splitlines()[0])
                self.assertEqual(script.parent.parent, self.root / ".playwright-mcp")
                self.assertEqual(script.stat().st_mode & 0o777, 0o600)
                self.assertEqual(script.parent.stat().st_mode & 0o777, 0o700)
                self.assertIn(json.dumps(capability), script.read_text())
                self.assertEqual(request("/creds", capability, "foreign.invalid")[0], 403)
                status, body = request("/creds", capability)
                self.assertEqual(status, 200)
                self.assertEqual(json.loads(body)["password"], "synthetic-password")
                self.assertEqual(json.loads(body)["next"], next_capability)
                if finish_after_creds:
                    future.result(timeout=4)
                    self.assertFalse(script.exists())
                else:
                    result["creds_again"] = request("/creds", capability)[0]
                    result["totp_with_first"] = request("/totp", capability)[0]
                    result["totp_wrong_next"] = request("/totp", "wrong-capability")[0]
                    result["totp_with_creds"] = request("/creds", next_capability)[0]
                    result["totp_first_use"] = request("/totp", next_capability)[0]
            finally:
                future.result(timeout=4)
                servers[0].server_close()
        self.assertFalse(script.exists())
        self.assertFalse(script.parent.exists())
        self.assertNotIn(capability, output.getvalue())
        self.assertNotIn("synthetic-password", output.getvalue())
        self.assertNotIn(next_capability, output.getvalue())
        return result


if __name__ == "__main__":
    unittest.main()
