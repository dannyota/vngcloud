# vMonitor check state and quotas

Status: Proposed. This design approves no code, purchase, or live run.

Expose synthetic check state and history, quota allowances and expiry,
notification quota orders, and quota renewal through `monitor`. Missing
Email quota is a plausible cause of missing alerts, not a proven delivery
failure diagnosis. No separate alarm or trigger creation step is missing.
[API evidence and checks](monitor-quotas-api.md) owns the wire shapes,
provenance, unknowns, and live gates.

This extends [vMonitor](monitor.md) and [Alerts](monitor-alerts.md), follows
[SDK and CLI](sdk-and-cli.md), [CLI](cli.md), and
[write conventions](../adr/0002-write-api-conventions.md). The SDK stays
standard-library only in `danny.vn/vngcloud/monitor`; the CLI calls only
public SDK operations. No dependency or authentication change is needed.

## Scope and release order

Ship the smallest independently useful slices, subject to owner decisions:

1. Check fields, alarm reads, verified quota reads, and the wiki correction.
   History waits for its completeness and range checks; other reads need
   not wait for it. These reads diagnose state and warn of quota expiry.
2. Email quota reads, quote, and free email order together, once their
   evidence gates close. Do not delay this slice for SMS or paid pricing.
3. Free Synthetic Test extension, then Email extension, independently as
   their gates close. Synthetic expiry stops checks, so prioritize it.
4. SMS writes, paid notification writes, and trash recovery only with their
   own evidence. Console similarity does not verify another kind or path.

A first release including the email order would unblock delivery sooner,
but exact-body pricing and write responses are unverified. Recommend reads
first and email immediately after its gates close. No gate is waived for a
free order. Only the manager performs owner-approved live checks.

Out of scope: quota delete/purge, auto-renew changes, metric quotas, log
quota renewal/resize, new or paid Synthetic Test orders, bundles,
`pay: false`, checkout URLs, introspection, and a public settings operation.
Slack, Telegram, Webhook, and Teams have counters but no package operations.
Deletion is not an approved cleanup path; keep an ordered quota.

## Public surface

Every operation has `Method(ctx, *MethodInput) (*MethodOutput, error)` and
operation name `monitor.Method`. Empty Inputs accept nil. Required fields
carry `vngcloud:"required"`; optional fields are values unless stated.
These are complete operations, not partial updates. All reads fail with nil
Output on any failed component; writes have explicit partial outputs below.

`Kind string` is required for notification operations. Constants
`NotificationKindEmail` and `NotificationKindSMS` are `Email` and `SMS`.
Match case insensitively and map to fixed `email` or `sms` path segments;
other kinds fail before requests. Gate each kind's operation independently.

Validate caller path IDs with `core.CheckPathID` before any request.
Validate response-derived path IDs before the next request, returning a
body-free `*vngcloud.APIError` for invalid peer data. This is a path shape
check, not UUID validation. Package IDs are opaque list values used only in
bodies; paid Email IDs need not be UUIDs. Never follow response URLs.

### Check state and history

`Check` adds `MonitorStatus string` tagged `monitor_status` and
`HistoryAlarmID string` tagged `history_alarm`. Keep `Status` as the existing
ENABLED/DISABLED run state. Constants `MonitorStatusOK`,
`MonitorStatusAlarm`, and `MonitorStatusUndetermined` name the observed
monitor states; unknown states pass through. `user_id` and `deleted_at`
remain omitted. Keyed literals and existing field semantics stay compatible;
unkeyed external literals and exact JSON snapshots can break on added
fields. Document the additions and test existing create/update behavior.

- `GetCheckAlarmInput`: required `CheckID string`.
  `GetCheckAlarmOutput`: `Alarm CheckAlarm`.
- `ListCheckAlarmHistoryInput`: required `CheckID string`, optional
  `Start, End, LocationID string`. `ListCheckAlarmHistoryOutput`:
  `Items []CheckAlarmTransition`, resolved `Start, End time.Time`.

`CheckAlarm` exposes `ID, Status string`, `StatusUpdatedAt time.Time`,
and `Locations []CheckLocationAlarm`. `CheckLocationAlarm` holds
`AlarmID, LocationID, LocationName, State string` and
`StateUpdatedAt, UpdatedAt, CreatedAt time.Time`. Keep this view small:
omit expression text, descriptions, actions, links, nested alarm definitions,
and arbitrary dimensions. State and location do not require those fields.

