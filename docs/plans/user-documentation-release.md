# User documentation and release plan

**Goal:** Make the README and user guides accurately describe the implemented SDK and CLI, make current documentation reachable, and land and release the completed documentation work.

**Authority:** The owner requests documentation review, README improvements, merging, release, and synchronizing the GitHub Wiki. Continue with Sol high implementation and independent Astra review. The explicit Wiki request authorizes a bounded manager-run publication for this task after exact-current-master validation, overriding the repository's otherwise-disabled manual publication for this run. It does not enable automatic publishing or change validation workflows. Existing SDK, CLI, and credential contracts apply; no new runtime contract is introduced. The requested documentation release uses the next available patch version, provisionally `v0.81.1`, with notes that describe documentation changes only.

**References:** [Documentation rules](../../instructions/documentation.md), [workflow](../../instructions/workflow.md), [release gates](../../instructions/release.md), [CLI design](../design/cli.md), [workload access](../design/storage-workload-access.md), and [wiki source decision](../adr/0001-wiki-from-docs.md).

## Scope and ownership

The implementation owns only `README.md`, `docs/wiki/Home.md`, `docs/wiki/Getting-Started.md`, `docs/wiki/Configuration.md`, `docs/wiki/Security.md`, `docs/wiki/Services.md`, and the new patch entry in `RELEASE_NOTES.md`. It may draft a short English introduction and matching release body in the manager's private scratchpad for review. The manager owns this plan, repository metadata, verification, Git, GitHub, Wiki publication, and release operations. Existing workload-access changes remain intact. Accepted ADRs remain unchanged under the ADR index rule.

No new user page is required: the existing workload guide fills the vServer/VKS use case. Link each subject to its current home rather than duplicate manuals. No Go, generated CLI reference, dependency, workflow, sync script, live test, or public API change belongs to this slice. Keep endpoint quote/create/delete and private connectivity evidence gates unchanged. No cloud API, SSH, cluster access, credential read, or paid operation is authorized by this documentation and release work.

## Implementation

- [x] Obtain independent review of this plan and its Wiki-authority amendment against existing contracts and the documentation audit before implementation.
- [x] Keep README user-guide links pointing to the GitHub Wiki, add a direct workload-access link, and keep both quick starts concise. Describe service references as overviews rather than exhaustive method inventories. Preserve selected-operation coverage and explicit Kubernetes inventory limits. The manager must synchronize the Wiki before claiming these links deliver the updated guides.
- [x] Clarify that `--read-only` guards CLI writes and supported `--max-price` operations compare a quote before ordering; a quote check is not a provider billing lock. Keep the distinction between standard-library SDK packages and CLI dependencies.
- [x] Make Home describe the Wiki as a mirror of reviewed repository documentation and route edits to `docs/wiki/`. Remove the incorrect automatic-publication claim without adding workflow implementation details to the user flow. Keep ADR 0001 and read-only CI previews unchanged.
- [x] Improve Getting Started by placing credential prerequisites and route choice first, keeping first-use commands read-only, identifying the SDK server count as one page, and linking contributor checks and existing authentication, configuration, and workload guides.
- [x] Qualify Configuration's endpoint overrides and retry defaults. Explain the fixed service-account and native-object boundaries and operation-specific send-once writes by linking their existing authorities. `IsRetryable` is not permission to repeat an ambiguous write.
- [x] Correct Security's universal `Secret` redaction claim to match the documented supported formatting and encoders and bypass limitations. Qualify credentials-file permissions with the existing Windows exception. Add no new secret handling mechanism.
- [x] Complete Services' overview with CDN and Monitor links and current native storage operations, while retaining detailed methods in the service guides.
- [x] Add patch release notes covering user navigation, onboarding and safety corrections, and the workload guide, without claiming endpoint APIs or new runtime behavior. Preserve existing release entries. Draft a concise English introduction with selected-operation, Kubernetes, quote-check, unofficial-project, Apache-2.0, and pre-v1 limitations. Prepare a matching release body with version-pinned repository links.
- [x] Check example commands against source and offline help; never execute authenticated examples. Check changed links and anchors, shell or Go example syntax where useful, whitespace, wrapping, line limits, and `make wiki-preview`. Do not add prose-mirroring tests.
- [x] Obtain independent Astra review of the implemented documents and introduction. Sol high fixes findings; Astra confirms fixes. The manager inspects the complete diff and runs `make check` before each signed commit.

## Landing and release

1. Update the repository description to name the implemented CLI and verify the resulting metadata.
2. Inspect the complete branch diff and commit signatures, refresh master, and require current ancestry. Push the slice branch and open a PR against master.
3. Require actual PR CI and Wiki preview validation on the exact head. Markdown-only paths exclude automatic Semgrep; unchanged pins, scripts, and workflows exclude Tools. Any independently triggered applicable validation remains required. Read available reviews and handle concrete findings without waiting for review-only runs.
4. Record all validation runs, attempts, tested source identities, exclusions, and evidence that automatic Wiki publishing remains disabled. Handle this repository's notifications, including old failed-run causes, under the workflow rules.
5. Recheck head, ancestry, reviews, and complete validation evidence, then fast-forward master using the approved landing procedure. Wait for all applicable master-push validations on the landed head.
6. Synchronize the Wiki under the owner's explicit request. Use only the reviewed renderer's `--dry-run` output from the exact validated source, a separate inspected Wiki clone, runtime Git authentication, and a signed commit naming the full source SHA. Do not invoke the script's publishing path, place a token in command arguments, consume PR artifacts, or bypass source validation. Review the rendered diff, stage exact Markdown paths, and preserve unowned files. Resolve ownership before removing any obsolete page. Recheck current master immediately before a non-force Wiki push, then verify remote identity and rendered content. Main-repository and Wiki updates are not atomic; keep manager landings serialized through this check.
7. Verify the next patch tag is free and points to the exact validated master commit. Create and verify an owner-signed tag, push it, and publish the GitHub release with the reviewed documentation notes. Do not claim prebuilt artifacts or features that are absent.
8. Recheck available reviews, validation, release metadata, master, and Wiki source identity after tagging. Update the local main checkout safely, then remove only the completed slice branch and worktree. Preserve unrelated work and private captures.

Keep this plan's completed implementation checkboxes current before the reviewed commit. GitHub run evidence and final landing/release results belong in the manager's private report so recording them does not invalidate the released head.
