#!/usr/bin/env bash
set -Eeuo pipefail

exec python3 -I - "$0" "$@" <<'PY'
import json
import os
from pathlib import Path
import re
import sys
import tomllib


def fail(message):
    print("codex-agent: " + message, file=sys.stderr)
    sys.exit(2)


def toml_string(value):
    # JSON basic strings share TOML escapes except DEL, which TOML forbids raw.
    return json.dumps(value, ensure_ascii=False).replace("\x7f", "\\u007f")


script = Path(sys.argv[1]).resolve()
args = sys.argv[2:]
options = {}
no_git = False
while args and args[0].startswith("--"):
    option = args.pop(0)
    if option == "--no-git":
        no_git = True
    elif option in ("--model", "--effort", "--resume"):
        if not args or option in options:
            fail("missing or repeated option value")
        options[option] = args.pop(0)
    else:
        fail("unknown option")
if len(args) != 4:
    fail("usage: --model <m> --effort <e> [--no-git] "
         "[--resume <session-id>] <role> <dir> <input-file> <report-file>")
model = options.get("--model", "")
effort = options.get("--effort", "")
session = options.get("--resume")
role, directory, input_file, report_file = args
if not re.fullmatch(r"gpt-[0-9]+(\.[0-9]+)?-[a-z]+", model):
    fail("a valid --model is required")
if effort not in ("low", "medium", "high", "xhigh"):
    fail("a valid --effort is required")
if not re.fullmatch(r"[a-z_]+", role):
    fail("invalid role")
if session is not None and not re.fullmatch(
        r"[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", session):
    fail("invalid session id")
directory = Path(directory).resolve()
input_file = Path(input_file).resolve()
report_file = Path(report_file).absolute()
role_file = directory / ".codex" / "agents" / (role + ".toml")
try:
    try:
        source = role_file.open("rb")
    except FileNotFoundError:
        source = (script.parent.parent / ".codex" / "agents" /
                  (role + ".toml")).open("rb")
    with source:
        config = tomllib.load(source)
except (OSError, ValueError):
    fail("cannot load role TOML")
sandbox = config.get("sandbox_mode")
instructions = config.get("developer_instructions")
if sandbox not in ("read-only", "workspace-write"):
    fail("invalid role sandbox_mode")
if not isinstance(instructions, str) or not instructions.strip():
    fail("role developer_instructions must be a nonempty string")
command = ["codex", "exec"]
if session is not None:
    command += ["resume", session]
command += ["-m", model, "-c", "model_reasoning_effort=" + effort]
if session is None:
    command += ["-c", "developer_instructions=" + toml_string(instructions),
                "--sandbox", sandbox, "-C", str(directory)]
else:
    command += ["-c", "sandbox_mode=" + toml_string(sandbox)]
if sandbox == "workspace-write":
    roots = [os.path.expanduser("~/.cache/go-build"),
             os.path.expanduser("~/.cache/golangci-lint"), "/tmp"]
    os.environ["GOCACHE"] = roots[0]
    os.environ["GOLANGCI_LINT_CACHE"] = roots[1]
    command += ["-c", "sandbox_workspace_write.writable_roots=[" +
                ",".join(toml_string(root) for root in roots) + "]",
                "-c", "sandbox_workspace_write.network_access=true"]
if no_git:
    command.append("--skip-git-repo-check")
command += ["-o", str(report_file), "-"]
try:
    with input_file.open("rb") as source:
        os.dup2(source.fileno(), 0)
    if session is not None:
        os.chdir(directory)
    elif not directory.is_dir():
        fail("worktree directory does not exist")
    os.execvp(command[0], command)
except OSError:
    fail("cannot open input, enter worktree, or execute codex")
PY