Read a bounded private check snapshot, then the definition identified by
`history_alarm`; do not reuse an unbounded full `GetCheck` internally.
An empty alarm reference returns `vngcloud.ErrNotFound` after that one read.
Project only location ID and name from metric dimensions. Refuse missing or
ambiguous location identity rather than attribute history to the wrong probe.

History parses nonempty `Start` and `End` as RFC 3339 before requests, like
existing string date inputs in `billing/cost.go` and `cdn/analytics.go`.
There is no new CLI flag type. Default End to the injected clock and Start
to End minus 24 hours. Convert to epoch milliseconds; reject a range empty
or reversed at that precision. No invented server maximum range is imposed.
Validate optional LocationID as an ID; no matching location is NotFound.

Fetch one history response per selected alarm sequentially using the check's
positive `TestFrequency` in minutes. Missing or nonpositive frequency is an
invalid response, not an invented interval of 1. Check cancellation between
requests. Validate alarm IDs before routing, and reject history whose
`alarm_id` conflicts with the requested alarm. Merge and sort by Timestamp,
LocationID, AlarmID, then ID. Identical timestamps remain separate events.

`CheckAlarmTransition` holds `ID, AlarmID, LocationID, LocationName,
OldState, NewState string`, `Timestamp time.Time`, and
`SubAlarms []CheckSubAlarmState`; the latter has `State string` and
`CurrentValues []float64`. Omit raw Reason, metrics, reason_date, and
subAlarmExpression. No raw response map escapes. A failed location returns
no partial history. Unknown paging and interval behavior gate a claim of
complete history and its release; do not silently truncate or invent pages.

### Quota reads

All remaining unspecified textual model fields are strings; timestamps must
be present and parse as RFC 3339 where a decision or identity needs them.

Empty Inputs: `GetQuotaUsage`, `GetSyntheticQuota`,
`GetSyntheticQuotaUsage`. Required Kind Inputs: `GetNotificationQuota`,
`GetNotificationQuotaUsage`, `ListNotificationPackages`.

- `GetQuotaUsageOutput` has `Usage QuotaUsage`. `QuotaUsage` has
  `SyntheticUsages []UsageCounts`, `FreeEmailQuota, FreeSMSQuota int`,
  `LogSeverity, MetricSeverity, SyntheticSeverity, EmailSeverity,
  SMSSeverity, NotificationSeverity string`, and `UnmodeledKinds []string`.
  Unseen nonempty usage arrays add their fixed kind names to UnmodeledKinds;
  they never appear as RawMessage or contribute invented totals.
- `UsageCounts` has `API, Location, Email, SMS, Slack, Webhook, Telegram,
  Teams int`. Use integral-number decoding; reject fractional counts.
- `GetNotificationQuotaOutput` has `Quota Quota`.
  `GetSyntheticQuotaOutput` has `Quota Quota; Detail SyntheticQuotaDetail`.
  Its quota and detail reads must both succeed and agree on resource and
  package identity. Check the returned quota ID before the detail path.
- `Quota` has `ID, Name, Type, Status, PackageID string`,
  `StartTime, EndTime, CreatedAt, UpdatedAt time.Time`,
  `AutoRenew, POC, System bool`, and `AutoRenewPeriod int` (months).
  AutoRenew and POC decode observed numeric 0/1 only; unknown encodings
  fail closed. Constants `QuotaStatusActive` and `QuotaStatusInTrash`
  describe known statuses; reads preserve other status strings.
- `SyntheticQuotaDetail` has `API, Location, Email, SMS, Slack, Webhook,
  Telegram, Teams int`, `ClassName, TierName string`, and `Price *float64`.
- Both quota usage Outputs have `Usage QuotaUsageDetail`, containing
  `Usage UsageCounts` and `Descriptions map[string]UsageDescription`.
  `UsageDescription` has `Name, Specs, Unit string`; keys are known counters.
- `ListNotificationPackagesOutput` is `core.List[NotificationPackage]`.
  A package has `ID, Name string`, `Price *float64` (VND), `Amount int`.
  Nil Price means absent or null, never free. Arrays have no observed pages.

