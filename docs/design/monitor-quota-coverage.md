# vMonitor quota catalog, status, and synthetic recovery

This page extends the accepted [quota design](monitor-quotas.md). It owns new
read contracts, recovery, the policy for unverified synthetic write responses,
and machine outcomes. The parent owns threat analysis, renewal inputs, shared
pricing, locks, attempts, errors, and waits. [API evidence and
checks](monitor-quotas-api.md) owns wire evidence and fixture provenance;
[lifecycle](monitor-quotas-lifecycle.md) owns auto-renew and deletion. These
additions are unimplemented. No live run is authorized here.

## Read contracts

Use `danny.vn/vngcloud/monitor`, standard library only, with the parent's
`Method(ctx, *MethodInput) (*MethodOutput, error)` convention. Both new reads
accept nil Input. ListSyntheticPackagesInput is empty; GetQuotaStatusInput has
optional `Kind string`. They add no pagination parameters: the source arrays
have no observed pages. Each component uses the existing bounded, sensitive
quota reader and normal finite read retries, with no outer retry. Check
cancellation between sequential component reads.

Cap each component response at 1 MiB (`MaxBody`). The 8 MiB composite
budget counts retained component bodies, including bodies rejected by later
validation; it is not a total network-byte or Go heap limit. Discarded retry
bodies do not consume it. Decode each returned component once, without an
outer refetch loop; refuse before retaining a body beyond the budget.
Sequential decoding also bounds transient buffers; decoded objects and
allocator overhead are not measured by this body budget.

Each attempt is bounded independently. For A attempts, component body bytes
are at most MaxBody times A for bodies within the cap. The existing oversize
detector may read one extra byte per attempt, so its strict read bound is
(MaxBody + 1) times A. Include the single auth-refresh resend: with retry
count R, A is at most 2 times (R + 1). Set NoRedirect on these composite
component requests so redirect hops cannot escape that accounting. This
uses existing transport controls, not a shared transport change. Cancellation
stops retries and remaining components. A future shared transport byte budget
could cap total reads; it is not required here.

Require `TestQuotaCompositeRetainedBodyBudgetWithRetries`: counted real
transport bodies, positive retries, retryable failures then success, and a
401 refresh case; assert per-attempt caps, attempt bounds, more than 8 MiB
read while retained bodies stay within 8 MiB, and rejection before composite
overflow. Include the oversize probe, cancellation, and nil catalog versus
partial status results. This is a required new test, not a test-run claim.
No truncated result. Catalog failures return nil Output; quota-status has
the per-row partial-result contract below. Preserve safe
`errors.As(*vngcloud.APIError)` metadata under the public operation name and
`errors.Is` sentinels. No new dependency is needed.

### ListSyntheticPackages

`ListSyntheticPackagesOutput` has `Items []SyntheticQuotaClass`,
`Packages []SyntheticPackage`, `MonthPeriods []int`,
`MaxMonthPeriodForFreeQuota int`, and `PaymentMethod string`.
`SyntheticQuotaClass` has `ID, Name, Status, Description string`,
`Priority int`, and `Options []SyntheticClassOption`.
`SyntheticClassOption` has `PackageID string`, `Location, MinAPI, MaxAPI,
Step int`, `Price *float64`, `MonthPeriods []int`, and
`AutoRenewEligible *bool`. PackageID is the class location option's
orderable identifier; Location is its amount. It is not a v1 package ID.
`SyntheticPackage` has `ID, Name string`, `TierID, API, Location int`,
`Price *float64`, `MonthPeriods []int`, and `AutoRenewEligible *bool`.
Prices are monthly VND; nil eligibility means unknown, false a refusal.

Compose `GET v2/synthetic/quota-class`, `GET v1/synthetic/packages`, and
`GET v1/settings`, in that order, beneath `billing-api/`. Keep the two
catalogs separate. Do not join on ID, name, tier, price, or matching limits;
none establishes a provider relationship. Preserve every v1 entry even if
no class option resembles it. Never substitute its ID in a quota quote.
Class options use config.locations; config.packages is a class-side
identifier, not a reference into the v1 list. Require nonempty option
PackageID; validate config shape against the raw fixture. Disabled classes have
empty Options when config is absent. Sort classes by priority/name/ID, options
by Location/PackageID,
and v1 Packages by Name/ID. Unknown statuses remain visible as unavailable.

