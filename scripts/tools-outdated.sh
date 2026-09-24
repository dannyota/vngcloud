#!/usr/bin/env bash
# Fails when a pinned tool or GitHub Action is behind its latest release, or
# when a pin disagrees with its mirror. .tool-versions is the source for tool
# versions; go.mod and the Semgrep image tag in ci.yml mirror it. Actions are
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
check semgrep "$(pin semgrep)" "$(curl -fsSL https://pypi.org/pypi/semgrep/json |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["info"]["version"])')"
check govulncheck "$(pin govulncheck)" "$(curl -fsSL https://proxy.golang.org/golang.org/x/vuln/@latest |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["Version"].lstrip("v"))')"

mod_go=$(awk '$1 == "go" { print $2 }' go.mod)
[[ "$mod_go" == "$(pin golang)" ]] || report "go.mod says go $mod_go, .tool-versions says $(pin golang)"
image_tag=$(grep -oE 'semgrep/semgrep:[0-9.]+' .github/workflows/ci.yml | head -1 | cut -d: -f2)
[[ "$image_tag" == "$(pin semgrep)" ]] || report "ci.yml Semgrep image $image_tag, .tool-versions says $(pin semgrep)"

while read -r action version; do
  check "$action" "$version" "$(gh_latest "$action")"
done < <(grep -hoE 'uses: [^@ ]+@[0-9a-f]{40} # v[0-9.]+' .github/workflows/*.yml |
  sed -E 's/uses: ([^@]+)@[0-9a-f]+ # v/\1 /' | sort -u)

if ((stale > 0)); then
  echo "tools-outdated: $stale pin(s) to update" >&2
  exit 1
fi
echo "tools-outdated: all pins current"