Notification quota and usage success schemas remain inferred. Do not ship
those reads based only on Email 404s. A quota-read 404 means NotFound;
a create may proceed only on the verified kind-specific absence response,
not an arbitrary 404 from another route, auth proxy, or usage read. A renewal
404 never falls back to create. Missing synthetic quota directs the user to
the console because synthetic creation is outside this design.

### Quote and price contract

`CreateNotificationQuotaInput` has required `Kind string`, optional
`PackageID string`, `MaxPrice float64`, and `NoWait bool`.
`RenewNotificationQuotaInput` has required `Kind, ExpectedEndTime string`,
optional `MaxPrice float64`, `MaxDaysLeft int`, and `NoWait bool`.
`RenewSyntheticQuotaInput` has the renewal fields except Kind.
ExpectedEndTime is the RFC 3339 expiry from the caller's prior quota read.

Each `QuoteCreateNotificationQuota`, `QuoteRenewNotificationQuota`, and
`QuoteRenewSyntheticQuota` takes its write's Input, shares its preparation,
and ignores MaxPrice, MaxDaysLeft, and NoWait. Each named Output aliases
`QuotaQuote`: `PackageID string`, `Quantity, MonthPeriod int`,
`ResourceID string`, `ExpectedEndTime *time.Time`,
`OptimumPrice, ListPrice, GuardPrice float64`, and `Currency string` (VND).
Do not return opaque quote properties or URLs. A standalone quote is an
estimate, never reusable authorization; a write quotes again.

Validate finite nonnegative MaxPrice, nonnegative MaxDaysLeft, and expiry
syntax before requests. Reject duration arithmetic overflow. Resolve a
package once; an omitted PackageID selects exactly one present zero-price
package. Unknown, duplicate, missing-price, or ambiguous candidates refuse
before quote/order, after the necessary list read.

**Exact-body gate:** prepare one immutable order body and quote that same
body, then guard and send it unchanged, as `CreateLogProject` does. The
observed console quote and order bodies differ. Neither accepting a union
of their fields nor pricing the full order body is verified. All writes
stay blocked until the manager establishes a supported exact-body contract
with the correct package, quantity, resource, period, pay, and redirect
semantics. Never quietly drop fields to make the quote pass. If the API
cannot support this, return to owner and independent design review.

Missing/null/negative/nonfinite prices fail closed as body-free API errors.
A zero quote for a positive list price is `monitor.ErrUnpriced`, an alias
of `vngcloud.ErrUnpriced`. Otherwise compare MaxPrice with the higher of
quote and applicable list price and return `ErrPriceAboveMax` if exceeded.
List and quote must cover the same quantity and one-month term. Synthetic
writes cover only the verified free package with zero detail and quote
prices. Missing package or uncertain price units never fall back to quote
alone. Paid notification writes remain gated on debit/tax evidence; the
higher observed price is conservative, not a proven all-in charge ceiling.

### Order and renewal execution

Preparation reads packages and quota under the per-Client write lock. Create
refuses an existing quota before quoting. Renew reads the current quota and
compares EndTime to ExpectedEndTime as instants; mismatch is ErrInvalidInput
and no quote/write. Positive MaxDaysLeft skips when expiry is further away,
returning `Renewed: false`; reads still occur. Zero allows early renewal.

Refuse POC, auto-renewing, unknown-status, missing-identity, or invalid-expiry
quotas before quoting. The console path rule is expired AND IN-TRASH for
recovery, otherwise extension. Do not invert it to status alone. Initially
only evidenced ACTIVE extension is eligible; inconsistent, other-status,
or gated recovery cases refuse without a write. If trash is absent from
ordinary GET, recovery waits for a verified trash lookup contract. Never
invent a resource from an empty trash list or use a quota-read 404 to buy.

Immediately before send, reread quota identity, package, expiry, and status;
for synthetic also recheck quantity. A change, including expiry crossing
the recovery boundary, aborts without send. Do not silently rebuild or
requote in a loop. No server precondition or price lock is known, so this
narrows but cannot eliminate cross-client races or quote-to-debit changes.

