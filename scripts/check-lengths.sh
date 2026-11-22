#!/usr/bin/env bash
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

MD_MAX=450
CODE_MAX=700

violations=0
check() {
  local max=$1 kind=$2 file lines
  while IFS= read -r file; do
    [[ -f "$file" ]] || continue
    if [[ "$kind" == go && ( "$file" == *_test.go || "/$file" == */testdata/* ) ]]; then
      continue
    fi
    head -n 5 "$file" | grep -E 'Code generated .* DO NOT EDIT' >/dev/null && continue
    lines=$(( $(wc -l <"$file") ))
    if ((lines > max)); then
      echo "too long: $file has $lines lines (max $max)"
      violations=$((violations + 1))
    fi
  done
}

if ! markdown_files=$(git ls-files --cached --others --exclude-standard -- '*.md'); then
  echo 'check-lengths: Markdown inventory failed' >&2
  exit 1
fi
if ! go_files=$(git ls-files --cached --others --exclude-standard -- '*.go'); then
  echo 'check-lengths: Go inventory failed' >&2
  exit 1
fi
check "$MD_MAX" md <<< "$markdown_files"
check "$CODE_MAX" go <<< "$go_files"

if ((violations > 0)); then
  echo "check-lengths: $violations file(s) over the limit" >&2
  exit 1
fi
echo "check-lengths: ok (docs <= $MD_MAX, Go <= $CODE_MAX lines)"
