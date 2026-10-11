@AGENTS.md

## Claude routing and invocation

Claude designs, researches, and coordinates only. Design runs on Fable high through `designer`; when unavailable, `gpt-6-astra` high designs through `codex exec`, never through a Claude subagent. No implementation, troubleshooting, or review runs on a Claude model. Follow [shared routing](AGENTS.md#ai-agent-definitions-and-routing), with explicit model and effort on every dispatch.

The Sonnet/Opus restriction applies only to workers and subagents. The owner chooses the main Claude Code session model without this restriction.

Use `codex exec` directly, not a companion script. The installed `codex exec --help` supports `-C`, `-m`, `-c`, `--sandbox`, `--skip-git-repo-check`, `-o`, and `-` for a brief on standard input. Include the matching role TOML's instructions from `.codex/agents/` in the brief: `-m` selects a model and does not load a role file. Append them with `python3 -I -c 'import sys,tomllib; print(tomllib.load(open(sys.argv[1],"rb"))["developer_instructions"])' .codex/agents/<role>.toml >> <scratchpad>/brief.md`. Use [shared duties](instructions/workflow.md#duties) and routing for every role.

Implementation and build-capable code review use `--sandbox workspace-write` with additional writable roots limited to `$HOME/.cache/go-build`, `$HOME/.cache/golangci-lint`, and `/tmp`, and `sandbox_workspace_write.network_access=true`. This configuration supports loopback sockets for tests and writes to the two tool caches required by `make check`. Writes stay confined to the owned worktree or disposable review copy, those caches, and `/tmp` for temporary work. The module cache needs no write access. Disabling network access blocks loopback listeners used by the race tests; no loopback-only option exists. The `-c` flags below are authoritative; `-m` does not load role files. Keep the [brief restrictions](instructions/workflow.md#briefs-and-reports) and [credential-free worker worktree rule](instructions/workflow.md#worktrees-and-parallel-work) as defense in depth for network access. For owned role-file changes, follow the [scratchpad patch rule](instructions/workflow.md#briefs-and-reports).

Implementation runs in its owned worktree:

```sh
codex exec -C <owned-worktree> -m gpt-6.1-sol -c model_reasoning_effort=medium --sandbox workspace-write -c "sandbox_workspace_write.writable_roots=[\"$HOME/.cache/go-build\",\"$HOME/.cache/golangci-lint\",\"/tmp\"]" -c 'sandbox_workspace_write.network_access=true' -o <scratchpad>/result.md - < <scratchpad>/brief.md
```

Troubleshooting uses the same command with `-m gpt-6.1-sol -c model_reasoning_effort=xhigh` and the `troubleshooter` instructions. Astra design, and implementation under an owner exception that names Astra, use `-m gpt-6-astra -c model_reasoning_effort=high` with the same sandbox flags; design research may use the network only for the first-party sources the brief names.

Redirect each run's output to `<scratchpad>/logs/<task>.log`; its header prints `session id: <id>`. To answer a worker's question, resume from the same directory with the original run's sandbox. `codex exec resume` rejects `--sandbox`, `-C`, and `--color`, so set the sandbox with `-c`. A `workspace-write` run repeats its writable roots and network flags as below; a read-only review resumes with `-c 'sandbox_mode="read-only"'` and nothing else:

```sh
codex exec resume <id> -m <model> -c model_reasoning_effort=<effort> -c 'sandbox_mode="workspace-write"' -c "sandbox_workspace_write.writable_roots=[\"$HOME/.cache/go-build\",\"$HOME/.cache/golangci-lint\",\"/tmp\"]" -c 'sandbox_workspace_write.network_access=true' -o <scratchpad>/result.md - < <scratchpad>/answer.md
```

Astra design review uses Sol/xhigh/read-only in the source worktree:

```sh
codex exec -C <owned-worktree> -m gpt-6.1-sol -c model_reasoning_effort=xhigh --sandbox read-only -o <scratchpad>/review.md - < <scratchpad>/review-brief.md
```

Use the other model at xhigh for Sol-authored documents under shared routing. A reviewer never fixes the reviewed source.

## Build-capable code review

The manager prepares a disposable copy outside every Git working tree. Supply only tracked source and authorized new files. Exclude `.git`, `.env`, private configs, captures, and local agent state. Add `CHANGES.txt` with the base commit, complete tracked diff, and new-file inventory. Include the matching code-review role TOML instructions and shared authorities in the brief; do not rely on model selection to load them.

```sh
codex exec -C <disposable-copy> --skip-git-repo-check -m gpt-6-astra -c model_reasoning_effort=xhigh --sandbox workspace-write -c "sandbox_workspace_write.writable_roots=[\"$HOME/.cache/go-build\",\"$HOME/.cache/golangci-lint\",\"/tmp\"]" -c 'sandbox_workspace_write.network_access=true' -o <scratchpad>/review.md - < <scratchpad>/review-brief.md
```

The copy has no Git metadata, so `scripts/check-lengths.sh`, which enumerates files with `git ls-files`, passes there without checking anything; the manager runs it in the source worktree. If `go build` reports a VCS status error in the copy, add `-buildvcs=false`. The reviewer may write build output and probes in that copy, never source fixes. Cache and network setup remain manager-owned. The brief's ban on unrelated network calls and secret reads provides defense in depth for network access; path restrictions still apply. Design reviews stay read-only in the source worktree.

## GitHub commands

Follow the notification cleanup rule in [landing a slice](instructions/workflow.md#landing-a-slice) whenever working with GitHub. List only this repository's threads; replace `<id>` with a handled thread's ID from that listing before closing it as done.

```sh
gh api 'notifications?all=true&per_page=100' --paginate --jq '.[] | select(.repository.full_name == "dannyota/vngcloud")'
gh api -X DELETE 'notifications/threads/<id>'
```

## Scratchpad

Use a private scratchpad directory for briefs, temporary files, review copies, and output; put no temporary files under `docs/`. Substitute actual paths for command placeholders and quote paths with spaces. Save review output in the scratchpad. Turn actionable findings into one-line TODO tasks without finding IDs; follow [documentation](instructions/documentation.md#writing-rules) for retention and completion.
