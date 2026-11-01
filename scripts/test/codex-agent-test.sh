#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d)
tmp=$(cd "$tmp" && pwd -P)
trap 'rm -rf "$tmp"' EXIT
export RECORD="$tmp"
export GOCACHE="$tmp/caller-go-cache"
export GOLANGCI_LINT_CACHE="$tmp/caller-lint-cache"
mkdir -p "$tmp/bin" "$tmp/work tree/.codex/agents" "$tmp/copy"
mkdir -p "$tmp/repo/scripts" "$tmp/repo/.codex/agents"
cp "$root/scripts/codex-agent.sh" "$tmp/repo/scripts/codex-agent.sh"
cat > "$tmp/repo/.codex/agents/code_reviewer.toml" <<'ROLE'
sandbox_mode = "read-only"
developer_instructions = "fake fallback reviewer"
ROLE
cat > "$tmp/bin/codex" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$RECORD/argv"
pwd > "$RECORD/cwd"
printf '%s\n' "${GOCACHE-}" "${GOLANGCI_LINT_CACHE-}" > "$RECORD/caches"
cat > "$RECORD/stdin"
exit "${FAKE_STATUS:-0}"
FAKE
chmod +x "$tmp/bin/codex"
export PATH="$tmp/bin:$PATH"
launcher="$tmp/repo/scripts/codex-agent.sh"
work="$tmp/work tree"
report="$tmp/report file"
printf 'private fake brief\n' > "$tmp/brief"
cat > "$work/.codex/agents/reader.toml" <<'ROLE'
sandbox_mode = "read-only"
developer_instructions = "say \"hello\"\\path\nnext\t\u0001\u007F"
model = "ignored"
model_reasoning_effort = "ignored"
ROLE
cat > "$work/.codex/agents/writer.toml" <<'ROLE'
sandbox_mode = "workspace-write"
developer_instructions = "write"
ROLE
run() {
  bash "$launcher" --model gpt-6.1-sol --effort medium "$@"
}
refuse() {
  rm -f "$tmp/argv"
  local status=0
  bash "$launcher" "$@" > "$tmp/out" 2> "$tmp/err" || status=$?
  [[ $status == 2 && ! -e "$tmp/argv" && ! -s "$tmp/out" && -s "$tmp/err" ]]
}
check_caches() {
  printf '%s\n' "$1" "$2" > "$tmp/expected-caches"
  cmp "$tmp/expected-caches" "$tmp/caches"
}
run reader "$work" "$tmp/brief" "$report"
check_caches "$GOCACHE" "$GOLANGCI_LINT_CACHE"
cmp "$tmp/brief" "$tmp/stdin"
python3 -I - "$tmp" "$work" "$report" <<'PY'
import pathlib, sys, tomllib
tmp, work, report = sys.argv[1:]
args = (pathlib.Path(tmp) / "argv").read_text().splitlines()
instructions = 'say "hello"\\path\nnext\t\x01\x7f'
assert tomllib.loads(args[6])["developer_instructions"] == instructions
assert args == ["exec", "-m", "gpt-6.1-sol", "-c",
                "model_reasoning_effort=medium", "-c", args[6],
                "--sandbox", "read-only", "-C", work, "-o", report, "-"]
PY
run --no-git writer "$work" "$tmp/brief" "$report"
check_caches "$HOME/.cache/go-build" "$HOME/.cache/golangci-lint"
python3 -I - "$tmp" <<'PY'
import os, pathlib, sys, tomllib
args = (pathlib.Path(sys.argv[1]) / "argv").read_text().splitlines()
assert "--skip-git-repo-check" in args
assert "--ephemeral" not in args
roots = next(a for a in args if a.startswith("sandbox_workspace_write.writable_roots="))
assert tomllib.loads(roots)["sandbox_workspace_write"]["writable_roots"] == [
    os.path.expanduser("~/.cache/go-build"),
    os.path.expanduser("~/.cache/golangci-lint"), "/tmp"]
