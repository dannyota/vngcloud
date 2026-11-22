# Releases

Ship one feature per `v0.x.y` tag when the feature is done and CI is green. Do not batch features. Governance and document-only slices need no feature tag.

## Release gates

Run [local checks](testing.md#required-checks) before each commit. A tag requires green GitHub validation on that exact commit under the [manual evidence procedure](workflow.md#manual-ci-evidence), including every applicable master-push run after landing. Inspect every triggered validation workflow; merge-ref results and missing, skipped, cancelled, pending, or failed required runs do not establish success. Classify review-only work by its reviewed workflow or app purpose, not its display name. Available GitHub reviews and comments, including Copilot's, are optional input: read them and fix or answer concrete findings without waiting for more or blocking on an absent, pending, failed, or unavailable review-only run. A validation run triggered by a review remains required. Fix red validation runs forward at once.

Findings from `govulncheck`, gitleaks, or golangci-lint, including gosec, block release until fixed. Suppress only verified false positives with the reason under [security](security.md#tools-and-scanners). Keep full-history gitleaks and the other CI scanner gates. CodeQL alerts through code scanning and is not a merge or release gate.

Auth, token handling, credential storage, write APIs, and release workflows require the independent adversarial review and fix confirmation in [workflow](workflow.md#reviews). Live verification and paid-write approval remain separate gates under [testing](testing.md#live-verification) and [live data](live-data.md).

## Landing and publication

Only the manager lands changes or creates tags. Follow [workflow](workflow.md#landing-a-slice) for PRs, exact-head CI, ancestry, and the owner's admin bypass; follow [Git rules](workflow.md#git) for signed commits and tags. Never force-push or delete master.

Keep the public SDK import paths, CLI contracts, wiki generation, release tags, and versioned release notes. When `RELEASE_NOTES.md` passes the 450-line limit, move the oldest tag range to a `docs/release-notes-archive-v0.<n>9.md` file linked from the newest archive, and write wiki links there as `wiki/...`. Update public docs and breaking-change notes under [documentation](documentation.md#public-surface-and-wiki).

CI publishes the Wiki only on a push to current master after `CI required` succeeds, through the master-only `wiki` environment with job-scoped `contents: write`. Every other event previews; the separate Wiki workflow always previews, and no manual publishing fallback is allowed. The publisher verifies the event and tested checkout SHA, checks public master before cloning and immediately before pushing, and records the full source SHA and run URL including attempt. Concurrency groups publishers by source SHA without cancellation; stale-source rejection and manager-serialized landings control ordering across SHAs. It does not wait for the cross-workflow landing-evidence checker; the manager still requires all applicable validation runs.

Before completing a landing or creating a tag, require the master-push `Wiki publish` job to succeed and the mirror's latest commit to name the landed SHA. A successful preview does not prove publication. Publication records source provenance even when rendered pages match. Keep landings serialized through every master validation result and publication. Main-repository and Wiki updates are not atomic; a master move aborts publication and requires manager resolution. A mirror bot commit does not prove a signed source commit; retain source and tag signature checks.
