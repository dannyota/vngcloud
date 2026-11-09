# Billing history implementation plan

Implement the accepted [billing history design](../design/billing-history.md) and [API evidence and checks](../design/billing-history-api.md), preserving [existing billing contracts](../design/billing.md) and [ADR 0002](../adr/0002-write-api-conventions.md). Those authorities own contracts; this file owns order, assignments, gates, and acceptance and grants no live-run, credential, purchase, or cleanup authority.

## Baseline and gates

- [x] The manager accepts fixed UTC+07:00, inclusive public bounds, and next-calendar-day exclusive wire end; zone uniqueness and exact-midnight behavior remain unproved and are not implementation blockers.
- [x] Recorded IAM-user discovery settles access, accepted size 50, 1-based pages, empty/beyond-end metadata, timestamps, and modeled nullability; do not repeat discovery or treat reports as raw fixtures.
- [x] Existing `billing.Client` has budgets, costs, balances, and resources; history methods, spend aggregation, history CLI registration, and raw history fixtures are absent. Code inspection is not test verification.
- [ ] Receive the independent design fix confirmation already running; do not anticipate its verdict or extend the accepted contract.
- [ ] Obtain independent plan review and fix confirmation through `design_reviewer`, `gpt-6.1-sol` xhigh before code; the author supplies no independent verdict.
- [ ] Manager supplies sanitized raw history fixtures under the [fixture rules](../design/billing-history-api.md#fixtures); SDK decode tests are acceptance gates, and known-code raw rows plus direction/amount semantics gate spend release.
- [ ] Dispatch complex history, spend, and existing-read hardening through `implementer`, `gpt-6.1-sol` xhigh; use separate read-only `code_reviewer`, `gpt-6-astra` high for review and every fix confirmation.
- [ ] Dispatch small CLI help/table slices through `implementer`, `gpt-6.1-terra` xhigh; use separate read-only `code_reviewer`, `gpt-6.1-sol` xhigh for review and every fix confirmation.
- [ ] Route failure investigation and fixes through `troubleshooter`, `gpt-6.1-sol` xhigh; planning workers use `gpt-6.1-sol` medium and exploration/mechanical workers use `explorer`, `gpt-6-luna` low under [routing](../../AGENTS.md#ai-agent-definitions-and-routing).
- [ ] Preserve service-account access, VAT normalization, tied-instant order, snapshot guarantees, payment enums, reference joins, description grammar, and unmodeled fields as explicit unknowns, without reopening settled discovery.
- [ ] Keep invoices, orders, PDFs, writes, description parsing, payment/transaction joins, and cost explorer decoding fixes outside these slices; cost tables, optional cost end dates, and cost auto-paging require separate contracts.

## Ordered, independently landable slices

Each row includes tests, wiki, generated docs where applicable, independent review/fix confirmation, and its own release notes and tag. Proposed tags follow the local release notes baseline `v0.93.0`; the manager must confirm unused versions at release and shift subsequent versions together if another feature ships first, without bundling features.

|Order|Slice|Assignment|Dependency and acceptance|Proposed tag|
|-|-|-|-|-|
|1|Transaction and payment history SDK and CLI|Complex implementer and reviewer|Accepted review gates and sanitized raw fixtures; both list methods, paging, dates, safe output, and live-read registration complete|`v0.94.0`|
|2|Spend SDK and CLI|Complex implementer and reviewer|History slice and known-code raw fixtures; complete-walk sums and bounded reconciliation with safe rendering|`v0.95.0`|
|3|Bound existing billing reads|Complex implementer and reviewer|Independent of new history; preserve existing contracts and prove response bounds|`v0.96.0`|
|4|Billing query-root help|Simple implementer and reviewer|Document actual exported roots for every billing command; no query behavior change|`v0.97.0`|
|5|Compact budget and balance tables|Simple implementer and reviewer|Table-only projections with absent balances shown as `none`|`v0.98.0`|
|6|Budget and threshold enum help|Simple implementer and reviewer|Document exported values without runtime enum rejection|`v0.99.0`|
|7|Resource timestamp tables|Simple implementer and reviewer|Offset-bearing table times with unchanged JSON epoch numbers|`v0.100.0`|

History and existing-read hardening can run in parallel with disjoint code paths: history owns new `billing/history*.go`, `internal/core/errors.go`, `vngcloud.go`, history CLI files, and `livetest/live_test.go`; hardening owns `billing/cost.go`, `billing/budgets.go`, and their tests. Full slices share `docs/wiki/Billing-and-Pricing.md` and `RELEASE_NOTES.md`, so concurrent briefs exclude those shared writes and the manager serializes their integration before either slice lands. Spend follows history. Query help, compact tables, enum help, and resource timestamps share `internal/cli/svc_billing.go`, generator notes, and wiki/generated pages; run those whole slices sequentially in ranked order. Hardening code can also overlap spend or a CLI-only slice with the same shared-document integration rule. Every concurrent writer needs a separate credential-free worktree and disjoint explicit paths; this plan grants no worktree or Git changes to workers.

## Shared implementation and acceptance rules

Only this file is writable under the current brief; paths below are future ownership proposals. Narrow each implementation brief to exact files and selected existing goldens, forbid unlisted changes, require initial `git status --short`, preserve others' edits, and forbid worker Git mutations, secret/private-output reads, and network beyond named checks. Read [Go](../../instructions/go.md), [testing](../../instructions/testing.md), [security](../../instructions/security.md), [documentation](../../instructions/documentation.md), and [workflow](../../instructions/workflow.md#briefs-and-reports) before implementation.

- [ ] Start every code slice with its named failing behavior tests, run focused tests to observe the intended failure, implement the minimum accepted behavior, rerun focused tests, then run `make check` including race tests; never retry a failure into a pass.
- [ ] Keep the SDK standard-library only and the CLI on public SDK methods; preserve public imports, existing command flags, JSON/query roots, and existing error classifications.
- [ ] For every public change, update `docs/wiki/Billing-and-Pricing.md` in the same slice; generator changes belong in source, followed by `make gen-docs` and inspection of every emitted diff, including `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`.
- [ ] For each slice run its focused checks, `make check`, `bash scripts/check-lengths.sh`, `git diff --check`, local link/anchor inspection, and `make wiki-preview`; CLI slices also run `make gen-docs` and verify generated freshness.
- [ ] Each slice owns its separate entry in `RELEASE_NOTES.md`, stating behavior and compatibility limits; hardening notes must identify newly rejected oversized responses and table slices must state table changes with unchanged JSON/query contracts.
- [ ] Manager lands and tags one feature at a time only after independent review/fix confirmation, exact-head PR and master validation, applicable scanner gates, and disabled Wiki-publication evidence under [release gates](../../instructions/release.md#release-gates); document-only planning has no feature tag.

## Transaction and payment history

Owned SDK paths: new `billing/history.go`, `billing/history_models.go`, `billing/history_dates.go`, `billing/history_decode.go`, `billing/history_paging.go`, `billing/history_security.go`, and matching `_test.go` files; `internal/core/errors.go` and `vngcloud.go` only for the shared `ErrHistoryIncomplete` definition and public alias; export its billing alias from the new history source. Reuse shared core/transport without changing existing billing helpers or transport behavior; request a bounded ownership change if reuse needs new shared code.

Owned CLI paths: `internal/cli/svc_billing.go`, new `internal/cli/svc_billing_history.go` and `internal/cli/svc_billing_history_test.go`, `internal/cli/errors.go`, `internal/cli/errors_test.go`, and `internal/cli/gendocs_notes_billing.go`; new history-only goldens selected in the implementation brief. Owned fixture/live paths: manager-sanitized `testdata/billing/ListTransactions.json`, `ListTransactionsPage2.json`, `ListTransactionsEmpty.json`, `ListPayments.json`, `ListPaymentsPage2.json`, `ListPaymentsEmpty.json`, and raw-derived known-code/beyond-end siblings named explicitly by the manager; `livetest/live_test.go` for both read registrations. Owned docs: `docs/wiki/Billing-and-Pricing.md`, `docs/wiki/Errors.md`, generated `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`, and `RELEASE_NOTES.md`.

- [ ] First add SDK tests through both public methods for every [deterministic check](../design/billing-history-api.md#deterministic-checks) applicable to lists, including all modeled fields, constants, nullable pointers, unknown codes, envelope representations, malformed/missing/null fields, row direction rules, and sanitized raw fixtures retaining unknown fields.
- [ ] Add failing date tests for nil/missing/invalid/reversed inputs, leap/month/year transitions, next-day overflow, one-day ranges, unchanged caller input, and one injected clock read across midnight/pages in `time.FixedZone("UTC+07:00", 7*60*60)` regardless of host local zone; prove invalid inputs cause zero authentication or GETs.
- [ ] Add failing route tests for only bodyless GET `gateway/api/v1/transactions` and `gateway/api/v1/payments`, explicit escaped `DD/MM/YYYY` bounds, page/size normalization, no region/project/filter keys, and `All` with `Page>1` rejected locally.
- [ ] Add failing paging tests for clamped size, empty/beyond-end conventions, full/final row counts, overflow-safe ceiling division, expected pages, changed totals/size, duplicates within/across pages, short walks, late failure, and preserved API order; no allocation from peer totals or partial Output.
- [ ] Test single-page totals above 100 pages remain usable; complete walks freeze page-1 totals/size, cap at 100 pages, 8 MiB per response, 32 MiB cumulative successful raw bodies, and at most 100 times verified size rows without preallocating that product. Exercise exact limits, limit+1, and cancellation during authentication, retries, paging, and row processing within a two-minute whole-invocation deadline or earlier caller deadline.
- [ ] Add failing tests for every [error-status row](../design/billing-history-api.md#error-statuses) on both endpoints, including HTTP 200/envelope 400 arrays, body overflow on 200/403/503, safe decode causes, `errors.Is`/`errors.As`, Retryable, and CLI exits. Keep existing `TestClassify` and `TestExitCode` assertions, especially wrapped NotFound exit 4, unchanged.
- [ ] Add token-rotation/401 canaries, peer account text, nested decode errors, pointer strings, query failures, and terminal-control tests; assert safe errors/chains/logs, redaction of all credentials observed across sends from successful strings, and nil Output on every error. Reuse local redirect/downgrade/hook/TLS/profile/permission tests; no fallback identity, quote, write, or cleanup is permitted.
- [ ] Implement `ListTransactions(ctx, *ListTransactionsInput) (*ListTransactionsOutput, error)` and `ListPayments(ctx, *ListPaymentsInput) (*ListPaymentsOutput, error)` on `billing.Client`, with exact types/constants from the design, a scoped presence-aware history helper using `DoJSONStatus`, `OK: []int{200}`, `MaxBody: 8 << 20`, `NoRedirect: true`, and fixed `WithholdMessage`. Do not route history through permissive existing billing envelope helpers or add an outer retry loop.
- [ ] Define the sentinel once in core with billing/root aliases; incomplete walks return fixed-text `APIError` code `HistoryIncomplete`, Retryable false, nil Output. Distinguish malformed pages from incomplete walks and preserve API/context/decode failure identity without adding the incomplete sentinel.
- [ ] First test CLI read-only access, derived required start/optional end/page/size/all flags, exported query roots, privacy help, default columns, all output formats, and buffered stdout on render failure; then register both reads and set service description to `Budgets, cost, balances, and history` without prompts, `--yes`, or `--since`.
- [ ] Implement command-local safe buffered rendering; default transaction columns are CreatedAt, Type, TypeCode, Direction, Amount, Balance and payment columns are CreatedAt, Gateway, Status, TypeCode, Amount. Keep descriptions/references for JSON/text/explicit query; runtime failures emit fixed `QueryFailed`, exit 1, empty stdout, following the existing console-log renderer pattern.
- [ ] Add both reads to the existing live suite with a fixed-at-run-start 30-calendar-day range through today, page 1, size 10, All false, bounded context, counts-only diagnostics, and no account values in failures; registration is not authorization to execute. Manager-only raw/SDK comparison and any live run require a separate approved brief.
- [ ] Document list examples, inclusive bounds and fixed zone as a manager choice, size and walk limits, no atomic snapshot guarantee, output privacy, reported monetary basis/VAT limits, and unverified service-account access; add HistoryIncomplete/error exits in CLI error guidance and source-generated help.
- [ ] Required focused checks: `go test ./billing ./internal/core ./internal/cli`, `go test -tags live -run '^$' ./livetest/`, and `go vet -tags live ./...`, plus all shared checks; do not run `make live` or the basic example.
- [ ] Accept when both endpoints decode manager-sanitized raw fixtures through the public SDK, all paging/date/security/error/CLI checks pass, docs match, and complex review/fix confirmation passes; ship history alone under its listed tag.

## Spend summary

Owned paths: new `billing/spend.go`, `billing/spend_test.go`, `internal/cli/svc_billing_spend.go`, `internal/cli/svc_billing_spend_test.go`; `internal/cli/svc_billing.go` for registration and `internal/cli/gendocs_notes_billing.go` for help; manager-named raw-derived known-code fixtures and clearly synthetic arithmetic fixtures in `testdata/billing/`; `docs/wiki/Billing-and-Pricing.md`, generated `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`, and `RELEASE_NOTES.md`. Consume the accepted history helper without changing its contract; any needed helper edit requires an explicit brief path.

- [ ] First add failing SDK tests for empty/single/shuffled rows, offset-equivalent instants and ties, unknown codes in both directions, known-direction contradictions, negative/non-finite values and intermediate sums, compensated summation, and reconciliation tolerance at and beyond 0.5 VND; raw known-code fixtures must decode through the public SDK.
- [ ] Prove the invocation shares the resolved date range and two-minute deadline with the complete size-50 transaction walk, preserves cancellation/error identity, and returns nil Output after any late walk or aggregation failure; assert zero payment/balance/pricing/cost calls and no description parsing.
- [ ] Implement `GetSpend(ctx, *GetSpendInput) (*GetSpendOutput, error)` on `billing.Client` with the exact design fields and `SpendGroup`; sum known types, calculate `NetSpend=Charges-Refunds`, signed unknown `Other`, group by code/direction in code/direction order, and sort a calculation copy by parsed instant then ID without changing list order.
- [ ] Derive opening/closing balances only without tied instants, check adjacent balances and the final equation including unknown credits/debits; ties return sums with nil boundaries and false reconciliation, failed arithmetic keeps sums and false reconciliation, and empty ranges return zero sums/count, empty groups, nil balances, false reconciliation.
- [ ] First add failing CLI tests for a summary table plus ByType table only without a query, `none`/JSON null balances, scalar `--query NetSpend --output text`, read-only access, all formats/control escaping, fixed query errors, and empty stdout after any render failure; then register `billing get-spend` with required start/optional end and a command-local buffered renderer.
- [ ] Add the spend example and explain unknown balances, VAT/reporting basis, exclusion of payments from wallet sums, reconciliation's arithmetic-only meaning, tied timestamps, and snapshot limits in wiki and generator source; regenerate docs and add the separate release entry.
- [ ] Required focused checks: `go test ./billing ./internal/cli`, plus all shared checks; acceptance requires known-code raw evidence, complete-walk reuse, matching docs, and complex review/fix confirmation before its own tag.

## Bound existing billing reads

Owned paths: `billing/cost.go`, `billing/budgets.go`, `billing/cost_test.go`, `billing/billing_test.go`, new `billing/read_bounds_test.go`, `docs/wiki/Billing-and-Pricing.md`, and `RELEASE_NOTES.md`; no history helper, resource reader, shared envelope, cost decoder, or mutation change is included.

- [ ] First add failing at-limit/limit+1 tests for each existing GET in cost/budgets, including balances and budget/threshold/alert lists, HTTP success/error bodies, malformed/truncated bodies, preserved error identity, and unchanged normal fixture outputs; synthetic bodies establish bounds rather than upstream response shape.
- [ ] Apply explicit `MaxBody` to those GET requests, preserving all existing output shapes, status/envelope behavior, paging, retries, and mutations; the design does not specify a numeric bound for these existing reads, so obtain the accepted bound in the authority before implementing this slice rather than copying history's value implicitly.
- [ ] Document the accepted bound and new oversized-response failure in wiki and release notes; generated CLI pages have no source change obligation unless help is amended, in which case name generator paths in the brief and run `make gen-docs`.
- [ ] Required focused checks: `go test ./billing`, plus shared checks; accept only after the bound is accepted, every owned GET is covered, existing behavior is preserved within the limit, and complex review/fix confirmation passes; ship under its own tag.

## Billing query-root help

Owned paths: `internal/cli/svc_billing.go`, `internal/cli/gendocs_notes_billing.go`, new `internal/cli/svc_billing_help_test.go`, `docs/wiki/Billing-and-Pricing.md`, generated `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`, and `RELEASE_NOTES.md`.

- [ ] First add failing help/generated-example tests covering every registered billing command's actual exported Output root, including Items, Budget, Balances, cost outputs, history metadata, spend fields, and empty write outputs; use synthetic queries to prove examples without cloud access.
- [ ] Add concise per-command query-root help and working examples from public types; preserve runtime classification, output shapes, query behavior, and cost explorer contracts, and regenerate source-owned docs.
- [ ] Required focused checks: `go test ./internal/cli`, plus shared checks and generation; accept when all billing roots/examples match code and simple review/fix confirmation passes; ship under its own tag.

## Compact budget and balance tables

Owned paths: `internal/cli/svc_billing.go`, new `internal/cli/svc_billing_tables.go`, `internal/cli/svc_billing_tables_test.go`, `internal/cli/gendocs_notes_billing.go`, `docs/wiki/Billing-and-Pricing.md`, generated `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`, and `RELEASE_NOTES.md`.

- [ ] First add failing tests for compact list-budgets/get-budget projections, null versus present zero balances, explicit queries bypassing default projections, empty rows, terminal controls, and unchanged JSON/text/query roots.
- [ ] Add table-only budget projections from UUID, Name, PeriodType, Type, LimitAmount, Currency, Status and a compact get-balances projection of Cash, POC, CashAvailable, CashHolding, POCHolding; absent balances print `none`, present zero remains zero. Do not change SDK models or add cost explorer tables.
- [ ] Explain default columns and unknown balances in wiki/help, regenerate docs, and record table compatibility in the separate release entry.
- [ ] Required focused checks: `go test ./internal/cli`, plus shared checks and generation; accept when compact tables preserve public data contracts and simple review/fix confirmation passes; ship under its own tag.

## Budget and threshold enum help

Owned paths: `internal/cli/svc_billing.go`, `internal/cli/gendocs_notes_billing.go`, `internal/cli/svc_billing_help_test.go`, `docs/wiki/Billing-and-Pricing.md`, generated `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`, and `RELEASE_NOTES.md`.

- [ ] First add failing help tests for applicable budget/threshold flags using exported billing constants: MONTHLY/QUARTERLY period values, ACTUAL/FORECASTED type values, ACTIVE/PAUSED status values, including threshold-type flags and optional update pointers; unknown values still reach the SDK/server under existing validation.
- [ ] Add only the applicable exported values to flag help and generated examples; do not invent payment enums, add completion-based rejection, or change ADR 0002's server-owned value validation.
- [ ] Required focused checks: `go test ./internal/cli`, plus shared checks and generation; accept when help matches exported values, arbitrary values are not newly rejected, and simple review/fix confirmation passes; ship under its own tag.

## Resource timestamp tables

Owned paths: `internal/cli/svc_billing.go`, new `internal/cli/svc_billing_resource_times.go`, `internal/cli/svc_billing_resource_times_test.go`, `internal/cli/gendocs_notes_billing.go`, `docs/wiki/Billing-and-Pricing.md`, generated `docs/wiki/CLI-Billing.md` and `docs/wiki/CLI.md`, and `RELEASE_NOTES.md`.

- [ ] First add failing tests for offset-bearing rendering of resource epoch milliseconds, nil timestamps, epoch zero, subsecond precision, and independence from host local time; prove JSON numbers, text, and explicit queries remain unchanged and Cost stays omitted from the default resource table.
- [ ] Convert StartBillingTime and EndBillingTime only in the default list-resources table projection using explicit-offset formatting; the accepted design does not choose an offset/format or nil display for these existing resource timestamps, so settle those details in its authority before implementation rather than applying history's date zone to resources.
- [ ] Replace the generator note that promises raw table epoch milliseconds, update wiki/examples and the separate release entry, and regenerate docs.
- [ ] Required focused checks: `go test ./internal/cli`, plus shared checks and generation; accept after timestamp presentation choices are accepted, raw public contracts remain unchanged, and simple review/fix confirmation passes; ship under its own tag.
