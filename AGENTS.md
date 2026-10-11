# vngcloud: guide for coding agents

Shared rules live here and in `instructions/`. [CLAUDE.md](CLAUDE.md) adds Claude-specific invocation details. Read the topic instructions for the work in hand.

## State

`vngcloud` is Danny's personal, unofficial Go SDK and AWS-CLI-style command-line tool for VNG Cloud, now GreenNode. The project uses Apache-2.0 and is not affiliated with VNG Cloud or GreenNode. The repository, wiki, and CI logs are public.

The module is `danny.vn/vngcloud`. Public service packages live at the root; `vngcloud.go` exposes shared names, `internal/` holds private implementation, and `cmd/vngcloud/` is the CLI entry point. Unit fixtures live in `testdata/`, live tests in `livetest/`, and user docs in `docs/wiki/`.

Public NAT create, quote, delete, and zone and package reads are implemented for IAM login in Hanoi. Designs with unresolved API evidence, cost, permission, or resource prerequisites remain gated; document presence does not approve implementation or a live run.

## Map

|Question|File|
|-|-|
|Scope and intended SDK/CLI surface|[SDK and CLI design](docs/design/sdk-and-cli.md)|
|Project facts and current service coverage|[README.md](README.md)|
|Blockers and unspecified contracts|[Domain designs](docs/design/README.md) and open work in [plans](docs/plans/)|
|Decisions and reasons|[Architecture decision records](docs/adr/README.md) and owner decisions in [designs](docs/design/README.md)|
|Licenses and dependency reasons|[LICENSE](LICENSE), [CLI dependencies](docs/design/cli.md#dependencies), and the dependency's approved design|
|Roadmap and dependency order|[Designs](docs/design/README.md) and [plans](docs/plans/)|
|Short-term TODO work|[TODO.md](TODO.md)|
|Architecture and package boundaries|[SDK](docs/design/sdk-and-cli.md#sdk) and [CLI layout](docs/design/cli.md#layout)|
|Domain rules, mechanism specifications, and API evidence|[Design index](docs/design/README.md) and its linked API and checks documents|
|Implementation plans|[docs/plans/](docs/plans/)|
|User-facing SDK and CLI docs|[Wiki source](docs/wiki/Home.md)|
|Release facts|[RELEASE_NOTES.md](RELEASE_NOTES.md) and its linked archives|
|Current behavior|Code and tests in the relevant package|

|Doing|Read first|
|-|-|
|Design or planning|[Workflow](instructions/workflow.md), [documentation](instructions/documentation.md), [ADRs](docs/adr/README.md), and the relevant [design](docs/design/README.md)|
|Writing documents|[Documentation](instructions/documentation.md)|
|Writing Go|[Go](instructions/go.md) and [testing](instructions/testing.md)|
|Testing or checking a report|[Testing](instructions/testing.md) and [workflow](instructions/workflow.md#briefs-and-reports)|
|Security, auth, credential, or write review|[Security](instructions/security.md) and [reviews](instructions/workflow.md#reviews)|
|Live calls, captures, or fixtures|[Live data](instructions/live-data.md) and [live verification](instructions/testing.md#live-verification)|
|Git, landing, or release|[Workflow](instructions/workflow.md#git), [landing](instructions/workflow.md#landing-a-slice), and [release](instructions/release.md)|
|Work in one domain|This guide, the domain's [design and API/checks siblings](docs/design/README.md), relevant [ADRs](docs/adr/README.md), and the topic instructions above|

Direct user instructions and platform safety rules come first. Accepted decisions override conflicting design or analysis; fix the conflicting text. Designs and mechanism specifications override plans. Code and tests describe behavior. Owner approval gates new contracts, and independent design and plan review precedes code. Fix wiki disagreement with code in the same change.

## AI agent definitions and routing

Definitions live in [.claude/agents/](.claude/agents/) and [.codex/agents/](.codex/agents/). The role definitions are `designer`, `design_reviewer`, `implementer`, and `code_reviewer`. Dispatch uses the routing below, with duties and brief-scoped ownership in [workflow](instructions/workflow.md#duties); role defaults do not select the dispatch model.

|Work|Model and effort|
|-|-|
|Design|Fable high through `designer`; when unavailable, `gpt-6-astra` high in a general-purpose worker|
|Planning and coordination workers|`gpt-6.1-sol` medium|
|All code, scripts, workflows, debugging, and small code edits|`gpt-6.1-sol` medium|
|Exploration, searches, summaries, test execution, and mechanical non-code work|`gpt-6-luna` low|
|Review and fix confirmation of Sol code or Sol-authored plans|`gpt-6-astra` xhigh|
|Review and fix confirmation of Astra designs|`gpt-6.1-sol` xhigh|
|Review and fix confirmation of Fable or human work|Whichever of Sol or Astra is free, at xhigh|

Claude designs, researches, and coordinates only. No worker or subagent runs on Sonnet or Opus. This restriction covers workers and subagents only; the owner chooses the main Claude Code session model. Set model and effort explicitly on every dispatch. A design author never implements that slice; a reviewer never fixes or reviews work it wrote. The primary agent remains responsible for planning, briefs, worktrees, verification, Git, releases, and reports.

## Rules for every task

1. Start with `git status --short`. Existing changes belong to someone else unless the brief says otherwise. Follow the brief's paths and stop on conflicting authority, overlapping ownership, or an unresolved contract.
2. Put security before features and convenience. Pick safety when ease conflicts with safety and tell the owner. Follow [security](instructions/security.md); never commit credentials, account data, live captures, or internal notes, or read secret values into the conversation.
3. Describe the current state in living docs, without dates or history. Follow [documentation](instructions/documentation.md); retain versioned release facts in release notes.
4. Report the exact files and checks, including failures, skipped checks, and open items. Never claim verification that did not run. Follow [briefs and reports](instructions/workflow.md#briefs-and-reports).
