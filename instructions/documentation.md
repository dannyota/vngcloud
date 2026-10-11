# Documentation

Each fact has one home. Link to that home instead of repeating it. [AGENTS.md](../AGENTS.md#map) routes readers to current authorities.

## Placement

|Content|Current home|
|-|-|
|Project facts and public quick start|[README.md](../README.md) and [LICENSE](../LICENSE)|
|Intended surface, architecture, domain rules, API mechanisms, evidence, and checks|[docs/design/](../docs/design/README.md)|
|Accepted choices and reasons|[docs/adr/](../docs/adr/README.md) and owner decisions in the relevant design|
|Open gates, work order, and completion checkboxes|[docs/plans/](../docs/plans/)|
|Dependency reasons|The approved design, including [CLI dependencies](../docs/design/cli.md#dependencies)|
|User-facing SDK and CLI guidance|[docs/wiki/](../docs/wiki/Home.md)|
|Versioned release facts|[RELEASE_NOTES.md](../RELEASE_NOTES.md) and its linked archives|
|Shared work rules|The relevant file in `instructions/`|

Use existing authorities until replacement documents exist. Create a directory with its first real file, not a placeholder. A domain design owns scope and rules; mechanism specifications own wire contracts and evidence. Link supporting API and checks documents instead of detaching checks from their contract. Preserve source provenance and distinguish verified behavior, inference, and unresolved gates.

## Writing rules

- Lead with the answer or purpose. Use plain, exact words, active voice, consistent terms, and short paragraphs. Keep each sentence focused and unambiguous. Define unfamiliar abbreviations. State real uncertainty and its cause; do not hedge verified facts.
- Use bullets for parallel items, numbers for ordered steps, tables for comparisons, and prose for connected reasoning. Add headings only when they help navigation. No em dashes, filler, unasked recaps, or restating the request.
- Living docs describe the current state without dates, status history, or stale statements. Rewrite affected text in touched files; do not start repository-wide sweeps. Preserve versioned facts in release notes.
- Never cite plans, tasks, review findings, or their names or IDs in code, comments, tests, or living docs. Cite the design or ADR, or state the rule. Navigation may link the plans directory.
- Keep completed plans with checked completion boxes. A short-term TODO task is one unchecked line and leaves the list in the commit that completes it. Do not put account details in plans or public TODO items.
- Agent-only files (`AGENTS.md`, `CLAUDE.md`, `instructions/`, `.claude/`, `.codex/`, and `docs/plans/`) use one line per paragraph or list item and unpadded tables. Human docs wrap at 80 columns.
- General Markdown stays at or under 450 lines. New or converted designs and specifications stay at or under 400 lines; existing source designs retain the general cap until conversion. Preserve generated-page exemptions in `scripts/check-lengths.sh`. Split by topic or section and link the parts without losing information.
- Inspect relative links and heading anchors after moves. Retain forwarding files and their consumed headings until every consumer moves in the atomic document cutover. Do not delete a forwarder while a contract still links to it.

## Public surface and wiki

Update the matching `docs/wiki/` page in the same change as a public SDK or CLI surface change. Fix disagreement between code and wiki in that change. A breaking public API change also updates `RELEASE_NOTES.md`.

Wiki links between pages use `Page-Name.md` so they work in the repo and published wiki. Links from wiki pages to repository files use absolute repository URLs. Preserve the [wiki source and publication decision](../docs/adr/0001-wiki-from-docs.md) and generated-doc workflow; change generated pages through their source.

Follow [Go](go.md#formatting-and-comments) for code comments and [testing](testing.md#required-checks) for documentation checks.
