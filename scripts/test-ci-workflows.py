#!/usr/bin/env python3
"""Check parsed workflow controls and execute gates against offline stubs.

Use an installed PyYAML parser, including the system Python when the active
interpreter lacks it. Never install a parser or call GitHub from these tests.
"""

import copy
import fnmatch
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
HEAD = "${{ github.event.pull_request.head.sha || github.sha }}"
TRUST = "github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository"
TOKEN = "${{ (" + TRUST + ") && secrets.GITHUB_TOKEN || '' }}"
FILES = ("ci", "semgrep", "tools", "wiki")
IDENTITY = '''actual=$(git rev-parse HEAD)
test "$actual" = "$EXPECTED_SHA"
echo "tested_sha=$actual" >> "$GITHUB_OUTPUT"
printf 'Tested SHA: %s; run: %s; attempt: %s\\n' "$actual" "$RUN_ID" "$RUN_ATTEMPT" >> "$GITHUB_STEP_SUMMARY"'''
SCAN = "semgrep ci --code --supply-chain --secrets --no-suppress-errors"
GO_COMMANDS = {
    "python3 scripts/test-ci-workflows.py", "go test -race ./...",
    "go vet ./... && go vet -tags live ./... && go vet -tags livewrite ./...",
    "go mod tidy\ngit diff --exit-code -- go.mod go.sum",
    "bash scripts/check-lengths.sh", "bash scripts/check-sdk-deps.sh",
    "GOOS=windows go vet ./... && GOOS=darwin go vet ./...",
    'go run ./cmd/vngcloud gen-docs docs/wiki\ntest -z "$(git status --porcelain -- docs/wiki)" || { git status --porcelain -- docs/wiki; exit 1; }',
    "bash scripts/wiki-sync.sh --dry-run",
}

PARSER = '''import json, sys, yaml
class Loader(yaml.BaseLoader):
    def construct_mapping(self, node, deep=False):
        result = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            if key in result:
                raise ValueError("Duplicate YAML key: " + key)
            result[key] = self.construct_object(value_node, deep=deep)
        return result
json.dump(yaml.load(sys.stdin, Loader=Loader), sys.stdout)
'''


def parser_python():
    for executable in dict.fromkeys((sys.executable, "/usr/bin/python3")):
        result = subprocess.run([executable, "-I", "-c", "import yaml"],
                                capture_output=True)
        if result.returncode == 0:
            return executable
    raise RuntimeError("PyYAML is unavailable; use a manager-provided parser")


def parse(text):
    result = subprocess.run([YAML_PYTHON, "-I", "-c", PARSER], input=text,
                            text=True, capture_output=True, check=True)
    return json.loads(result.stdout)


def shell(script, **env):
    return subprocess.run(["bash", "--noprofile", "--norc", "-eo", "pipefail",
                           "-c", script], capture_output=True, text=True,
                          env=dict(os.environ, **env))


def controls(value, conditions=(None,)):
    assert value.get("continue-on-error", "false") == "false", "failure suppression"
    assert value.get("if") in conditions, "conditional skip"
    assert value.get("shell", "bash") == "bash", "changed shell failure handling"


def runs(job):
    return [step for step in job["steps"] if "run" in step]


def triggered(workflow, name, paths):
    if name not in workflow["on"]:
        return False
    config = workflow["on"][name] or {}
    if "paths-ignore" in config:
        return any(not any(fnmatch.fnmatchcase(p, rule) for rule in config["paths-ignore"])
                   for p in paths)
    if "paths" in config:
        return any(fnmatch.fnmatchcase(p, rule) for p in paths for rule in config["paths"])
    return True


