# vMonitor check state and quotas implementation plan

Ship [vMonitor check state and quotas](../design/monitor-quotas.md) as three feature releases: check state and verified quota reads, then the Email and SMS quota reads with the free-by-default order, then the three renewals.

For workers: use test-first development under the project's brief, worktree, ownership, routing, and review rules; the manager dispatches workers and owns Git state.

**Goal:** A user can see why a check is in alarm and when it got there, see that the account has no email quota, order the free email package, and renew the free Synthetic Test quota before it lapses, all through the public SDK and the CLI.

**Architecture:** Extend the standard-library `monitor` package with two check alarm reads, quota reads, and quote-then-order writes that copy `CreateLogProject`; register them in the existing `monitorOps` table; add `time.Time` to the CLI flag reflection.

**Tech stack:** Repository-pinned Go, standard-library SDK, existing Cobra and JMESPath dependencies, `httptest` and the injected clock; no dependency additions.

**Authorities:** [Design](../design/monitor-quotas.md), [API evidence and checks](../design/monitor-quotas-api.md), [vMonitor](../design/monitor.md), [vMonitor Alerts](../design/monitor-alerts.md), [ADR 0002](../adr/0002-write-api-conventions.md), [workflow](../../instructions/workflow.md), [testing](../../instructions/testing.md), [live data](../../instructions/live-data.md), and [release](../../instructions/release.md).

## Constraints and execution

- [ ] Code starts only after the owner approves the design's six owner decisions and an independent `design_reviewer` on `gpt-6.1-sol` xhigh reviews the design and this plan.
- [ ] Dispatch `implementer` on `gpt-6.1-sol` medium, failures to `troubleshooter` on `gpt-6.1-sol` xhigh, and every review and fix confirmation to `code_reviewer` or `design_reviewer` on `gpt-6.1-sol` xhigh as a separate job from the author; set model and effort explicitly on every dispatch.
- [ ] Each worker gets a credential-free `.claude/worktrees/<slice>` worktree and disjoint owned paths; at most three workers run across all slices, reviewers included.
- [ ] Each brief states exact paths, authorities, public interfaces, checks, and forbidden actions; workers start with `git status --short`, preserve others' edits, make no Git mutations, and report conflicts instead of widening scope.
- [ ] Forbid secret and private-config reads, `.env`, captures, live calls, unrelated network, dependencies, and edits outside the brief.
- [ ] The manager inspects every diff and reruns focused checks before accepting a report or starting dependent work; one independent adversarial review runs per release after its last code task, then fix confirmation.
- Preserve every existing `monitor` method, field, constant, and command; `Check` gains two fields and nothing changes shape.
- Every path ID passes `core.CheckPathID`; `Kind` maps to a fixed segment and never reaches a path as typed.
- No order or renewal is resent after a 5xx, a dropped connection, a redirect, or a cancelled context; the CLI never retries a write.
- Inferred shapes in the API document stay inferred until the named live check produces a sanitized fixture; a fixture built from decoded SDK output is not evidence.
- Human docs wrap at 80 columns; this plan uses one line per paragraph or list item, no dates or em dashes; keep the Markdown and Go length limits.

## Gates

- [ ] Owner approval of the design decisions, recorded in the design's owner decisions section before the first code brief.
- [ ] Live check 1 (read-only) before release 1 is tagged: confirms the first-release shapes against the fixtures.
- [ ] Owner approval of the free `emailfree` order on the test account before live check 2; release 2 code can be written and reviewed before it, but its inferred fixtures and tag wait for it.
- [ ] Owner approval before live checks 3 and 4; release 3 code can be written and reviewed before them, but its tag waits for check 3 at least.
- [ ] No paid order or renewal runs without explicit owner approval of the amount; without live check 5 the price guard keeps the higher bound and the wiki says so.

## Files and dependencies

