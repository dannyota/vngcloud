# 0001. Publish the GitHub wiki from `docs/wiki/`

Status: Accepted

## Context

SDK and CLI docs should be easy to read on GitHub, and they should change in
the same commit as the code they describe. A GitHub wiki is a separate Git
repository with no review, CI, or link to code commits.

## Decision

`docs/wiki/` in the main repository is the only source of wiki pages.
`scripts/wiki-sync.sh` copies it to the wiki on every push to `master` that
touches it, through `.github/workflows/wiki.yml`. The wiki is a one-way mirror.

Pages link to each other as `Page-Name.md`, which works when browsing the
repository. The sync rewrites those links for the wiki. Links to other
repository files use absolute URLs, and the sync rejects relative ones.

## Consequences

- Doc changes get the same review and history as code.
- Edits made in the wiki web UI are lost on the next sync.
- The wiki must be enabled and have one page created in the web UI before the
  first sync, because GitHub creates the wiki repository only then.
