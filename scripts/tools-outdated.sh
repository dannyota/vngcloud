#!/usr/bin/env bash
set +x
set -Eeuo pipefail

tool_token=${GH_TOKEN:-}
export -n tool_token
unset GH_TOKEN
if [[ -n "$tool_token" && ! "$tool_token" =~ ^[A-Za-z0-9_]+$ ]]; then
  echo 'tools-outdated: invalid GitHub token format' >&2
  exit 1
fi

cd "$(dirname "${BASH_SOURCE[0]}")/.."

stale=0
report() {
  echo "outdated: $*"
  stale=$((stale + 1))
}

pin() { awk -v n="$1" '$1 == n { print $2 }' .tool-versions; }

gh_get() {
  if [[ -n "$tool_token" ]]; then
    printf 'header = "Authorization: Bearer %s"\n' "$tool_token" |
      curl -q -fsSL --config - "$1"
  else
    curl -q -fsSL "$1"
  fi
}

gh_latest() {
  if [[ "$1" == github/codeql-action ]]; then
    gh_get "https://api.github.com/repos/$1/releases?per_page=100" |
      python3 -c 'import json,re,sys
versions = [r["tag_name"].removeprefix("v") for r in json.load(sys.stdin)
            if not r.get("draft") and not r.get("prerelease")
            and re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", r["tag_name"])]
print(max(versions, key=lambda v: tuple(map(int, v.split(".")))))'
    return
  fi
  gh_get "https://api.github.com/repos/$1/releases/latest" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"].lstrip("v"))'
}

check() {
  if [[ "$2" != "$3" ]]; then
    report "$1 $2 -> $3"
  else
    echo "current: $1 $2"
  fi
}

go_latest=$(curl -fsSL 'https://go.dev/dl/?mode=json' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["version"].removeprefix("go"))')
check golang "$(pin golang)" "$go_latest"
latest=$(gh_latest golangci/golangci-lint)
check golangci-lint "$(pin golangci-lint)" "$latest"
latest=$(gh_latest gitleaks/gitleaks)
check gitleaks "$(pin gitleaks)" "$latest"
latest=$(curl -fsSL https://proxy.golang.org/golang.org/x/vuln/@latest |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["Version"].lstrip("v"))')
check govulncheck "$(pin govulncheck)" "$latest"

mod_go=$(awk '$1 == "go" { print $2 }' go.mod)
[[ "$mod_go" == "$(pin golang)" ]] || report "go.mod says go $mod_go, .tool-versions says $(pin golang)"
if ! action_pins=$(python3 scripts/check-workflow-pins.py --list); then
  echo 'tools-outdated: action inventory failed' >&2
  exit 1
fi
while read -r action version; do
  latest=$(gh_latest "$action")
  check "$action" "$version" "$latest"
done <<< "$action_pins"

if ! module_updates=$(go list -m -u -f '{{if and (not .Main) (not .Indirect) .Update}}{{.Path}} {{.Version}} {{.Update.Version}}{{end}}' all); then
  echo 'tools-outdated: module inventory failed' >&2
  exit 1
fi
while read -r mod version latest; do
  [[ -n "$mod" ]] || continue
  report "$mod $version -> $latest"
done <<< "$module_updates"

if ((stale > 0)); then
  echo "tools-outdated: $stale pin(s) to update" >&2
  exit 1
fi
echo "tools-outdated: all pins current"
