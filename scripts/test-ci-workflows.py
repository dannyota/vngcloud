#!/usr/bin/env python3
"""Offline checks for workflow policy and the shell commands enforcing it.

These text checks enforce the reviewed workflow shape; actionlint separately
parses YAML and validates Actions syntax. No GitHub or scanner calls run here.
"""

import copy
import fnmatch
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
HEAD = "${{ github.event.pull_request.head.sha || github.sha }}"
FILES = ("ci", "semgrep", "tools", "wiki")


def jobs(text):
    section = text.split("\njobs:\n", 1)[1]
    starts = list(re.finditer(r"^  ([a-z][a-z0-9_-]*):\n", section, re.M))
    return {m[1]: section[m.end():starts[i + 1].start() if i + 1 < len(starts)
                            else len(section)] for i, m in enumerate(starts)}


def event(text, name):
    section = text.split("\non:\n", 1)[1].split("\npermissions:", 1)[0]
    match = re.search(rf"^  {name}:\n(.*?)(?=^  \w+:|\Z)", section,
                      re.M | re.S)
    return None if match is None else match[1]


def triggered(text, name, paths):
    config = event(text, name)
    if config is None:
        return False
    patterns = re.findall(r'^      - ["\']?([^"\'\n]+)["\']?$', config, re.M)
    if "paths-ignore:" in config:
        return any(not any(fnmatch.fnmatchcase(p, rule) for rule in patterns)
                   for p in paths)
    if "paths:" in config:
        return any(fnmatch.fnmatchcase(p, rule) for p in paths for rule in patterns)
    return True


def run_blocks(text):
    # Literal run blocks use ten spaces for command lines in these workflows.
    return re.findall(r"^        run: \|\n((?:          .*\n|\n)+)", text, re.M)


def command(block):
    return "\n".join(line[10:] if line.startswith("          ") else line
                     for line in block.splitlines()) + "\n"


def wiki_policy(text):
    if "permissions:\n  contents: read\n" not in text:
        return False
    if set(jobs(text)) != {"preview"} or text.count("uses:") != 1:
        return False
    if re.search(r"\b(write|secrets|environment|GH_TOKEN|GITHUB_TOKEN)\b", text):
        return False
    commands = re.findall(r"^\s+(?:- )?run: (.+)$", text, re.M)
    if commands != ["|", "bash scripts/wiki-sync.sh --dry-run"]:
        return False
    blocks = run_blocks(text)
    return len(blocks) == 1 and "wiki-sync" not in blocks[0]


class WorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflows = {name: (ROOT / f".github/workflows/{name}.yml").read_text()
                         for name in FILES}

    def test_triggers_and_concurrency(self):
        for name, text in self.workflows.items():
            with self.subTest(workflow=name):
                for trigger in ("pull_request", "push"):
                    self.assertIn("branches: [master]", event(text, trigger) or "")
                self.assertIsNotNone(event(text, "workflow_dispatch"))
                self.assertNotIn("pull_request_target", text)
                self.assertNotIn("wip/", text)
                self.assertIn("${{ github.run_id }}", text)
                self.assertIn("${{ github.run_attempt }}", text)
                self.assertIn("cancel-in-progress: false", text)
                self.assertIn("permissions:\n  contents: read\n", text)
        self.assertIn('cron: "0 1 * * 1"', event(self.workflows["tools"], "schedule"))
        self.assertNotIn("paths", event(self.workflows["ci"], "push"))
        self.assertNotIn("paths", event(self.workflows["ci"], "pull_request"))

    def test_event_path_matrix(self):
        cases = [(["nat/nat.go"], {"ci", "semgrep"}),
                 (["README.md"], {"ci"}),
                 (["docs/wiki/Home.md"], {"ci", "wiki"}),
                 ([".tool-versions"], {"ci", "semgrep", "tools"}),
                 (["go.sum"], {"ci", "semgrep", "tools"}),
                 ([".github/workflows/wiki.yml"], set(FILES)),
                 (["docs/example.json"], {"ci", "semgrep"}),
                 (["LICENSE"], {"ci"}),
                 (["nested/LICENSE"], {"ci", "semgrep"}),
                 (["scripts/test-ci-workflows.py"], {"ci", "semgrep", "tools"}),
                 (["scripts/test-wiki-publication.py"], {"ci", "semgrep", "wiki"}),
                 (["scripts/ci-evidence.py"], {"ci", "semgrep", "wiki"})]
        for trigger in ("push", "pull_request"):
            for paths, expected in cases:
                with self.subTest(event=trigger, paths=paths):
                    actual = {name for name, text in self.workflows.items()
                              if triggered(text, trigger, paths)}
                    self.assertEqual(actual, expected)
        for name, text in self.workflows.items():
            self.assertTrue(triggered(text, "workflow_dispatch", ["README.md"]))
            self.assertEqual(triggered(text, "schedule", []), name == "tools")

    def test_every_validation_job_asserts_head(self):
        for name, text in self.workflows.items():
            for job, body in jobs(text).items():
                if job == "required":
                    continue
                with self.subTest(workflow=name, job=job):
                    self.assertIn("timeout-minutes:", body)
                    self.assertNotIn("environment:", body)
                    self.assertIn(f"ref: {HEAD}", body)
                    self.assertIn("persist-credentials: false", body)
                    self.assertIn(f"EXPECTED_SHA: {HEAD}", body)
                    self.assertIn('test "$actual" = "$EXPECTED_SHA"', body)
                    self.assertIn("github.run_id", body)
                    self.assertIn("github.run_attempt", body)
                    self.assertIn('git rev-parse HEAD', body)
                    self.assertNotIn("${{", "".join(run_blocks(body)))

    def test_sha_assertion_rejects_merge_checkout(self):
        block = next(b for b in run_blocks(jobs(self.workflows["ci"])["go"])
                     if "git rev-parse HEAD" in b)
        with tempfile.TemporaryDirectory() as temp:
            git = Path(temp) / "git"
            git.write_text('#!/bin/sh\nprintf "%s\\n" "$ACTUAL_SHA"\n')
            git.chmod(0o700)
            for actual, expected_code in (("head", 0), ("merge", 1)):
                env = dict(os.environ, PATH=temp + ":" + os.environ["PATH"],
                           EXPECTED_SHA="head", ACTUAL_SHA=actual,
                           GITHUB_OUTPUT=str(Path(temp) / "output"),
                           GITHUB_STEP_SUMMARY=str(Path(temp) / "summary"),
                           RUN_ID="1", RUN_ATTEMPT="1")
                result = subprocess.run(["bash", "-e", "-c", command(block)],
                                        env=env, capture_output=True)
                self.assertEqual(result.returncode, expected_code)

    def test_existing_ci_gates_and_regressions_remain(self):
        text = self.workflows["ci"]
        for gate in ("go test -race ./...", "go vet ./...", "go vet -tags live ./...",
                     "go vet -tags livewrite ./...", "go mod tidy",
                     "git diff --exit-code -- go.mod go.sum", "scripts/check-lengths.sh",
                     "scripts/check-sdk-deps.sh", "GOOS=windows go vet ./...",
                     "GOOS=darwin go vet ./...", "gen-docs docs/wiki",
                     "scripts/wiki-sync.sh --dry-run", "golangci-lint-action@",
                     "gitleaks detect --redact --no-banner", "govulncheck ./...",
                     "python3 scripts/test-ci-workflows.py"):
            self.assertIn(gate, text)
        self.assertIn("fetch-depth: 0", jobs(text)["security"])
        make = (ROOT / "Makefile").read_text()
        self.assertRegex(make, r"(?m)^check: .*ci-workflows-check")
        self.assertIn("python3 scripts/test-ci-workflows.py", make)

    def test_aggregate_rejects_every_non_success_and_wrong_identity(self):
        self.assertEqual(set(jobs(self.workflows["ci"])),
                         {"go", "lint", "security", "required"})
        body = jobs(self.workflows["ci"]).get("required", "")
        self.assertIn("name: CI required", body)
        self.assertIn("if: ${{ always() }}", body)
        self.assertIn("needs: [go, lint, security]", body)
        self.assertNotIn("checkout@", body)
        self.assertIn("NEEDS_JSON: ${{ toJSON(needs) }}", body)
        good = {name: {"result": "success", "outputs": {"tested_sha": "head"}}
                for name in ("go", "lint", "security")}
        cases = [(good, True)]
        for name in good:
            for status in ("failure", "skipped", "cancelled", "pending", "neutral",
                           "timed_out", ""):
                bad = copy.deepcopy(good)
                bad[name]["result"] = status
                cases.append((bad, False))
            bad = copy.deepcopy(good)
            bad[name]["outputs"]["tested_sha"] = "merge"
            cases.append((bad, False))
            bad = copy.deepcopy(good)
            del bad[name]
            cases.append((bad, False))
        cases.extend([({}, False), ({**good, "unexpected": good["go"]}, False)])
        script = command(run_blocks(body)[0])
        for needs, accepted in cases:
            result = subprocess.run(["bash", "-e", "-c", script], capture_output=True,
                                    env=dict(os.environ, NEEDS_JSON=json.dumps(needs),
                                             EXPECTED_SHA="head"))
            self.assertEqual(result.returncode == 0, accepted, needs)
        result = subprocess.run(["bash", "-e", "-c", script], capture_output=True,
                                env=dict(os.environ, NEEDS_JSON="{", EXPECTED_SHA="head"))
        self.assertNotEqual(result.returncode, 0)

    def test_semgrep_trust_and_missing_token(self):
        body = jobs(self.workflows["semgrep"])["semgrep"]
        self.assertIn("fetch-depth: 0", body)
        self.assertIn("semgrep/semgrep:1.180.0@sha256:", body)
        self.assertIn("semgrep ci --code --supply-chain --secrets --no-suppress-errors", body)
        self.assertEqual(body.count("secrets.SEMGREP_APP_TOKEN"), 1)
        self.assertNotIn("SEMGREP_APP_TOKEN", body.split("    steps:", 1)[0])
        block = next(b for b in run_blocks(body) if "semgrep ci" in b)
        with tempfile.TemporaryDirectory() as temp:
            scanner = Path(temp) / "semgrep"
            scanner.write_text('#!/bin/sh\ntouch "$SCAN_MARKER"\n')
            scanner.chmod(0o700)
            for trusted, token, accepted in (("true", "synthetic", True),
                                            ("true", "", False),
                                            ("false", "synthetic", False),
                                            ("false", "", False)):
                marker = Path(temp) / "scanned"
                marker.unlink(missing_ok=True)
                result = subprocess.run(["bash", "-e", "-c", command(block)],
                                        capture_output=True,
                                        env=dict(os.environ, TRUSTED_SOURCE=trusted,
                                                 SEMGREP_APP_TOKEN=token,
                                                 SCAN_MARKER=str(marker),
                                                 PATH=temp + ":" + os.environ["PATH"]))
                self.assertEqual(result.returncode == 0, accepted)
                self.assertEqual(marker.exists(), accepted)
                if trusted == "false":
                    self.assertIn(b"same-repository branch", result.stderr)
                elif not token:
                    self.assertIn(b"requires SEMGREP_APP_TOKEN", result.stderr)

    def test_wiki_has_no_publication_path(self):
        text = self.workflows["wiki"]
        self.assertTrue(wiki_policy(text))
        mutations = [text.replace("contents: read", "contents: write"),
                     text + "  publisher:\n    runs-on: ubuntu-latest\n",
                     text.replace("--dry-run", ""),
                     text + "      - uses: synthetic/publisher@head\n",
                     text + "    env:\n      GH_TOKEN: synthetic\n",
                     text + "    environment: wiki\n",
                     text + "    env:\n      TOKEN: ${{ secrets.GITHUB_TOKEN }}\n"]
        for mutated in mutations:
            self.assertFalse(wiki_policy(mutated))
        # Execute the actual preview command against a write-detecting stub.
        preview = "bash scripts/wiki-sync.sh --dry-run"
        self.assertIn("run: " + preview, text)
        with tempfile.TemporaryDirectory() as temp:
            scripts = Path(temp) / "scripts"
            scripts.mkdir()
            (scripts / "wiki-sync.sh").write_text(
                '#!/bin/bash\nif [[ "$*" != --dry-run ]]; then\n'
                '  touch "$WRITE_MARKER"; exit 1\nfi\n')
            for event_name in ("push", "workflow_dispatch", "pull_request"):
                for validation in ("pending", "failure"):
                    marker = Path(temp) / "write"
                    result = subprocess.run(["bash", "-e", "-c", preview], cwd=temp,
                                            env=dict(os.environ, WRITE_MARKER=str(marker),
                                                     VALIDATION_RESULT=validation,
                                                     GITHUB_EVENT_NAME=event_name))
                    self.assertEqual(result.returncode, 0)
                    self.assertFalse(marker.exists())

    def test_manual_recovery_preserves_required_event_evidence(self):
        rules = (ROOT / "instructions/workflow.md").read_text()
        for phrase in ("complete change set", "pagination", "independent run",
                       "workflow_dispatch", "push or pull_request", "missing PR CI",
                       "cutoff", "run attempt", "tested SHA"):
            self.assertIn(phrase, rules)
        self.assertIn("omitted scan is not a clean scan", rules)


if __name__ == "__main__":
    unittest.main()
