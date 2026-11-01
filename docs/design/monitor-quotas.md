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

The code has check and quota reads, notification create/quote, notification
auto-renew disable and active delete, and the synthetic auto-renew refusal.
The next independently landable slices are:

1. [Catalog and status](monitor-quota-coverage.md#read-contracts).
2. [Synthetic recovery](monitor-quota-coverage.md#synthetic-recovery).
3. Synthetic renewal and its quote, using the contract below and the
   [response policy](monitor-quota-coverage.md#unverified-write-responses).

The owner authorizes building renewal before its first in-window live run.
That manager-run check verifies success and arithmetic, not permission to
implement. Email and SMS renewal and recovery remain deferred. Lifecycle
rows retain their
[support gates](monitor-quotas-lifecycle.md#support-matrix).

Four emails against three logged transitions stay open; no gate is waived.

Out of scope: metric quotas, log quota renewal/resize, new or paid
Synthetic Test orders, bundles, `pay: false`, checkout URLs, introspection,
and a public settings operation; Slack, Telegram, Webhook, and Teams have
counters but no packages. Telegram delivery prerequisites remain unknown.
Production quotas are never deleted for a test.

## Public surface

Every operation has `Method(ctx, *MethodInput) (*MethodOutput, error)` and
operation name `monitor.Method`. Empty Inputs accept nil. Required fields
carry `vngcloud:"required"`; optional fields are values unless stated. All
reads fail with nil Output on any failed component except quota-status
rows. New writes use the [outcome contract][quota-outcomes].

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
  `StartTime, EndTime, CreatedAt, UpdatedAt time.Time` (a wire null
  `updatedAt`, live while CREATING, decodes as the zero time; the type is
  unchanged), `AutoRenew, POC, System bool`, and `AutoRenewPeriod int`.
  AutoRenew decodes numeric 0 and 1 (both live); POC decodes 0 (live) and
  1 (inferred, gated); other encodings fail closed. `QuotaStatusActive`
  and `QuotaStatusInTrash` name known statuses; other strings pass through.
- `SyntheticQuotaDetail` has `API, Location, Email, SMS, Slack, Webhook,
  Telegram, Teams int`, `ClassName, TierName string`, and `Price *float64`.
  The additive [nullable view](monitor-quota-coverage.md#nullable-limits)
  preserves null channel limits without changing those integer fields.
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

Writes validate finite nonnegative MaxPrice and nonnegative MaxDaysLeft
before requests; reject duration overflow. Quotes and writes validate
required fields and expiry syntax; quotes ignore write-only controls.
Notification creates resolve a package once: an omitted PackageID selects
the single zero-price entry; unknown, duplicate, unpriced, or ambiguous
candidates refuse. Synthetic writes never resolve through the v1 list.

**Order terms and quote mapping:** every write freezes one immutable set
of order terms before any quote: kind, package ID, `monthPeriod` 1, and
for a renewal the quota ID, package, and frozen expiry. One builder per
write maps those terms to its quote request and mutation body (ADR 0002
rule 8 asks for shared construction; no quote accepts a mutation body).

| Write | Quote request (from the terms) | Mutation body |
|-|-|-|
| Email or SMS create | `POST v2/{kind}/prices/created-price` `{redirectUrl:"", packageId, quantity:1}`; live for free and paid email | `POST v1/{kind}/quota` `{monthPeriod:1, packageId, redirectUrl, pay:true}` |
| Email or SMS extension | The same v2 call plus `resourceId:<quota>`; console only | `POST v1/{kind}/quota/extend-time` `{monthPeriod:1, redirectUrl, pay:true}` |
| Synthetic extension or recovery | `POST v2/synthetic/prices/created-price` `{redirectUrl:"", packageId, quantity:<detail API>, resourceId}`; live, 0 | `POST v2/synthetic/quota/extend-time` or `POST v2/trash/synthetic/quota` `{redirectUrl, monthPeriod:1, pay:true}` |

The v2 quote ignores the period; the v1 query quote
(`?package_id=&month_period=`) prices paid packages per month but 500s on
free ones. At period 1 both agree on a paid package, so v2 is the price
source (owner decision 10). Never add a quote-only field to a mutation
body, drop a field to make a quote pass, or send any period but 1.

Missing/null/negative/nonfinite prices fail closed as body-free API errors.
A zero quote for a positive list price is `monitor.ErrUnpriced`, an alias
of `vngcloud.ErrUnpriced`. Otherwise compare MaxPrice with the higher of
quote and list price and return `ErrPriceAboveMax` if exceeded. List and
quote must cover the same quantity and one-month term. For synthetic,
ListPrice is the identity-checked detail price, not a v1 catalog price.
Renew/recover require Basic, API 10, location 1, and zero prices; freeze the
quota PackageID and require detail equality. Quotes and read-back use that
ID, never a v1 list ID. No package/class/settings read is needed.
Email quotes are inferred pre-VAT in the API sibling, so the actual debit
can exceed the quote; a paid package is allowed whenever MaxPrice covers.

### Order and renewal execution

Preparation reads quota and applicable price sources under the write lock.
Create
refuses an existing quota before quoting. Renew first reconciles any open
attempt from retained terms. While unresolved, a guard mismatch preserves
Sent and Stage unconfirmed, with NotSettled wrapping ErrQuotaChanged.
With no unresolved attempt, compare EndTime to ExpectedEndTime as instants;
a mismatch is ErrQuotaChanged with no quote or write. After eligibility
checks, positive MaxDaysLeft skips a
further-away expiry with `Renewed: false`, PreviousEndTime set, and Quota
nil after the reads; OrderID is empty and PaymentPending false. Zero
allows early renewal. A free synthetic quota's extension outside its last
7 days gets 409 (live), a definite refusal with
no state change, returned as the status-classified `Conflict` `*APIError`:
visible, never retried; a job avoids it with `MaxDaysLeft` 7, not by
suppressing 409s.

Refuse POC, auto-renewing, unknown-status, missing-identity, or
invalid-expiry quotas before quoting. Renew accepts only ACTIVE with
EndTime strictly after now. It never
switches to recovery. Recovery has a separate
[expected-ID contract](monitor-quota-coverage.md#synthetic-recovery) and
uses the quota read, not an inferred trash item.

Immediately before send, reread and repeat the complete eligibility predicate on
the fresh snapshot: quota identity, package, expiry equal to the frozen terms,
status, `POC` false, `AutoRenew` false; for synthetic, repeat detail class,
location, price, and quantity checks. Create verifies absence again. Any change,
including a newly
enabled auto-renew or expiry crossing the recovery boundary, aborts without
send. New synthetic writes use ErrQuotaChanged for changed terms; malformed peer
data is an APIError. Create keeps its existing errors. Do not rebuild or requote
in a loop. No server precondition or price lock is known, so this narrows but
cannot eliminate cross-client races or price changes.

Send the evidenced body with `pay: true`, one month, and the fixed console
redirect value, with `Once: true` on every order, extension, and recovery:
no retry on any status or error, no token-refresh resend, no redirect, and
no outer SDK or CLI retry.

`CreateNotificationQuotaOutput` has `Quota *Quota`, `OrderID string`,
`PaymentPending bool`. `RenewQuotaOutput`, aliased by both renewal Outputs, adds
`PreviousEndTime *time.Time`, `Renewed bool`, and the outcome fields. Renewed
means confirmed new expiry, not merely sent. NoWait returns immediately after a
valid response with Quota nil and Renewed false; it does not skip preparation.
For evidenced order envelopes, PaymentPending means paymentUrl was nonempty and
differed from the sent redirectUrl (a free order echoes it). Then return partial
Output with `dns.ErrNotSettled` and stop; never follow or expose that URL or
reorder.

Synthetic renewal and recovery use the
[response policy](monitor-quota-coverage.md#unverified-write-responses);
they never require the inferred order envelope.

Otherwise wait with reads every 2 seconds under a 120-second child context
bounded also by the caller's context. Create settles only on the selected
package at ACTIVE; a 404 or a CREATING read, whose `updatedAt` can be null,
keeps polling (live: 404, CREATING at 3 s, ACTIVE at 5 to 6 s). Renewal
needs the same quota/package, ACTIVE, and EndTime later than
PreviousEndTime; trash exit alone is insufficient. NoWait skips the wait only.

New synthetic writes return Output on every outcome under
[the outcome contract][quota-outcomes]; existing create errors are unchanged.
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
`ErrQuotaChanged` for a guard mismatch, otherwise `ErrInvalidInput`, never
a send or a fulfilled result; the caller rereads and decides. Separate
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
SDK order and renewal MaxPrice defaults to 0. New synthetic commands
require [explicit consent](monitor-quota-coverage.md#cli-and-documentation).
Help and a stderr warning before a positive-budget
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

These are design constraints for independent review, not review findings
or claims that the new paths are tested. Each model covers both recover
and renew; their mutations are unimplemented.

- **Leaked tokens, high.** An error or optional order string could echo
  credentials into logs or partial Output. Existing suppression/redaction
  is in `monitor/quotas.go:184`, `monitor/quotas_write.go:321`, and
  `internal/transport/transport.go:594`; debug logging at line 607 omits
  bodies and query strings. Reuse Sensitive, bounded decoding, withheld
  messages, fixed codes, and operation-wide sent-token redaction through
  later refreshes. Suppress routine captures; never retain payment URLs or
  raw fields. Test echo strings in successes, malformed bodies, errors,
  retained attempts, all CLI formats, query, and logs. Keep private raw
  capture sanitation separate; nested escaped credentials retain the
  security policy's documented limitation.
- **Hostile API peer, high.** Malformed prices, flags, IDs, and timestamps
  could bypass consent or fabricate settlement. The bounded reader at
  `monitor/quotas.go:184`, shape decoder at line 282, and identity join at
  `monitor/quotas_lifecycle.go:375` are existing paths. Preserve 1 MiB
  bounds, required presence, integral counts, validated response path IDs,
  and same-ID/package confirmation. Missing envelopes cannot manufacture
  success; malformed possible-send responses retain the attempt. Test
  bounds before allocation, invalid joins, cancellation, safe errors.Is
  and errors.As, nil catalog versus partial status/write outputs. No raw maps,
  links, account dimensions, or invented pages enter new projections.
- **Foreign host, high.** A peer redirect or a caller redirect hook could
  send credentials elsewhere or replay a mutation. Token attachment at
  `internal/transport/transport.go:471`, origin checks before and after
  hooks at `internal/transport/redirect.go:11`, and Once redirect refusal
  at `internal/transport/transport.go:433` are existing defenses. Keep TLS
  verification, fixed Monitor routes, and fixed redirectUrl as body data
  only. Test every 3xx including 307/308, malicious links, and hook URL
  rewrites; no foreign server receives a request. Explicit endpoint and
  custom transport overrides remain trusted caller choices, not protection
  against that caller.
- **Local tampering, high.** A replaced profile/cache could select another
  account or defeat read-only intent. Identity-bound cache keys at
  `internal/tokencache/cache.go:41`, directory checks at line 242,
  credential writes at `internal/cli/configfile.go:158`, and read-only
  checks at `internal/cli/readonly.go:24` and line 43 remain authoritative.
  Add no credential files or reuse rules. Retain 0600 files/0700 cache
  directory and identity binding, and test CLI/profile/SDK read-only
  refusal before requests. These controls do not defeat an attacker with
  the same user privileges; ExpectedQuotaID/ExpectedEndTime are not account
  authentication. Retain existing cache permission/identity tests.
- **Concurrent or repeated charges, high.** Both mutations target the
  account's current quota without a server condition. Price or resource
  replacement after the final read remains possible. Existing attempt
  reconciliation is `monitor/quotas_write.go:109`, mutation Once is at
  line 168, and the hard wait is `monitor/quotas_wait.go:38`. Extend them
  with operation-bound frozen identity, full pre-send reread, price guards,
  and the same lock/ledger used by auto-renew. Test two callers, stale
  guards, flag/quantity flips, changed operations, delayed visibility,
  NoWait, and lost responses. At transport level verify Once overrides
  status/dial/auth retries and HTTP replay, not just a mocked send count.
  No cross-process lock, price lock, or idempotency key exists. Callers
  serialize and persist baselines; no cleanup delete, replacement order,
  or renewed attempt follows uncertainty without separate authority.

Existing history bounds remain 16 MiB per response, 32 MiB per call, and
64 alarms; quota responses remain capped at 1 MiB. Never truncate results.

## Owner decisions

These decisions are accepted under owner direction. Approval is no
evidence; unverified response bodies retain that label.

1. Release order, changed under owner direction: build synthetic catalog,
   status, recovery, and renewal now. The first renewal in the last 7 days
   is the manager's live check; it no longer gates implementation.
   Renewed still requires read-back. Email renewal is removed from the
   implementation sequence; Email and SMS renewal and recovery stay deferred.
2. Time inputs: RFC 3339 strings on existing flags, no time.Time support.
3. Price safety, changed: frozen terms mapped by one builder per write,
   and the higher of quote and ListPrice as the local guard. Paid
   notification packages ship when MaxPrice covers; synthetic stays free.
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
11. Email lifecycle rows, decided: SMS evidence covers Email auto-renew
    disable and active-quota delete, which ship with the SMS rows: one
    console path, identical bodies, matching live order responses and
    quota reads; only the unused v1 VAT treatment differs. Orders are not
    covered; each was verified on its own.
12. Notification auto-renew enable: defer Email and SMS enable until a
    live renewal quote with `resourceId` exists. No prerequisite purchase
    is authorized. Synthetic free-package enable keeps its visible refusal.
13. Recovery and read coverage use the contracts in
    [quota coverage](monitor-quota-coverage.md); paid Synthetic orders stay
    out until an ACTIVE class has a priced package and its write evidence.
14. Defer a trash list until its item shape is evidenced; a count alone
    cannot identify a quota. Keep check notification editing on JSON input,
    as explained in [Alerts](monitor-alerts.md#cli).

[quota-outcomes]: monitor-quota-coverage.md#machine-readable-write-outcomes
