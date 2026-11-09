# Billing history: API evidence and checks

[Billing history](billing-history.md) owns public types, limits, decisions,
and release order. New methods remain unimplemented. Console observations,
recorded manager-run evidence, and accepted choices are distinct from
published contracts. Private reports are not sanitized raw fixtures.

## Sources

- Published: [payment overview][payment-overview] says a payment can cover
  multiple resources and distinguishes payment history from invoice
  history. It does not specify these gateway endpoints or row types.
- Published: [wallet display rules][vat] describe pre-VAT credit display,
  top-up conversion, and deductions. They do not establish whether these
  APIs normalize older history. Preserve reported values without conversion.
- Unverified source claims: the Vietnamese [credit guide][credit-doc] and
  [payment guide][payment-doc] could not be retrieved for this check. The
  claimed labels and filters must not serve as wire-contract evidence.
- Console observation: authenticated GETs to transactions and payments on
  the Billing host succeeded using the IAM user's bearer token. An
  unauthenticated transactions fetch returned 401; the supplied summary
  does not establish a separate unauthenticated payments result.
- Console observation: fields and example enum values below. Account
  values stay private. Two balance checkpoints do not prove reconciliation.
- Recorded live evidence: the manager supplied a probe report and structured
  summary from shared-SDK requests, with raw captures retained privately.
  The [results](#recorded-live-evidence) settle the listed discovery gates;
  they do not establish public-method decoding or fixture sanitation.
- Existing contract: [billing](billing.md#shared-rules) owns envelope and
  monetary conventions; [billing endpoints](billing.md#endpoints) owns the
  global Billing base and account scope; cost paging is not history evidence.

[payment-overview]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment
[vat]: https://docs.greennode.ai/overview/product-updates-all/2026
[credit-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole/lich-su-tin-dung
[payment-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole/lich-su-thanh-toan

## Recorded live evidence

Provenance: manager-run temporary Go probe using shared SDK Config, IAM-user
authentication, Billing routes, and raw transport for both endpoints. The
completed run has 38 GETs within its 64-call bound. Private reports retain
stopped probe-check attempts as well as results; raw bodies stay private.
No account values or private capture paths belong here.

Both endpoints establish SDK IAM-user access with HTTP/envelope 200,
accepted sizes 10, 49, and 50, and rejection of size 100 by HTTP 200 with
envelope code 400. The reported validation rule names a maximum of 50;
50 succeeds despite error text saying "less than 50". Default and maximum
SDK `Size` are 50. The server default without a size was not tested.

First and second responses number pages 1 and 2 with distinct IDs. Complete
walks succeeded. Successful empty ranges report `number=1`, `size=50`,
`totalPages=0`, `totalElements=0`. Both timestamp forms parse as
`time.RFC3339Nano`, with fractional seconds and explicit UTC offsets.
The [row sections](#transaction-row) record nullable and populated fields.

Page 0 and omitted date bounds also yield validation errors inside HTTP
200. The summary records `Min_value` for `pageNumber`, `Max_value` for
`pageSize`, and `Not_null` for absent bounds; [error handling](#error-statuses)
checks the envelope before attempting to decode success `data`.

Date membership fits inclusive `dateStart` and exclusive `dateEnd` under
UTC+07:00 for every returned row; UTC+08:00 fits only the selected witnesses;
UTC fails. Exact-midnight membership and a unique filtering zone are not proved.
The manager accepts UTC+07:00 for implementation as an observed fit, not a
published rule, with the [public-date conversion](billing-history.md#dates). The
structured summary's unresolved-zone labels describe evidence limits, not an
implementation veto. First-party confirmation is desirable, not a release gate.
No approval upgrades this fit to verified uniqueness.

## Requests

Both operations send GET without body, project/region, or extra filters.
Authentication comes from shared SDK Config, never an Input field.

| Operation | Path under `Billing` |
|-|-|
| `ListTransactions` | `gateway/api/v1/transactions` |
| `ListPayments` | `gateway/api/v1/payments` |

Required query keys are `pageNumber`, `pageSize`, `dateStart`, `dateEnd`.
Request and response pages are 1-based. The SDK sends explicit dates and
size under [inputs](billing-history.md#listtransactions-and-listpayments).
Wire dates use `DD/MM/YYYY`; encode with `url.Values`. Apply the accepted
[date conversion](billing-history.md#dates), never timestamp-offset inference.

History requests set `OK: []int{200}`, `MaxBody: 8 << 20`,
`NoRedirect: true`, and a fixed `WithholdMessage`. Retain sent credentials
across retries for sanitation. Use a scoped history helper calling the
shared core client's `DoJSONStatus` with its own credential collector and
presence-aware envelope decoding. Billing's `doRaw` classifies budget
messages, `do` accepts empty bodies, and `finishEnvelope` ignores
`WithholdMessage`. Those defaults are unsuitable for history. Do not weaken
or silently change existing billing methods.

No new wait or application retry loop exists. Shared GET retries cover
network failures and HTTP 429/502/503/504, not every 5xx; one 401 recovery
may refresh authentication. With retry count R, the SDK send loops permit
at most `2*(R+1)` attempts per page, excluding authentication requests and
Go's internal connection replay. Defaults use R=3; a sleep is at most 30
seconds. The design's whole-operation deadline also bounds this activity.
Use caller cancellation throughout and never retry the complete walk.

## Response envelope and paging

Observed structure, with types rather than invented captured values:

```text
code: number, message: string, data: object
  content: array of row objects
  pageable: object
    totalPages: integer
    totalElements: integer
    size: integer
    number: integer
```

The recorded response `number` echoes the requested 1-based page. There is
no established stable sort or snapshot token; a successful walk is not a
guarantee of consistency during concurrent account changes.

Decode `code` before `data`: validation failures have array-valued `data`.
Only a successful envelope requires a non-null `data` object, non-null
`content` array, and all four non-null integer metadata fields. Preserve
accepted code representations (numeric, numeric string, and null); missing
`code` fails. Envelope failures use [error rules](#error-statuses). Empty
body, missing envelope, or missing data is `InvalidResponse`, not an empty
wallet. Unknown JSON fields are allowed and retained in raw fixtures.

Use presence-aware structs so zero/false cannot mask missing/null fields.
Require positive size no larger than requested, nonnegative totals, and
response number equal to requested page. A clamped positive size is usable;
freeze it
for the walk and compute page counts from it. Reject inconsistent counts,
more rows than size, duplicate IDs within a page, and integer overflow.
A single page can advertise more than 100 pages; only `All` rejects a
well-formed total exceeding its walk bound.

Empty ranges have `content=[]`, `totalPages=0`, `totalElements=0`, returned
size, and the requested 1-based number. The recorded first empty page uses
number 1 and size 50. Beyond-last-page requests also succeed with empty
content but retain the range's nonzero totals and echo the requested page.
Single-page mode preserves this metadata; `All` never requests beyond its
frozen total. Require full pages except the final remainder, or an empty
array for empty ranges/beyond-end pages. Null/missing content still fails.

## Transaction row

Observed shapes; numeric code meaning remains an inference from text and
direction, not a published enum:

| Wire field | Observed type | SDK field |
|-|-|-|
| `transactionID` | integer | `ID int64` |
| `transactionType` | integer | `TypeCode int`, derived `Type` |
| `creditStatus` | boolean | Derived `Direction` |
| `transactionValue` | number, nonnegative in sample | `Amount float64` |
| `cumulativeValue` | number | `Balance float64` |
| `sourceReference` | string or null | `SourceReference *string` |
| `description` | string or null | `Description *string` |
| `createTimeStamp` | string with UTC offset | `CreatedAt string` |
| `userServiceReference` | string or null | `UserServiceReference *string` |
| `invoiceId` | integer or null | `InvoiceID *int64` |

Require modeled keys and their stated types; only the four pointer fields
above accept null. The manager report permits null descriptions; structured
transaction field analysis directly records null/populated references and
invoice IDs, with string descriptions in sampled rows. Null becomes nil;
empty strings stay present. Require positive primary IDs, finite amounts
and balances, nonnegative amounts, and offset-bearing timestamps. Invoice
IDs retain integer values without coercion or an inferred positivity rule.
Codes retain integers without coercion; 1 and 2 were credits, 5 a debit.
Descriptions associate them
with credit purchase, refund, and resource charge respectively. Preserve
`TypeCode` even after deriving names; unknown codes have empty `Type`.

The summary identifies `[CREATE]`, `[RENEW]`, `[RESIZE]`, `[RECOVER]`, and
`[REFUND]` tags and describes resource names/IDs inside free text. It does
not supply full sanitized raw rows pinning a grammar. Never parse those
fragments for resource grouping. No evidence establishes monotonic IDs
within identical timestamps, negative amounts, or null balances.

## Payment row

| Wire field | Observed type/value | SDK field |
|-|-|-|
| `paymentID` | integer | `ID int64` |
| `transactionID` | identifier string | `TransactionID string` |
| `payGate` | string, `use_balance` | `Gateway string` |
| `paymentStatus` | string, `"1"`; meaning unknown | `Status string` |
| `paymentAmount` | number | `Amount float64` |
| `paymentType` | integer, `8084`; meaning unknown | `TypeCode int` |
| `description` | string or null | `Description *string` |
| `createTimeStamp` | RFC3339Nano string | `CreatedAt string` |

Require modeled keys with stated types, positive ID, finite nonnegative
amount, and an offset-bearing timestamp. Description accepts null under the
manager's row-nullability report; other modeled fields stay non-null.
Unknown gateway/status/type values stay raw, without inferred names.

The structured probe also records nullable strings `payGateTransactionID`,
`paymentOptions`, `paymentRefID`, `executorName`, and nullable numeric
`executorID`. These remain deliberately unmodeled: their semantics and an
integer contract for `executorID` are not established. `payGateName`,
`bankCode`, `bankName`, `merchantCode`, and `actualAmount` remain observed
only as null. Retain all fields in raw fixtures. None blocks the modeled
payment read; no evidence establishes reference joins to wallet rows.

## Error statuses

Recorded errors are console transactions 401 and probe HTTP 200/envelope
400. Other statuses specify handling, not observed or published behavior.

| Case | History result; CLI exit |
|-|-|
| Local input validation | Wrap `ErrInvalidInput`; 2, zero requests |
| HTTP 400 | `APIError`, status-derived `BadRequest`; 1 |
| HTTP 401 after recovery | `APIError`, `ErrAuth`, `Unauthorized`; 3 |
| HTTP 403 | `APIError`, `ErrPermission`, `Forbidden`; 1 |
| HTTP 404 | `APIError`, `ErrNotFound`, `NotFound`; 4 |
| HTTP 429 exhausted | `APIError`, `ErrRateLimited`, `Throttled`; 1 |
| HTTP 500/502/503/504 exhausted | `APIError`, `ServerError`; 1 |
| Unexpected 2xx or any 3xx | `APIError`, `InvalidResponse`; 1 |
| Other 4xx/5xx | `APIError`, safe status-derived code; 1 |
| HTTP 200, envelope 400 | `APIError`, status 200, code `400`; 1 |
| HTTP 200, other failing numeric code | `APIError`, decimal code; 1 |
| Unsupported envelope code or malformed data | `InvalidResponse`; 1 |
| Walk bound or changed coverage | `ErrHistoryIncomplete`; 1 |
| Context canceled/deadline | Preserve context identity; 1 |

Preserve HTTP status, Operation, safe cause chain, and transport Retryable.
History withholds peer messages and arbitrary peer codes, including budget
not-found text. Numeric failing envelope codes do not become HTTP status
sentinels: an HTTP 200 envelope code 403 remains status 200, code `403`.
Envelope 400 keeps the existing billing API error identity: `errors.As`
reaches `*vngcloud.APIError`, `Retryable=false`, and `errors.Is` does not
match `ErrInvalidInput`. Local shape validation alone uses that sentinel.
This follows `billing/billing.go:100` without adding global mappings. Use
fixed text and discard validation `data`, including `errorMsg`; never
retain peer arrays in error fields or chains, or decode them as a page.

For 2xx body overflow, return `*vngcloud.APIError` with
`Code: "InvalidResponse"`, fixed text, observed status, and underlying
`transport.ErrBodyTooLarge`. That internal sentinel is testable internally;
public callers use `errors.As` and `Code`. Oversized HTTP 4xx/5xx bodies
retain status-based identity per `DoJSONStatus`; do not claim they wrap the
size sentinel. Malformed JSON uses `SanitizeDecodeError`, preserving safe
JSON error types for `errors.As` without original values or exported fields.
`errors.Is` must preserve context cancellation/deadline errors. All failures
return nil Output and print no report on stdout.

## Free verification call

The [recorded probe](#recorded-live-evidence) settles modeled-read discovery.
This procedure requires an approved manager-run recheck; it authorizes no
run, purchases, policy changes, or manufactured transactions. Workers make
no cloud calls or credential reads.

### Runner and bounds

Use a temporary Go program under a git-ignored module path. Load Config
with the approved explicit IAM-user profile, required region, and
`WithRetry(0, 0)`; no overrides, custom transport, logger, or capture hook.
Use `core.ClientOf(cfg).RouteURL` with `routes.ProductBilling`, then `DoRaw`.
Set GET, `Once=true`, `NoRedirect=true`, `MaxBody=8<<20`. Bound each request
at 20 seconds and the run at ten minutes and 64 serial GETs. Stop on
401/403/429/5xx, redirect, malformed body, overflow, deadline, or drift.
Expected invalid parameters may return HTTP 200/envelope 400. No automatic
retry or restart. Save raw bodies, status, and queries privately with mode
0600, no overwrite or headers. Print only step labels/pass/fail; remove the
program after use. Captures require manager sanitation before publication.

### Exact request sequence

Freeze N in UTC; D is its date, S=D minus 90 calendar days, E=D plus 2 days.
Format dates `DD/MM/YYYY`. For each endpoint, within the shared run budget:

1. GET page 1, size 10, S/E; record status, code, shape, and metadata.
2. Repeat sizes 49, 50, 100; verify accepted size echoes and rejected
   validation envelopes. Never infer server defaults or expand the SDK cap.
3. At size 10 request page 2, page 0, baseline totalPages+1; check distinct
   IDs, numbering, validation errors, and beyond-end metadata.
4. At page 1, size 10, omit end, then both bounds; record envelope errors.
5. Walk S/E at size 50. Select the latest existing row older than two UTC
   days with UTC hour at least 17, ties by smallest ID. No witness means
   missing evidence; never create one.
6. Let U/L be its UTC/UTC+07:00 dates. For each distinct d in {U,L}, query
   `(d-1,d)`, `(d,d)`, `(d,d+1)`, `(d+1,d+1)` at size 50. Walk results before
   judging absence; reuse identical requests. Record compatible UTC,
   UTC+07:00, UTC+08:00 models, not a uniquely proved zone/midnight rule.
7. Query `(E+1,E+2)`, page 1, size 50; record successful empty shape.

Report unresolved steps and discrepancies without changing the accepted
[date choice](billing-history.md#dates). Contradictory evidence needs manager
resolution before release. Further zone or service-account probes require
separate approval; never create credentials or widen policy for them.

## Fixtures

Manager-sanitized raw responses go in `testdata/billing/` and must decode
through the eventual public SDK methods in tests. Never reconstruct them
from this summary, tables, SDK output, or hand-built expected values.

- `ListTransactions.json` and `ListTransactionsPage2.json`: consecutive
  coherent pages. Add separate raw-derived fixtures for each observed known
  code if one page does not contain all three; no requirement forces live
  purchases or refunds to populate a page.
- `ListTransactionsEmpty.json`: successful empty range, distinct from any
  beyond-last-page fixture. Add a beyond-page fixture if that result is 200.
- `ListPayments.json`, `ListPaymentsPage2.json`, and
  `ListPaymentsEmpty.json`: independent evidence for that endpoint.

Keep private provenance mapping operation, exact query, status, auth mode,
raw capture, and sanitizer transformations. Public fixture notes contain
only operation, field shape, and synthetic-data status. Retain nulls and
unknown fields. Replace identifiers consistently with numeric or string
synthetic IDs of the same JSON type. Replace names, IDs, hostnames, and
other account fragments inside descriptions and references. Replace money
and timestamps consistently, preserving offset syntax and intended ledger
relations. Redact credentials before any public artifact. Label adversarial
and arithmetic fixtures synthetic; they do not establish upstream behavior.

Both reads join `make live` after implementation. Use a fixed-at-run-start
30-calendar-day range through today under the accepted conversion, page 1,
`Size=min(10, endpoint cap)`, `All=false`, bounded context, and counts-only
logs. They still require manager live-run authorization. Capture and compare
raw versus SDK examples privately under [live data][live-data].

[live-data]: ../../instructions/live-data.md#adding-or-fixing-an-api

## Deterministic checks

Use `httptest`, injected clocks, raw-derived fixtures, and explicitly
synthetic hostile bodies. No unit test contacts a cloud endpoint.

- Assert both exact paths, empty body, escaped wire dates, page/size, no
  project/region/filter keys, and read-only CLI access without consent.
- Nil Input, invalid/leap/zero-padded dates, reversed ranges, converted-date
  overflow, and `All` with `Page>1`: invalid input, zero authentication/GETs.
- Empty end uses one clock read in fixed UTC+07:00 across midnight/pages;
  host local zone changes nothing. Assert end-plus-one-day conversion across
  month/year/leap boundaries and one-day public ranges. Size 0 or >50 sends
  50; sizes 1 and 49 survive. No request omits either bound.
- Synthetic 18:xx UTC rows fitting UTC+07:00 and UTC+08:00 cannot prove a
  unique zone. The accepted UTC+07:00 rule remains a decision, not evidence.
- Decode every field through the SDK. Missing/null false/zero fields,
  wrong types, negative amounts, bad offsets, overflow, empty body,
  unenveloped body, and null data/content/metadata fail with no Output.
  Nullable fields decode null, empty, and populated values distinctly;
  missing keys or wrong populated types fail. Include pointer sanitation.
- Exercise each error-table row on both endpoints, including HTTP 200
  failing envelopes. Assert `errors.Is`, `errors.As`, status, fixed message,
  code, Retryable, stdout emptiness, and absence of peer values in chains.
- Envelope-400 validation arrays for size 100, page 0, and omitted bounds:
  assert status 200, code `400`, nil Output, exit 1, no `ErrInvalidInput`,
  no retry, and no validation text leakage. Decode the code before data.
- Regress HTTP 404 on both history commands: `NotFound`, exit 4, empty
  stdout. Run existing `TestClassify` and `TestExitCode` assertions unchanged
  in `internal/cli/errors_test.go`, including wrapped not-found errors.
- Test body overflow on 200, 403, and 503 separately. Assert request-size,
  cumulative-byte, page, row, time, and cancellation bounds; exactly-at-limit
  succeeds. Cancel during authentication, retries, paging, and aggregation.
- Multi-page success, empty range, beyond-end form, wrong page numbers,
  duplicate/conflicting IDs, changed total/size, short middle/final page,
  negative/huge metadata, and late HTTP failure: no silently partial result.
- Spend: shuffled offset-equivalent instants, ties, single row, no rows,
  signed unknown types in both directions, known-type direction mismatch,
  refunded charges, compensated sums, and reconciliation tolerance edges.
- Inject token canaries across 401 recovery and concurrent token rotation,
  descriptions/IDs in peer errors, nested decode fields, and runtime query
  errors. Verify all output formats, queries, logs, and error fields; only
  intended account data reaches successful stdout. Escape terminal controls.
- CLI: default columns omit descriptions/references; explicit queries keep
  roots; spend renders two tables only without query, null balances as none,
  and `--query NetSpend --output text` as a scalar. No render error emits a
  partial report. Check generated-doc freshness and matching wiki guidance.
- Reuse local transport/profile tests for redirect/downgrade/hook refusal,
  TLS verification, explicit-profile isolation, and credential permissions.
  Assert no write, quote, account fallback, or cleanup request occurs.

## Open evidence

Discovery gates for SDK IAM-user access, SDK size default/cap, page base,
empty/beyond-end conventions, timestamps, and modeled row nullability are
closed by recorded evidence. The manager's date decision closes the
implementation gate while retaining UTC+08:00 and exact-midnight ambiguity.

Implementers can plan the reads now; independent design and plan review
still precede code. Manager-sanitized raw fixtures and SDK decode tests are
acceptance gates: the JSON summary holds analysis, not response bodies.
`GetSpend` release needs raw known-code rows, direction/amount semantics,
and reconciliation tests. Prepare fixtures through the manager, never from
SDK output; probe success does not replace these checks.

Nonblocking unknowns: service-account access, server size default, new
transaction codes, remaining unmodeled field types/semantics, payment enums,
reference joins, description grammar, historical VAT basis, tied-instant
ledger order, snapshot guarantees, unique zone, and exact-midnight behavior.
Do not reopen settled discovery or hide these limits behind approval.
