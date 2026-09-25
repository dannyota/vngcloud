#!/usr/bin/env bash
# Fails when any package other than cmd/... or internal/cli depends on
# anything but the standard library and this module's own packages, so the
# SDK stays standard-library only. A go list failure is never hidden: it
# aborts the script under set -e instead of being swallowed by a stray
# "|| true". Exits 0 when clean.
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

module="danny.vn/vngcloud"

packages="$(go list ./...)"
if [[ -z "$packages" ]]; then
  echo "check-sdk-deps: go list ./... returned no packages" >&2
  exit 1
fi

violations=0
while IFS= read -r pkg; do
  case "$pkg" in
  "$module"/cmd/* | "$module"/internal/cli) continue ;;
  esac

  deps="$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "$pkg")"
  while IFS= read -r dep; do
    [[ -z "$dep" ]] && continue
    case "$dep" in
    "$module" | "$module"/*) continue ;;
    esac
    echo "forbidden non-standard-library dependency in $pkg: $dep"
    violations=$((violations + 1))
  done <<<"$deps"
done <<<"$packages"

if ((violations > 0)); then
  echo "check-sdk-deps: $violations violation(s): a non-CLI package depends on something other than the standard library or this module" >&2
  exit 1
fi
echo "check-sdk-deps: ok (every non-CLI package depends only on the standard library and this module's own packages)"
