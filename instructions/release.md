# Releases

Ship one feature per `v0.x.y` tag when the feature is done and CI is green. Do not batch features. Governance and document-only slices need no feature tag.

## Release gates

Run [local checks](testing.md#required-checks) before each commit. A tag requires green GitHub CI on that exact commit. Inspect every triggered validation workflow; merge-ref results and missing, skipped, cancelled, pending, or failed required runs do not establish success. Fix red runs forward at once.

Findings from `govulncheck`, gitleaks, or Semgrep block release until fixed. Suppress only verified false positives with the reason under [security](security.md#tools-and-scanners). Keep the full-history gitleaks scan and connected Semgrep Pro Code, Supply Chain, and Secrets checks for code changes.

Auth, token handling, credential storage, write APIs, and release workflows require the independent adversarial review and fix confirmation in [workflow](workflow.md#reviews). Live verification and paid-write approval remain separate gates under [testing](testing.md#live-verification) and [live data](live-data.md).

## Landing and publication

Only the manager lands changes or creates tags. Follow [workflow](workflow.md#landing-a-slice) for PRs, exact-head CI, ancestry, and the owner's admin bypass; follow [Git rules](workflow.md#git) for signed commits and tags. Never force-push or delete master.

Keep the public SDK import paths, CLI contracts, wiki generation, release tags, and versioned release notes. Update public docs and breaking-change notes under [documentation](documentation.md#public-surface-and-wiki). Wiki publication stays isolated from PR code and read-only PR validation.
