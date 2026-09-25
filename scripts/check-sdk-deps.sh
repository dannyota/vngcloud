#!/usr/bin/env bash
# Fails when any package other than cmd/... or internal/cli depends on a
# CLI-only module (cobra, pflag, jmespath, or x/term), so the SDK stays
# standard-library only. Exits 0 when clean.
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

forbidden='github.com/spf13/cobra|github.com/spf13/pflag|github.com/jmespath/go-jmespath|golang.org/x/term'

violations=0
while IFS= read -r pkg; do
  case "$pkg" in
  danny.vn/vngcloud/cmd/* | danny.vn/vngcloud/internal/cli) continue ;;
  esac
  deps=$(go list -deps "$pkg" 2>/dev/null | grep -E "$forbidden" || true)
  if [[ -n "$deps" ]]; then
    echo "forbidden CLI-only dependency in $pkg:"
    echo "$deps" | sed 's/^/  /'
    violations=$((violations + 1))
  fi
done < <(go list ./...)

if ((violations > 0)); then
  echo "check-sdk-deps: $violations package(s) depend on a CLI-only module" >&2
  exit 1
fi
echo "check-sdk-deps: ok (only cmd/... and internal/cli depend on cobra, pflag, jmespath, or x/term)"