|Task|Exact owned paths|Dependency|
|-|-|-|
|1 Check state SDK|Modify `monitor/models.go`, `monitor/models_test.go`; new `monitor/checkalarms.go`, `monitor/checkalarms_test.go`, `testdata/monitor/GetCheckAlarm.json`, `testdata/monitor/ListCheckAlarmHistory.json`|Approved design; sanitized fixtures from the manager|
|2 Quota reads SDK|New `monitor/quotas.go`, `monitor/quotas_test.go`, `testdata/monitor/GetQuotaUsage.json`, `testdata/monitor/ListNotificationPackagesEmail.json`, `testdata/monitor/ListNotificationPackagesSMS.json`, `testdata/monitor/GetSyntheticQuota.json`, `testdata/monitor/GetSyntheticQuotaDetail.json`, `testdata/monitor/GetSyntheticQuotaUsage.json`; modify `monitor/monitor.go` only for route helpers|Approved design; may run alongside task 1|
|3 Release 1 CLI and docs|Modify `internal/cli/flags.go`, `internal/cli/flags_test.go`, `internal/cli/input.go` only if `time.Time` needs a JSON case, `internal/cli/svc_monitor.go`, `internal/cli/svc_monitor_test.go`, `internal/cli/gendocs_notes.go`; new `internal/cli/gendocs_notes_monitor_quotas.go`; modify `examples/basic/monitor.go`, `livetest/live_test.go` only inside `testLiveMonitor`, `docs/wiki/Monitor.md`, `docs/wiki/Home.md`, `docs/wiki/CLI-Monitor.md` and `docs/wiki/CLI.md` through `make gen-docs`|Tasks 1 and 2 accepted|
|4 Notification quota SDK|Modify `monitor/quotas.go`, `monitor/quotas_test.go`, `monitor/monitor.go` for `quotaMu`; new `monitor/quotas_write.go`, `monitor/quotas_write_test.go`, `testdata/monitor/GetNotificationQuotaNotFound.json`, `testdata/monitor/QuoteCreateNotificationQuota.json`, and after live check 2 `testdata/monitor/GetNotificationQuota.json`, `testdata/monitor/GetNotificationQuotaUsage.json`, `testdata/monitor/CreateNotificationQuota.json`|Release 1 landed|
|5 Release 2 CLI and docs|Modify `internal/cli/svc_monitor.go`, `internal/cli/svc_monitor_test.go`, `internal/cli/gendocs_notes_monitor_quotas.go`, `internal/cli/gendocs_notes_monitor_logalarm.go` only the send-channel-otp note, `examples/basic/monitor.go`, `docs/wiki/Monitor-Alerts.md`, `docs/wiki/Home.md`, `README.md`; new `docs/wiki/Monitor-Quotas.md`; generated `docs/wiki/CLI-Monitor.md` and `docs/wiki/CLI.md`|Task 4 accepted|
|6 Renewals SDK|Modify `monitor/quotas_write.go`, `monitor/quotas_write_test.go`; new `testdata/monitor/QuoteRenewSyntheticQuota.json`, and after live checks 3 and 4 `testdata/monitor/RenewSyntheticQuota.json`, `testdata/monitor/RenewNotificationQuota.json`|Release 2 landed|
|7 Release 3 CLI and docs|Modify `internal/cli/svc_monitor.go`, `internal/cli/svc_monitor_test.go`, `internal/cli/gendocs_notes_monitor_quotas.go`, `docs/wiki/Monitor-Quotas.md`, `docs/wiki/Monitor.md`; generated `docs/wiki/CLI-Monitor.md` and `docs/wiki/CLI.md`|Task 6 accepted|
|Manager|`RELEASE_NOTES.md`, `docs/design/monitor-quotas.md`, `docs/design/monitor-quotas-api.md`, `docs/design/README.md`, `docs/design/monitor-alerts.md`, this plan|Each release verified; preserve evidence marks|

Tasks 1 and 2 touch disjoint files and may run in parallel; `monitor/monitor.go` belongs to task 2 in release 1 and to task 4 in release 2. If `monitor/quotas.go` nears 700 lines, task 4 splits reads and writes into `quotas.go` and `quotas_write.go` as listed rather than growing one file.

Existing seams consumed unchanged: `core.CheckRequired`, `core.CheckPathID`, `core.ErrNotFound`, `core.ErrInvalidInput`, `core.ErrPriceAboveMax`, `core.ErrUnpriced`, `core.List`, `transport.Request` with `Idempotent`, `NoRedirect`, and `MaxBody`, `(*Client).alarmRoute`, `(*Client).logBillingRoute` and `logBillingRouteV1` (renamed or wrapped as shared billing route helpers in task 2), `sendLogProjectQuote`'s decoder and nil-price refusal (generalized in task 4), `poll`, `contextSleep`, `checkLogProjectMaxPrice` (generalized in task 4), `wrapAmbiguousLogProjectOrderErr` (generalized in task 4), and `monitorOps` with `Read`, `Write`, and `NoFlag`.

## Review focus

