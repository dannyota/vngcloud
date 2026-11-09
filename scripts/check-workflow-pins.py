#!/usr/bin/env python3
"""Check action SHA pins and version comments without network access."""

import argparse
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parent.parent
FILES = {"ci.yml", "codeql.yml", "tools.yml", "wiki.yml"}
USE = re.compile(r"^\s*-?\s*uses:")
PIN = re.compile(r"\s*-?\s*uses: ([\w.-]+/[\w./-]+)@([0-9a-f]{40}) # v([0-9]+\.[0-9]+\.[0-9]+)\s*")


def action_pins(paths):
    pins = {}
    for path in paths:
        for number, line in enumerate(path.read_text().splitlines(), 1):
            if not USE.match(line):
                continue
            match = PIN.fullmatch(line)
            if not match:
                raise ValueError(f"{path}:{number}: expected a full SHA and version comment")
            action, sha, version = match.groups()
            repo = "/".join(action.split("/")[:2])
            if repo in pins and pins[repo] != (sha, version):
                raise ValueError(f"{path}:{number}: conflicting pins for {repo}")
            pins[repo] = (sha, version)
    return pins


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--list", action="store_true", help="print repositories and versions")
    parser.add_argument("paths", nargs="*", type=Path)
    args = parser.parse_args()
    try:
        paths = sorted(path for pattern in ("*.yml", "*.yaml")
                       for path in (ROOT / ".github/workflows").glob(pattern))
        actual = {path.name for path in paths}
        if actual != FILES:
            raise ValueError("workflow inventory differs from reviewed files: "
                             f"unexpected {sorted(actual - FILES)}; missing {sorted(FILES - actual)}")
        pins = action_pins(args.paths or paths)
    except (OSError, ValueError) as error:
        sys.exit(str(error))
    if args.list:
        for repo, (_, version) in sorted(pins.items()):
            print(repo, version)
    else:
        print("check-workflow-pins: ok")


if __name__ == "__main__":
    main()
