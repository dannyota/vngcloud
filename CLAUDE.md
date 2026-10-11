@AGENTS.md

## Claude routing and invocation

Claude designs, researches, and coordinates only. Design runs on Fable high through `designer`; when unavailable, use `gpt-6-astra` high in a general-purpose worker. No implementation or review runs on a Claude model. Follow [shared routing](AGENTS.md#ai-agent-definitions-and-routing), with explicit model and effort on every dispatch.

The Sonnet/Opus restriction applies only to workers and subagents. The owner chooses the main Claude Code session model without this restriction.

Use `codex exec` directly, not a companion script. The installed `codex exec --help` supports `-C`, `-m`, `-c`, `--sandbox`, `--skip-git-repo-check`, `-o`, and `-` for a brief on standard input. Include the matching role TOML's instructions from `.codex/agents/` in the brief: `-m` selects a model and does not load a role file. Use [shared duties](instructions/workflow.md#duties) and routing when a wrapper still names a legacy role.

Implementation and build-capable code review use `--sandbox danger-full-access`: the required checks need loopback sockets and tool caches outside the worktree. `workspace-write` allows `go build` here but blocks `make lint` and the race suite, so it cannot run `make check`. Follow the [brief restrictions](instructions/workflow.md#briefs-and-reports) and [credential-free worker worktree rule](instructions/workflow.md#worktrees-and-parallel-work).

Implementation runs in its owned worktree:

```sh
codex exec -C <owned-worktree> -m gpt-6.1-sol -c model_reasoning_effort=medium --sandbox danger-full-access -o <scratchpad>/result.md - < <scratchpad>/brief.md
```

Astra design review uses Sol/xhigh/read-only in the source worktree:

```sh
codex exec -C <owned-worktree> -m gpt-6.1-sol -c model_reasoning_effort=xhigh --sandbox read-only -o <scratchpad>/review.md - < <scratchpad>/review-brief.md
```

Use the other model at xhigh for Sol-authored documents under shared routing. A reviewer never fixes the reviewed source.

## Build-capable code review

The manager prepares a disposable copy outside every Git working tree. Supply only tracked source and authorized new files. Exclude `.git`, `.env`, private configs, captures, and local agent state. Add `CHANGES.txt` with the base commit, complete tracked diff, and new-file inventory. Include the matching code-review role TOML instructions and shared authorities in the brief; do not rely on model selection to load them.

```sh
codex exec -C <disposable-copy> --skip-git-repo-check -m gpt-6-astra -c model_reasoning_effort=xhigh --sandbox danger-full-access -o <scratchpad>/review.md - < <scratchpad>/review-brief.md
```

The reviewer may write build output and probes in that copy, never source fixes. Cache and network setup remain manager-owned. The brief's network, secret-read, and path restrictions apply despite the sandbox mode. Design reviews stay read-only in the source worktree.

## Scratchpad

Use a private scratchpad directory for briefs, temporary files, review copies, and output; put no temporary files under `docs/`. Substitute actual paths for command placeholders and quote paths with spaces. Save review output in the scratchpad. Turn actionable findings into one-line TODO tasks without finding IDs; follow [documentation](instructions/documentation.md#writing-rules) for retention and completion.
