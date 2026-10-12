# Releases

Ship one feature per `v0.x.y` tag when the feature is done and CI is green. Do not batch features. Governance and document-only slices need no feature tag.

## Release gates

Run [local checks](testing.md#required-checks) before each commit. A tag requires green GitHub validation on that exact commit under the [manual evidence procedure](workflow.md#manual-ci-evidence), including every applicable master-push run after landing. Inspect every triggered validation workflow; merge-ref results and missing, skipped, cancelled, pending, or failed required runs do not establish success. Classify review-only work by its reviewed workflow or app purpose, not its display name. Available GitHub reviews and comments, including Copilot's, are optional input: read them and fix or answer concrete findings without waiting for more or blocking on an absent, pending, failed, or unavailable review-only run. A validation run triggered by a review remains required. Fix red validation runs forward at once.

Findings from `govulncheck`, gitleaks, or Semgrep block release until fixed. Suppress only verified false positives with the reason under [security](security.md#tools-and-scanners). Full-history gitleaks scans secrets. Keep that scan and connected Semgrep Pro Code and Supply Chain checks for code changes.

Auth, token handling, credential storage, write APIs, and release workflows require the independent adversarial review and fix confirmation in [workflow](workflow.md#reviews). Live verification and paid-write approval remain separate gates under [testing](testing.md#live-verification) and [live data](live-data.md).

## Landing and publication

Only the manager lands changes or creates tags. Follow [workflow](workflow.md#landing-a-slice) for PRs, exact-head CI, ancestry, and the owner's admin bypass; follow [Git rules](workflow.md#git) for signed commits and tags. Never force-push or delete master.

Keep the public SDK import paths, CLI contracts, wiki generation, release tags, and versioned release notes. Update public docs and breaking-change notes under [documentation](documentation.md#public-surface-and-wiki). Wiki publication is disabled for every event while the validated-current-master gate is absent. PRs, matching master pushes, and manual dispatches render read-only previews; no manual publishing fallback is allowed. The mirror stays unchanged. Successful previews do not prove publication. The manager records disabled-publication evidence before completing a landing or creating a tag, and keeps landings serialized through all master validation results.
