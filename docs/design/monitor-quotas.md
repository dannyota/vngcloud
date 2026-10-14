# vMonitor check state and quotas

Status: Accepted; each write keeps its live gate, and no live run is approved.

Expose synthetic check state and history, quota allowances and expiry,
notification quota orders, and quota renewal through `monitor`. On the
tested account, with no Email quota and a zero free allowance, the missing
quota was the confirmed cause of the silent alarm: after the free order an
In-alarm email arrived. [API evidence and checks](monitor-quotas-api.md)
owns wire shapes, provenance, and live gates.

This extends [vMonitor](monitor.md) and [Alerts](monitor-alerts.md), follows
[SDK and CLI](sdk-and-cli.md), [CLI](cli.md), and
[write conventions](../adr/0002-write-api-conventions.md). The SDK stays
standard-library only; the CLI calls only public SDK operations.

## Scope and release order

Ship the smallest independently useful slices, subject to owner decisions:

1. Check fields, alarm reads, history (with the owner decision 9 limit),
   verified quota reads, and the wiki correction.
2. Email quota reads, quote, and free email order together. The order
   response and all four alert receipts are verified; the rest stays gated.
3. SMS quota read, quote, and paid order, SMS auto-renew disable, and SMS
   delete of an active quota; SMS enable deferred under decision 12.
4. Free Synthetic Test extension after an in-window renewal (the server
   allows a free renewal only in its last 7 days), then Email extension.
5. Email auto-renew and delete, synthetic delete, and purge or recovery
   under [quota lifecycle](monitor-quotas-lifecycle.md), each after its
   own live check.

Four emails against three logged transitions stay open; no gate is waived.

Out of scope: metric quotas, log quota renewal/resize, new or paid
Synthetic Test orders, bundles, `pay: false`, checkout URLs, introspection,
and a settings operation; Slack, Telegram, Webhook, and Teams have counters
but no packages. Production quotas are never deleted for a test.

## Public surface

Every operation has `Method(ctx, *MethodInput) (*MethodOutput, error)` and
operation name `monitor.Method`. Empty Inputs accept nil. Required fields
carry `vngcloud:"required"`; optional fields are values unless stated. All
reads fail with nil Output on any failed component; writes have explicit
partial outputs below.

`Kind string` is required for notification operations. Constants
`NotificationKindEmail` and `NotificationKindSMS` are `Email` and `SMS`,
matched case insensitively and mapped to fixed `email` or `sms` path
segments; other kinds fail before requests. Gate each kind independently.

Validate caller path IDs with `core.CheckPathID` before any request and
response-derived path IDs before the next request, returning a body-free
`*vngcloud.APIError` for invalid peer data; this is a shape check, not UUID
validation. Package IDs are opaque list values used only in bodies; paid
Email IDs are not UUIDs. Never follow response URLs.

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

Fetch one history response per selected alarm sequentially, sending
`interval` as the check's `test_frequency` in minutes; a missing,
non-positive, non-integral, or out-of-range frequency is an invalid
response, refused before any history request. Check cancellation between
requests, validate alarm IDs before routing, and reject history whose
`alarm_id` conflicts with the requested alarm. Sort by Timestamp,
LocationID, AlarmID, then ID; identical timestamps stay separate events.

`CheckAlarmTransition` holds `ID, AlarmID, LocationID, LocationName,
OldState, NewState string`, `Timestamp time.Time`, and
`SubAlarms []CheckSubAlarmState`; the latter has `State string` and
`CurrentValues []float64`. Omit raw Reason, metrics, reason_date, and
subAlarmExpression. No raw response map escapes. A failed location returns
no partial history. Items is the records the server returned, not a
complete history: the server reports no state at the window start, paging
and truncation are both unknown (the response has no paging keys), and an
unknown alarm ID gets 200 with an empty list, so empty Items proves
neither that no transition happened nor that the alarm exists (owner
decision 9). Current state comes from `GetCheckAlarm`. Never truncate or
invent pages. An unknown definition ID gets 500, returned as that
`*APIError`, never NotFound.

### Quota reads

Unspecified textual fields are strings; timestamps parse as RFC 3339 where
a decision or identity needs them. Empty Inputs: `GetQuotaUsage`,
`GetSyntheticQuota`, and `GetSyntheticQuotaUsage`; required `Kind` Inputs:
`GetNotificationQuota`, `GetNotificationQuotaUsage`, and the package list.

- `GetQuotaUsageOutput` has `Usage QuotaUsage`. `QuotaUsage` has
  `SyntheticUsages []UsageCounts`, `EmailUsages []NotificationUsage`
  (`Amount int`, live), `FreeEmailQuota, FreeSMSQuota int`, the six
  severity strings, and `UnmodeledKinds []string`: an unseen nonempty
  usage array (log, metric, SMS) adds its fixed kind name there and never
  appears as RawMessage or contributes invented totals.