Send the evidenced body with `pay: true`, one month, and the fixed console
redirect value. `Once: true` prevents retries on every status/error, token
refresh resend, and redirects. Use it for every order, extension, and
recovery, not merely NoRedirect. SDK and CLI add no outer write retries.

`CreateNotificationQuotaOutput` has `Quota *Quota`, `OrderID string`,
`PaymentPending bool`. `RenewQuotaOutput`, aliased by both renewal Outputs,
adds `PreviousEndTime time.Time`, `Renewed bool`. Renewed means confirmed
new expiry, not merely sent. NoWait returns immediately after a valid
response with Quota nil and Renewed false; it does not skip preparation.
PaymentPending means only a nonempty paymentUrl was returned. Return partial
Output with `dns.ErrNotSettled` and stop; never follow or expose that URL,
pretend payment completed, or issue another order.

Otherwise wait with reads every 2 seconds under a 120-second child context
bounded also by the caller's context. Create settles only on the selected
package at ACTIVE. Renewal requires the same quota/package, ACTIVE, and
EndTime later than PreviousEndTime; leaving trash alone is insufficient.
NoWait suppresses the wait, not single-send or uncertainty handling.

Pre-send errors return nil Output except the documented expiry skip.
Definite 4xx refusal or a proven failed dial returns its sanitized error.
Any other send failure, oversized/malformed success, cancellation after
possible send, or failed wait returns partial Output and an error wrapping
`dns.ErrNotSettled` plus the safe underlying cause. `errors.As` retains
`*vngcloud.APIError` status; `errors.Is` retains cause and sentinel identity.
Never imply a failed read proves that an order did not happen.

Hold a context-aware per-Client lock across preparation, send, and settlement.
Remember unresolved mutation attempts by quota kind for that Client,
including NoWait until read reconciliation succeeds. Further writes for
that kind only reconcile by reading; they never resend an unresolved body.
A failed or inconclusive reconciliation keeps the block. A known rejection
clears it. A confirmed prior result clears the block and returns that result
without a new send. Separate clients/processes have no shared ledger:
callers serialize per
account/kind and persist the attempt and baseline before invoking a write.
This is not an exactly-once guarantee.

After uncertainty, compare read-only quota identity, package, status, and
expiry with the saved baseline and inspect billing in the console. A 404 or
unchanged expiry can be stale; neither authorizes retry. Resolve with the
owner/provider if reads remain inconclusive. Never reset a client merely to
bypass the block. Cleanup, a replacement purchase, and a second renewal need
separate authority. The monthly action uncertainty is defined in the API
sibling; an automatic job must not use failures as retry signals.

All reads and quotes use the existing configured finite retry count; there
is no additional outer retry loop. Context cancellation stops retries and
fan-out. Size/fan-out or decode failures become body-free APIError values
with fixed local codes, inspectable with errors.As; no public internal
transport sentinel is required. Unknown quota status on a write wraps the
existing `monitor.ErrUnexpectedStatus` (UnexpectedStatus/1); unsupported
kind/path or POC/auto-renew eligibility uses ErrInvalidInput before send.

## CLI and errors

Register each method's kebab-case command in `monitorOps`. Quotes and GETs
are Read; creates and renewals are Write. Read-only flag/environment refusal
precedes config, and profile refusal follows config but precedes requests.
No `--yes` is added: consent is finite `--max-price`, default 0, following
existing paid creates. Help and a stderr cost warning before a positive-
budget write state that `pay: true` may debit the balance and that MaxPrice
is a local estimate guard, not a server-enforced cap. Do not prompt.

String flags `--start`, `--end`, `--expected-end-time`, and `--kind`, int
`--max-days-left`, float `--max-price`, bool `--no-wait`, and
`--package-id` use existing flag support and the same JSON-input validation.
Quote commands hide ignored fields with NoFlag, as log project quotes do.
Tables show status, expiry with UTC zone, AutoRenew, and absent prices as
unknown. Normal json/table/text and --query work on safe public Outputs;
partial write Outputs must print even when an error follows.

Use existing error identities: InvalidUsage/2 for local validation and
read-only refusal (ReadOnly/2); NotFound/4 for missing reads;
PriceAboveMax/1, Unpriced/1, and NotSettled/1 for their sentinels.
NotSettled takes precedence over wrapped cancellation, auth, or 404.
Other API errors retain safe status and existing classification, including
401/auth exit 3, 403 exit 1, and 409/429 exit 1. Unknown package validation
needs a list read; only locally invalid inputs promise zero requests.

