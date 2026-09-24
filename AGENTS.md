# AGENTS.md

## Scope and authority

This file is the single source of coding-agent rules for Claude Code and Codex. `CLAUDE.md` imports it and adds nothing. Role agents in `.claude/agents/` and `.codex/agents/` are thin wrappers that name a role and the [instruction files](#instruction-files) it reads.

`vngcloud` is a personal, unofficial Go SDK and AWS-CLI-style command-line tool for VNG Cloud (now GreenNode), for the owner (Danny) and AI agents. It is Apache-2.0 and not affiliated with VNG Cloud or GreenNode. **The repository, its wiki, and its CI logs are public.** Never commit credentials, account data, live captures, or internal notes.

Direct user instructions and platform safety rules come first. Then:

| Question | Authority |
|-|-|
| Intended SDK and CLI design | `docs/design/` |
| Why one decision was made | `docs/adr/` |
| Current behavior | Code and tests |
| User-facing SDK and CLI docs | `docs/wiki/`, published to the GitHub wiki |
| Work order for a multi-step goal | The active plan in `docs/plans/`, when one exists |

Design wins over a plan. An accepted ADR wins over design text it contradicts; fix the text. When code and `docs/wiki/` disagree, fix the doc in the same change.

## Instruction files

| File | Read when |
|-|-|
| [`instructions/roles.md`](instructions/roles.md) | Managing work or checking another role's duty |
| [`instructions/verification.md`](instructions/verification.md) | Writing, testing, or reviewing code |
| [`instructions/live-data.md`](instructions/live-data.md) | Running anything against the real API, or writing fixtures |

## Roles

Every agent works in one role. The owner talks to the manager. Other roles take work only from a manager brief and report back to it.

| Role | Owns | Never |
|-|-|-|
| manager | Plans, briefs, Git, releases, repo tooling (`AGENTS.md`, `instructions/`, `.claude/`, `.codex/`, `.github/`, `scripts/`, `Makefile`, `go.mod`, `README.md`) | Writes SDK or CLI code another role owns |
| architect | `docs/design/`, `docs/adr/` | Implements the slice it designed |
| sdk | `vngcloud.go`, `internal/` except `internal/cli/`, `testdata/`, `examples/`, `live_test.go`, SDK pages in `docs/wiki/` | Edits CLI code |
| cli | `cmd/vngcloud/`, `internal/cli/`, CLI pages in `docs/wiki/` | Calls the API except through the public SDK |
| reviewer | Read-only review of a diff, plan, or release | Edits files, or reviews work it wrote |

A path outside every row belongs to the manager, which assigns it in a brief. Every Claude Code session starts as the manager (`"agent": "manager"` in `.claude/settings.json`). Role duties and models are in [`instructions/roles.md`](instructions/roles.md).

## How we work

- **Small releases.** One feature per `v0.x.y` tag. Ship a feature when it is done and CI is green; do not batch features.
- **Local checks, CI gate.** Run `make check` before each commit; it takes seconds. GitHub CI runs the same checks plus a full-history gitleaks scan. A red run is fixed forward at once. A tag needs green CI on that exact commit.
- **Merge to `master` locally and push; no pull requests.** Use a branch for multi-commit work and delete it after merging.
- **Parallel work:** at most three workers, on disjoint file sets. Do not spawn workers to repeat verification.
- Match review depth to risk. Do not invent extra gates.

## Briefs and reports

A brief names the objective, authorities, owned paths, forbidden actions, definition of done, and required checks. It repeats the writing rules below, so plan and task IDs never reach code.

Start every task with `git status --short`: existing changes belong to someone else unless the brief says otherwise. Read the named authorities, inspect the code and tests, and write the failing test before the fix. Stop at a document conflict, overlapping ownership, or missing decision, and report it; do not invent a contract.

A report gives the exact file set (new, changed, deleted), checks run with results, checks not run with the reason, and open items. Never claim a check that did not run.

## Git

Only the manager touches Git. Workers never add, commit, stash, reset, checkout, or switch branches unless the brief says so.

- Stage exact paths with `git add -- <paths>`. Never use `git add .`, `git add -A`, `git commit -a`, or force-add. Never stage `.env` or `examples/basic/config.*.json`.
- Amend or reorder only commits that are not pushed.
- Conventional Commits. Messages describe the change only and never mention agents, AI, or automated assistance.
- Keep local-only paths in `.git/info/exclude`, not in the committed `.gitignore`. Of `.claude/` and `.codex/`, only `agents/` and `.claude/settings.json` are tracked.
- Install the pre-commit hook once per clone with `make hooks-install`. It runs gitleaks on staged content and the length check.

## Writing docs and code comments

- Keep text short and plain. Say each fact once, in the file that owns it, and link to it elsewhere. No em dashes.
- Never cite plans, tasks, review findings, or their IDs in code, comments, tests, or living docs. Cite the design doc or ADR, or state the rule. Plans are deleted when their work ends.
- Describe the current state, not history.
- Fix stale text in files your change touches. No repository-wide sweeps.
- Markdown stays at or under 450 lines and non-test Go at or under 700 (`scripts/check-lengths.sh`). Split a long file by topic.
- Files only agents read (`AGENTS.md`, `CLAUDE.md`, `instructions/`, `.claude/`, `.codex/`, `docs/plans/`) stay minified: one line per paragraph or list item, tables without padding. Markdown people read wraps at 80 columns.
- A comment explains a constraint the code cannot express. Do not narrate the code.
