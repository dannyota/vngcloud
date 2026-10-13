# vStorage platform access implementation plan

> **For agentic workers:** Use `superpowers:subagent-driven-development` for authorized implementation. Follow repository ownership, review, evidence, and Git rules instead of conflicting generic defaults.

**Goal:** Let operators prepare cloud-side identity, bucket permissions, credentials, and network access for applications on vServer or VKS without entering those workloads.

**Architecture:** Compose existing IAM, storage, and network operations. Ship guidance under their accepted contracts independently of private endpoint lifecycle work. Keep endpoint writes gated until the proposed contract has owner approval and the required API, billing, and DNS evidence.

**Tech stack:** Existing Go SDK and CLI; Markdown wiki; no new dependencies, guest agents, Kubernetes clients, or secret exporters.

**Spec:** [Workload access](../design/storage-workload-access.md), [proposed endpoint lifecycle](../design/network-endpoints.md), [endpoint API evidence](../design/network-endpoints-api.md), [endpoint checks](../design/network-endpoints-checks.md), and [accepted write conventions](../adr/0002-write-api-conventions.md).

## Authority and constraints

- The owner requests discovery, planning, and implementation with Sol high and independent Astra review. These model choices override the repository's default routing for this work. Designer, implementer, and reviewer remain separate people or sessions; at most three workers run concurrently.
- The requested boundary excludes SSH, guest commands, Kubernetes API calls, cluster credentials, Kubernetes Secrets or manifests, host-file edits, application restart, and credential deployment.
- Existing accepted IAM, S3 key, bucket-policy, object, and network contracts authorize the documentation slice. A proposed design does not approve a new endpoint API or a live run.
- Only the manager performs live discovery or verification. The owner's discovery request covers reviewed, bounded catalog and inventory reads using the existing SDK authentication flow. It authorizes no endpoint purchase, DNS mutation, resource deletion, key rotation, or secret-value disclosure. Credentials resolve at runtime and must never enter model context, public files, logs, or command arguments.
- Preserve the distinction between management client ID/client secret and S3 access key/secret key. A token exchange does not establish authorization for every product API.
- Preserve complete bucket policies, explicit storage project selection, attached-key restrictions, key-create ambiguity handling, protected secret files, read-only refusal, and destructive `--yes` gates.
- Native object clients retain fixed regional HTTPS hosts, TLS checks, Signature V4, and their current regional capability limits. No arbitrary endpoint override is introduced.
- Platform status and local object access do not prove connectivity from a VM or pod. Private endpoint presence does not establish private-only bucket access.
- Living docs use current-state prose without dates or history, no em dashes, and 80-column wrapping. Plans use one line per paragraph or list item. Follow repository line limits and secret rules.
- Only the manager changes Git state, verifies the integrated result, and handles landing. Preserve other workers' edits and use separate credential-free worktrees for concurrent writers.

## Readiness

|Slice|State|Required evidence or decision|
|-|-|-|
|Existing-command access, rotation, and handoff guide|Implemented and independently reviewed|Offline command, document, wiki, and integration checks passed|
|IAM-user endpoint catalog discovery|Partial|Hanoi zone, category, service, package, and VPC reads captured; subnet response semantics and general pagination remain unresolved|
|Public endpoint catalog SDK and CLI|Held|Approved typed contracts, populated sanitized raw fixtures, eligibility semantics, authentication scope, and matching tests|
|Endpoint quote|Held|Verified quote request and response, billing unit, tax, term, recurring costs, and renewal semantics|
|Endpoint create and delete|Held|Owner-approved contract; identity, readiness, billing and deletion semantics; explicitly authorized paid verification|
|Native private S3 compatibility|Held|Preserved regional hostname, DNS, TLS, signing, and workload-side evidence supplied by the operator|

## Review focus

1. A bucket-policy put replaces the entire document. The guide must preserve unrelated grants and explain concurrent-editor coordination.
2. Detaching an S3 key restores broad creator rights. Rotation and retirement must delete the old key while attached, never detach as revocation.
3. Resetting a management client secret is separate from S3 rotation and does not guarantee revocation of issued tokens.
4. A ready endpoint or successful local S3 call does not prove VM/pod connectivity, disable public access, or establish support for a custom S3 hostname.
5. Public console request builders are frontend evidence only. They cannot establish minimum permissions, stable response schemas, an affordable bounded purchase, or safe deletion of managed/shared endpoints.

The guide's source-to-command comparison and independent adversarial documentation review cover these cases. Add no tests that merely duplicate prose. Any later runtime implementation needs the endpoint checks and meaningful failing tests before code.

## Work package: existing-command workload guide

**Owned files:** Create `docs/wiki/Storage-Workload-Access.md`. Update `docs/wiki/Storage.md`, `docs/wiki/IAM.md`, `docs/wiki/Network.md`, `docs/wiki/VKS.md`, `docs/wiki/Home.md`, and `docs/wiki/_Sidebar.md` for focused navigation and relevant boundary notes. Correct the existing retirement instructions in `docs/wiki/Storage.md` and `docs/design/storage-cli.md`: delete identified keys while still attached, preserve removal of every bucket-policy grant, and retain the name-reuse warning. The manager owns this plan and `docs/design/README.md`; the designer owns the four linked design documents. No generated CLI reference, Go source, workflow, dependency, or live-test changes belong to this package.

**Consumes:** Existing `iam create-service-account`, `storage ensure-service-account-principal`, `storage get-bucket-policy`, `storage put-bucket-policy`, `storage create-s3-key --service-account-id --secret-file`, `storage list-s3-keys`, `storage delete-s3-key`, and endpoint inventory commands, plus the accepted object-client credential/region contract.