## Threat analysis

These are design constraints for separate adversarial review; every new
path is unimplemented. [API checks](monitor-quotas-api.md#deterministic-checks)
owns the required probes. Existing file references identify inspected code,
not a claim that the proposed operations already satisfy these invariants.

- **Leaked token or account data, high.** Debug logs omit body/query at
  `internal/transport/transport.go:564`, but captures at line 550 otherwise
  see raw bodies. New calls use Sensitive, WithholdMessage, bounded safe
  codes, and sent-credential redaction on retained strings. Replace unknown
  APIError.Code values with a fixed local code; WithholdMessage alone does
  not hide them. Classify verified absence before withholding its message.
  Omit links,
  user_id, raw dimensions, free-form reason/expression text, and paymentUrl
  from all public projections. Errors never quote peer data. Test success,
  error, capture, partial Output, and CLI formatting paths. Private manager
  captures require separate authorization and sanitation.
- **Hostile peer, high.** `internal/transport/transport.go:514` reads without
  limit when MaxBody is unset. Cap every new response at 1 MiB, history at
  16 MiB per call and 32 MiB total, and alarm fan-out at 64. Fail explicitly
  beyond local safety limits with nil read Output; never truncate. Validate
  timestamps, numeric presence, IDs, envelope shape, and identity. A write
  decode failure is uncertain. Test limits, malformed values, and errors.Is
  and errors.As; protect aggregate memory as well as individual responses.
- **Foreign endpoint or redirect, high.** Token attachment is at
  `internal/transport/transport.go:439`; redirect origin checks run before
  and after caller hooks in `internal/transport/redirect.go:11`. Route only
  under configured Monitor, keep TLS validation, and use Once for writes.
  Fixed redirectUrl is data, not a destination to visit. Explicit endpoint
  overrides and custom transports are trusted caller inputs, not protected
  from their author. Test foreign redirects, hook rewrites, and 307/308.
- **Local profile/cache tampering, high.** Cache keys bind identity at
  `internal/tokencache/cache.go:41`; directory checks are at line 238 and
  credential writes use 0600 at `internal/cli/configfile.go:152`.
  Read-only has separate pre-config and profile checks at
  `internal/cli/readonly.go:24` and line 43. Reuse these mechanisms without
  new files, permissions, or token reuse rules. They do not defeat an
  attacker running as the same user. Test refusal before any network call
  and retain cache identity/permission regressions; no cleanup uses secrets.
- **Concurrent or repeated charge, high.** Existing quote-body reuse is at
  `monitor/logprojects_write.go:128`; Once behavior is at
  `internal/transport/transport.go:308` and line 403. Require the pricing
  gate, expiry precondition, lock through settlement, unresolved-attempt
  block, and caller serialization. Test delayed visibility, two callers,
  repeated same expiry, NoWait, cancellation, and a lost response. No
  idempotency key, atomic price guard, or cross-process lock is evidenced.

## Owner decisions

All remain proposed; approval cannot replace wire evidence.

1. Release order: recommend reads first, then free Email, then free
   Synthetic extension and Email extension; gate SMS/paid/recovery separately.
2. Time inputs: recommend RFC 3339 strings using existing flags instead of
   adding generic time.Time reflection support.
3. Price safety: recommend exact-body pricing as a hard gate, the higher
   comparable price as a local guard, and debit/tax evidence before paid
   writes. If identical bodies are unsupported, revise the design explicitly.
4. Renewal consent: recommend required ExpectedEndTime and optional
   MaxDaysLeft; the former refuses stale requests, the latter supports cron.
5. Repetition: recommend Once plus a per-Client unresolved-attempt block;
   callers still serialize and reconcile across processes.
6. Scope: recommend keeping delete/purge and auto-renew out; no evidence
   proves deletion consumes an action or free auto-renew works.
7. Output safety: recommend small state/history views, omit raw free-form
   text and unseen usage shapes, and enforce explicit local size bounds.
8. Existing-quota create: recommend refusing before quote/order; a second
   order's effect is unknown and may charge again.
