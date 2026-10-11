# Workflow

Work proceeds in briefed slices with independent review. [AGENTS.md](../AGENTS.md#ai-agent-definitions-and-routing) owns model routing; ownership comes from each brief, not a static path table.

## Duties

- The primary agent acts as manager: turn the owner's request into small releases, plan the work, assign disjoint paths, brief workers, and keep worktrees, verification, Git, releases, and the final answer. Ask the owner only for decisions the owner must make, one focused question at a time, with options and a recommendation.
- The designer researches and writes the brief-owned documents. Cover the public SDK or CLI surface, errors, compatibility with existing callers, security, dependencies, limits, alternatives, trade-offs, and release order. Name choices that require owner approval. Answer contract questions during implementation, but never implement the slice designed.
- The implementer writes only within the brief's owned paths, follows [Go](go.md) and [testing](testing.md), and updates affected user docs under [documentation](documentation.md#public-surface-and-wiki). Report a needed file outside the brief instead of editing it.
- The reviewer inspects plans, designs, and completed code independently under [Reviews](#reviews). Reviewers never fix reviewed source.

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

Review each design and plan independently before implementation. Review completed code once per plan or release after the last code task, then confirm fixes. Match review depth to risk; do not invent extra gates. Every review and fix confirmation uses xhigh on the other model under [routing](../AGENTS.md#ai-agent-definitions-and-routing).

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
4. Require every triggered validation workflow to pass for the exact PR head, including push, PR, and manually dispatched validation runs. A merge-ref result is not head evidence. An empty set, missing, skipped, cancelled, pending, or failed required run is not success. Save the head and workflow evidence privately.
5. Land with `git push origin <head>:master` using the owner's approved admin bypass. Never force-push or delete master, even when bypass permissions would allow it. Delete the completed slice branch after landing. Dependent slices follow release order and this same ancestry and CI gate.

Fix red CI forward at once. Tags and feature scope follow [release](release.md).
