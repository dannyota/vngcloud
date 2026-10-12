#!/usr/bin/env python3
"""Exercise connected coverage with synthetic scanner children only."""

import contextlib
import importlib.util
import io
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import textwrap
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parent.parent
ARGV = ["semgrep", "ci", "--code", "--supply-chain", "--secrets", "--no-suppress-errors"]
ENABLED = "  Enabled products: Code, Supply Chain, Secrets\n"
TITLE = "┌─────────────┐\n│ Scan Status │\n└─────────────┘\n"
HEADER = "  Scanning 19 files tracked by git with 2944 Code rules, 5 Secrets rules, 1479 Supply Chain rules:\n"
GOOD = ENABLED + TITLE + HEADER
OBSERVED = ("  Enabled products: Code, Supply Chain\n"
            "  Semgrep Secrets requires Semgrep Pro Engine\n"
            "  Using Semgrep Pro Version: 1.180.0\n" + TITLE +
            "  Scanning 19 files tracked by git with 2944 Code rules, 1479 Supply Chain rules:\n"
            "✅ CI scan completed successfully.\n"
            " • Findings: 0 (0 blocking)\n • Rules run: 117199\n"
            "  No blocking findings so exiting with code 0\n")


def load_wrapper():
    spec = importlib.util.spec_from_file_location("connected", ROOT / "scripts/semgrep-connected.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def native_summaries(files, rules, products):
    """Use the pinned formatter's degenerate and single-language forms."""
    file_unit = "file" if files == 1 else "files"
    rule_unit = "rule" if rules == 1 else "rules"
    header = (f"Scanning {files} {file_unit} tracked by git with {rules} Code {rule_unit}, "
              f"{rules} Secrets {rule_unit}, 4 Supply Chain rules:\n")
    output = ENABLED + TITLE + header
    for product in products:
        detail = f"Scanning {files} {file_unit}"
        if rules > 1:
            detail += f" with {rules} generic rules"
        output += f"\n  {product.upper()} RULES\n  {detail}.\n"
    return output


def rendered_forms(output):
    for width in (None, 38, 78, 118):
        wrapped = output if width is None else "\n".join(
            "\n".join(textwrap.wrap(line, width=width)) for line in output.splitlines()) + "\n"
        yield wrapped
        yield "\x1b[1;36m" + wrapped.replace("\n", "\x1b[0m\r\n\x1b[1m") + "\x1b[0m"


class WorkflowBoundaryTests(unittest.TestCase):
    def test_workflow_rejects_green_without_secrets(self):
        text = (ROOT / ".github/workflows/semgrep.yml").read_text()
        script = text.rsplit("        run: |\n", 1)[1]
        script = "\n".join(line[10:] for line in script.splitlines())
        with tempfile.TemporaryDirectory() as temp:
            scanner = Path(temp) / "semgrep"
            scanner.write_text(f"#!{sys.executable}\nimport sys\nsys.stdout.write({OBSERVED!r})\n")
            scanner.chmod(0o700)
            result = subprocess.run(["sh", "-e", "-c", script], cwd=ROOT,
                                    env={"PATH": temp + ":" + os.defpath,
                                         "TRUSTED_SOURCE": "true", "SEMGREP_APP_TOKEN": "synthetic"},
                                    capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0, "exit zero must not replace Secrets evidence")


class CoverageTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.wrapper = load_wrapper()

    def run_child(self, output=GOOD, code=0, body=None, capture_failure=None, **limits):
        real_popen = subprocess.Popen
        children = []
        output_stream = io.StringIO()
        body = body or f"import os\nos.write(1, {output.encode()!r})\nraise SystemExit({code})"

        def launch(argv, **kwargs):
            self.assertEqual(list(argv), ARGV)
            self.assertNotIn("shell", kwargs)
            self.assertNotIn("env", kwargs)
            self.assertTrue(kwargs["start_new_session"])
            self.assertEqual(kwargs["stdin"], subprocess.DEVNULL)
            self.assertEqual(kwargs["stdout"], subprocess.PIPE)
            self.assertEqual(kwargs["stderr"], subprocess.STDOUT)
            child = real_popen([sys.executable, "-I", "-c", body], **kwargs)
            children.append(child)
            return child

        with contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(self.wrapper.subprocess, "Popen", side_effect=launch))
            if capture_failure is not None:
                stack.enter_context(patch.object(self.wrapper, "capture", side_effect=OSError(capture_failure)))
            for name, value in limits.items():
                stack.enter_context(patch.object(self.wrapper, name, value))
            stack.enter_context(contextlib.redirect_stdout(output_stream))
            stack.enter_context(contextlib.redirect_stderr(output_stream))
            result = self.wrapper.run()
        self.assertEqual(len(children), 1)
        child = children[0]
        self.assertIsNotNone(child.returncode)
        self.assertTrue(child.stdout.closed)
        with self.assertRaises(ChildProcessError):
            os.waitpid(child.pid, os.WNOHANG)
        return result, output_stream.getvalue()

    def test_all_products_enabled_and_positive(self):
        result, output = self.run_child()
        self.assertEqual(result, 0)
        self.assertEqual(output, "semgrep-connected: coverage-ok\nCode rules: 2944\nSupply Chain rules: 1479\nSecrets rules: 5\n")
        self.assertEqual(self.wrapper.coverage(GOOD.encode()),
                         {"Code": 2944, "Secrets": 5, "Supply Chain": 1479})

    def test_supplied_green_without_secrets(self):
        result, output = self.run_child(OBSERVED)
        self.assertEqual(result, 1)
        self.assertIn("enabled-products-invalid", output)

    def test_wrapped_and_ansi_forms(self):
        forms = [GOOD,
                 GOOD.replace("Code, Supply Chain, Secrets", "Code, Supply\n  Chain, Secrets"),
                 GOOD.replace("2944 Code rules, 5 Secrets rules, 1479 Supply Chain rules:",
                              "2944 Code rules, 5 Secrets\n  rules, 1479 Supply Chain rules:"),
                 GOOD.replace("with 2944", "with\n  2944"),
                 GOOD.replace("19 files", "1 file").replace("5 Secrets rules", "1 Secrets rule")]
        for form in forms:
            for styled in (form, "\x1b[1;36m" + form.replace("\n", "\x1b[0m\n\x1b[1m") + "\x1b[0m"):
                with self.subTest(form=form, ansi=styled != form):
                    self.assertEqual(self.run_child(styled)[0], 0)

    def test_native_single_rule_per_product_summaries(self):
        for products in (("Code",), ("Secrets",), ("Code", "Secrets")):
            with self.subTest(products=products):
                data = native_summaries(1, 1, products)
                self.assertEqual(self.run_child(data)[0], 0)
                self.assertEqual(self.wrapper.coverage(data.encode()),
                                 {"Code": 1, "Secrets": 1, "Supply Chain": 4})

    def test_native_generic_summaries_wrapped_and_ansi(self):
        for files, rules in ((1, 1), (19, 1), (19, 5)):
            for products in (("Code",), ("Secrets",), ("Code", "Secrets")):
                data = native_summaries(files, rules, products)
                for form in rendered_forms(data):
                    with self.subTest(files=files, rules=rules, products=products, form=form):
                        self.assertEqual(self.run_child(form)[0], 0)
                        self.assertEqual(self.wrapper.coverage(form.encode()),
                                         {"Code": rules, "Secrets": rules, "Supply Chain": 4})

    def test_native_summaries_do_not_hide_main_record_forgery(self):
        for product in ("Code", "Secrets"):
            data = native_summaries(19, 5, (product,))
            detail = "Scanning 19 files with 5 generic rules."
            for forged in (HEADER, HEADER.replace("5 Secrets rules", "0 Secrets rules"),
                           HEADER.replace("5 Secrets rules, ", ""),
                           HEADER.replace("5 Secrets rules", "five Secrets rules"),
                           HEADER.replace("tracked by git ", ""),
                           HEADER.replace("rules:", "rules."),
                           HEADER.replace("rules:", "rules extra:"),
                           "finding: " + HEADER, "path/" + HEADER,
                           "Scanning 19 files with 1 Secrets rule:\n",
                           "Scanning 19 files with 1 Code rule.\n"):
                for location in (data + forged, data.replace(detail, forged.strip())):
                    for form in rendered_forms(location):
                        with self.subTest(product=product, forged=forged, form=form):
                            self.assertEqual(self.run_child(form)[0], 1)
                            with self.assertRaises(self.wrapper.Failure):
                                self.wrapper.coverage(form.encode())
            for malformed in (data.replace("5 Secrets rules, ", ""),
                              data.replace("5 Secrets rules", "0 Secrets rules"),
                              data.replace("5 Secrets rules", "five Secrets rules")):
                self.assertEqual(self.run_child(malformed + HEADER)[0], 1)
            for suffix in (ENABLED, TITLE + HEADER):
                self.assertEqual(self.run_child(data + suffix)[0], 1)

    def test_native_summary_requires_exact_section_context(self):
        data = native_summaries(19, 5, ("Secrets",))
        detail = "Scanning 19 files with 5 generic rules."
        for malformed in (data.replace("SECRETS RULES", "SUPPLY CHAIN RULES"),
                          data.replace("SECRETS RULES", "finding: SECRETS RULES"),
                          data.replace("  " + detail, "  path/" + detail),
                          data.replace(detail, "Scanning 19 files with five generic rules."),
                          data.replace(detail, "Scanning 19 files with 0 generic rules."),
                          data.replace(detail, "Scanning 1 files with 5 generic rules."),
                          data.replace(detail, detail.replace(".", ":")),
                          data.replace(detail, detail + " extra"),
                          data + "\n  SECRETS RULES\n  " + detail + "\n",
                          GOOD + "\n" + detail + "\n",
                          GOOD + "\nfindings:\n  SECRETS RULES\n  " + HEADER):
            self.assertEqual(self.run_child(malformed)[0], 1)
        ai_detail = data.replace("SECRETS RULES\n", "SECRETS RULES\n  AI augmented rules are active for secrets detection.\n")
        for form in rendered_forms(ai_detail):
            self.assertEqual(self.run_child(form)[0], 0)

    def test_zero_targets_still_need_rule_evidence(self):
        self.assertEqual(self.run_child(GOOD.replace("19 files", "0 files"))[0], 0)
        self.assertEqual(self.run_child(ENABLED + TITLE + "Scanning 0 files tracked by git:\n")[0], 1)

    def test_missing_and_zero_products(self):
        for product, count in (("Code", 2944), ("Secrets", 5), ("Supply Chain", 1479)):
            for output in (GOOD.replace(f"{count} {product} rules", f"0 {product} rules"),
                           GOOD.replace(f"{count} {product} rules, ", "").replace(f", {count} {product} rules", ""),
                           GOOD.replace(f"{product}, ", "").replace(f", {product}\n", "\n")):
                with self.subTest(product=product, output=output):
                    self.assertEqual(self.run_child(output)[0], 1)

    def test_malformed_absent_duplicate_and_contradictory_records(self):
        bad = [GOOD.replace(ENABLED, ""), GOOD.replace(TITLE, ""), GOOD.replace(HEADER, ""),
               GOOD.replace("Enabled products:", "Enabled products="),
               GOOD.replace("Secrets\n", "Secrets, Secrets\n"),
               GOOD.replace("Secrets\n", "Unknown\n"),
               GOOD.replace("5 Secrets rules", "five Secrets rules"),
               GOOD.replace("5 Secrets rules", "-5 Secrets rules"),
               GOOD.replace("5 Secrets rules", "1 Secrets rules"),
               GOOD.replace("5 Secrets rules", "999999999999 Secrets rules"),
               GOOD.replace("5 Secrets rules", "5 Secrets rules, 5 Secrets rules"),
               GOOD.replace("rules:\n", "rules extra:\n"),
               GOOD + ENABLED, GOOD + TITLE + HEADER, GOOD + HEADER,
               OBSERVED + GOOD, GOOD + "finding: Enabled products: Code, Supply Chain, Secrets\n",
               GOOD + "path/Scanning 3 files tracked by git with 1 Secrets rule:\n",
               GOOD.replace("  Enabled", "filename Enabled"),
               GOOD.replace("\n│ Scan Status │\n", "\nScan Status\n"),
               GOOD.replace("Supply Chain, Secrets", "Supply\n" + " \n" * 20 + "Chain, Secrets")]
        for output in bad:
            with self.subTest(output=output):
                self.assertEqual(self.run_child(output)[0], 1)
                with self.assertRaises(self.wrapper.Failure):
                    self.wrapper.coverage(output.encode())

    def test_invalid_bytes_and_controls(self):
        for suffix in (b"\xff", b"\x00"):
            with self.assertRaises(self.wrapper.Failure):
                self.wrapper.coverage(GOOD.encode() + suffix)

    def test_scanner_failure_preserves_status(self):
        for code in (1, 23, 137):
            result, output = self.run_child(code=code)
            self.assertEqual(result, code)
            self.assertEqual(output, "semgrep-connected: scanner-failed\n")
        self.assertEqual(self.run_child(body="import os, signal\nos.kill(os.getpid(), signal.SIGTERM)")[0], 143)

    def test_launch_failure_is_safe(self):
        output = io.StringIO()
        with patch.object(self.wrapper.subprocess, "Popen", side_effect=OSError("synthetic private cause")), contextlib.redirect_stderr(output):
            self.assertEqual(self.wrapper.run(), 1)
        self.assertEqual(output.getvalue(), "semgrep-connected: launch-failed\n")

    def test_output_bound_and_timeout_reap_ignoring_child(self):
        prelude = "import os, signal, time\nsignal.signal(signal.SIGTERM, signal.SIG_IGN)\n"
        result, output = self.run_child(body=prelude + "os.write(1, b'x' * 8192)\ntime.sleep(30)",
                                        MAX_BYTES=1024, GRACE_SECONDS=0.1)
        self.assertEqual(result, 1)
        self.assertEqual(output, "semgrep-connected: output-limit\n")
        result, output = self.run_child(body=prelude + "time.sleep(30)",
                                        MAX_SECONDS=0.2, GRACE_SECONDS=0.1)
        self.assertEqual(result, 1)
        self.assertEqual(output, "semgrep-connected: timeout\n")

    def test_exact_output_bound_and_stderr_capture(self):
        self.assertEqual(self.run_child(MAX_BYTES=len(GOOD.encode()))[0], 0)
        self.assertEqual(self.run_child(body=f"import os\nos.write(2, {GOOD.encode()!r})")[0], 0)
        self.assertLess(self.wrapper.MAX_SECONDS + 2 * self.wrapper.GRACE_SECONDS, 15 * 60)

    def test_eof_does_not_end_child_time_bound(self):
        result, output = self.run_child(body="import os, time\nos.close(1)\nos.close(2)\ntime.sleep(30)",
                                        MAX_SECONDS=0.2, GRACE_SECONDS=0.1)
        self.assertEqual(result, 1)
        self.assertEqual(output, "semgrep-connected: timeout\n")

    def test_capture_error_reaps_child_without_exception_cause(self):
        result, output = self.run_child(body="import time\ntime.sleep(30)",
                                        capture_failure="synthetic-private-exception-cause")
        self.assertEqual(result, 1)
        self.assertEqual(output, "semgrep-connected: capture-failed\n")

    def test_cancellation_reaps_child(self):
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            with self.subTest(signal=sig):
                result, output = self.run_child(body=f"import os, signal, time\nos.kill(os.getppid(), {sig})\ntime.sleep(30)")
                self.assertEqual(result, 1)
                self.assertEqual(output, "semgrep-connected: cancelled\n")

    def test_secret_looking_values_never_escape(self):
        value = "synthetic-" + "private-token-value"
        for data, code in ((GOOD + "config/findings/path/" + value + "\n", 0),
                           (GOOD.replace("Secrets\n", value + "\n"), 0), (value, 0), (GOOD + value, 23)):
            result, output = self.run_child(data, code)
            self.assertNotIn(value, output)
            self.assertNotIn("config/findings/path", output)
            self.assertNotIn("Traceback", output)

    def test_cli_has_no_override_options(self):
        result = subprocess.run([sys.executable, "-I", str(ROOT / "scripts/semgrep-connected.py"),
                                 "--command", "synthetic-private-command"],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "semgrep-connected: invalid-invocation\n")
        self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