def source_policy(name, workflow):
    assert workflow["permissions"] == {"contents": "read"}
    assert "env" not in workflow
    assert workflow["concurrency"] == {
        "group": f"{name}-${{{{ github.run_id }}}}-${{{{ github.run_attempt }}}}",
        "cancel-in-progress": "false"}
    for job_id, job in workflow["jobs"].items():
        assert "strategy" not in job, "unexpected matrix"
        assert int(job["timeout-minutes"]) > 0
        assert "environment" not in job
        assert "env" not in job and "permissions" not in job
        controls(job, ("${{ always() }}",) if job_id == "required" else (None,))
        for step in job["steps"]:
            if "uses" in step:
                assert re.fullmatch(r"[\w.-]+/[\w./-]+@[0-9a-f]{40}", step["uses"]), "action pin"
        if job_id == "required":
            continue
        checkout = [s for s in job["steps"] if s.get("uses", "").startswith("actions/checkout@")]
        assert len(checkout) == 1
        controls(checkout[0])
        assert checkout[0]["with"]["ref"] == HEAD
        assert checkout[0]["with"]["persist-credentials"] == "false"
        identity = [s for s in job["steps"] if s.get("id") == "identity"]
        assert len(identity) == 1
        controls(identity[0])
        assert identity[0]["run"].strip() == IDENTITY, "identity command"
        assert identity[0]["env"] == {"EXPECTED_SHA": HEAD, "RUN_ID": "${{ github.run_id }}",
                                      "RUN_ATTEMPT": "${{ github.run_attempt }}"}
        assert job["steps"].index(checkout[0]) < job["steps"].index(identity[0])
        if name == "ci":
            assert job["outputs"] == {"tested_sha": "${{ steps.identity.outputs.tested_sha }}"}


def aggregate_policy(workflow):
    assert set(workflow["jobs"]) == {"go", "lint", "security", "required"}
    job = workflow["jobs"]["required"]
    controls(job, ("${{ always() }}",))
    assert job["name"] == "CI required"
    assert job["needs"] == ["go", "lint", "security"]
    assert len(job["steps"]) == 1
    step = job["steps"][0]
    controls(step, (None, "${{ always() }}"))
    assert "uses" not in step
    assert step["env"] == {"EXPECTED_SHA": HEAD, "NEEDS_JSON": "${{ toJSON(needs) }}"}
    return step["run"]


def scanner_policy(workflow):
    job = workflow["jobs"]["semgrep"]
    controls(job)
    assert job["container"]["image"] == "semgrep/semgrep:1.180.0@sha256:529ee8a277ec8adc5b534d7c74eea0a47e9de21d62852b6ba7ac6ba9566845c3"
    assert job["steps"][0]["with"]["fetch-depth"] == "0"
    step = job["steps"][-1]
    controls(step)
    assert step["env"] == {"TRUSTED_SOURCE": "${{ " + TRUST + " }}",
                            "SEMGREP_APP_TOKEN": "${{ secrets.SEMGREP_APP_TOKEN }}"}
    assert "env" not in job
    assert all("SEMGREP_APP_TOKEN" not in s.get("env", {}) for s in job["steps"][:-1])
    assert step["run"].strip().splitlines()[-1] == SCAN, "scanner failure suppression"
    return step["run"]


def ci_gates(workflow):
    job = workflow["jobs"]["go"]
    steps = [s for s in runs(job) if s.get("id") != "identity"]
    for step in steps:
        controls(step, (None, "${{ !cancelled() }}"))
    assert {s["run"].strip() for s in steps} == GO_COMMANDS, "mandatory executable commands"
    lint = workflow["jobs"]["lint"]
    actions = [s for s in lint["steps"] if s.get("uses", "").startswith("golangci/golangci-lint-action@")]
    assert len(actions) == 1
    controls(actions[0])
    assert actions[0]["with"]["version"] == "${{ env.GOLANGCI_LINT }}"
    security = workflow["jobs"]["security"]
    assert security["steps"][0]["with"]["fetch-depth"] == "0"
    for cmd in ("~/tools/gitleaks detect --redact --no-banner", "~/tools/govulncheck ./..."):
        matches = [s for s in runs(security) if s["run"].strip() == cmd]
        assert len(matches) == 1, "mandatory security command"
        controls(matches[0], (None, "${{ !cancelled() }}"))


def wiki_policy(workflow):
    source_policy("wiki", workflow)
    assert set(workflow["jobs"]) == {"preview"}
    job = workflow["jobs"]["preview"]
    assert len(job["steps"]) == 3
    step = job["steps"][-1]
    controls(step)
    assert step["run"].strip() == "bash scripts/wiki-sync.sh --dry-run"
    assert "env" not in workflow and "env" not in job and "env" not in step
    assert "secrets." not in json.dumps(workflow)
    return step["run"]


class WorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        global YAML_PYTHON
        YAML_PYTHON = parser_python()
        cls.texts = {name: (ROOT / f".github/workflows/{name}.yml").read_text() for name in FILES}
        cls.workflows = {name: parse(text) for name, text in cls.texts.items()}

    def rejected(self, policy, original, mutate):
        changed = copy.deepcopy(original)
        mutate(changed)
        with self.assertRaises(AssertionError):
            policy(changed)

    def test_triggers_and_source_controls(self):
        for name, workflow in self.workflows.items():
            source_policy(name, workflow)
            self.assertEqual(set(workflow["on"]), {"push", "pull_request", "workflow_dispatch"}
                             | ({"schedule"} if name == "tools" else set()))
            for event in ("push", "pull_request"):
                self.assertEqual(workflow["on"][event]["branches"], ["master"])
        self.assertEqual(self.workflows["tools"]["on"]["schedule"], [{"cron": "0 1 * * 1"}])
        for event in ("push", "pull_request"):
            self.assertEqual(self.workflows["ci"]["on"][event], {"branches": ["master"]})

    def test_yaml_parser_rejects_duplicate_controls(self):
        with self.assertRaises(subprocess.CalledProcessError):
            parse("permissions:\n  contents: read\n  contents: write\n")

    def test_event_path_matrix(self):
        cases = [(["nat/nat.go"], {"ci", "semgrep"}), (["README.md"], {"ci"}),
                 (["docs/wiki/Home.md"], {"ci", "wiki"}),
                 ([".tool-versions"], {"ci", "semgrep", "tools"}),
                 (["go.sum"], {"ci", "semgrep", "tools"}),
                 ([".github/workflows/wiki.yml"], set(FILES)),
                 (["docs/example.json"], {"ci", "semgrep"}), (["LICENSE"], {"ci"}),
                 (["nested/LICENSE"], {"ci", "semgrep"}),
                 (["scripts/test-ci-workflows.py"], {"ci", "semgrep", "tools"}),
                 (["scripts/test-wiki-publication.py"], {"ci", "semgrep", "wiki"}),
                 (["scripts/ci-evidence.py"], {"ci", "semgrep", "wiki"})]
        for event in ("push", "pull_request"):
            for paths, expected in cases:
                self.assertEqual({n for n, w in self.workflows.items() if triggered(w, event, paths)}, expected)
        for name, workflow in self.workflows.items():
            self.assertTrue(triggered(workflow, "workflow_dispatch", ["README.md"]))
            self.assertEqual(triggered(workflow, "schedule", []), name == "tools")

    def test_every_identity_command_rejects_merge_checkout(self):
        with tempfile.TemporaryDirectory() as temp:
            git = Path(temp) / "git"
            git.write_text('#!/bin/sh\nprintf "%s\\n" "$ACTUAL_SHA"\n')
            git.chmod(0o700)
            for workflow in self.workflows.values():
                for job in workflow["jobs"].values():
                    for step in job["steps"]:
                        if step.get("id") != "identity":
                            continue
                        for actual in ("head", "merge"):
                            result = shell(step["run"], PATH=temp + ":" + os.environ["PATH"],
                                           EXPECTED_SHA="head", ACTUAL_SHA=actual, RUN_ID="1", RUN_ATTEMPT="1",
                                           GITHUB_OUTPUT=temp + "/output", GITHUB_STEP_SUMMARY=temp + "/summary")
                            self.assertEqual(result.returncode == 0, actual == "head")

    def test_ci_commands_and_mutations(self):
        ci = self.workflows["ci"]
        ci_gates(ci)
        mutants = [self.texts["ci"].replace("run: go test -race ./...", "run: true # go test -race ./..."),
                   self.texts["ci"].replace("group: ci-${{ github.run_id }}-${{ github.run_attempt }}", "group: ci-${{ github.ref }}"),
                   self.texts["ci"].replace("actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/checkout@v7"),
                   self.texts["ci"].replace('test "$actual" = "$EXPECTED_SHA"', 'test "$actual" = "$EXPECTED_SHA" || true')]
        for text in mutants:
            with self.assertRaises(AssertionError):
                workflow = parse(text)
                source_policy("ci", workflow)
                ci_gates(workflow)
        # Mutate only lint, so another job's valid identity cannot mask it.
        self.rejected(lambda w: source_policy("ci", w), ci,
                      lambda w: w["jobs"]["lint"]["steps"][1].update(
                          run=IDENTITY.replace('test "$actual" = "$EXPECTED_SHA"',
                                               'test "$actual" = "$EXPECTED_SHA" || true')))
        make = (ROOT / "Makefile").read_text()
        self.assertRegex(make, r"(?m)^check: .*ci-workflows-check")
        self.assertIn("python3 scripts/test-ci-workflows.py", make)

    def test_aggregate_controls_and_mutations(self):
        ci = self.workflows["ci"]
        aggregate_policy(ci)
        for scope in ("job", "step"):
            for field, value in (("continue-on-error", "true"), ("continue-on-error", "${{ true }}"),
                                 ("if", "github.event_name == 'push'"), ("if", "${{ success() }}")):
                def mutate(w, scope=scope, field=field, value=value):
                    target = w["jobs"]["required"]
                    if scope == "step":
                        target = target["steps"][0]
                    target[field] = value
                self.rejected(aggregate_policy, ci, mutate)

    def test_aggregate_rejects_non_success_and_wrong_identity(self):
        script = aggregate_policy(self.workflows["ci"])
        good = {n: {"result": "success", "outputs": {"tested_sha": "head"}} for n in ("go", "lint", "security")}
        cases = [(good, True), ({}, False), ({**good, "unexpected": good["go"]}, False)]
        for name in good:
            for status in ("failure", "skipped", "cancelled", "pending", "neutral", "timed_out", ""):
                bad = copy.deepcopy(good)
                bad[name]["result"] = status
                cases.append((bad, False))
            bad = copy.deepcopy(good)
            bad[name]["outputs"]["tested_sha"] = "merge"
            cases.append((bad, False))
            bad = copy.deepcopy(good)
            del bad[name]
            cases.append((bad, False))
        for needs, accepted in cases:
            self.assertEqual(shell(script, NEEDS_JSON=json.dumps(needs), EXPECTED_SHA="head").returncode == 0, accepted)
        self.assertNotEqual(shell(script, NEEDS_JSON="{", EXPECTED_SHA="head").returncode, 0)

    def scan_result(self, script, trusted, token, exit_code):
        with tempfile.TemporaryDirectory() as temp:
            scanner = Path(temp) / "semgrep"
            scanner.write_text('#!/bin/sh\ntouch "$SCAN_MARKER"\nexit "$SCAN_EXIT"\n')
            scanner.chmod(0o700)
            marker = Path(temp) / "scanned"
            result = shell(script, TRUSTED_SOURCE=trusted, SEMGREP_APP_TOKEN=token,
                           SCAN_MARKER=str(marker), SCAN_EXIT=str(exit_code), PATH=temp + ":" + os.environ["PATH"])
            return result, marker.exists()

    def test_semgrep_failure_trust_and_mutations(self):
        workflow = self.workflows["semgrep"]
        script = scanner_policy(workflow)
        for trusted, token, expected in (("true", "synthetic", 0), ("true", "", 1),
                                         ("false", "synthetic", 1), ("false", "", 1)):
            result, scanned = self.scan_result(script, trusted, token, 0)
            self.assertEqual(result.returncode, expected)
            self.assertEqual(scanned, expected == 0)
            if trusted == "false":
                self.assertIn("same-repository branch", result.stderr)
            elif not token:
                self.assertIn("requires SEMGREP_APP_TOKEN", result.stderr)
        result, scanned = self.scan_result(script, "true", "synthetic", 23)
        self.assertTrue(scanned)
        self.assertEqual(result.returncode, 23)
        # The same failing stub exposes the formerly accepted shell suppression.
        self.assertEqual(self.scan_result(script.rstrip() + " || true\n", "true", "synthetic", 23)[0].returncode, 0)
        for field, value in (("run", script.rstrip() + " || true\n"), ("continue-on-error", "true"),
                             ("if", "github.event_name == 'push'"), ("shell", "bash {0}")):
            self.rejected(scanner_policy, workflow, lambda w, f=field, v=value: w["jobs"]["semgrep"]["steps"][-1].update({f: v}))
        self.rejected(scanner_policy, workflow,
                      lambda w: w["jobs"]["semgrep"]["steps"][-1]["env"].update(TRUSTED_SOURCE="${{ true }}"))
        for field, value in (("continue-on-error", "true"), ("if", "github.event_name == 'push'")):
            self.rejected(scanner_policy, workflow,
                          lambda w, f=field, v=value: w["jobs"]["semgrep"].update({f: v}))

    def test_tools_fork_token_and_unauthenticated_script(self):
        step = self.workflows["tools"]["jobs"]["outdated"]["steps"][-1]
        self.assertEqual(step["env"], {"GH_TOKEN": TOKEN})
        self.assertEqual(step["run"].strip(), "bash scripts/tools-outdated.sh")
        # Exact expression validation binds this truth table to the workflow.
        for event, same_repo, expected in (("pull_request", False, ""), ("pull_request", True, "synthetic"),
                                           ("push", True, "synthetic"), ("workflow_dispatch", True, "synthetic"),
                                           ("schedule", True, "synthetic")):
            self.assertEqual("synthetic" if event != "pull_request" or same_repo else "", expected)
        pins = dict(line.split() for line in (ROOT / ".tool-versions").read_text().splitlines() if line.strip())
        responses = {"https://go.dev/dl/?mode=json": [{"version": "go" + pins["golang"]}],
                     "https://pypi.org/pypi/semgrep/json": {"info": {"version": pins["semgrep"]}},
                     "https://proxy.golang.org/golang.org/x/vuln/@latest": {"Version": "v" + pins["govulncheck"]}}
        for repo, pin in (("golangci/golangci-lint", "golangci-lint"), ("gitleaks/gitleaks", "gitleaks")):
            responses[f"https://api.github.com/repos/{repo}/releases/latest"] = {"tag_name": "v" + pins[pin]}
        for text in self.texts.values():
            for repo, version in re.findall(r'uses: ([^@ ]+)@[0-9a-f]{40} # v([0-9.]+)', text):
                responses[f"https://api.github.com/repos/{repo}/releases/latest"] = {"tag_name": "v" + version}
        with tempfile.TemporaryDirectory() as temp:
            curl = Path(temp) / "curl"
            curl.write_text(f'#!{sys.executable}\nimport json, os, sys\n'
                            'assert not any("Authorization:" in a for a in sys.argv)\n'
                            'print(json.dumps(json.loads(os.environ["MOCK_RESPONSES"])[sys.argv[-1]]))\n')
            curl.chmod(0o700)
            go = Path(temp) / "go"
            go.write_text('#!/bin/sh\nexit 0\n')
            go.chmod(0o700)
            result = shell(step["run"], GH_TOKEN="", MOCK_RESPONSES=json.dumps(responses),
                           PATH=temp + ":" + os.environ["PATH"])
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("tools-outdated: all pins current", result.stdout)

    def test_wiki_has_no_publication_path(self):
        workflow = self.workflows["wiki"]
        preview = wiki_policy(workflow)
        for mutate in (lambda w: w["permissions"].update(contents="write"),
                       lambda w: w["jobs"].update(publisher=copy.deepcopy(w["jobs"]["preview"])),
                       lambda w: w["jobs"]["preview"]["steps"][-1].update(run="bash scripts/wiki-sync.sh"),
                       lambda w: w["jobs"]["preview"].update(environment="wiki"),
                       lambda w: w["jobs"]["preview"].update(permissions={"contents": "write"}),
                       lambda w: w["jobs"]["preview"]["steps"][-1].update(env={"GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}"})):
            self.rejected(wiki_policy, workflow, mutate)
        with tempfile.TemporaryDirectory() as temp:
            scripts = Path(temp) / "scripts"
            scripts.mkdir()
            (scripts / "wiki-sync.sh").write_text('#!/bin/bash\nif [[ "$*" != --dry-run ]]; then\n  touch "$WRITE_MARKER"; exit 1\nfi\n')
            for event in ("push", "workflow_dispatch", "pull_request"):
                for validation in ("pending", "failure"):
                    marker = Path(temp) / "write"
                    result = subprocess.run(["bash", "-e", "-c", preview], cwd=temp,
                                            env=dict(os.environ, WRITE_MARKER=str(marker), VALIDATION_RESULT=validation, GITHUB_EVENT_NAME=event))
                    self.assertEqual(result.returncode, 0)
                    self.assertFalse(marker.exists())

    def test_manual_recovery_preserves_required_event_evidence(self):
        rules = (ROOT / "instructions/workflow.md").read_text()
        for phrase in ("complete change set", "pagination", "independent run", "workflow_dispatch",
                       "push or pull_request", "missing PR CI", "cutoff", "run attempt", "tested SHA"):
            self.assertIn(phrase, rules)
        self.assertIn("omitted scan is not a clean scan", rules)


if __name__ == "__main__":
    unittest.main()
