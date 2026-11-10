#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d)
tmp=$(cd "$tmp" && pwd -P)
trap 'rm -rf "$tmp"' EXIT
if [[ ${CHECK_LENGTHS_ISOLATION_CHILD:-0} != 1 ]]; then
  mkdir -p "$tmp/child-tmp"
  status=0
  CHECK_LENGTHS_ISOLATION_CHILD=1 TMPDIR="$tmp/child-tmp" GIT_DIR="$tmp/hostile.git" \
    bash "$root/scripts/test/check-lengths-test.sh" > "$tmp/child-out" 2> "$tmp/child-err" || status=$?
  if [[ $status != 0 || -e "$tmp/hostile.git" ]]; then
    printf 'hostile GIT_DIR affected the test: status %s\n' "$status" >&2
    cat "$tmp/child-out" "$tmp/child-err" >&2
    exit 1
  fi
fi
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY
unset GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_COMMON_DIR GIT_CEILING_DIRECTORIES GIT_NAMESPACE
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
mkdir -p "$tmp/repo/scripts" "$tmp/bin"
cp "$root/scripts/check-lengths.sh" "$tmp/repo/scripts/check-lengths.sh"
git -C "$tmp/repo" init -q

python3 -I - "$tmp/repo" <<'PY'
from pathlib import Path
import sys

repo = Path(sys.argv[1])
(repo / "generated.md").write_text(
    "Code generated example DO NOT EDIT\n" + "x" * 1048576 + "\n" + "line\n" * 449
)
PY
printf 'check-lengths: ok (docs <= 450, Go <= 700 lines)\n' > "$tmp/success"
run_success() {
  local status=0
  bash "$tmp/repo/scripts/check-lengths.sh" > "$tmp/out" 2> "$tmp/err" || status=$?
  if [[ $status != 0 ]] || ! cmp -s "$tmp/success" "$tmp/out" || [[ -s "$tmp/err" ]]; then
    printf 'expected success, got status %s\n' "$status" >&2
    cat "$tmp/out" "$tmp/err" >&2
    exit 1
  fi
}

# Delay the rest of head's output until a quiet grep has closed its input.
cat > "$tmp/bin/head" <<'PY'
#!/usr/bin/env python3
import signal
import sys
import time

signal.signal(signal.SIGPIPE, signal.SIG_DFL)
with open(sys.argv[-1], "rb") as source:
    sys.stdout.buffer.write(source.readline())
    sys.stdout.buffer.flush()
    time.sleep(0.1)
    for _ in range(4):
        sys.stdout.buffer.write(source.readline())
    sys.stdout.buffer.flush()
PY
chmod +x "$tmp/bin/head"
PATH="$tmp/bin:$PATH" run_success

for ((i = 0; i < 200; i++)); do
  run_success
done

python3 -I - "$tmp/repo" <<'PY'
from pathlib import Path
import sys

repo = Path(sys.argv[1])
(repo / "at-limit.md").write_text("line\n" * 450)
(repo / "at-limit.go").write_text("line\n" * 700)
(repo / "oversize_test.go").write_text("line\n" * 701)
PY
run_success

python3 -I - "$tmp/repo" <<'PY'
from pathlib import Path
import sys

repo = Path(sys.argv[1])
(repo / "over-limit.md").write_text("line\n" * 451)
(repo / "over-limit.go").write_text("line\n" * 701)
(repo / "sixth-line.md").write_text(
    "line\n" * 5 + "Code generated example DO NOT EDIT\n" + "line\n" * 445
)
PY
cat > "$tmp/expected-out" <<'EXPECTED'
too long: over-limit.md has 451 lines (max 450)
too long: sixth-line.md has 451 lines (max 450)
too long: over-limit.go has 701 lines (max 700)
EXPECTED
printf 'check-lengths: 3 file(s) over the limit\n' > "$tmp/expected-err"
run_violations() {
  local status=0
  bash "$tmp/repo/scripts/check-lengths.sh" > "$tmp/out" 2> "$tmp/err" || status=$?
  if [[ $status != 1 ]] || ! cmp -s "$tmp/expected-out" "$tmp/out" || ! cmp -s "$tmp/expected-err" "$tmp/err"; then
    printf 'expected three violations, got status %s\n' "$status" >&2
    cat "$tmp/out" "$tmp/err" >&2
    exit 1
  fi
}
mkdir -p "$tmp/padded-bin"
cat > "$tmp/padded-bin/wc" <<'SH'
#!/usr/bin/env bash
count=$(/usr/bin/wc "$@")
printf '%10d\n' "$count"
SH
chmod +x "$tmp/padded-bin/wc"
PATH="$tmp/padded-bin:$PATH" run_violations
run_violations
echo 'check-lengths tests: ok'
