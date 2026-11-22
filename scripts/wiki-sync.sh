#!/usr/bin/env bash
set +x
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

SRC=docs/wiki
dry_run=false
[[ "${1:-}" == "--dry-run" ]] && dry_run=true

die() {
  echo "wiki-sync: $*" >&2
  exit 1
}

[[ -f "$SRC/Home.md" ]] || die "$SRC/Home.md is missing"

if bad=$(grep -EnH '\]\((\.\./|[^):#]+/)[^)]*\)' "$SRC"/*.md); then
  echo "$bad" >&2
  die "relative links above leave $SRC; use absolute GitHub URLs"
fi

render=$(mktemp -d)
trap 'rm -rf "$render"' EXIT
for page in "$SRC"/*.md; do
  sed -E 's/\]\(([A-Za-z0-9_-]+)\.md(#[^)]*)?\)/](\1\2)/g' "$page" \
    >"$render/$(basename "$page")"
done

if $dry_run; then
  trap - EXIT
  echo "wiki-sync: rendered $(ls "$render" | wc -l) pages into $render"
  exit 0
fi

[[ -n "${GH_TOKEN:-}" ]] || die "GH_TOKEN is not set"
[[ "${WIKI_SOURCE_SHA:-}" =~ ^[0-9a-f]{40}$ ]] || die "WIKI_SOURCE_SHA must be a full lowercase commit SHA"
auth=$(printf 'x-access-token:%s' "$GH_TOKEN" | base64 | tr -d '\r\n')
unset GH_TOKEN
[[ "$(git rev-parse HEAD)" == "$WIKI_SOURCE_SHA" ]] || die "WIKI_SOURCE_SHA differs from the checkout"
source_root=$PWD

check_master() {
  local master
  master=$(git -C "$source_root" ls-remote origin refs/heads/master 2>/dev/null) ||
    die "cannot read current master"
  [[ "$master" == "$WIKI_SOURCE_SHA"$'\trefs/heads/master' ]] ||
    die "source is not current master"
}

check_master
repo=${GITHUB_REPOSITORY:-$(git remote get-url origin | sed -E 's#^(https://github.com/|git@github.com:)##; s#\.git$##')}
wiki="$render/.wiki"

GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.extraheader GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth" \
  git clone --quiet --depth 1 "https://github.com/$repo.wiki.git" "$wiki" >/dev/null 2>&1 ||
  die "cannot clone the wiki; enable it and create the first page in the web UI"

find "$wiki" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
cp "$render"/*.md "$wiki"/

cd "$wiki"
git add -A
message="Sync from $WIKI_SOURCE_SHA"
if [[ -n "${WIKI_SOURCE_RUN_URL:-}" ]]; then
  message+=$'\n\n'"$WIKI_SOURCE_RUN_URL"
fi
git -c user.name="${GIT_AUTHOR_NAME:-github-actions[bot]}" \
  -c user.email="${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}" \
  commit --quiet --allow-empty -m "$message"
check_master
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.extraheader GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth" \
  git push --quiet origin HEAD >/dev/null 2>&1 || die "cannot push the wiki"
echo "wiki-sync: pushed $(git diff --stat HEAD~1 | tail -1)"