1. A redirect or a transport retry can replay a paid `POST`; tasks 4 and 6 count attempts under 502, dropped connection, 307, 429, and cancellation.
2. The price guard can be bypassed by a quote below the list price or a zero quote; task 4 tests both bounds and `ErrUnpriced`.
3. A concurrent order on the same `Client` can pass the pre-order 404 twice; task 4 tests the mutex.
4. The history read is the only caller-sized read; task 1 tests the `MaxBody` error and that no partial Output escapes.
5. `paymentUrl`, `links`, and `dimensions.user_id` must never reach Output, errors, or debug logs; tasks 1, 3, 4, and 5 assert their absence.

## Task 1: Check state fields and alarm reads

**Produces:** `Check.MonitorStatus`, `Check.HistoryAlarmID`, constants `MonitorStatusOK`, `MonitorStatusAlarm`, `MonitorStatusUndetermined`, `GetCheckAlarm(ctx, *GetCheckAlarmInput) (*GetCheckAlarmOutput, error)`, `ListCheckAlarmHistory(ctx, *ListCheckAlarmHistoryInput) (*ListCheckAlarmHistoryOutput, error)`, and the types `CheckAlarm`, `CheckAlarmSubExpression`, `CheckLocationAlarm`, `CheckAlarmTransition`, `CheckSubAlarmState` named in the design.

- [ ] Write the `Check` decode test reading `MonitorStatus` and `HistoryAlarmID` from the existing `GetCheck.json`, and asserting `user_id` and `deleted_at` stay dropped.
- [ ] Write `GetCheckAlarm` tests: fixture decode with two locations; `links`, `dimensions`, `alarm_definition`, and the three action lists absent from the Output; empty `history_alarm` returns not-found after one request; 404 on the check and on the definition; bad `CheckID` sends nothing; a non-RFC-3339 timestamp fails the read.
- [ ] Write `ListCheckAlarmHistory` tests: zero `End` is the injected clock's now and zero `Start` is 24 hours earlier; `Start >= End` sends nothing; query holds millisecond epochs and `interval` equal to `TestFrequency`, 1 when 0; one history request per location in definition order; `LocationID` filter and its not-found; an error on the second location returns nil Output; items merged and sorted by `Timestamp` then `LocationName`; a body over 16 MiB is an error; cancelled `ctx` between locations stops.
- [ ] Run `go test ./monitor -run 'TestCheck|TestGetCheckAlarm|TestListCheckAlarmHistory' -count=1` and record the red result.
- [ ] Implement the fields and `monitor/checkalarms.go` under `alarmRoute`, decoding only the modeled keys, with `MaxBody` 16 MiB on the history read and sequential location reads.
- [ ] Rerun the focused tests and `go test -race ./monitor`, expecting PASS; report the exact public types.

## Task 2: Verified quota reads

**Produces:** `GetQuotaUsage`, `ListNotificationPackages`, `GetSyntheticQuota`, `GetSyntheticQuotaUsage`, the `Kind` mapping helper, constants `NotificationKindEmail`, `NotificationKindSMS`, `QuotaStatusActive`, `QuotaStatusInTrash`, and the types `QuotaUsage`, `UsageCounts`, `NotificationPackage`, `Quota`, `SyntheticQuotaDetail`, `QuotaUsageDetail`.

- [ ] Write decode tests for the six fixtures; `autoRenew` 0 and 1 and `poc` 0 and 1 decode to bools; empty usage arrays decode; unknown severity strings pass through; a non-UUID package id decodes.
- [ ] Write `Kind` tests: `Email`, `email`, `SMS`, and `sms` map to `email` and `sms`; `Teams`, `../email`, and empty are `ErrInvalidInput` with no request.
- [ ] Write `GetSyntheticQuota` tests: two requests in order; a 404 on the quota is not-found; a 500 on the detail returns nil Output.
- [ ] Run `go test ./monitor -run 'TestGetQuotaUsage|TestListNotificationPackages|TestGetSyntheticQuota|TestNotificationKind' -count=1` and record red.
- [ ] Implement `monitor/quotas.go` with shared `billing-api` v1 and v2 route helpers in `monitor/monitor.go`, leaving the log project helpers behaving as before.
- [ ] Rerun the focused tests and `go test -race ./monitor`, expecting PASS.

## Task 3: Release 1 CLI, example, live read, and wiki

**Consumes:** Task 1 and 2 public types.

