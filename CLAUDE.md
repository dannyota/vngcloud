@AGENTS.md

## Claude routing and invocation

Claude designs, researches, and coordinates only. Design runs on Fable high through `designer`; when unavailable, use `gpt-6-astra` high in a general-purpose worker. No implementation or review runs on a Claude model. Follow [shared routing](AGENTS.md#ai-agent-definitions-and-routing), with explicit model and effort on every dispatch.

The Sonnet/Opus restriction applies only to workers and subagents. The owner chooses the main Claude Code session model without this restriction.

Use `codex exec` directly, not a companion script. The installed `codex exec --help` supports `-C`, `-m`, `-c`, `--sandbox`, `--skip-git-repo-check`, `-o`, and `-` for a brief on standard input. Include the matching role TOML's instructions from `.codex/agents/` in the brief: `-m` selects a model and does not load a role file. Use [shared duties](instructions/workflow.md#duties) and routing for every role.

Implementation and build-capable code review use `--sandbox workspace-write` with additional writable roots limited to `$HOME/.cache/go-build`, `$HOME/.cache/golangci-lint`, and `/tmp`, and `sandbox_workspace_write.network_access=true`. This configuration supports loopback sockets for tests and writes to the two tool caches required by `make check`. Writes stay confined to the owned worktree or disposable review copy, those caches, and `/tmp` for temporary work. The module cache needs no write access. Disabling network access blocks loopback listeners used by the race tests; no loopback-only option exists. The `-c` flags below are authoritative; `-m` does not load role files. Keep the [brief restrictions](instructions/workflow.md#briefs-and-reports) and [credential-free worker worktree rule](instructions/workflow.md#worktrees-and-parallel-work) as defense in depth for network access. For owned role-file changes, follow the [scratchpad patch rule](instructions/workflow.md#briefs-and-reports).

Implementation runs in its owned worktree:

```sh
codex exec -C <owned-worktree> -m gpt-6.1-sol -c model_reasoning_effort=medium --sandbox workspace-write -c "sandbox_workspace_write.writable_roots=[\"$HOME/.cache/go-build\",\"$HOME/.cache/golangci-lint\",\"/tmp\"]" -c 'sandbox_workspace_write.network_access=true' -o <scratchpad>/result.md - < <scratchpad>/brief.md
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

The reviewer may write build output and probes in that copy, never source fixes. Cache and network setup remain manager-owned. The brief's ban on unrelated network calls and secret reads provides defense in depth for network access; path restrictions still apply. Design reviews stay read-only in the source worktree.

## Scratchpad

Use a private scratchpad directory for briefs, temporary files, review copies, and output; put no temporary files under `docs/`. Substitute actual paths for command placeholders and quote paths with spaces. Save review output in the scratchpad. Turn actionable findings into one-line TODO tasks without finding IDs; follow [documentation](instructions/documentation.md#writing-rules) for retention and completion.
