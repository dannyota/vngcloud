@AGENTS.md

## Claude routing and invocation

Claude designs, researches, and coordinates only. Design runs on Fable high through `designer`, then the Codex `designer` role (`gpt-6-astra` high) reviews and improves the same documents, except for [low-risk slices](instructions/workflow.md#reviews); when Fable is unavailable, Astra high writes the design. Astra never runs through a Claude subagent. No implementation, troubleshooting, or review runs on a Claude model. Follow [shared routing](AGENTS.md#ai-agent-definitions-and-routing) and [shared duties](instructions/workflow.md#duties), with explicit model and effort on every dispatch.

The Sonnet/Opus restriction applies only to workers and subagents. The owner chooses the main Claude Code session model without this restriction.

## Codex dispatch

Run every Codex job through the launcher; never hand-build a `codex exec` command:

```sh
scripts/codex-agent.sh --model <m> --effort <e> [--no-git] <role> <dir> <brief-file> <report-file> > <scratchpad>/logs/<task>.log 2>&1
scripts/codex-agent.sh --resume <session-id> --model <m> --effort <e> [--no-git] <role> <dir> <answer-file> <report-file> >> <scratchpad>/logs/<task>.log 2>&1
```

The launcher requires model and effort, loads the role's developer instructions and sandbox from `<dir>/.codex/agents/<role>.toml` (or this repository's copy when `<dir>` has none), and feeds the brief on standard input. Briefs therefore need no appended role text. `workspace-write` roles get writable roots limited to `$HOME/.cache/go-build`, `$HOME/.cache/golangci-lint`, and `/tmp`, plus network access: race tests need loopback listeners, no loopback-only option exists, and `make check` writes those caches. Writes stay inside the worktree or disposable copy, those caches, and `/tmp`. Network access never authorizes unrelated calls; the [brief restrictions](instructions/workflow.md#briefs-and-reports) and [credential-free worktrees](instructions/workflow.md#worktrees-and-parallel-work) stay as defense in depth. `--no-git` adds `--skip-git-repo-check` for disposable copies on both new and resumed runs.

Pick each job's model and effort from [routing](AGENTS.md#ai-agent-definitions-and-routing), which separates simple from complex code jobs; this table maps work to roles and directories.

|Work|Role|Directory|
|-|-|-|
|Design after Fable, or instead of it|`designer`|Owned worktree; network only for first-party sources the brief names|
|Code, scripts, workflows|`implementer`|Owned worktree|
|Failures and root cause|`troubleshooter`|Owned worktree|
|Design and plan review|`design_reviewer`|Source worktree, read-only|
|Code review and fix confirmation|`code_reviewer`|Disposable copy, `--no-git`; source read-only|
|Exploration, summaries, test runs|`explorer`|Owned worktree|

Each log header prints `session id: <id>`; resume with that ID to answer a worker's question. Every review starts as a new run, never a resumed author session. Fix confirmation may resume the reviewer's own session, since the reviewer did not write the fixes. A reviewer never fixes the reviewed source. Codex mounts `.codex/` read-only, so role-file changes follow the [scratchpad patch rule](instructions/workflow.md#briefs-and-reports).

- `-o` overwrites `<report-file>`; copy the report before a resume. Briefs end with "Return the complete report as your final message"; the Fable `designer` subagent cannot write report files either.
- Parallel jobs: `(scripts/codex-agent.sh ...; echo done > <scratchpad>/logs/<task>.done) &`, then wait with one background until-loop on the `.done` files; a bare `&` gives no completion notice.

## Build-capable code review

The manager prepares a disposable copy outside every Git working tree. Supply only tracked source and authorized new files. Exclude `.git`, `.env`, private configs, captures, and local agent state. Add `CHANGES.txt` with the base commit, the source's initial `git status --short`, new and deleted files, and the complete diff; for fix confirmation, put the fix diff (`<old>..<new>`) first. Run `code_reviewer` there with `--no-git`.

The copy has no Git metadata, so `scripts/check-lengths.sh`, which enumerates files with `git ls-files`, passes there without checking anything; the manager runs it in the source worktree. If `go build` reports a VCS status error in the copy, add `-buildvcs=false`. The reviewer may write build output and probes in that copy, never source fixes. Cache and network setup remain manager-owned. Design reviews stay read-only in the source worktree.

## GitHub commands

Follow the notification cleanup rule in [landing a slice](instructions/workflow.md#landing-a-slice) whenever working with GitHub. List only this repository's threads; replace `<id>` with a handled thread's ID from that listing before closing it as done.

```sh
gh api 'notifications?all=true&per_page=100' --paginate --jq '.[] | select(.repository.full_name == "dannyota/vngcloud")'
gh api -X DELETE 'notifications/threads/<id>'
```

## Scratchpad

Use a private scratchpad directory for briefs, temporary files, review copies, and output; put no temporary files under `docs/`. Substitute actual paths for command placeholders and quote paths with spaces. Save review output in the scratchpad. Turn actionable findings into one-line TODO tasks without finding IDs; follow [documentation](instructions/documentation.md#writing-rules) for retention and completion.

## Gotchas

- The Bash tool runs zsh: `$var` does not word-split, globs that match nothing abort the command, and a word starting with `=` expands to a command path. `sed` is GNU sed, so `sed -i ''` fails. Write multi-step scripts to the scratchpad and run them with `bash`.
- Codex sandboxes cannot write the Go module cache, and macOS puts the default `GOCACHE` under `~/Library/Caches`, outside the writable roots. The launcher points `GOCACHE` and `GOLANGCI_LINT_CACHE` at the writable caches; before a Go worker starts, run `go mod download all` in the worktree, then `git restore go.sum`, which the download rewrites.
- Local tools must match `.tool-versions`; a newer local golangci-lint reports findings CI does not. [Security](instructions/security.md#tools-and-scanners) owns pin and Actions allowlist rules.
- Wait on a PID, a file, or a background-task notification, never on `pgrep -f '<pattern>'`, which matches its own command line.
- Fable can stop mid-run at its usage limit (HTTP 429). Check the worktree for partial edits, then send the same brief to the Codex `designer` role.
- Playwright MCP writes files only under the repository or `.playwright-mcp/`; it refuses the scratchpad. [Live data](instructions/live-data.md) covers sign-in and captures.
