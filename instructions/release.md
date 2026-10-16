# Releases

Ship one feature per `v0.x.y` tag when the feature is done and CI is green. Do not batch features. Governance and document-only slices need no feature tag.

## Release gates

Run [local checks](testing.md#required-checks) before each commit. A tag requires green GitHub validation on that exact commit under the [executable evidence procedure](workflow.md#executable-ci-evidence) and the [manual evidence procedure](workflow.md#manual-ci-evidence) during bootstrap, including every applicable master-push run after landing. Inspect every triggered validation workflow; merge-ref results and missing, skipped, cancelled, pending, or failed required runs do not establish success. Classify review-only work by its reviewed workflow or app purpose, not its display name. Available GitHub reviews and comments, including Copilot's, are optional input: read them and fix or answer concrete findings without waiting for more or blocking on an absent, pending, failed, or unavailable review-only run. A validation run triggered by a review remains required. Fix red validation runs forward at once.

Findings from `govulncheck`, gitleaks, or Semgrep block release until fixed. Suppress only verified false positives with the reason under [security](security.md#tools-and-scanners). Full-history gitleaks scans secrets. Keep that scan and connected Semgrep Pro Code and Supply Chain checks for code changes.

Auth, token handling, credential storage, write APIs, and release workflows require the independent adversarial review and fix confirmation in [workflow](workflow.md#reviews). Live verification and paid-write approval remain separate gates under [testing](testing.md#live-verification) and [live data](live-data.md).

## Landing and publication

Only the manager lands changes or creates tags. Follow [workflow](workflow.md#landing-a-slice) for PRs, exact-head CI, ancestry, and the owner's admin bypass; follow [Git rules](workflow.md#git) for signed commits and tags. Never force-push or delete master.

Keep the public SDK import paths, CLI contracts, wiki generation, release tags, and versioned release notes. Update public docs and breaking-change notes under [documentation](documentation.md#public-surface-and-wiki). Wiki publication is disabled for every event while the validated-current-master gate is absent. PRs, matching master pushes, and manual dispatches render read-only previews; no manual publishing fallback is allowed. The mirror stays unchanged. Successful previews do not prove publication. The manager records disabled-publication evidence before completing a landing or creating a tag, and keeps landings serialized through all master validation results.


Run the trusted checker in release mode after creating the local annotated signed tag and before pushing it. Supply the original reviewed base, exact eligible head, current master equal to that head, retained PR execution-source policy bindings, and the private independent project-review record. Supply the original successful pre-mode output through `--retained-pre` under [executable evidence](workflow.md#executable-ci-evidence). Missing original runs, attempts, source objects, or bindings block release even when current master runs pass. The tag's signed internal name must equal the requested release name, its peeled commit target must equal that head, and its signature must match the explicit owner principal and fingerprint using existing public trust outside the candidate. The requested ref must remain stable. The checker neither creates nor pushes the tag. Recollect all validation evidence because a later manual run can invalidate an earlier cutoff. Keep manual verification until matching real checker, annotation, immutable-source, and signer evidence is established.
