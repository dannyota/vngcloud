@AGENTS.md

## Dispatch

Follow [routing](AGENTS.md#ai-agent-definitions-and-routing), [roles](instructions/roles.md), and [duties](instructions/workflow.md#duties). The owner chooses the main session model; the routing applies to workers.

- Dispatch each worker as a fresh `Agent` subagent with `subagent_type` `terra`, `sol`, or `astra` and no `model` override. These are the owner's user-level agents; each resolves to the newest model of its family.
- The worker's first report line is its runtime model ID, which must belong to the named family. Do not require a specific versioned ID.
- Each brief names the role and tells the worker to follow `instructions/roles.md` for it, plus the objective, owned paths, forbidden actions, checks, and report contract. These agents have every tool, so reviewer briefs forbid edits, writing commands, and Git changes.
- Give reviewers the exact diff, base and head commits, worktree status, local check output, and exact-commit CI evidence. A reviewer may read and test the source worktree but never writes to it.
- Run independent workers in the background in one message; you are notified when each finishes. Answer a worker's question, or confirm fixes with the same reviewer, through `SendMessage` to that agent instead of starting a new one. Every first review is a fresh agent, never the author.
- When a user-level agent cannot be dispatched, use the Codex plugin: the `codex:codex-rescue` agent, `/codex:review`, or `/codex:adversarial-review`, with `--model` set to the family's newest model ID. When GPT is unavailable, use the Claude stand-in from the routing table.
- Keep at most about three workers that run `make check` at once; more saturate the laptop and race suites fail with load-sensitive errors.

## GitHub commands

Follow the notification cleanup rule in [landing a slice](instructions/workflow.md#landing-a-slice) whenever working with GitHub. List only this repository's threads; replace `<id>` with a handled thread's ID from that listing before closing it as done.

```sh
gh api 'notifications?all=true&per_page=100' --paginate --jq '.[] | select(.repository.full_name == "dannyota/vngcloud")'
gh api -X DELETE 'notifications/threads/<id>'
```

## Scratchpad

Use a private scratchpad directory for briefs, temporary files, and output; put no temporary files under `docs/`. Substitute actual paths for command placeholders and quote paths with spaces. Save review output in the scratchpad. Turn actionable findings into one-line TODO tasks without finding IDs; follow [documentation](instructions/documentation.md#writing-rules) for retention and completion.

## Gotchas

- The Bash tool runs zsh: `$var` does not word-split, globs that match nothing abort the command, and a word starting with `=` expands to a command path. `sed` is GNU sed, so `sed -i ''` fails. Write multi-step scripts to the scratchpad and run them with `bash`.
- Before a Go worker starts in a new worktree, run `go mod download all`, then `git restore go.sum`, which the download rewrites.
- Local tools must match `.tool-versions`; a newer local golangci-lint reports findings CI does not. [Security](instructions/security.md#tools-and-scanners) owns pin and Actions allowlist rules.
- Wait on a PID, a file, or a background-task notification, never on `pgrep -f '<pattern>'`, which matches its own command line.
- Playwright MCP writes files only under the repository or `.playwright-mcp/`; it refuses the scratchpad. [Live data](instructions/live-data.md) covers sign-in and captures.
