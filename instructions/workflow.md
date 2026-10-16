# Workflow

Work proceeds in briefed slices with independent review. [AGENTS.md](../AGENTS.md#ai-agent-definitions-and-routing) owns model routing; ownership comes from each brief, not a static path table.

## Duties

- The primary agent acts as manager: turn the owner's request into small releases, plan the work, assign disjoint paths, brief workers, and keep worktrees, verification, Git, releases, and the final answer. Ask the owner only for decisions the owner must make, one focused question at a time, with options and a recommendation.
- The manager does not investigate failures itself. It fetches evidence workers cannot reach, such as CI logs, run artifacts, and authorized live output, saves it in the private scratchpad without secrets or account data, and briefs the troubleshooter with the failure, the evidence paths, and the owned paths.
- The troubleshooter reproduces a failing or flaky test, CI failure, or wrong behavior, finds the root cause with evidence, and fixes it within owned paths with a failing test first. It never retries a flaky test into a pass, weakens a check, or widens a timeout to hide a race. When the cause lies outside owned paths or needs a contract change, it reports the cause and the needed change instead of editing.
- The designer researches and writes the brief-owned documents. Cover the public SDK or CLI surface, errors, compatibility with existing callers, security, dependencies, limits, alternatives, trade-offs, and release order. Name choices that require owner approval. After a Fable design, Astra high reviews and improves the same documents before the independent Sol review. Answer contract questions during implementation, but never implement the slice designed.
- The implementer writes only within the brief's owned paths, follows [Go](go.md) and [testing](testing.md), and updates affected user docs under [documentation](documentation.md#public-surface-and-wiki). Report a needed file outside the brief instead of editing it.
- The reviewer inspects plans, designs, and completed code independently under [Reviews](#reviews), including troubleshooter fixes. Reviewers never fix reviewed source.

## Design and implementation

1. Read the relevant [designs](../docs/design/README.md), [accepted ADRs](../docs/adr/README.md), and active plan. Place document changes under [documentation](documentation.md#placement).
2. Define the slice's boundaries, contracts, dependencies, and required checks in its design and implementation plan. Obtain owner approval for new contracts and independent review of both design and plan before code starts.
3. Keep unresolved API, security, cost, and resource gates as blockers for dependent implementation. Inferred schemas remain inferred until evidence establishes them; document moves and approvals do not prove API behavior.
4. When a worker reports a conflict, resolve it from the reviewed contract or return it to the designer and independent reviewer. Record an approved contract change in its authority before resuming with a brief that states the answer.

## Briefs and reports

A self-contained brief names the objective, authorities, exact owned and forbidden paths, required checks, definition of done, and report format. Repeat the writing rules: plain words, active voice, short paragraphs, no dates or em dashes, no plan/task/review IDs in code or living docs, one line per paragraph or list item in agent-only files, and 80-column wrapping in human docs. Tell each worker it is not alone, must preserve others' edits, and must not change Git state.

Every worker brief forbids network calls other than those the task's checks need, secret reads, and edits outside owned paths. Implementation and build-capable code review use `workspace-write` with the exact cache, temporary-directory, and network settings in [CLAUDE.md](../CLAUDE.md#claude-routing-and-invocation). Those settings allow loopback sockets for tests and writes to the two tool caches required by `make check`. Writes stay confined to the owned worktree or disposable review copy, those caches, and `/tmp` for temporary work. The brief's network and secret-read restrictions and the credential-free worktree rule provide defense in depth for network access; that access does not authorize unrelated calls or edits.

Codex keeps `.codex/` read-only under `workspace-write`. A worker assigned role-file changes returns a proposed patch in the private scratchpad for the manager to inspect and apply. Do not widen permissions or bypass the sandbox to edit protected files. This procedure applies only to brief-owned changes; reviewers never prepare or apply source fixes.

Start with `git status --short`; existing changes belong to someone else unless assigned in the brief. Read the named authorities and inspect the code and tests before editing. Stop and report conflicting documents, overlapping ownership, or a missing decision. Do not invent a contract or widen the task.

Reports list exact new, changed, and deleted files; commands run and their results; failed or skipped checks with reasons; and open items. Claim only checks that ran. The manager inspects each diff and runs required checks before accepting a report or starting dependent work. Worker reports support verification; they do not replace it.

## Working rules

Prefer dedicated file and search tools when available. Read files before editing and inspect a target before deleting or overwriting it. Use the shell for commands with an explicit working directory or absolute paths. Run independent reads and searches in parallel.

Match surrounding style, names, and comment density. Make the smallest correct change; do not add features, refactors, or files outside the request. Treat external material under [security](security.md#untrusted-input).

Confirm actions that are hard to reverse or reach outside the machine when existing owner authority does not cover them. The [landing procedure](#landing-a-slice) has standing owner authority. That authority does not approve live calls, purchases, cleanup, or credential reads; [live verification](testing.md#live-verification) and [live data](live-data.md) still apply.

## Worktrees and parallel work

Each slice uses `.claude/worktrees/<slice>` and `wip/<slice>`. Each concurrent writer gets a separate worktree and disjoint paths. Run at most three workers across all slices, counting designers, reviewers, and nested workers. Do not spawn workers to repeat verification.

Run `make check`, including race tests, before every commit. Editor diagnostics can come from other worktrees; trust the checks in the owned worktree. Follow the complete [check table](testing.md#required-checks).

A worker's worktree must never contain `.env` or other credentials. Only the manager performs authorized live runs. For a live run it performs itself, the manager may symlink the main checkout's `.env` into its own live-run worktree without reading its values, then must remove the symlink afterwards. Never place that symlink in a worker's worktree. Follow [live data](live-data.md) for live-run approval, privacy, serialization, and cleanup.

## Reviews

Review each design and plan independently before implementation. Review completed code once per plan or release after the last code task, then confirm fixes. Match review depth to risk; do not invent extra gates. Every review and fix confirmation runs on Sol xhigh under [routing](../AGENTS.md#ai-agent-definitions-and-routing), as a separate job from the author; a worker's self-review never counts.

Auth, token handling, credential storage, every write API, and release workflows require adversarial review before landing or release. Follow the [security review scope](security.md#adversarial-review). Name confirmed invariants. Rank each finding by severity with a concrete failure scenario, `path:line`, and fix direction. State checks and what was not checked. A reviewer never reviews its own work or implements fixes to reviewed files.

Design reviews are read-only. A build-capable code reviewer may write build output and probes in a disposable public-source copy, never source fixes. The manager prepares caches and any authorized network setup. [CLAUDE.md](../CLAUDE.md#build-capable-code-review) gives the invocation and copy procedure for Claude sessions.

## Git

Only the manager changes Git state. Workers never add, commit, stash, reset, checkout, switch, or push. Never use `git stash`; use `git show` or `git diff` for comparisons. The manager can preserve work with a temporary signed commit instead.

- Stage exact paths with `git add -- <paths>`. Never use `git add .`, `git add -A`, `git commit -a`, or force-add. Never stage `.env` or `examples/basic/config.*.json`.
- Every commit and tag uses the owner's SSH signature. Keep `commit.gpgsign` and `tag.gpgSign` enabled. Never use `--no-gpg-sign` or `-c commit.gpgsign=false`, and never push an unsigned commit or tag.
- Use a plain imperative commit subject and a body explaining why. Commit subjects, commit bodies, and PR bodies describe only the changes and their purpose; never mention agents, AI, or automated assistance. Add the owner's Developer Certificate of Origin sign-off with `git commit -s`; sign-off does not replace the SSH signature. No Conventional Commit prefixes, tool attribution, AI co-author trailers, or generated footers in commits or PR bodies.
- Amend or reorder only unpushed commits. The sole pushed-history exception is an explicitly coordinated `wip/` rebase with `--force-with-lease` under [landing](#landing-a-slice). Never rewrite commits on master.
- Keep local-only paths in `.git/info/exclude`, not committed `.gitignore`. Preserve tracked files and repo-local exceptions. Only shared agent definitions under `.claude/agents/` and `.codex/agents/`, plus `.claude/settings.json`, belong in the tracked agent configuration; keep local state ignored.
- Never replace or reconfigure global ignores or `core.excludesFile`. Add global entries only at the owner's explicit request. A repo instruction to track named files permits scoped repo-local negations for those files; otherwise do not newly track globally ignored files or add exceptions. No exception permits secrets.
- Install the staged-content hook once per clone with `make hooks-install`. It runs gitleaks and the file-length check.

## Landing a slice

1. The manager may commit completed work locally and push its `wip/<slice>` branch under standing owner authority. Open a PR against master. Inspect the full branch log and diff, and keep each commit reviewable.
2. Resolve findings on the slice branch and obtain independent fix confirmation. Amend or combine fixups only within the [history rule](#git).
3. Refresh master. Land only a descendant of current master. If stale, coordinate a rebase, review behavior-changing conflict resolution, and push with `--force-with-lease` to that `wip/` branch only. Rerun CI on the new head.
4. Apply the [executable evidence procedure](#executable-ci-evidence) and the [manual evidence procedure](#manual-ci-evidence) to the exact PR head during bootstrap. Require every applicable validation run to succeed and obtain required independent project review and fix confirmation. Read available GitHub reviews and comments, including Copilot's, and fix or answer concrete findings. Do not wait for a review, request one to fill an optional slot, or block landing because a review-only run is absent, pending, failed, or unavailable. Classify review-only work by the reviewed workflow or app purpose, not its display name. Any validation run triggered by a review remains required and must succeed.
5. Land with `git push origin <head>:master` using the owner's approved admin bypass. Never force-push or delete master, even when bypass permissions would allow it. Delete the completed slice branch after landing. Dependent slices follow release order and this same ancestry and CI gate.
6. Wait for every applicable master-push validation run on the landed head. Confirm master still names that head and Wiki publication remains disabled. Serialize landings through this check; a failed post-landing run blocks the next slice and tags.
7. After landing and after any tag, read available reviews and comments on the landed work, including Copilot's, and fix or answer concrete findings. Do not wait for later reviews or block post-landing work because a review-only run is absent, pending, failed, or unavailable. Handle this repository's GitHub notification threads, including read ones, whenever working with GitHub. List threads with `gh api 'notifications?all=true&per_page=100' --paginate` and filter to repository `dannyota/vngcloud`. Each failed-run thread needs its cause fixed or explained, naming the cause. Close each handled thread as done with `gh api -X DELETE notifications/threads/<id>`; HTTP 204 means done. Never use PATCH, which only marks a thread read. `all=true` still lists done threads because the API has no done filter; repeat the listing with `all=false` to check unread work and trust the 204 instead of expecting done threads to disappear. Leave unresolved threads open and touch only this repository's threads.

Fix red CI forward at once. Tags and feature scope follow [release](release.md).

## Manual CI evidence

The manager-run checker evaluates validation evidence with version-2 source assertions and exact reviewed source bindings. Manual evidence remains necessary until the manager verifies matching real PR, master, full-rerun, and retained-source evidence for the checker. `CI required` checks only the mandatory CI jobs and their tested SHA. A green aggregate does not excuse another triggered validation run's failure. The owner's admin bypass retains the manager's responsibility for landing and release checks.

1. Record the PR number, source and base repositories, intended master target, PR head, current master, and reviewed validation workflow identities. Require the head to descend from current master. Inspect the complete branch log and diff, signatures, and required independent project review. Read any available GitHub reviews and comments, including Copilot's, and handle concrete findings without waiting for more reviews.
2. Derive expected workflows from the complete change set and [trigger rules](testing.md#workflow-triggers). Use merge-base-to-head paths for PRs and before-to-after paths for pushes, including both sides of renames. Missing comparison bases and incomplete path lists fail. Record every conditional exclusion and its paths.
3. Require an actual PR CI run. Enumerate validation runs and check runs for the head with complete pagination and source/PR association, including manual and scheduled runs when applicable. Inspect newly introduced or renamed workflows too. Classify review-only work by its reviewed workflow or app purpose, not its display name, and do not make its absence, pending state, or failure block landing. A validation run triggered by a review remains required. Empty validation evidence, an API error, or incomplete enumeration fails.
4. Inspect each independent validation run and every run attempt. Keep earlier attempt outcomes and rerun reasons; a successful independent run never erases a failed one. Require completed success for every applicable validation run and mandatory job. Reject failure, cancellation, timeout, neutral, pending, missing evidence, and unexpected skipped jobs. Never retry a flaky test into a pass.
5. Confirm every source-validation job recorded the expected tested SHA, run ID, and attempt. A synthetic merge checkout and run metadata alone do not prove head identity. The aggregation-only `CI required` job must run and accept every mandatory dependency's result and SHA.
6. If a platform path filter omits a conditional workflow required by the full change set, dispatch that workflow at the head and record the omission and successful recovery. An omitted scan is not a clean scan. A workflow_dispatch run cannot replace missing PR CI or satisfy a ruleset's required status check; the ruleset needs eligible push or pull_request evidence.
7. Immediately before landing, re-read the PR head, master, ancestry, and complete validation run set. Restart when any changes. Save the accepted SHA, validation run IDs, run attempts, URLs, conclusions, tested SHAs, exclusions, and evidence cutoff privately.
8. After landing, repeat the check for all applicable master-push runs and successful Wiki previews. Record the absence of every Wiki publisher, publishing credential, and write permission. A preview is not publication. Use bounded polling with an explicit timeout failure, and keep manager landings serialized.
9. Recheck the head and complete validation evidence before tagging. Later validation runs can appear after a cutoff; no finite wait proves future runs cannot exist. Review-only work is optional and does not enter the validation gate. Evaluate with trusted reviewed workflow definitions, not an unreviewed evidence script from the PR.


## Executable CI evidence

Run the trusted reviewed `scripts/ci-evidence.py` outside the candidate checkout with an explicitly supplied runtime `GH_TOKEN`. The checker has no credential fallback or stored-token launcher. It reads only fixed-host GitHub evidence and local Git objects; it never fetches, changes Git state, publishes, or creates tags. Its bytes must match the expected policy commit in the trusted manager checkout. Keep the candidate checkout, private policy, private review record, and existing public signing trust separate. Do not send private review records or owner signing configuration to Actions.

The manager prepares a version-1 policy with fixed workflow IDs, the Actions app identity, required job-step names, reviewed review-only purposes, and `publication: "disabled"`. Every allowed source tuple binds repository, workflow ID and path, workflow ref and SHA, blob SHA, content SHA-256, and an exact-byte review report. The policy SHA identifies the trusted checker policy commit. Preserve original PR source bindings after landing and before release. Unknown workflows, checks, or source revisions require review; no display-name exception or Boolean override exists. The synthetic fixtures in [the checker tests](../scripts/test-ci-evidence.py) define the input structure.

The private version-1 project-review record binds repository, PR, head, reviewed base, policy SHA, manager, and nonempty independent reviews. Each review identifies author, reviewer, reviewed SHA, report, accepting outcome, findings, and final-head fix confirmations for blocking findings. The checker validates these bindings. The manager checks reports and actual reviewer independence; an unsigned record is not cryptographic proof of review. Private inputs must remain outside the candidate and fetched artifacts.

With the runtime token already supplied, invoke the reviewed checker from the trusted manager side and save output privately:

```sh
python3 /trusted/reviewed/scripts/ci-evidence.py \
  --mode pre --pr "$PR" --head "$H" --base "$B" --master "$B" \
  --policy-sha "$POLICY_SHA" --manager "$MANAGER" \
  --signer "$OWNER_PRINCIPAL" --fingerprint "$OWNER_FINGERPRINT" \
  --checkout /trusted/manager --candidate /candidate/checkout \
  --trust /trusted/public/allowed_signers \
  --policy /private/ci/policy.json --review /private/ci/review.json \
  --wait 120 > /private/ci/accepted-pre.json
```

`B` is the reviewed pre-landing master. Retain the complete successful pre-mode output, including its cutoff, as `/private/ci/accepted-pre.json`. The manager attests that the file is the original accepted inventory and checks its completeness against the manual evidence. Keep the file private and outside the candidate and fetched artifacts. The unsigned inventory does not prove its own origin or completeness; never reconstruct it from a later run listing or the current policy.

For post-landing validation, keep `B`, `H`, the original review/source records, and the saved pre-mode output. Add `--retained-pre /private/ci/accepted-pre.json`, set `--mode post --master "$H"`, and save separate output. Release uses the same retained input with `--mode release --master "$H" --tag "$TAG"` after the manager creates the local annotated signed tag and before pushing it. Post and release reject a missing or changed original run, attempt, job, check, annotation, source revision, blob, or policy binding. New attempts and independent runs remain required. The retained input uses the exact pre-output structure and a 4 MiB file bound; extra fields, malformed identities, and candidate paths reject. Pre mode rejects a retained input.

Every introduced commit in `B..H` and `H` must verify against the explicit owner principal and fingerprint using existing public trust outside the candidate. Release verifies the exact annotated tag object, its internal name equal to the requested release name, its peeled commit target, and the stable requested ref. Missing objects or failed verification block the gate; the checker does not fetch or replace trust.

The checker enumerates every independent validation run and every attempt, checks mandatory jobs, steps, checks, app associations, and exactly one reserved source annotation per source job, then reads immutable workflow bytes through Git objects. Source jobs and attempts must agree on their workflow source tuple. Independent runs may have different reviewed revisions. Review-only runs remain optional. Manual recovery can cover omitted conditional workflows, but cannot replace missing PR CI or applicable master-push CI. Repeated collection rejects a changed head, master, run/attempt/job/check inventory, policy, review record, or retained pre-mode input. Numeric identities require exact integers; Booleans and floats reject. Git reads bound stdout and stderr separately at 4 MiB each while reading and stop and reap the child on a bound, timeout, cancellation, or read error. Request, byte, page, attempt, commit, and total-wait bounds fail explicitly; successful reruns never waive earlier failures.

Output records the head, reviewed base, mode, cutoff, exclusions, each run and attempt, jobs and checks, annotations, and reviewed source bindings. Source association is a workflow assertion backed by exact-byte review, not independent execution attestation. Inspect full branch changes and available GitHub findings separately. Keep manual evidence and manager landings serialized until matching real checker evidence proves the transport and source association; offline tests do not establish those gates. Wiki remains a read-only preview for every event, with publication explicitly disabled.
