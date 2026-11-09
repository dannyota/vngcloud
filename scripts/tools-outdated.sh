#!/usr/bin/env bash
# Fails when a pinned tool, a GitHub Action, or a direct Go module dependency is
# behind its latest release, or
# when a pin disagrees with its mirror. .tool-versions is the source for tool
# versions; go.mod mirrors the Go pin. Actions are
# pinned by SHA with a "# vX.Y.Z" comment that this script compares.
#
# Set GH_TOKEN to avoid GitHub API rate limits.
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

stale=0
report() {
  echo "outdated: $*"
  stale=$((stale + 1))
}

pin() { awk -v n="$1" '$1 == n { print $2 }' .tool-versions; }

gh_latest() {
  local auth=()
  [[ -n "${GH_TOKEN:-}" ]] && auth=(-H "Authorization: Bearer $GH_TOKEN")
  if [[ "$1" == github/codeql-action ]]; then
    # Bundle releases share this repository; compare stable action versions.
    curl -fsSL "${auth[@]}" "https://api.github.com/repos/$1/releases?per_page=100" |
      python3 -c 'import json,re,sys
versions = [r["tag_name"].removeprefix("v") for r in json.load(sys.stdin)
            if not r.get("draft") and not r.get("prerelease")
            and re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", r["tag_name"])]
print(max(versions, key=lambda v: tuple(map(int, v.split(".")))))'
    return
  fi
  curl -fsSL "${auth[@]}" "https://api.github.com/repos/$1/releases/latest" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"].lstrip("v"))'
}

check() { # name pinned latest
  if [[ "$2" != "$3" ]]; then
    report "$1 $2 -> $3"
  else
    echo "current: $1 $2"
  fi
}

go_latest=$(curl -fsSL 'https://go.dev/dl/?mode=json' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["version"].removeprefix("go"))')
check golang "$(pin golang)" "$go_latest"
check golangci-lint "$(pin golangci-lint)" "$(gh_latest golangci/golangci-lint)"
check gitleaks "$(pin gitleaks)" "$(gh_latest gitleaks/gitleaks)"
check govulncheck "$(pin govulncheck)" "$(curl -fsSL https://proxy.golang.org/golang.org/x/vuln/@latest |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["Version"].lstrip("v"))')"

mod_go=$(awk '$1 == "go" { print $2 }' go.mod)
[[ "$mod_go" == "$(pin golang)" ]] || report "go.mod says go $mod_go, .tool-versions says $(pin golang)"
action_pins=$(python3 scripts/check-workflow-pins.py --list)
while read -r action version; do
  check "$action" "$version" "$(gh_latest "$action")"
done <<< "$action_pins"

while read -r mod version latest; do
  report "$mod $version -> $latest"
done < <(go list -m -u -f '{{if and (not .Main) (not .Indirect) .Update}}{{.Path}} {{.Version}} {{.Update.Version}}{{end}}' all)

if ((stale > 0)); then
  echo "tools-outdated: $stale pin(s) to update" >&2
  exit 1
fi
echo "tools-outdated: all pins current"
