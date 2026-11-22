#!/usr/bin/env python3
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
WIKI_GUARD = "github.event_name == 'push' && github.ref == 'refs/heads/master'"
WIKI_GROUP = "wiki-publish-${{ github.sha }}"
WIKI_IDENTITY = '''actual=$(git rev-parse HEAD)
test "$actual" = "$EXPECTED_SHA"
test "$actual" = "$TESTED_SHA"'''
WIKI_ENV = {
    "GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}",
    "WIKI_SOURCE_SHA": "${{ github.sha }}",
    "WIKI_SOURCE_RUN_URL": "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}/attempts/${{ github.run_attempt }}",
}
TRUST = "github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository"
TOKEN = "${{ (" + TRUST + ") && secrets.GITHUB_TOKEN || '' }}"
FILES = ("ci", "codeql", "tools", "wiki")
IDENTITY = '''actual=$(git rev-parse HEAD)
test "$actual" = "$EXPECTED_SHA"
echo "tested_sha=$actual" >> "$GITHUB_OUTPUT"
printf 'Tested SHA: %s; run: %s; attempt: %s\\n' "$actual" "$RUN_ID" "$RUN_ATTEMPT" >> "$GITHUB_STEP_SUMMARY"'''
GO_COMMANDS = {
    "python3 scripts/test-ci-workflows.py", "go test -race ./...",
    "python3 scripts/test-wiki-publication.py",
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


def workflow_paths():
    paths = sorted(path for pattern in ("*.yml", "*.yaml")
                   for path in (ROOT / ".github/workflows").glob(pattern))
    actual = {path.name for path in paths}
    expected = {f"{name}.yml" for name in FILES}
    assert actual == expected, ("workflow inventory differs from reviewed files: "
                                f"unexpected {sorted(actual - expected)}; missing {sorted(expected - actual)}")
    return paths


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
    assert "pull_request_target" not in workflow["on"]
    assert "env" not in workflow
    assert workflow["concurrency"] == {
        "group": f"{name}-${{{{ github.run_id }}}}-${{{{ github.run_attempt }}}}",
        "cancel-in-progress": "false"}
    for job_id, job in workflow["jobs"].items():
        assert "strategy" not in job, "unexpected matrix"
        assert int(job["timeout-minutes"]) > 0
        if name == "ci" and job_id == "wiki":
            wiki_publish_policy(job)
            continue
        assert "environment" not in job
        assert "env" not in job
        if name == "codeql":
            assert job["permissions"] == {"contents": "read", "security-events": "write"}
        else:
            assert "permissions" not in job
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
    assert set(workflow["jobs"]) == {"go", "lint", "security", "required", "wiki"}
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


def codeql_policy(workflow):
    source_policy("codeql", workflow)
    assert workflow["on"] == {"push": {"branches": ["master"]},
                              "schedule": [{"cron": "0 3 * * 1"}],
                              "workflow_dispatch": ""}
    assert set(workflow["jobs"]) == {"analyze"}
    job = workflow["jobs"]["analyze"]
    steps = job["steps"]
    assert len(steps) == 6
    for step in steps:
        controls(step)
    setup = steps[2]
    assert setup["uses"].startswith("actions/setup-go@")
    assert setup["with"]["go-version-file"] == "go.mod"
    init, build, analyze = steps[3:]
    assert init["uses"].startswith("github/codeql-action/init@")
    assert init["with"] == {"languages": "go", "build-mode": "manual"}
    assert build["run"].strip() == "go build ./..."
    assert build["env"] == {"GOPROXY": "https://proxy.golang.org", "GOTOOLCHAIN": "local"}
    assert analyze["uses"] == init["uses"].replace("/init@", "/analyze@")
    assert analyze["with"] == {"category": "/language:go"}
    assert "secrets." not in json.dumps(workflow)


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


def wiki_publish_policy(job):
    assert job["name"] == "Wiki publish"
    controls(job, (WIKI_GUARD,))
    assert job["needs"] == ["go", "required"]
    assert job["environment"] == "wiki"
    assert job["permissions"] == {"contents": "write"}
    assert job["concurrency"] == {"group": WIKI_GROUP, "cancel-in-progress": "false"}
    assert int(job["timeout-minutes"]) > 0
    assert "env" not in job and "strategy" not in job
    assert len(job["steps"]) == 3
    checkout, identity, publish = job["steps"]
    for step in job["steps"]:
        controls(step)
        assert "${{" not in step.get("run", ""), "shell context interpolation"
    assert re.fullmatch(r"actions/checkout@[0-9a-f]{40}", checkout["uses"])
    assert checkout["with"] == {"ref": "${{ github.sha }}", "persist-credentials": "false"}
    assert "env" not in checkout
    assert identity["id"] == "identity"
    assert identity["run"].strip() == WIKI_IDENTITY
    assert identity["env"] == {"EXPECTED_SHA": "${{ github.sha }}",
                               "TESTED_SHA": "${{ needs.go.outputs.tested_sha }}"}
    assert publish["run"].strip() == "bash scripts/wiki-sync.sh"
    assert publish["env"] == WIKI_ENV


def publication_policy(workflows):
    assert "wiki" in workflows["ci"]["jobs"], "missing CI publisher"
    publishers = []
    writers = []
    for name, workflow in workflows.items():
        source_policy(name, workflow)
        for job_id, job in workflow["jobs"].items():
            if job.get("permissions", {}).get("contents") == "write":
                writers.append((name, job_id))
            if any("wiki-sync.sh" in s.get("run", "") and "--dry-run" not in s["run"]
                   for s in job["steps"]):
                publishers.append((name, job_id))
    assert writers == [("ci", "wiki")]
    assert publishers == [("ci", "wiki")]
    ci = workflows["ci"]
    assert ci["jobs"]["wiki"]["steps"][0]["uses"] == ci["jobs"]["go"]["steps"][0]["uses"]
    aggregate_policy(ci)
    ci_gates(ci)
    wiki_policy(workflows["wiki"])


class WorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        global YAML_PYTHON
        YAML_PYTHON = parser_python()
        cls.texts = {path.stem: path.read_text() for path in workflow_paths()}
        cls.workflows = {name: parse(text) for name, text in cls.texts.items()}

    def rejected(self, policy, original, mutate):
        changed = copy.deepcopy(original)
        mutate(changed)
        with self.assertRaises(AssertionError):
            policy(changed)

    def test_triggers_and_source_controls(self):
        for name, workflow in self.workflows.items():
            source_policy(name, workflow)
            events = {"push", "workflow_dispatch"}
            if name != "codeql":
                events.add("pull_request")
            if name in ("tools", "codeql"):
                events.add("schedule")
            self.assertEqual(set(workflow["on"]), events)
            for event in events & {"push", "pull_request"}:
                self.assertEqual(workflow["on"][event]["branches"], ["master"])
        self.assertEqual(self.workflows["tools"]["on"]["schedule"], [{"cron": "0 1 * * 1"}])
        for event in ("push", "pull_request"):
            self.assertEqual(self.workflows["ci"]["on"][event], {"branches": ["master"]})

    def test_yaml_parser_rejects_duplicate_controls(self):
        with self.assertRaises(subprocess.CalledProcessError):
            parse("permissions:\n  contents: read\n  contents: write\n")

    def test_event_path_matrix(self):
        cases = [(["nat/nat.go"], {"ci"}), (["README.md"], {"ci"}),
                 (["docs/wiki/Home.md"], {"ci", "wiki"}),
                 ([".tool-versions"], {"ci", "tools"}),
                 (["go.sum"], {"ci", "tools"}),
                 ([".github/workflows/wiki.yml"], {"ci", "tools", "wiki"}),
                 ([".github/workflows/codeql.yml"], {"ci", "tools"}),
                 (["docs/example.json"], {"ci"}), (["LICENSE"], {"ci"}),
                 (["nested/LICENSE"], {"ci"}),
                 (["scripts/test-ci-workflows.py"], {"ci", "tools"}),
                 (["scripts/test-wiki-publication.py"], {"ci", "wiki"}),
                 (["scripts/ci-evidence.py"], {"ci", "wiki"})]
        for event in ("push", "pull_request"):
            for paths, expected in cases:
                if event == "push":
                    expected = expected | {"codeql"}
                self.assertEqual({n for n, w in self.workflows.items() if triggered(w, event, paths)}, expected)
        for name, workflow in self.workflows.items():
            self.assertTrue(triggered(workflow, "workflow_dispatch", ["README.md"]))
            self.assertEqual(triggered(workflow, "schedule", []), name in ("tools", "codeql"))

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
                                           EXPECTED_SHA="head", TESTED_SHA="head", ACTUAL_SHA=actual, RUN_ID="1", RUN_ATTEMPT="1",
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
        self.rejected(lambda w: source_policy("ci", w), ci,
                      lambda w: w["jobs"]["lint"]["steps"][1].update(
                          run=IDENTITY.replace('test "$actual" = "$EXPECTED_SHA"',
                                               'test "$actual" = "$EXPECTED_SHA" || true')))
        make = (ROOT / "Makefile").read_text()
        self.assertRegex(make, r"(?m)^check: .*ci-workflows-check")
        self.assertIn("python3 scripts/test-ci-workflows.py", make)
        self.assertIn("python3 scripts/test-wiki-publication.py", make)

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

    def test_workflow_inventory(self):
        self.assertEqual({path.name for path in workflow_paths()}, {f"{name}.yml" for name in FILES})

    def test_unreviewed_workflows_are_rejected(self):
        unsafe = """name: Unsafe
on: pull_request_target
permissions: write-all
jobs:
  unsafe:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@main
"""
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            scripts = root / "scripts"
            scripts.mkdir()
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            for name in ("check-workflow-pins.py", "test-ci-workflows.py"):
                (scripts / name).write_text((ROOT / "scripts" / name).read_text())
            for name, text in self.texts.items():
                (workflows / f"{name}.yml").write_text(text)
            commands = (("check-workflow-pins.py",),
                        ("check-workflow-pins.py", "--list"),
                        ("test-ci-workflows.py", "WorkflowTests.test_workflow_inventory"))
            for command in commands:
                result = subprocess.run([sys.executable, str(scripts / command[0]), *command[1:]],
                                        cwd=root, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            for name, text in (("unsafe.yaml", unsafe), ("unsafe.yml", unsafe),
                               ("ci.yaml", self.texts["ci"]), ("extra.yml", self.texts["wiki"])):
                path = workflows / name
                path.write_text(text)
                try:
                    for command in commands:
                        with self.subTest(workflow=name, command=command):
                            result = subprocess.run(
                                [sys.executable, str(scripts / command[0]), *command[1:]],
                                cwd=root, capture_output=True, text=True)
                            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                            self.assertIn("inventory", result.stderr.lower())
                            self.assertIn(name, result.stderr)
                finally:
                    path.unlink()

    def test_codeql_controls_and_mutations(self):
        self.assertIn("codeql", self.workflows, "Missing CodeQL workflow")
        workflow = self.workflows["codeql"]
        codeql_policy(workflow)
        checkout = workflow["jobs"]["analyze"]["steps"][0]
        self.assertEqual(checkout["uses"], self.workflows["ci"]["jobs"]["go"]["steps"][0]["uses"])
        mutants = [lambda w: w["permissions"].update(contents="write"),
                   lambda w: w["on"].update(pull_request_target={}),
                   lambda w: w["on"].update(pull_request={"branches": ["master"]}),
                   lambda w: w["on"]["schedule"][0].update(cron="0 1 * * 1"),
                   lambda w: w["jobs"]["analyze"]["permissions"].update(actions="write"),
                   lambda w: w["jobs"]["analyze"]["permissions"].pop("contents"),
                   lambda w: w["jobs"]["analyze"]["permissions"].pop("security-events"),
                   lambda w: w["jobs"]["analyze"]["steps"][0]["with"].update({"persist-credentials": "true"}),
                   lambda w: w["jobs"]["analyze"]["steps"][3].update(uses="github/codeql-action/init@v4"),
                   lambda w: w["jobs"]["analyze"]["steps"][3]["with"].update(languages="javascript"),
                   lambda w: w["jobs"]["analyze"]["steps"][4].update(run="true"),
                   lambda w: w["jobs"]["analyze"]["steps"][-1].update({"continue-on-error": "true"})]
        for mutate in mutants:
            self.rejected(codeql_policy, workflow, mutate)

    def test_action_pin_checker(self):
        checker = ROOT / "scripts/check-workflow-pins.py"
        self.assertTrue(checker.is_file(), "Missing workflow pin checker")
        result = subprocess.run([sys.executable, str(checker)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        text = self.texts["codeql"]
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "codeql.yml"
            for changed in (text, re.sub(r"(codeql-action/init)@[0-9a-f]{40}", r"\1@v4", text),
                            re.sub(r"(codeql-action/init@[0-9a-f]{40}) # v[0-9.]+", r"\1", text),
                            re.sub(r"(codeql-action/analyze@)[0-9a-f]{40}", r"\g<1>" + "0" * 40, text)):
                path.write_text(changed)
                result = subprocess.run([sys.executable, str(checker), str(path)], capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, changed == text, result.stdout + result.stderr)

    def test_tools_fork_token_and_unauthenticated_script(self):
        step = self.workflows["tools"]["jobs"]["outdated"]["steps"][-1]
        self.assertEqual(step["env"], {"GH_TOKEN": TOKEN})
        self.assertEqual(step["run"].strip(), "bash scripts/tools-outdated.sh")
        for event, same_repo, expected in (("pull_request", False, ""), ("pull_request", True, "synthetic"),
                                           ("push", True, "synthetic"), ("workflow_dispatch", True, "synthetic"),
                                           ("schedule", True, "synthetic")):
            self.assertEqual("synthetic" if event != "pull_request" or same_repo else "", expected)
        pins = dict(line.split() for line in (ROOT / ".tool-versions").read_text().splitlines() if line.strip())
        responses = {"https://go.dev/dl/?mode=json": [{"version": "go" + pins["golang"]}],
                     "https://proxy.golang.org/golang.org/x/vuln/@latest": {"Version": "v" + pins["govulncheck"]}}
        for repo, pin in (("golangci/golangci-lint", "golangci-lint"), ("gitleaks/gitleaks", "gitleaks")):
            responses[f"https://api.github.com/repos/{repo}/releases/latest"] = {"tag_name": "v" + pins[pin]}
        for text in self.texts.values():
            for repo, version in re.findall(r'uses: ([^@ ]+)@[0-9a-f]{40} # v([0-9.]+)', text):
                repo = "/".join(repo.split("/")[:2])
                if repo == "github/codeql-action":
                    codeql_url = f"https://api.github.com/repos/{repo}/releases?per_page=100"
                    responses[codeql_url] = [
                        {"tag_name": "codeql-bundle-v2.99.0"},
                        {"tag_name": "v3.99.0"},
                        {"tag_name": "v5.0.0", "prerelease": True},
                        {"tag_name": "v6.0.0", "draft": True},
                        {"tag_name": "v" + version}]
                else:
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
            self.assertEqual(result.stdout.count("current: github/codeql-action "), 1)
            responses[codeql_url].append({"tag_name": "v99.0.0"})
            result = shell(step["run"], GH_TOKEN="", MOCK_RESPONSES=json.dumps(responses),
                           PATH=temp + ":" + os.environ["PATH"])
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertEqual(result.stdout.count("outdated: github/codeql-action "), 1)

    def test_wiki_preview_controls_and_mutations(self):
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

    def test_wiki_publication_event_and_ref_matrix(self):
        guard = self.workflows["ci"]["jobs"]["wiki"]["if"]
        expression = guard.replace("github.event_name", '"$EVENT_NAME"').replace("github.ref", '"$SOURCE_REF"')
        for event in ("push", "workflow_dispatch", "pull_request"):
            for ref in ("refs/heads/master", "refs/heads/other", "refs/tags/master", "refs/pull/1/merge"):
                with self.subTest(event=event, ref=ref):
                    result = shell("[[ " + expression + " ]]", EVENT_NAME=event, SOURCE_REF=ref)
                    self.assertEqual(result.returncode == 0, event == "push" and ref == "refs/heads/master")

    def test_wiki_concurrency_is_scoped_to_source_sha(self):
        job = self.workflows["ci"]["jobs"]["wiki"]
        self.assertEqual(job["concurrency"], {"group": WIKI_GROUP, "cancel-in-progress": "false"})
        group = job["concurrency"]["group"]
        self.assertNotEqual(group.replace("${{ github.sha }}", "a" * 40),
                            group.replace("${{ github.sha }}", "b" * 40))
        for value in ("wiki-publish", "wiki-publish-${{ github.ref }}", "wiki-publish-${{ github.run_id }}"):
            with self.subTest(group=value):
                self.rejected(wiki_publish_policy, job, lambda j: j["concurrency"].update(group=value))

    def test_wiki_publisher_controls_and_mutations(self):
        publication_policy(self.workflows)
        ci = self.workflows["ci"]
        mutants = [
            lambda j: j.update(needs=["go"]),
            lambda j: j.update(needs=["required"]),
            lambda j: j.pop("if"),
            lambda j: j.update({"if": "github.ref == 'refs/heads/master'"}),
            lambda j: j.update({"if": "github.event_name != 'pull_request'"}),
            lambda j: j.update({"if": WIKI_GUARD.replace("&&", "||")}),
            lambda j: j.update({"if": "always() && (" + WIKI_GUARD + ")"}),
            lambda j: j.update({"if": WIKI_GUARD.replace("== 'push'", "!= 'pull_request'", 1)}),
            lambda j: j.update({"if": WIKI_GUARD + " || github.event_name == 'pull_request'"}),
            lambda j: j.update(environment="other"),
            lambda j: j["permissions"].update(actions="write"),
            lambda j: j["permissions"].update(checks="read"),
            lambda j: j["concurrency"].update({"cancel-in-progress": "true"}),
            lambda j: j["concurrency"].update(group="other"),
            lambda j: j.update({"timeout-minutes": "0"}),
            lambda j: j["steps"][0]["with"].update({"persist-credentials": "true"}),
            lambda j: j["steps"][0]["with"].update(ref=HEAD),
            lambda j: j["steps"][1].update(run='test "$EXPECTED_SHA" = "$TESTED_SHA"'),
            lambda j: j["steps"][1]["env"].update(TESTED_SHA="${{ github.sha }}"),
            lambda j: j["steps"][2]["env"].pop("WIKI_SOURCE_SHA"),
            lambda j: j["steps"][2]["env"].update(WIKI_SOURCE_RUN_URL="${{ github.server_url }}"),
            lambda j: j["steps"][2].update(run="bash scripts/wiki-sync.sh ${{ github.sha }}"),
        ]
        for index, mutate in enumerate(mutants):
            with self.subTest(mutant=index):
                self.rejected(publication_policy, self.workflows,
                              lambda w: mutate(w["ci"]["jobs"]["wiki"]))
        self.rejected(aggregate_policy, ci, lambda w: w["jobs"]["required"]["needs"].append("wiki"))

    def test_publication_inventory_and_preview_mutations(self):
        publication_policy(self.workflows)
        for name in self.workflows:
            with self.subTest(workflow=name):
                self.rejected(publication_policy, self.workflows,
                              lambda w: w[name]["permissions"].update(contents="write"))
                self.rejected(publication_policy, self.workflows,
                              lambda w: w[name]["permissions"].update(actions="write"))
                self.rejected(publication_policy, self.workflows,
                              lambda w: w[name]["jobs"].update(second=copy.deepcopy(w["ci"]["jobs"]["wiki"])))
                job_id = next(j for j in self.workflows[name]["jobs"] if j != "wiki")
                self.rejected(publication_policy, self.workflows,
                              lambda w: w[name]["jobs"][job_id].update(permissions={"contents": "write"}))
        for name, job_id in (("ci", "go"), ("wiki", "preview")):
            self.rejected(publication_policy, self.workflows,
                          lambda w: next(s for s in w[name]["jobs"][job_id]["steps"]
                                         if "--dry-run" in s.get("run", "")).update(run="bash scripts/wiki-sync.sh"))

    def test_wiki_identity_rejects_untested_source(self):
        publication_policy(self.workflows)
        script = self.workflows["ci"]["jobs"]["wiki"]["steps"][1]["run"]
        with tempfile.TemporaryDirectory() as temp:
            git = Path(temp) / "git"
            git.write_text('#!/bin/sh\nprintf "%s\\n" "$ACTUAL_SHA"\n')
            git.chmod(0o700)
            for actual, expected, tested, accepted in (("head", "head", "head", True),
                                                       ("other", "head", "head", False),
                                                       ("head", "head", "other", False),
                                                       ("head", "head", "", False)):
                result = shell(script, PATH=temp + ":" + os.environ["PATH"],
                               ACTUAL_SHA=actual, EXPECTED_SHA=expected, TESTED_SHA=tested)
                self.assertEqual(result.returncode == 0, accepted)

    def test_manual_recovery_preserves_required_event_evidence(self):
        rules = (ROOT / "instructions/workflow.md").read_text()
        for phrase in ("complete change set", "pagination", "independent run", "workflow_dispatch",
                       "push or pull_request", "missing PR CI", "cutoff", "run attempt", "tested SHA"):
            self.assertIn(phrase, rules)
        self.assertIn("omitted scan is not a clean scan", rules)


if __name__ == "__main__":
    unittest.main()
