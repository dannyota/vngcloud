#!/usr/bin/env bash
# Fails when a tracked Markdown file passes MD_MAX lines or a non-test Go file
# passes CODE_MAX lines. Split an oversize file by topic: a sibling file in the
# same Go package, or a focused doc page.
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

MD_MAX=450
CODE_MAX=700

violations=0
check() {
  local max=$1 file lines
  while IFS= read -r file; do
    [[ -f "$file" ]] || continue
    head -n 5 "$file" | grep -Eq 'Code generated .* DO NOT EDIT' && continue
    lines=$(wc -l <"$file")
    if ((lines > max)); then
      echo "too long: $file has $lines lines (max $max)"
      violations=$((violations + 1))
    fi
  done
}

check "$MD_MAX" < <(git ls-files --cached --others --exclude-standard -- '*.md')
check "$CODE_MAX" < <(
  git ls-files --cached --others --exclude-standard -- '*.go' |
    grep -Ev '_test\.go$|(^|/)testdata/' || true
)

if ((violations > 0)); then
  echo "check-lengths: $violations file(s) over the limit" >&2
  exit 1
fi
echo "check-lengths: ok (docs <= $MD_MAX, Go <= $CODE_MAX lines)"