Require unique class IDs, v1 IDs, and option (PackageID, Location) pairs
within a class; sharing a package across location options is allowed.
Validate positive option bounds/step/location, min <= max, and nonnegative
integral v1 capacity counts. Reject malformed structures with a body-free
InvalidResponse;
unequal IDs across the two catalogs are expected, never a join error.
Require positive distinct settings periods and a positive free maximum.
Cap periods for v1 zero-price entries by that maximum; missing/null price
leaves periods empty. Negative/nonfinite prices fail. Array size uses the
response cap; there is no inferred provider maximum or pagination.

V1 Price comes only from its entry; its AutoRenewEligible stays nil because
no refusal identifies that v1 ID. Class-option Price stays nil: the class
response supplies no price. The evidenced Basic ACTIVE option (location 1,
min/max API 10, step 1) has AutoRenewEligible false and settings periods
capped as free, from the quota/detail and refusal evidence, not a v1 join.
Other options have nil eligibility and empty periods until priced/evidenced;
show the global settings periods separately. Never infer true eligibility.

The table has separate class-option and v1-package sections, labeled with
ID source, class status/description, limits, price, periods, and eligibility.
Disabled classes remain rows. JSON keeps Items and Packages separate and
preserves nil prices/eligibility; --query and text use the same projection.
Explain unknown option price even when v1 free10 shows 0. The additive
[class packages](monitor-synthetic-lifecycle.md#class-packages) view supplies
real unit prices in its own array. Options retain their existing
meaning; catalog reads cannot authorize a paid write.

### GetQuotaStatus

`GetQuotaStatusOutput` has `AsOf time.Time` and `Items []QuotaStatusRow`.
A row has `Kind, ClassName, PackageName, Status string`, `Quota *Quota`,
`Price *float64`, `StartTime, EndTime *time.Time`, `TimeLeftSeconds *int64`,
`AutoRenew, RenewWindowOpen *bool`, and `NextSafeCommand string`.
Add `Error *QuotaStatusError` to each row and `Complete bool` to Output.
QuotaStatusError holds `Code, Operation string; Status int`, with no message.
Kind accepts synthetic, email, or sms case insensitively; empty means all.
Reject any other value before requests with ErrInvalidInput and nil Output.
A filter emits
only that row; otherwise order Synthetic, Email, SMS. Capture one UTC
AsOf from the injected clock before reads. The result is a sequential
snapshot, not an atomic account view or authorization to write.

Compose the existing synthetic quota/detail and Email/SMS quota reads.
For present notification quotas, use their existing package lists to join
PackageID to list price and name; this is not a historical billed price.
Synthetic price/class come only from its identity-checked detail;
PackageName stays empty (unknown), and Quota.PackageID retains the quota ID
space. Do not fetch or join the synthetic v1 package list. Reject ambiguous
notification joins; unmatched packages have unknown price/name. Do not call
usage endpoints, trash, quotes, or writes. A source quota GET's NotFound
produces Status `none` with Quota,
prices, times, and booleans nil. A detail or package-list 404 fails the
row; never hide it as a missing quota.

On a failed row set Status `unavailable`, Error to fixed safe metadata, and all
unavailable values nil/empty. Continue other rows unless cancellation or the
aggregate byte cap stops further reads; mark unattempted rows with
RequestFailed. Return nonnil Output with Complete false and an error wrapping
new `monitor.ErrQuotaStatusIncomplete` plus safe component causes, preserving
errors.Is/errors.As. Error code QuotaStatusIncomplete and exit 1 take precedence
over component auth/404/cancellation. All rows succeeding or missing gives
Complete true, nil error, exit 0. Do not invent quota data. Filtered synthetic
calls only its existing quota/detail reads; never call Email, SMS, packages,
classes, settings, trash, or quotes for this view.

For present quotas, preserve raw Status (including IN-TRASH), render times
in UTC, and compute signed whole seconds EndTime minus AsOf without
`time.Duration` overflow. Negative means expired; the table says expired
and its elapsed time, zero says expires now. Keep null distinct from zero
and false. RenewWindowOpen is known only for the evidenced free synthetic
package: true if ACTIVE and 0 < time left <= 7 days, false otherwise;
it is nil for absent, paid, or notification quotas. It describes the time
window, not full write eligibility. Identify free synthetic by the parent's
Basic/detail price and capacity predicate, never a v1 ID or name match.
POC or auto-renew still prevents renewal.

NextSafeCommand is a fixed command name, never peer text or an executed
shell command. Choose `monitor recover-synthetic-quota` only for expired
IN-TRASH free synthetic with POC and auto-renew off; choose
`monitor renew-synthetic-quota` only for that free package when ACTIVE,
within the window, and both flags off. Otherwise use the matching
`monitor get-synthetic-quota` or `monitor get-notification-quota` read.
Missing notification quota can suggest `monitor list-notification-packages`
with its kind shown separately. Never recommend an unsupported renewal,
auto-renew enable, purchase, purge, or delete. Before the corresponding
write slice lands, show the read fallback. Help explains that a suggested
write still requires explicit guards from a fresh read.

The `quota-status` table has kind, class/package, status, price (VND), UTC
start/end, time left, auto-renew, renew window, and next safe command. `none` is
a row, not an error; all missing returns exit 0. JSON returns AsOf and typed
Items without table-only strings. Row failures print the partial table/JSON and
error exit; --query cannot hide a failed row on that path. Unknown values print
unknown.

### Nullable limits

The existing `SyntheticQuotaDetail` integer fields collapse nullable
channel limits to zero at `monitor/quotas_lifecycle.go:392`. Preserve those
fields for callers; add `ChannelLimits SyntheticNotificationLimits` with
`Email, SMS, Slack, Webhook, Telegram, Teams *int`. Fill both views from
the same raw response; ChannelLimits preserves null versus an explicit 0.
Missing required keys and fractional or negative values fail. Tables use the
nullable view; JSON adds it without changing old field types. Document
unkeyed-literal and exact-output compatibility limits. A Telegram usage
counter of 0 and a null limit establish no delivery prerequisite.

## Synthetic recovery

`RecoverSyntheticQuotaInput` has required `ExpectedQuotaID string`,
`MaxPrice float64` (default 0), and `NoWait bool`.
`RecoverSyntheticQuotaOutput` has `Quota *Quota`, `PreviousEndTime *time.Time`,
`Recovered bool`, `OrderID string`, `PaymentPending bool`, and the
[common outcome fields](#machine-readable-write-outcomes).
Quota is nil until read-back confirms; PreviousEndTime is the frozen
baseline. Recovered never means merely sent or no longer in trash.
There is no public QuoteRecover method: recovery uses the same evidenced
synthetic quote mapping internally. It neither creates nor purges.

Before requests validate nonnil Input, required ExpectedQuotaID with
`core.CheckPathID`, finite nonnegative MaxPrice, and the loaded profile's
read_only setting under the [SDK exception](cli.md#read-only). The SDK does
not inspect the CLI flag or VNGCLOUD_READ_ONLY; the CLI enforces those.
Acquire the context-aware quota lock and reconcile any open synthetic
attempt from its retained terms before preparing a new one. A mismatched
ExpectedQuotaID never replaces that attempt's Sent evidence. While it is
unresolved, return its partial Output with Stage unconfirmed and
ErrNotSettled wrapping ErrQuotaChanged; never report Sent no. Only after
resolution may the stale guard return QuotaChanged at preflight.
A matching recovery reuses that record's frozen baseline, never a newly
read expiry, and returns its confirmed outcome without a second mutation.

Read quota and detail; require ExpectedQuotaID equality, nonempty package, valid
timestamps, IN-TRASH, EndTime strictly before now, POC false, and AutoRenew
false. Unknown status wraps ErrUnexpectedStatus; known ineligible state wraps
ErrInvalidInput; a stale expected ID wraps ErrQuotaChanged. A quota 404 is
NotFound, never permission to create. No trash item or count is a precondition.

Freeze kind, quota ID, package, previous EndTime, status, flags, detail
class/location/price, API quantity, and monthPeriod 1. Use the parent's
synthetic price/identity
contract: quota/detail package equality, Basic capacity, detail price as
ListPrice, and the quota PackageID in the quote. Never fetch a v1 list to
resolve it. Check ErrUnpriced first, then max(quote, ListPrice) against
MaxPrice; both prices must be zero. A positive price stays unsupported even
with a larger budget. Missing prices fail closed. Immediately
before send reread quota/detail and repeat the entire predicate with the
same ID, package, expiry, flags, quantity, and zero detail price. Any
change aborts with ErrQuotaChanged; do not requote or rebuild terms.

Send the evidenced `POST v2/trash/synthetic/quota` body through Once.
The route names no ID: ExpectedQuotaID narrows a race but cannot bind the
server's actual target. Recovery starts a term at recovery time, not at
the old expiry. Use the unverified-response policy below. Read-back must
show the frozen ID and package, ACTIVE, and EndTime > PreviousEndTime.
A changed ID/package, trash exit alone, or a count of zero never confirms.
Do not demand exact month arithmetic to declare settlement.

Use the parent's 2-second polling and 120-second hard child deadline,
also bounded by caller cancellation. A 404, unchanged IN-TRASH, CREATING,
or unchanged expiry remains inconclusive; other read errors return an
uncertain partial Output. NoWait skips only settlement and leaves an open
record. Every synthetic write, including existing auto-renew, reconciles
that record first. Its identity is operation, kind, ID, package, previous
expiry, quantity, period, and frozen flags/status. A changed operation or
intent can reconcile but cannot claim the prior result or send in that
call. Preserve the parent's cross-client limits and cleanup authority.

## Unverified write responses

This policy applies to synthetic recovery, renewal, and
[create](monitor-synthetic-lifecycle.md#create). It does not loosen the
evidenced notification create decoder. Recovery has a
recorded HTTP 200; renewal uses 200 as a provisional console-derived
expectation until its in-window live check. Accept no other success status
as a verified response contract. A 2xx never sets Recovered or Renewed.

Bound the body before decoding. Tolerate empty body, JSON null, or an object
with absent/null orderId, amount, or paymentUrl; ignore unknown fields.
An optional string orderId may populate sanitized OrderID. An optional
string paymentUrl unequal to the fixed redirect marks PaymentPending and
returns partial Output with ErrNotSettled without visiting it. Absence or
an echo establishes no settlement. A wrong type for a recognized field,
non-object non-null JSON, malformed JSON, oversize body, or unexpected
status returns uncertain partial Output and a safe cause; keep the record.
Amount is unused and imposes no schema requirement. No raw body escapes.

After a tolerated 200, wait for read-back, or return the unconfirmed Output on
NoWait with nil error and the record open. A later matching call only
reconciles. Definite 4xx or transport-proven not-sent errors return Output and
their safe error under the outcome contract, without a new record or automatic
retry. The early renewal 409 is Conflict, exit 1, not a skip or a hidden
success. Every other possible-send failure wraps `dns.ErrNotSettled`;
`errors.As` retains safe APIError status/operation and `errors.Is` retains safe
causes. Uncertainty wins CLI exit classification, including over a later 404 or
cancellation.

## Machine-readable write outcomes

Recovery and renewal Outputs embed `QuotaMutationOutcome` with
`Sent, Stage string` and `Error *QuotaOutcomeError`. The error has
`Code, Operation string` and `Status int` (0 without an HTTP status); omit no
fields. Initialize Output before validation and return it on every error,
with confirmation false and Quota nil until read-back. PreviousEndTime is
null until a baseline exists. Error is null on success; otherwise it is a
safe classification, never peer text. The returned Go error remains the
authority for errors.Is/errors.As; Error.Code mirrors its CLI class.

JSON stdout contains exactly Sent, Stage, Error, PreviousEndTime, Quota,
OrderID, PaymentPending, and Renewed (renew) or Recovered (recover), with those
capitalized keys. Never emit the other operation's confirmation key. Quota uses
the existing CLI quota projection, including EndTime; Error.Operation always
names the requested public operation. Renewed/Recovered and PaymentPending
default false; OrderID defaults "". The table below is the outcome/exit
authority for both commands. An SDK validation error still carries this Output.
CLI failures before SDK entry synthesize it, using the same fixed
classification, without a cloud call.

| Result | Sent | Stage | Error.Code (Error.Status) | Exit |
|-|-|-|-|-|
| Renewal outside positive MaxDaysLeft | no | not-due | null Error | 0 |
| Same-ID/package read-back confirms | yes; unknown if retained send evidence is unknown | confirmed | null Error; confirmation true | 0 |
| Price guard refusal | no | preflight | PriceAboveMax or Unpriced (0) | 1 |
| Stale expected expiry/ID or changed final terms, no unresolved prior attempt | no | preflight | QuotaChanged (0) | 1 |
| Guard mismatch while a prior attempt remains unresolved | retained yes or unknown, never no | unconfirmed | NotSettled (retained status or 0); ErrQuotaChanged remains a safe cause | 1 |
| Definite mutation refusal, including early renewal | yes | refused | Conflict (409), BadRequest (400), Forbidden (403), Throttled (429) | 1 |
| Mutation auth or not-found refusal | yes | refused | Unauthorized (401), NotFound (404) | 3, 4 respectively |
| Accepted NoWait, no confirmation | yes | accepted | null Error; confirmation false | 0 |
| Response received but outcome unconfirmed, including malformed body/payment/wait failure | yes | unconfirmed | NotSettled (mutation status if known, else cause status or 0) | 1 |
| Disconnect/timeout/cancel with possible send and no response evidence | unknown | unconfirmed | NotSettled (0) | 1 |
| Proven not-sent transport failure | no | preflight | RequestFailed (0) | 1 |
| Input, consent, read-only, or config refusal | no | preflight | InvalidUsage, ReadOnly, InvalidConfig (0) | 2 |
| Missing credentials or token exchange failure before send | no | preflight | NoCredentials or ServiceAccountLoginFailed (status or 0) | 3; canceled exchange 1 |
| Other pre-send failure | no | preflight | TokenCacheFailed, RequestFailed, UnexpectedStatus (0); NotFound (404); BadRequest, Unauthorized, Forbidden, Conflict, Throttled, ServerError, ClientError, InvalidResponse, ResponseTooLarge (safe source status) | Existing mapping: 1, except auth 3 and NotFound 4 |

Sent describes the logical mutation attempt, including an attempt retained
in this Client's ledger. `no` requires proof of no transmission; `yes`
means a mutation response was observed, not that it changed state; unknown
means possible transmission without such proof. Read or quote responses
never imply yes. Preserve observed status before body decoding. Matching
reconciliation retains Sent; read-back alone cannot prove transmission. An
unresolved prior attempt keeps unconfirmed metadata even if this call
makes no new send or its guard mismatches. Reconcile retained terms first;
NotSettled overrides QuotaChanged until resolution. Preserve both sentinels
through errors.Is and never clear the account/kind hold on that mismatch.
A different intent must not claim its confirmation;
a resolved prior attempt returns the parent's changed-intent error with
Sent no for the new intent. Never infer no from a stale read or 4xx after
an uncertain attempt. NoWait is not used by the scheduled job.

Add `monitor.ErrQuotaChanged` for valid guards that no longer match;
it does not wrap ErrInvalidInput. Malformed guards still do. NotSettled
keeps priority over QuotaChanged, auth, cancellation, and NotFound. Preserve
a safe APIError for errors.As and all safe errors.Is causes. Recovery has no
not-due result; an ineligible state is refused at preflight. A no-longer-
matching expected guard always precedes a skip.

These two commands emit the complete result on stdout even for preflight
failure, plus the existing lowercase `error` envelope on stderr on error.
The envelope code/status/operation must agree with Output.Error; include
status 0 when no status exists, with fixed safe text. Scope this behavior to
recovery, renewal, and the new
[create and ensure](monitor-synthetic-lifecycle.md#outcomes-and-errors)
commands; existing notification write Outputs do not change.
Refuse --query for them before any request so every JSON result retains
outcome fields. Debug remains on stderr; scripts parse stdout, not logs.
A killed process, broken stdout, or missing/invalid JSON is externally
unknown, never a proven no-send, and never permission to repeat a write.

## CLI and documentation

Register `list-synthetic-packages` and `quota-status [--kind synthetic]` as
Read, safe for `--read-only`, environment, and read-only profiles. Register
`recover-synthetic-quota` and `renew-synthetic-quota` as Write; they require
no destructive `--yes` and never prompt. Recovery flags are
`--expected-quota-id`, `--max-price`, and `--no-wait`. Renewal flags and
`quote-renew-synthetic-quota` follow the parent; quotes are Read and hide
MaxPrice, MaxDaysLeft, and NoWait with NoFlag. Require explicit
`--expected-end-time` for renewal and `--expected-quota-id` for recovery,
or the equivalent required JSON field. The SDK MaxPrice default is 0;
the CLI requires `--max-price` or an explicit JSON MaxPrice, including 0,
for these two new writes before any request. This records deliberate cost
consent without changing existing create command compatibility.

Tables and text show the same outcome fields as JSON. Positive budgets
retain the parent's cost warning. The [CI contract](cli.md#unattended-ci)
owns environment auth, pinned installation, and wiki example obligations.

Update Monitor-Quotas and linked Monitor/Monitor-Alerts guidance in the
same implementation slice. Explain separate catalog ID spaces, unknown
option prices and v1 eligibility, status's unknown package name, and detail
price as the renewal/recovery guard source. Explain disabled classes,
nullable limits, recovery's new term, renewal's visible 409, required
consent/identity guards, NoWait, residual races, and read-only recovery
from uncertainty. Extend examples with reads only. Generate CLI-Monitor
and CLI from their source notes with `make gen-docs`; check generation
idempotence, wiki preview, links, and json/table/text goldens. No hand edits
to generated pages. Additions keep existing imports and field semantics.

## Threat analysis

The [parent threat analysis](monitor-quotas.md#threat-analysis) covers these
reads and recovery/renewal. The [synthetic lifecycle threats][synthetic-threats]
cover class-package fan-out, create, delete, and ensure.

[synthetic-threats]: monitor-synthetic-lifecycle.md#threat-analysis

## Deterministic coverage additions

Use httptest and injected clock/sleep; all public fixtures decode through
the SDK. The [API checks](monitor-quotas-api.md#deterministic-checks) remain
required, including existing create, lifecycle, transport, and cache tests.

- Catalog: disabled classes without config; deterministic ordering;
  separate ID spaces and arrays; duplicate IDs within each source refused;
  equal limits never assign identity, price, or eligibility across sources;
  null prices; periods; unknown eligibility; future paid packages never
  enabling writes. Status: all-present/all-none/mixed, intermediate and
  unknown states, source versus detail 404, one fixed AsOf, UTC offsets,
  large/negative time differences, 7-day boundary, no usage/write requests,
  safe next commands, and partial rows on component failure.
- Detail: explicit 0 versus null for every channel, legacy integer
  compatibility, and no Telegram prerequisite inferred from either view.
- Recovery: expired IN-TRASH only; exact-now refused; expected ID required;
  quota/detail package equality and capacity; same terms at final read;
  v1 ID never in quote/read-back; no catalog calls; zero mutation
  counts for every refusal. A delayed same-ID ACTIVE with later expiry
  confirms; a new ID/package, unchanged expiry, trash exit, or empty trash
  cannot. Neither recover nor renew falls back to the other or to create.
- Both writes: tolerant-response cases above, nullable OrderID, payment
  detection, every failure and wait boundary, repeated calls and cross-
  operation attempt reconciliation, no second mutation. Preserve the
  parent's renewal offset, MaxDaysLeft, overflow, and stale-expiry checks.
- Use the real SDK transport for 400/401/403/404/409/429, 500/502/503/504,
  failed dial, response timeout, disconnect after body receipt, cancelled
  send, and redirects. Count actual server requests and dial attempts;
  assert one attempt at most, no token-refresh resend, and zero mutation
  requests on local/preflight refusal. Retain transport tests for HTTP/1
  stale pooled connections, HTTP/2 REFUSED_STREAM/GOAWAY, no GetBody, and
  stripped idempotency headers. Do not replace them with mock assertions.
- CLI: explicit MaxPrice presence via flags/JSON including 0; guard-field
  precedence; pre-network read-only refusal; nil versus false/zero output;
  all outcome fields/error identities/exits; no secrets in partial outputs
  or generated examples. Reject --query before send; NoWait stays explicit.