- `UsageCounts` has `API, Location, Email, SMS, Slack, Webhook, Telegram,
  Teams int`. Use integral-number decoding; reject fractional counts.
- `GetNotificationQuotaOutput` has `Quota Quota`.
  `GetSyntheticQuotaOutput` has `Quota Quota; Detail SyntheticQuotaDetail`.
  Its quota and detail reads must both succeed and agree on resource and
  package identity. Check the returned quota ID before the detail path.
- `Quota` has `ID, Name, Type, Status, PackageID string`,
  `StartTime, EndTime, CreatedAt time.Time`, `UpdatedAt *time.Time` (null
  while CREATING, live for SMS), `AutoRenew, POC, System bool`, and
  `AutoRenewPeriod int`. AutoRenew decodes numeric 0 and 1 (both live);
  POC decodes 0 (live) and 1 (inferred, gated); other encodings fail
  closed. Constants `QuotaStatusActive` and `QuotaStatusInTrash` describe
  known statuses; reads preserve other status strings.
- `SyntheticQuotaDetail` has `API, Location, Email, SMS, Slack, Webhook,
  Telegram, Teams int`, `ClassName, TierName string`, and `Price *float64`.
- Both quota usage Outputs have `Usage QuotaUsageDetail`: `Counts
  UsageCounts` (synthetic), `Amount int` (notification, live `{amount}`),
  and `Descriptions map[string]UsageDescription{Name, Specs, Unit}`.
- `ListNotificationPackagesOutput` is `core.List[NotificationPackage]`.
  A package has `ID, Name string`, `Price *float64` (VND), `Amount int`.
  Nil Price means absent or null, never free. Arrays have no observed pages.

