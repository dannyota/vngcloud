# Billing History Design

The [owner decisions](#owner-decisions) are accepted. Recorded live evidence
settles the list-read discovery gates. Implementation still requires
independent design and implementation-plan review and the remaining
[fixture checks](billing-history-api.md#open-evidence). All paths below are
proposed unless identified as existing. This document authorizes no live
run, purchase, activation, or cleanup.

Add wallet transactions, payments, and a one-command spend summary to
`danny.vn/vngcloud/billing` and the CLI. Preserve the existing package,
imports, methods, flags, output shapes, and cost explorer contracts. The
SDK remains standard-library only; no new dependency is needed. The CLI
calls only public SDK methods. Wire evidence, probes, fixtures, and checks
have one home in [billing history API](billing-history-api.md).

## Scope and evidence

The manager's console summary and shared-SDK probe establish the recorded
observations in [API evidence](billing-history-api.md#recorded-live-evidence).
The source register distinguishes published rules, recorded live evidence,
manager decisions, and missing evidence. Reports are not raw fixtures and
do not prove snapshot stability or complete reconciliation.

These paths differ from `/v1/credits`, whose IAM-user 403 remains recorded
in [billing](billing.md#deferred). The probe confirms SDK IAM-user access
for both history endpoints. Service-account access remains unverified;
never switch identity or widen policy automatically after a denial.

Invoices, orders, PDFs, writes, description parsing, and joins between
payments and wallet transactions are out of scope. Payment totals are not
added to wallet charges: they may describe the same economic activity.
Cost explorer decoding belongs to a separate slice.

## SDK surface

Each method uses `Method(ctx, *Input) (*Output, error)` on `billing.Client`.
The new list outputs embed `core.PagedList[T]`, as existing billing lists
do; callers use the exported output names without importing `internal`.
History uses the shared client, route builder, authentication, transport,
and envelope rules, with the stricter history checks below. Do not change
existing billing behavior to make these methods work.

### Dates

`StartDate` is required; `EndDate` is optional. Both are strict
`YYYY-MM-DD` calendar strings checked with `core.CheckDate`. Nil Input,
missing start, invalid dates, or start after the effective end return
`ErrInvalidInput` before authentication or network activity. Do not mutate
the caller's Input. Dates carry no time of day or caller-local time zone.

Both public bounds are inclusive calendar days in fixed UTC+07:00, the
manager's accepted filtering-zone choice for Asia/Ho_Chi_Minh. This is an
observed fit, not a published or uniquely proved zone; UTC+08:00 also fits
the probe. [Recorded evidence](billing-history-api.md#recorded-live-evidence)
keeps that ambiguity and exact-midnight evidence limits explicit.

Empty `EndDate` means today in UTC+07:00, resolved once per invocation.
Use `time.FixedZone("UTC+07:00", 7*60*60)` so host timezone data cannot
change the chosen offset. Inject a package-private clock into the resolver
in tests; all pages reuse the resolved range and never use host local time.

Send explicit bounds through `url.Values` in `DD/MM/YYYY`: `dateStart`
is `StartDate`, and exclusive `dateEnd` is the inclusive effective
`EndDate` plus one calendar day using `AddDate(0, 0, 1)`. Equal public dates
therefore request one full calendar day. Reject an unrepresentable next-day
bound before sending. Do not omit dates or use duration-based calendar math.

`CreatedAt` retains the wire string and offset. Parse with
`time.RFC3339Nano` for comparisons; never sort timestamps lexically. The
observed transaction timestamps carry UTC offsets while the console shows
local time. Neither proves which calendar the date filters use. The CLI
keeps explicit offsets and never silently uses the machine's local zone.

### ListTransactions and ListPayments

Both exported Input types contain these fields:

| Field | Type | Rule |
|-|-|-|
| `StartDate` | string | Required, with `vngcloud:"required"` |
| `EndDate` | string | Optional; resolved by [dates](#dates) |
| `Page` | int | Nonpositive means 1 |
| `Size` | int | Nonpositive or above 50 means 50 |
| `All` | bool | Start at 1; reject `Page > 1` before requests |

Both endpoint defaults and SDK maximum sizes are 50; `GetSpend` uses 50.
The [probe](billing-history-api.md#recorded-live-evidence) establishes
acceptance at 50 and rejection at 100. The SDK always sends a size and does
not depend on an untested server default.

`ListTransactionsOutput` embeds `core.PagedList[Transaction]`.
`Transaction` contains:

| Field | Go type | Meaning |
|-|-|-|
| `ID` | int64 | Transaction identifier |
| `Type` | string | Derived name, or empty for an unknown code |
| `TypeCode` | int | Raw transaction code |
| `Direction` | string | `CREDIT` or `DEBIT` from `creditStatus` |
| `Amount`, `Balance` | float64 | Amount and balance after the row |
| `SourceReference` | *string | Nullable opaque payment or bill reference |
| `Description` | *string | Nullable sensitive free text |
| `UserServiceReference` | *string | Nullable opaque service reference |
| `InvoiceID` | *int64 | Nullable invoice identifier |
| `CreatedAt` | string | Timestamp with offset |

Export `TransactionPurchase = "PURCHASE"`, `TransactionRefund = "REFUND"`,
`TransactionCharge = "CHARGE"`, `DirectionCredit = "CREDIT"`, and
`DirectionDebit = "DEBIT"`. Codes 1, 2, and 5 map to the respective types;
this mapping is observed and inferred, not a published numeric enum.
`TypeCode` always survives. Unknown codes do not fail a list.

`ListPaymentsOutput` embeds `core.PagedList[Payment]`. `Payment` contains
`ID int64`, `TypeCode int`, `Amount float64`, and string fields
`TransactionID`, `Gateway`, `Status`, `CreatedAt`, plus
`Description *string`. Nullable fields preserve null as nil and populated
empty strings as non-nil pointers; JSON includes null, without `omitempty`.
These are unimplemented types, so this changes no existing caller. Export
only the observed `GatewayBalance = "use_balance"`. Do not infer a
payment status name, derived payment type, or a reference join. Additional
payment fields remain explicitly unmodeled, with observed types and limits
listed in the API document; their presence does not block core reads.

### Paging

Single-page mode preserves the server's 1-based `number` as public `Page`.
`PageSize` is the returned size, not row count. Empty `Items` is a non-nil
empty slice. Validate metadata, content, and rows using the recorded
[empty and beyond-end conventions](billing-history-api.md#response-envelope-and-paging).

`All` uses a sequential walk, with no goroutines or automatic restarts:

1. Resolve dates and size once, request page 1, and freeze `totalPages`,
   `totalElements`, and returned size. Validate integers before arithmetic
   or allocation; never allocate from peer totals.
2. Request consecutive pages only. Require the expected page number,
   unchanged totals and size, and the exact expected row count on each
   page. Validate `totalPages` against ceiling division of total and size,
   using overflow-safe arithmetic and the recorded empty convention.
3. Reject every duplicate ID within or across pages, including identical
   rows. Do not discard duplicates and call the walk complete. A duplicate
   within one page is malformed; one across pages means an incomplete walk.
4. Return success only if unique row count equals the frozen total. Keep
   API order. Aggregate metadata is `Page=1`, `TotalPage=1`, and
   `PageSize=TotalItem=len(Items)`, including zero for an empty walk.

Limits: 100 pages, 8 MiB per response, and 32 MiB cumulative successful raw
response bodies per walk. Count bytes before retaining decoded rows; the
row limit is also at most 100 times the verified size. Do not preallocate
that product. The whole invocation, including authentication and retries,
has a two-minute context deadline or the caller's earlier deadline. Check
cancellation before requests and during row processing and aggregation.

A well-formed page advertising more than the walk bounds, changed totals,
repeated pages, duplicates across pages, or a short walk returns
`ErrHistoryIncomplete` and nil Output. Malformed fields return
`InvalidResponse`. An API, context, or decode failure on any page also
returns nil Output, retaining its error identity, not an additional
incomplete sentinel. No partial rows or sums escape.

These checks detect many changes, not snapshot isolation. A replacement
that preserves counts and evades duplicate checks can remain invisible.
There is no observed snapshot token or stable ordering guarantee. The wiki
must state that a complete walk is not an atomic account snapshot; a
narrower range may help but cannot guarantee success.

### GetSpend

`GetSpendInput` contains required `StartDate string` and optional
`EndDate string`. It uses the same complete transaction walk. Its exported
`GetSpendOutput` fields are:

| Field | Go type | Meaning |
|-|-|-|
| `StartDate`, `EndDate` | string | Effective inclusive public dates |
| `Purchases`, `Refunds`, `Charges` | float64 | Known-type amount sums |
| `Other` | float64 | Unknown-type credits minus unknown-type debits |
| `NetSpend` | float64 | `Charges - Refunds`; excludes `Other` |
| `OpeningBalance`, `ClosingBalance` | *float64 | Ledger range boundaries |
| `Reconciled` | bool | Observed ordered balance checks pass |
| `Transactions` | int | Number of rows |
| `ByType` | []SpendGroup | Groups ordered by code, then direction |

`SpendGroup` has `Type string`, `TypeCode int`, `Direction string`,
`Count int`, and `Amount float64`. Group by `(TypeCode, Direction)` so an
unknown code used in both directions cannot acquire a misleading label.
Amounts in groups are unsigned; `Other` uses direction explicitly.

Known purchases and refunds must be credits; known charges must be debits.
A contradiction, negative amount, missing amount/balance/direction, or
invalid timestamp is `InvalidResponse`, never silently a zero value.
The same structural checks apply to list rows. Unknown integer codes and
negative finite balances are allowed. Reject non-finite decoded values or
non-finite intermediate sums. Use compensated float64 summation; round
only when formatting, following existing billing money representation.

Order by parsed instant then ascending ID for deterministic calculations.
Increasing ID as ledger order for equal instants is unproved: if any
instants tie, return sums but nil boundary balances and `Reconciled=false`.
Otherwise reverse the first row's signed amount to obtain opening balance
and take the last row's balance as closing balance. Check each adjacent
balance and the final opening-plus-signed-total equation with absolute
tolerance 0.5 VND. A failed arithmetic check sets `Reconciled=false`; it
never changes totals or conceals an incomplete walk. This flag establishes
only arithmetic consistency, not completeness or accounting accuracy.

An empty range returns zero sums and count, empty `ByType`, nil balances,
and `Reconciled=false`. No observed row means no evidence of wallet
balance. All credits and debits, including unknown types, participate in
reconciliation. The summary never parses descriptions or calls payments,
balances, pricing, or cost explorer.

Wallet credits use the API's reported monetary basis. Published
[wallet display rules](billing-history-api.md#sources) describe pre-VAT
credits and deductions, but do not establish historical API normalization.
Do not convert VAT or equate credits purchased to tax-inclusive cash paid.

### Descriptions and output safety

Descriptions, references, IDs, timestamps, and amounts are account data.
SDK outputs expose them for authorized callers; raw description structure
is not a stable contract. Apply string sanitation to non-nil pointers too.
Do not derive actions, products, resource types,
or IDs from it. Redact all credentials observed across sends from returned
string fields, including unknown gateway/status strings. Retain credential
observations within the new history helper; never expose the observation
set. Ordinary account data remains available on the requested success path.

Errors, debug output, runtime query failures, and test failure messages
must contain neither peer text nor account values. History error messages
use fixed text; preserve only safe numeric envelope codes or status-derived
codes. The existing envelope helper alone does not meet that contract.
Capture hooks intentionally receive raw bodies and remain a trusted,
opt-in caller boundary, never enabled by ordinary commands or CI.

### Errors

Define `ErrHistoryIncomplete` once in `internal/core`, with aliases in
`billing` and `vngcloud`. This avoids the root-to-service import cycle.
Return `*vngcloud.APIError{Code: "HistoryIncomplete", Err: sentinel}` with
fixed text and `Retryable=false`. `errors.Is` works through wrapping and
`errors.As` reaches the public API error. CLI code is `HistoryIncomplete`,
exit 1. Do not alias a sentinel owned by another service.

All history errors return nil Output. The [error rules][history-errors]
define status and cause handling, including cancellation, body limits, and
sanitized decode causes. Extend the CLI classifier without changing
existing command classifications.

[history-errors]: billing-history-api.md#error-statuses

## CLI

| Command | Derived flags |
|-|-|
| `billing list-transactions` | `--start-date`, `--end-date`, `--page` |
| `billing list-payments` | Same, plus `--size` and `--all` on both lists |
| `billing get-spend` | `--start-date`, `--end-date` |

All commands are reads, work with read-only profiles, and need no `--yes`
or prompt. The service description becomes "Budgets, cost, balances, and
history". `--start-date` is required; no `--since` alias is added.

JSON and queries keep the exported Output shape; list query roots are
`Items` plus metadata, and spend queries start at the Output. Successful
JSON and text can contain account data. Help says so explicitly. Default
transaction tables show `CreatedAt`, `Type`, `TypeCode`, `Direction`,
`Amount`, `Balance`; payment tables show `CreatedAt`, `Gateway`, `Status`,
`TypeCode`, `Amount`. Descriptions and references require JSON, text, or an
explicit query rather than occupying default tables.

`get-spend` table output contains one summary table and a separate `ByType`
table only without a query. Use a command renderer; existing table views
only project one value and do not implement multiple tables. Unknown
balances print `none`. JSON encodes them as null. Query failures use fixed
`QueryFailed` text, exit 1, no underlying peer-bearing error, and empty
stdout, following `internal/cli/svc_compute_console_log.go:20`. Escape
terminal control characters in all formats. Buffer rendering before stdout
so query or formatting failure cannot emit a partial financial report.

Update `gen-docs` sources and regenerate `docs/wiki/CLI-Billing.md` when
implementing. Update `Billing-and-Pricing.md` in that same slice with the
spend example, effective date zone, bounds, output privacy, VAT limits,
unknown balances, paging consistency limits, and unverified service-account
access. Add CLI error documentation. No generated wiki edits occur here.

## Ranked improvements

Separate briefs own these changes; none is implied implementation scope:

1. Bound existing billing reads with `MaxBody`; `billing/cost.go:86` and
   `billing/budgets.go` currently omit it. Preserve existing output shapes
   and test the new limit. This is an observable hardening change.
2. Explain query roots in all billing help and generated wiki examples.
3. Add compact budget and balance tables; show absent balances as `none`.
4. List exported enum values in budget and threshold flag help. Runtime
   enum rejection needs its own approval under ADR 0002's server-owned
   value-validation rule, so do not add it incidentally.
5. Render resource epoch milliseconds as offset-bearing times in tables;
   keep JSON numbers unchanged.

### After the cost explorer fix

Cost tables, optional end dates, and automatic cost paging need separate
contracts against that endpoint's verified shape and filtering rules.
Do not transplant history bounds or `ErrHistoryIncomplete` into cost
explorer, or edit its design section as part of this work.

## Security obligations

These are design constraints for independent review, not a review verdict.

- Leaked tokens or account text: high impact if a peer echoes credentials
  or descriptions into errors. Existing `billing/billing.go:135` tracks
  sent credentials for envelope errors; `internal/transport/transport.go:593`
  logs no query/body. `billing/billing.go:112` still takes peer messages,
  and `internal/cli/render.go:59` can retain query errors. Proposed history
  sanitization and rendering above must cover errors, unwrap chains,
  successful string fields, retries, and partial results. Test canaries in
  all these paths. Raw captures remain private and require sanitation.
- Hostile peer: medium impact from memory exhaustion or incorrect totals.
  `billing/billing.go:143` accepts empty bodies, and missing/null fields can
  decode to zero. Proposed presence-aware decoding, byte/page/time limits,
  duplicate rejection, and nil-on-error outputs prevent these from becoming
  valid history. Test malformed envelopes and metadata, overflow, repeated
  pages, unknown codes, cancellation, and the full error matrix.
- Foreign host: high impact if credentials reach another origin. Existing
  `internal/transport/redirect.go:11` checks scheme and host before and
  after caller redirect hooks; `transport.go:454` attaches bearer tokens.
  Use fixed history routes and set `NoRedirect=true`. Default TLS verifies
  certificates. Endpoint overrides and arbitrary caller transports are
  trusted configuration, not a host allowlist or an enforced TLS guarantee;
  never let response data select a host. Test foreign redirects, downgrade,
  rewriting hooks, and untrusted certificates with local servers.
- Tampered local files: high impact from token theft or identity swapping.
  Existing `internal/core/load.go:175` checks credential file type/mode;
  `internal/tokencache/cache.go:29` binds IAM cache keys to identity and
  login endpoints, but `cache.go:157` reads existing tokens without the
  service-account strict checks at `service_account.go:81`. Proposed reads
  add no files or cache path and must preserve explicit-profile isolation.
  Check profile precedence, insecure credentials, cache isolation, and
  canceled token acquisition. The legacy IAM-cache read gap is outside this
  slice; do not claim resistance to a local actor able to replace it.
- Concurrent or repeated paid writes: low direct impact because these paths
  only issue GETs. No quote, price acceptance, write retry, or cleanup path
  exists. A concurrent writer can shift pages; the consistency limits above
  apply. `internal/transport/transport.go:405` owns retry attempts; history
  adds no retry loop. Test that only the two allowed GET paths are reached
  and that incomplete reads never trigger purchase, policy change, or
  cleanup. A spend summary cannot authorize a paid write.

## Release order and alternatives

The recorded evidence and manager's date decision support a concrete list
implementation plan; no repeat discovery run is required for settled facts.
Obtain independent design and plan review, then implement list methods,
renderers, sanitized raw fixtures, examples, wiki, and both live reads.
Release `GetSpend` only after known-type raw fixtures and SDK decoding tests
support its calculations. Hardening/help improvements can ship separately.

SDK aggregation avoids duplicate CLI paging and serves Go callers. A
CLI-only loop would split the error and completeness contract. Fail-closed
walking sacrifices partial results for reliable failure signaling; snapshot
accuracy remains limited by the upstream API. Unknown codes remain visible
instead of rejecting future types. Monetary floats preserve existing SDK
conventions at the cost of finite precision; this is not an invoice engine.

## Owner decisions

The manager accepted all seven choices under delegated owner authority:

1. Derive `--start-date` and `--end-date` from Inputs for consistent flags.
2. Optional `EndDate` means through today under [dates](#dates).
3. Put `GetSpend` in the SDK for one reusable aggregation contract.
4. Fail closed on incomplete walks with nil Output.
5. Expose derived transaction `Type` and raw `TypeCode` together.
6. Set both default and maximum SDK sizes to 50, the proved accepted size.
7. Add both reads to `make live`; inclusion does not authorize execution.

The manager also accepts fixed UTC+07:00 and inclusive-start/exclusive-end
wire filtering, with inclusive public dates converted under [dates](#dates).
That decision permits implementation despite the UTC+08:00 ambiguity; it
does not establish a published server zone or exact-midnight behavior.
Remaining [evidence gates](billing-history-api.md#open-evidence) stay explicit.
Approval never converts reports into raw fixtures or removes review.