- [ ] Write flag tests: a `time.Time` field binds a string flag parsed with `time.RFC3339`; other text exits 2 naming the flag; an unset flag leaves the zero value; `--cli-input-json` with an RFC 3339 string decodes into the same field.
- [ ] Write command tests: golden `json`, `table`, and `text` for the six new reads; `--location-id`; exit 4 when the check has no alarm; `--debug` output holds no body, query string, or URL; help lists `--start` and `--end` as RFC 3339.
- [ ] Run `go test ./internal/cli -run 'TestFlags|TestMonitor' -count=1` and record red.
- [ ] Implement the `time.Time` case in `flags.go`, register the six reads in `monitorOps`, and add doc notes for the history range default and the dropped internal fields.
- [ ] Add the reads to `examples/basic/monitor.go` and to the monitor live read test without pinning counts; compile-only checks `go test -tags live -run '^$' ./livetest/` and `go vet -tags live ./...`.
- [ ] Update `docs/wiki/Monitor.md` for the two fields and two reads, link the coming quota page from `docs/wiki/Home.md` only once it exists, and run `make gen-docs`, comparing a second generation in a private temporary directory with `diff -ru`.
- [ ] Run `make wiki-preview`, `git diff --check`, and `bash scripts/check-lengths.sh`; rerun focused tests and `go test -race ./internal/cli`, expecting PASS.

## Task 4: Notification quota reads and the order

**Produces:** `GetNotificationQuota`, `GetNotificationQuotaUsage`, `QuoteCreateNotificationQuota`, `CreateNotificationQuota`, `CreateNotificationQuotaInput{Kind, PackageID, MaxPrice, NoWait}`, `CreateNotificationQuotaOutput{Quota, OrderID, PaymentPending}`, `QuoteNotificationQuotaOutput`, `monitor.ErrUnpriced`, and `Client.quotaMu`.

- [ ] Write read tests: the 404 fixture maps to not-found whose message names the kind and `CreateNotificationQuota`; the inferred 200 shape decodes; both kinds route to their paths.
- [ ] Write order tests from the design's deterministic list: free default selection, zero or two free packages refuse, unknown `PackageID` refuses, a 200 pre-order read refuses before any quote, quote and order bodies with the console `redirectUrl` and `pay` true, `NoRedirect` set, the guard against quote 100000 and list 110000 at `MaxPrice` 100000 refusing and 110000 passing, zero quote for a priced package is `ErrUnpriced`, free zero quote passes at `MaxPrice` 0, nil `optimumPrice` orders nothing, exactly one `POST` after 502 and after a dropped connection with the read-first hint, 400, 401, 403, 404, 409, and 429 unchanged, `PaymentPending` true from a non-empty `paymentUrl` with the URL absent from Output, error text, and the debug log, the wait to `ACTIVE`, the timeout as `dns.ErrNotSettled` with the Output, and `NoWait`.
- [ ] Write the mutex test: two concurrent orders on one `Client` against a server whose quota read flips to 200 after the first order produce one order and one `ErrInvalidInput`.
- [ ] Write quote tests: same body as the order, no order sent, `MaxPrice` ignored, `Idempotent` retry after a 502.
- [ ] Run `go test ./monitor -run 'TestGetNotificationQuota|TestCreateNotificationQuota|TestQuoteCreateNotificationQuota' -count=1` and record red.
- [ ] Implement `monitor/quotas_write.go`, generalizing the log project quote decoder, `MaxPrice` check, and ambiguous-order wrapper into shared helpers without changing `CreateLogProject`'s behavior or tests.
- [ ] Rerun the focused tests and `go test -race ./monitor`, expecting PASS; report any live-check fixture still missing.

## Task 5: Release 2 CLI, docs, and the free-20 correction

**Consumes:** Task 4 public types.

- [ ] Write command tests: `get-notification-quota --kind Email` exits 4 with `NotFound` on the 404 fixture; read-only refusal of `create-notification-quota` with no request; `PriceAboveMax`, `Unpriced`, and `NotSettled` codes with exit 1; `quote-create-notification-quota` hides `--max-price` and `--no-wait`; golden output for the four commands.
- [ ] Run `go test ./internal/cli -run 'TestMonitor' -count=1` and record red.
- [ ] Register the four commands and their notes: the free default, the one-quota rule, `pay` debiting a paid package at once, the three-actions-a-month limit, the guard bound, and that `PaymentPending` means the order awaits payment in the console.
- [ ] Replace "the free 20" in `docs/wiki/Monitor-Alerts.md` and in the send-channel-otp note: a synthetic-only account has no free Email or SMS until it orders the free package, whose `Amount` then sets the monthly allowance; a paid package replaces it.
- [ ] Write `docs/wiki/Monitor-Quotas.md` for the reads and the order, including `MaxPrice`, `PaymentPending`, and the limits; link it from `Home.md` and the README coverage row; add the order to `examples/basic/monitor.go` only as a quote, never an order.
- [ ] Run `make gen-docs` with the second-generation diff, `make wiki-preview`, `git diff --check`, `bash scripts/check-lengths.sh`, the focused tests, and `go test -race ./internal/cli`, expecting PASS.

