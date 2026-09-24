#!/usr/bin/env bash
# Publishes docs/wiki/ to the repository's GitHub wiki. docs/wiki/ is the
# source; the wiki is a mirror, so edits made in the wiki web UI are overwritten
# on the next sync.
#
# Pages link to each other as `Page-Name.md` so links work when browsing the
# repository. The wiki serves pages without the extension, so this script
# rewrites those links. Links to other repository files must be absolute URLs.
#
# Usage:
#   scripts/wiki-sync.sh --dry-run   render into a temp dir and print its path
#   scripts/wiki-sync.sh             render, then push to the wiki
#
# Pushing needs GH_TOKEN with contents:write on the repository. GITHUB_REPOSITORY
# (owner/name) defaults to the origin remote.
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

# Relative links must point at another wiki page, not into the repository.
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
repo=${GITHUB_REPOSITORY:-$(git remote get-url origin | sed -E 's#^(https://github.com/|git@github.com:)##; s#\.git$##')}
auth=$(printf 'x-access-token:%s' "$GH_TOKEN" | base64 -w0)
wiki="$render/.wiki"

git -c http.extraheader="AUTHORIZATION: basic $auth" \
  clone --quiet --depth 1 "https://github.com/$repo.wiki.git" "$wiki" ||
  die "cannot clone the wiki; enable it and create the first page in the web UI"

find "$wiki" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
cp "$render"/*.md "$wiki"/

cd "$wiki"
git add -A
if git diff --cached --quiet; then
  echo "wiki-sync: wiki is up to date"
  exit 0
fi
source_sha=$(git -C "$OLDPWD" rev-parse --short HEAD)
git -c user.name="${GIT_AUTHOR_NAME:-github-actions[bot]}" \
  -c user.email="${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}" \
  commit --quiet -m "Sync from $source_sha"
git -c http.extraheader="AUTHORIZATION: basic $auth" push --quiet origin HEAD
echo "wiki-sync: pushed $(git diff --stat HEAD~1 | tail -1)"