assert "sandbox_workspace_write.network_access=true" in args
PY
run --resume 12345678-1234-abcd-ABCD-1234567890ab writer "$work" "$tmp/brief" "$report"
check_caches "$HOME/.cache/go-build" "$HOME/.cache/golangci-lint"
cmp "$tmp/brief" "$tmp/stdin"
python3 -I - "$tmp" "$work" "$report" <<'PY'
import pathlib, sys
tmp, work, report = sys.argv[1:]
args = (pathlib.Path(tmp) / "argv").read_text().splitlines()
assert args[:7] == ["exec", "resume", "12345678-1234-abcd-ABCD-1234567890ab", "-m", "gpt-6.1-sol",
                    "-c", "model_reasoning_effort=medium"]
assert 'sandbox_mode="workspace-write"' in args
assert "sandbox_workspace_write.network_access=true" in args
assert "--sandbox" not in args and "-C" not in args
assert not any(a.startswith("developer_instructions=") for a in args)
assert args[-3:] == ["-o", report, "-"]
assert (pathlib.Path(tmp) / "cwd").read_text().strip() == work
PY
run --resume 12345678-1234-abcd-ABCD-1234567890ab reader "$work" "$tmp/brief" "$report"
check_caches "$GOCACHE" "$GOLANGCI_LINT_CACHE"
! grep -q sandbox_workspace_write "$tmp/argv"
grep -q 'sandbox_mode="read-only"' "$tmp/argv"
for hostile in --help --last --dangerously-bypass-approvals-and-sandbox; do
  refuse --resume "$hostile" --model gpt-6.1-sol --effort medium reader "$work" "$tmp/brief" "$report"
done
run code_reviewer "$work" "$tmp/brief" "$report"
grep -q 'fake fallback reviewer' "$tmp/argv"
printf 'malformed TOML\n' > "$work/.codex/agents/code_reviewer.toml"
refuse --model gpt-6.1-sol --effort medium code_reviewer "$work" "$tmp/brief" "$report"
run code_reviewer "$tmp/copy" "$tmp/brief" "$report"
grep -q '^developer_instructions=' "$tmp/argv"
refuse --effort medium reader "$work" "$tmp/brief" "$report"
refuse --model gpt-6.1-sol reader "$work" "$tmp/brief" "$report"
for pair in 'bad medium reader' 'gpt-6.1-sol ultra reader' 'gpt-6.1-sol medium bad-role'; do
  read -r model effort role <<< "$pair"
  refuse --model "$model" --effort "$effort" "$role" "$work" "$tmp/brief" "$report"
done
printf 'broken TOML\n' > "$work/.codex/agents/bad.toml"
refuse --model gpt-6.1-sol --effort medium bad "$work" "$tmp/brief" "$report"
printf 'sandbox_mode = "read-only"\ndeveloper_instructions = 42\n' > "$work/.codex/agents/bad.toml"
refuse --model gpt-6.1-sol --effort medium bad "$work" "$tmp/brief" "$report"
for content in 'sandbox_mode = "danger-full-access"' 'sandbox_mode = "read-only"'; do
  printf '%s\ndeveloper_instructions = ""\n' "$content" > "$work/.codex/agents/bad.toml"
  refuse --model gpt-6.1-sol --effort medium bad "$work" "$tmp/brief" "$report"
done
printf 'sandbox_mode = "danger-full-access"\ndeveloper_instructions = "fake"\n' > "$work/.codex/agents/bad.toml"
refuse --model gpt-6.1-sol --effort medium bad "$work" "$tmp/brief" "$report"
refuse --model gpt-6.1-sol --effort medium absent "$work" "$tmp/brief" "$report"
refuse --resume 'bad/session' --model gpt-6.1-sol --effort medium reader "$work" "$tmp/brief" "$report"
status=0
FAKE_STATUS=17 run reader "$work" "$tmp/brief" "$report" || status=$?
[[ $status == 17 ]]
echo "codex-agent tests: ok"