## Task 6: Renewals

**Produces:** `QuoteRenewNotificationQuota`, `RenewNotificationQuota`, `QuoteRenewSyntheticQuota`, `RenewSyntheticQuota`, `RenewNotificationQuotaInput{Kind, MaxPrice, MaxDaysLeft, NoWait}`, `RenewSyntheticQuotaInput{MaxPrice, MaxDaysLeft, NoWait}`, and `RenewQuotaOutput{Renewed, Quota, OrderID, PaymentPending}`.

- [ ] Write tests from the design's renewal list: 404 is not-found naming the order call; `MaxDaysLeft` skip returns `Renewed` false with no quote or send; extend when active, extend when expired but not in trash, recover when expired and `IN-TRASH`; `v1` for Email and SMS and `v2` for synthetic; quote body with `resourceId` and the synthetic `quantity` from the detail; the guard and `ErrUnpriced` as the order; exactly one `POST` after 502 and a dropped connection; 409 unchanged; the wait on `EndTime` moving and on leaving `IN-TRASH`; timeout; `NoWait`; the mutex across a renewal and an order.
- [ ] Run `go test ./monitor -run 'TestRenew|TestQuoteRenew' -count=1` and record red.
- [ ] Implement the renewals in `monitor/quotas_write.go` sharing the order's quote, guard, send, and wait helpers.
- [ ] Rerun the focused tests and `go test -race ./monitor`, expecting PASS.

## Task 7: Release 3 CLI and docs

- [ ] Write command tests: read-only refusal of both renewals; `--max-days-left` skip prints `Renewed` false and exits 0; the quote commands hide `--max-price`, `--max-days-left`, and `--no-wait`; golden output; `NotSettled` prints the Output.
- [ ] Run `go test ./internal/cli -run 'TestMonitor' -count=1` and record red.
- [ ] Register the four commands and notes: early renewal extends from the current `EndTime`, one month a renewal, the monthly limit, the cron pattern with `--max-days-left`, and that a lapsed Synthetic Test quota stops every check.
- [ ] Update `docs/wiki/Monitor-Quotas.md` and the quota note in `docs/wiki/Monitor.md`; run `make gen-docs` with the second-generation diff, `make wiki-preview`, `git diff --check`, `bash scripts/check-lengths.sh`, the focused tests, and `go test -race ./internal/cli`, expecting PASS.

## Manager verification, live checks, review, and release

- [ ] Before each release: inspect every owned diff, run `make check`, `bash scripts/check-sdk-deps.sh`, `GOOS=windows go vet ./...`, `GOOS=darwin go vet ./...`, `go vet -tags live ./...`, `go vet -tags livewrite ./...`, `go test -tags live -run '^$' ./livetest/`, and `go test -tags livewrite -run '^$' ./livetest/`; send failures to a briefed troubleshooter instead of weakening or retrying checks.
- [ ] Run live check 1 read-only and hand sanitized fixtures to tasks 1 and 2 before their briefs; rerun it after release 1 code to confirm the fixtures still match.
- [ ] Obtain owner approval and run live check 2 on the test account after task 4's review: free `emailfree` order through the SDK with `MaxPrice` 0, then the two reads until `ACTIVE`, then a check alarm transition and whether an email arrives; hand the sanitized fixtures to task 4 and record the root-cause result in the design's open questions.
- [ ] Obtain owner approval and run live check 3 after task 6's review: `RenewSyntheticQuota` on the free quota, recording `EndTime` before and after; run live check 4 only within the monthly budget; correct the design's inferred rows from what the checks show before tagging.
- [ ] Run no paid order or renewal unless the owner approves the amount; record live check 5 as not run when skipped and keep the higher-bound guard text in the wiki.
- [ ] Prepare a disposable public-source review copy per release with the base commit, complete diff, and new-file inventory and no secrets or private state; dispatch one independent `code_reviewer` on `gpt-6.1-sol` xhigh adversarial review per release tracing the five review focus items, then confirm fixes in a separate job.
- [ ] Update `RELEASE_NOTES.md`, the design status and evidence marks, and the design index after each release; stage exact paths, commit with the owner's SSH signature and DCO sign-off, open the PR, require every applicable exact-head validation run, land a descendant of current master, and tag one signed feature tag per release after master validation passes.
- [ ] Report exact new, changed, and deleted files, checks and results, skipped checks with reasons, confirmed review invariants, evidence limits, and the remaining gates; mark boxes only after manager verification.