**Produces:** A user guide for platform setup, private handoff, overlap rotation, and retirement with executable command shapes and synthetic placeholders. The guide introduces no public runtime API or binding resource.

- [x] Obtain independent Astra review of the complete designs and this plan. Resolve findings before dispatching the implementer; proposed endpoint contracts remain held independently.
- [x] Dispatch a separate Sol high implementer with the owned files and existing authorities. Start with `git status --short`, preserve unrelated edits, forbid Git mutations and all live calls, and keep credentials out of the worker worktree.
- [x] Document selection of an existing storage project and exact region, bucket selection or explicit creation, and creation or selection of a dedicated IAM service account by an authorized IAM-user provisioner.
- [x] Document principal resolution, inspection and preservation of the whole bucket policy, and explicit permissions. Link the established object-access template and explain its delete/list/multipart grants and the limits of minimal-permission evidence.
- [x] Document attached S3 key creation to a new protected file and private metadata handoff: region, bucket, permitted operations, key ID, principal, and endpoint. Keep the management secret with the provisioner and use existing deployment processes for the application key.
- [x] Document public egress prerequisites and private endpoint inventory without inventing endpoint writes, mandatory NAT, VKS identity federation, custom native object endpoints, or a private-connectivity success claim.
- [x] Document overlap rotation: confirm key capacity, create a second attached key, let the operator deploy and verify it, then delete the identified old key while attached. Keep ambiguous outcomes and name-reuse/retirement safeguards explicit.
- [x] Reconcile the existing retirement paragraphs in `Storage.md` and `storage-cli.md` with attached-key deletion. Preserve bucket-grant removal and the warning that a reused service-account name can inherit stale grants.
- [x] Verify every command and flag against source and offline `--help`; use no authenticated command. Confirm example JSON and shell syntax where examples contain them. Inspect local Markdown links and document wrapping. Run `make wiki-preview`, `git diff --check`, and `bash scripts/check-lengths.sh`; report exact results.
- [x] The manager inspects the complete diff and runs `make check`, including race tests, vet, lint, line limits, and workflow checks. Create no new tests for documentation-only changes.
- [x] Obtain independent Astra review of the implemented guide, with adversarial attention to credential handoff, policy replacement, overbroad grants, rotation, deletion, and unsupported connectivity claims. Sol high fixes findings and Astra confirms them.
- [x] Mark only this completed package done. Endpoint gates remain open. Any commit or landing follows the repository's signed-Git and exact-head validation rules; wiki publication remains disabled.

## Work package: close endpoint evidence gates

This package prepares future implementation. Reviewed, bounded catalog and inventory reads fall within the owner's discovery request; purchases and mutations require separate authority. Keep raw captures private and derive sanitized fixtures only from actual raw responses. Public asset evidence and a safe discovery helper are not live verification.

The manager's bounded Hanoi IAM-user run captured and validated zones, categories, explicit services and packages, and VPC inventory. The vStorage service catalog maps to the native HAN02 hostname; this does not prove private traffic. The selected VPC subnet read returned HTTP 200 and `success=true` without `data` or pagination, so the helper stopped. No usable-subnet schema, empty inventory, eligibility guarantee, or complete inventory is inferred. Details and remaining limits are recorded in the [API evidence](../design/network-endpoints-api.md#recorded-iam-user-catalog-reads). No endpoint quote, purchase, mutation, service-account creation, SSH, or Kubernetes call ran.

- [x] Inspect first-party endpoint guides, public API reference, public provider source, and unauthenticated console request builders. Record exact sources and confidence in the API document.
- [x] Prepare and independently review a bounded private manager-run discovery probe. Restrict product calls to approved GET catalogs and inventory, fixed trusted origins, explicit region selection, request/page/time limits, private output files, and sanitized structural summaries. Existing IAM authentication is the sole allowed auth exchange; no order, price POST, mutation, secret-returning product API, or workload access is included.
- [ ] After independent probe review, the manager runs bounded read-only discovery under the owner's request. Verify origin and project/region namespaces, IAM-user access, populated category/service/package/zone and eligible VPC/subnet shapes, pagination and error envelopes. Do not widen into mutations or service-account credential creation; service-account product access needs separate evidence.
- [ ] Resolve quote semantics through a separately scoped non-purchasing investigation. Verify billing unit/term, amount/currency/tax, recurring obligations, renewal behavior, payment settlement, and whether a quote binds the later debit. Revise the candidate one-month renewal-off contract if the service cannot support it.
- [ ] Resolve DNS fields and uniqueness, eligibility, response identity, ready/failure states, and managed/shared endpoint ownership. Never infer delete safety from a name prefix or delete VKS-managed resources as cleanup.
- [ ] Present the completed endpoint contract, bounded paid verification procedure, and any remaining owner choices. Approval of the contract does not authorize purchases; a paid run must separately name account, region, resources, cap, term, cleanup, and maximum unresolved obligation.
- [ ] After contract and evidence gates close, add independently reviewed implementation tasks with exact public catalog models, signatures, request serializers, typed safe outputs, guards, wait behavior, recovery rules, and owned paths. Do not implement placeholders or guess missing fields before that amendment.

## Deferred runtime implementation order

1. Verified endpoint catalogs and prerequisite reads, including typed fixtures and current SDK/CLI documentation.
2. Quote and guarded create/delete as one coherent lifecycle, using the same resolved pricing specification, send-once mutations, explicit consent, safe partial results, and confirmed billing behavior.
3. Matching CLI commands, generated references, examples, offline tests, manager verification, and independent Astra adversarial review.
4. Native private S3 guidance only after its compatibility gate is established without adding SSH or Kubernetes access to the tool.

The manager will expand this order into executable tasks after evidence establishes the missing interfaces. None of these rows is a claim that endpoint writes are implemented or approved.