Support is per operation and kind, in the [lifecycle support
matrix](monitor-quotas-lifecycle.md#support-matrix): the SMS quota read,
quote, and create are live, while the SMS usage read stays refused until
evidenced. A quota-read 404 means NotFound; a create proceeds only on the
verified kind-specific absence response, not a 404 from another route,
auth proxy, or usage read. A renewal 404 never falls back to create.

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
`OptimumPrice, ListPrice, GuardPrice float64`, and `Currency string` (VND),
no opaque properties or URLs. A quote is an estimate, never authorization.

Validate finite nonnegative MaxPrice, nonnegative MaxDaysLeft, and expiry
syntax before requests; reject duration overflow. Resolve a package once:
an omitted PackageID selects the single zero-price package; unknown,
duplicate, unpriced, or ambiguous candidates refuse after the list read.

**Order terms and quote mapping:** every write freezes one immutable set
of order terms before any quote: kind, package ID, `monthPeriod` 1, and
for a renewal the quota ID, package, and frozen expiry. One builder per
write maps those terms to its quote request and mutation body (ADR 0002
rule 8 asks for shared construction; no quote accepts a mutation body).

| Write | Quote request (from the terms) | Mutation body |
|-|-|-|
| Email or SMS create | `POST v2/{kind}/prices/created-price` `{redirectUrl:"", packageId, quantity:1}`; live for free and paid email | `POST v1/{kind}/quota` `{monthPeriod:1, packageId, redirectUrl, pay:true}` |
| Email or SMS extension | The same v2 call plus `resourceId:<quota>`; console only | `POST v1/{kind}/quota/extend-time` `{monthPeriod:1, redirectUrl, pay:true}` |
| Synthetic extension | `POST v2/synthetic/prices/created-price` `{redirectUrl:"", packageId, quantity:<detail API>, resourceId}`; live, 0 | `POST v2/synthetic/quota/extend-time` `{redirectUrl, monthPeriod:1, pay:true}` |

The v2 quote ignores the period; the v1 query quote
(`?package_id=&month_period=`) prices paid packages per month but 500s on
free ones. At period 1 both agree on a paid package, so v2 is the price
source (owner decision 10). Never add a quote-only field to a mutation
body, drop a field to make a quote pass, or send any period but 1.

Missing/null/negative/nonfinite prices fail closed as body-free API errors.
A zero quote for a positive list price is `monitor.ErrUnpriced`, an alias
of `vngcloud.ErrUnpriced`. Otherwise compare MaxPrice with the higher of
quote and list price and return `ErrPriceAboveMax` if exceeded. List and
quote must cover the same quantity and one-month term. Synthetic writes
cover only the verified free package with zero detail and quote prices.
Quotes are pre-VAT, so the actual debit can exceed the quote; a paid
package is allowed whenever MaxPrice covers the higher value.

### Order and renewal execution

Preparation reads packages and quota under the per-Client write lock. Create
refuses an existing quota before quoting. Renew compares the read EndTime
to ExpectedEndTime as instants; a mismatch is ErrInvalidInput with no
quote or write. Positive MaxDaysLeft skips a further-away expiry with
`Renewed: false` after the reads; zero allows early renewal. The server
refuses a free quota's extension outside its last 7 days with 409 (live),
a definite refusal with no state change, returned as the status-classified
`Conflict` `*APIError` naming the renew operation; the wiki tells a job to
treat a 409 from a renew as not yet and never retry, and `MaxDaysLeft` 7
avoids sending it.

Refuse POC, auto-renewing, unknown-status, missing-identity, or
invalid-expiry quotas before quoting. The console recovers only when
expired AND IN-TRASH, otherwise extends; only evidenced ACTIVE extension
is eligible, other-status or gated recovery cases refuse without a write,
and recovery waits for a verified trash lookup contract.

Immediately before send, reread and repeat the complete eligibility
predicate on the fresh snapshot: quota identity, package, expiry equal to
the frozen terms, status, `POC` false, `AutoRenew` false, and for synthetic
the quantity; for create, the verified absence response again. Any change,
including a newly enabled auto-renew, a flag that no longer decodes, or
expiry crossing the recovery boundary, aborts without send. Do not rebuild
or requote in a loop. No server precondition or price lock is known, so
this narrows but cannot eliminate cross-client races or price changes.

Send the evidenced body with `pay: true`, one month, and the fixed console
redirect value, with `Once: true` on every order, extension, and recovery:
no retry on any status or error, no token-refresh resend, no redirect, and
no outer SDK or CLI retry.

`CreateNotificationQuotaOutput` has `Quota *Quota`, `OrderID string`,
`PaymentPending bool`. `RenewQuotaOutput`, aliased by both renewal Outputs,
adds `PreviousEndTime time.Time`, `Renewed bool`. Renewed means confirmed
new expiry, not merely sent. NoWait returns immediately after a valid
response with Quota nil and Renewed false; it does not skip preparation.
PaymentPending means paymentUrl was nonempty and differed from the sent
redirectUrl (a free order echoes it). Then return partial Output with
`dns.ErrNotSettled` and stop; never follow or expose that URL or reorder.

Otherwise wait with reads every 2 seconds under a 120-second child context
bounded also by the caller's context. Create settles only on the selected
package at ACTIVE; a 404 or a CREATING read, whose `updatedAt` can be null,
keeps polling (live: 404, CREATING at 3 s, ACTIVE at 5 to 6 s). Renewal
needs the same quota/package, ACTIVE, and EndTime later than
PreviousEndTime; trash exit alone is insufficient. NoWait skips the wait only.

Pre-send errors return nil Output except the documented expiry skip.
Definite 4xx refusal or a proven failed dial returns its sanitized error.
Any other send failure, oversized/malformed success, cancellation after
possible send, or failed wait returns partial Output and an error wrapping
`dns.ErrNotSettled` plus the safe cause; `errors.As` keeps the APIError
status and `errors.Is` the sentinel. A failed read never proves no order.

Hold a context-aware per-Client lock across preparation, send, and
settlement. Record each unresolved attempt, NoWait included, under an
identity of operation, kind, and frozen terms (package; renewals add quota
ID and the extended-from expiry; lifecycle writes add theirs) until a read
reconciles it. While a record is open, every write on that kind reads
first and never resends. A matching call clears the record on a confirmed
outcome and returns it, clears it on a known rejection and returns the
error, and on an inconclusive read keeps it and returns `ErrNotSettled`. A
different call on that kind (other operation, package, or baseline expiry)
reconciles the same way, but a confirmed prior outcome then yields
`ErrInvalidInput` naming that attempt and the changed state, never a send
or a fulfilled result; the caller rereads and decides. Separate
clients/processes share no ledger: callers serialize per account/kind and
persist attempt and baseline before a write. This is not exactly-once.

After uncertainty, compare read-only quota identity, package, status, and
expiry with the saved baseline and inspect billing in the console. A 404 or
unchanged expiry can be stale; neither authorizes retry. Resolve with the
owner/provider if reads remain inconclusive. Never reset a client merely to
bypass the block. Cleanup, a replacement purchase, and a second renewal need
separate authority. The monthly action uncertainty is defined in the API
sibling; an automatic job must not use failures as retry signals.

Reads and quotes use the existing finite retry count with no outer retry
loop; cancellation stops retries and fan-out. Size, fan-out, or decode
failures become body-free APIError values with fixed local codes,
inspectable with errors.As. Unknown quota status on a write wraps the
existing `monitor.ErrUnexpectedStatus` (UnexpectedStatus/1); unsupported
kind/path or POC/auto-renew eligibility uses ErrInvalidInput before send.

## CLI and errors

Register each method's kebab-case command in `monitorOps`. Quotes and GETs
are Read; creates, renewals, and auto-renew settings are Write; deletes are
Write and Destructive, needing `--yes`. Read-only flag/environment refusal
precedes config, and profile refusal follows config but precedes requests.
Order and renewal consent is finite `--max-price`, default 0, as for
existing paid creates; help and a stderr warning before a positive-budget
write say `pay: true` debits the balance and MaxPrice is a local guard.

String flags `--start`, `--end`, `--expected-end-time`, and `--kind`, int
`--max-days-left`, float `--max-price`, bool `--no-wait`, and
`--package-id` use existing flag support and the same JSON-input validation.
Quote commands hide ignored fields with NoFlag, as log project quotes do.
Tables show status, expiry with UTC zone, AutoRenew, and absent prices as
unknown; partial write Outputs print even when an error follows. The wiki
says each alert consumes Email quota, observed usage can exceed the
transition count, `update-check` was followed by an extra transition, and
users check both `freeEmailQuota` in composite usage and the Email quota.

Use existing error identities: InvalidUsage/2 for local validation and
read-only refusal (ReadOnly/2); NotFound/4 for missing reads;
PriceAboveMax/1, Unpriced/1, and NotSettled/1 for their sentinels.
NotSettled takes precedence over wrapped cancellation, auth, or 404.
Other API errors retain safe status and existing classification, including
401/auth exit 3, 403 exit 1, and 409/429 exit 1. Unknown package validation
needs a list read; only locally invalid inputs promise zero requests.

## Threat analysis

Design constraints for separate adversarial review; every new path is
unimplemented. [API checks](monitor-quotas-api.md#deterministic-checks) owns
the probes. File references name inspected code, not satisfied invariants.

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
  credential writes use 0600 at `internal/cli/configfile.go:152`. Read-only
  has pre-config and profile checks at `internal/cli/readonly.go:24` and
  line 43. Reuse these without new files, permissions, or token reuse
  rules; they do not defeat an attacker running as the same user. Test
  refusal before any network call and keep cache identity regressions.
- **Concurrent or repeated charge, high.** Existing one-builder quoting is
  at `monitor/logprojects_write.go:128`; Once behavior is at
  `internal/transport/transport.go:308` and line 403. Require the frozen
  terms mapping, expiry precondition, full eligibility re-check before
  send, lock through settlement, operation-bound unresolved-attempt record,
  and caller serialization. Test delayed visibility, two callers, repeated
  same expiry, flag flips during quoting, changed baselines, create versus
  renew after an unresolved attempt, NoWait, cancellation, and a lost
  response. No idempotency key, price lock, or cross-process lock exists.

## Owner decisions

Ten are decided (3 and 6 changed from the recommendation, the rest
approved as recommended); 11 and 12 are proposed. Approval is no evidence.

1. Release order: reads and free Email (shipped), then SMS create, disable,
   and delete, then renewals after an in-window synthetic renewal, then
   the remaining rows as evidenced.
2. Time inputs: RFC 3339 strings on existing flags, no time.Time support.
3. Price safety, changed: frozen terms mapped by one builder per write,
   and the higher of quote and list price as the local guard. Paid
   packages ship whenever MaxPrice covers them; no debit or tax gate.
4. Renewal consent: required ExpectedEndTime, optional MaxDaysLeft for cron.
5. Repetition: Once, the full re-check before send, and a per-Client
   attempt record keyed by operation and frozen terms; callers serialize.
6. Scope, changed: delete, purge, and auto-renew in scope under [quota
   lifecycle](monitor-quotas-lifecycle.md), each row gated on its own
   evidence; the server's free-quota 400 is reported, not refused locally.
7. Output safety: small views, no raw text or unseen usage shapes, bounds.
8. Existing-quota create: refuse before quote; a second order may charge.
9. History: Items are the server's records; paging and truncation unknown.
10. Creation price source: the v2 quote at period 1, bounded by list price.
11. Email lifecycle rows, proposed: keep Email delete and auto-renew gated
    on their own evidence despite the SMS evidence on the same API shape;
    the only Email quota is the production one, and a test delete or flip
    is expected (unverified) to cut alerts. Recommended: keep gated.
12. SMS auto-renew enable, proposed: defer it to a release with its own
    live renewal-quote evidence; the quote with `resourceId` was never
    sent and the SMS quota is deleted. Recommended: defer; the alternative
    is another paid SMS purchase to capture the quote.
