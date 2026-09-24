# Roles and models

Role details for the table in [AGENTS.md](../AGENTS.md#roles). The manager reads this file; other roles read their own section.

## manager

The manager turns the owner's request into small releases, splits each into disjoint file sets, briefs one role per set, and keeps planning, verification, Git, tags, and the final answer. It asks the owner only for decisions the owner must make, one question at a time, with options and a recommendation. It verifies a report by reading the diff and running `make check`, not by trusting the report.

## architect

The architect writes design docs in `docs/design/` and ADRs in `docs/adr/`. A design covers the public SDK or CLI surface, errors, compatibility with existing callers, security, and the release order. The owner approves a design before code starts. The architect answers contract questions during the build but does not implement it.

## sdk and cli

Implementers. Each writes the failing test first inside its owned paths, runs `make check`, and reports. A change that needs a file another role owns is reported, not made. The cli role uses only the public `danny.vn/vngcloud` package: if the CLI needs something the SDK lacks, it reports the missing SDK method.

Each implementer updates the `docs/wiki/` pages for the surface it changed, in the same change.

## reviewer

Reviews once per plan or release, after the last code task. Reviews auth, token handling, credential storage, and every write API adversarially. It names each invariant it confirmed, ranks findings by severity with a failure scenario and `path:line`, and confirms fixes. It never reviews its own work.

## Models

| Work | Claude Code | Codex |
|-|-|-|
| Management, design, planning, and review | Opus | `gpt-5.6-sol` |
| Routine implementation and debugging | Sonnet | `gpt-5.6-terra` |
| Search, summaries, test runs, and small mechanical edits | Haiku | `gpt-5.6-luna` |

Set the model on every dispatch. A re-check of a small fix after review uses the implementation tier.
