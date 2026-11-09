# Billing history: API evidence and checks

[Billing history](billing-history.md) owns public types, limits, decisions,
and release order. No new method is implemented. Console evidence below
comes from the manager's sanitized shape summary, not raw-response files.
It establishes observations, not fixture provenance or a published schema.
No SDK-token probe has run for these endpoints.

## Sources

- Published: [payment overview][payment-overview] says a payment can cover
  multiple resources and distinguishes payment history from invoice
  history. It does not specify these gateway endpoints or row types.
- Published: [wallet display rules][vat] describe pre-VAT credit display,
  top-up conversion, and deductions. They do not establish whether these
  APIs normalize older history. Preserve reported values without conversion.
- Unverified source claims: the Vietnamese [credit guide][credit-doc] and
  [payment guide][payment-doc] could not be retrieved for this check. The
  claimed action names, filters, gateway names, and six payment type labels
  must not serve as wire-contract evidence. No numeric mapping depends on
  those labels being published.
- Console observation: authenticated GETs to transactions and payments on
  the Billing host succeeded using the IAM user's bearer token. An
  unauthenticated transactions fetch returned 401; the supplied summary
  does not establish a separate unauthenticated payments result.
- Console observation: fields and example enum values below. Account
  counts, amounts, dates, identifiers, and private capture paths are omitted.
  The summary reports two balance checkpoints, not every adjacent ledger
  equation. Raw responses and full reconciliation remain missing.
- Existing contract: [billing](billing.md#shared-rules) owns envelope and
  monetary conventions; [billing endpoints](billing.md#endpoints) owns the
  global Billing base and account scope. Cost explorer has a different
  pagination shape and provides no history paging evidence.

[payment-overview]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment
[vat]: https://docs.greennode.ai/overview/product-updates-all/2026
[credit-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole/lich-su-tin-dung
[payment-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole/lich-su-thanh-toan

## Requests

Both operations send GET with no body, project ID, region parameter,
keyword filter, or payment gateway filter. Authentication comes from the
configured shared SDK client, never from an Input field.

| Operation | Path under `Billing` |
|-|-|
| `ListTransactions` | `gateway/api/v1/transactions` |
| `ListPayments` | `gateway/api/v1/payments` |

Observed query keys: `pageNumber=1`, `pageSize=10`, `dateStart`, `dateEnd`.
Date strings use `DD/MM/YYYY`; encode slashes through `url.Values`, not
string concatenation. Public inputs use `YYYY-MM-DD`. The captured first
page request is 1-based; response numbering, later pages, ordering,
server defaults, maxima, omission semantics, and date inclusivity are
unverified. A returned UTC timestamp does not establish UTC filtering.

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

The manager's summary gives no actual `pageable.number` value. Do not claim
that the first response number is 1 or 0 until the probe compares pages.
There is no published or raw evidence of a stable sort or snapshot token.

For history, HTTP 200 must contain a `code` key, a non-null `data` object,
a non-null `content` array, and all four non-null integer metadata fields.
Preserve accepted envelope code representations (numeric, numeric string,
and null); envelope failures use [error rules](#error-statuses). Empty
body, missing envelope, or missing data is `InvalidResponse`, not an empty
wallet. Unknown JSON fields are allowed and retained in raw fixtures.

Use presence-aware private wire structs so zero and false cannot mask
missing/null fields. Require positive returned size no larger than the
requested size, nonnegative totals, and the verified response number for
the requested page. A silently clamped positive size is usable; freeze it
for the walk and compute page counts from it. Reject inconsistent counts,
more rows than size, duplicate IDs within a page, and integer overflow.
A single page can advertise more than 100 pages; only `All` rejects a
well-formed total exceeding its walk bound.

The empty and beyond-last-page metadata rules are unresolved. Record the
observed exact forms here before implementing their normalization. Never
accept a null/missing `content` as empty by default. If real empty responses
use null, return for an explicit contract update and independent review.

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
| `sourceReference` | string | `SourceReference string` |
| `description` | string | `Description string` |
| `createTimeStamp` | string with UTC offset | `CreatedAt string` |
| `userServiceReference`, `invoiceId` | null | Unmodeled |

All modeled fields must be present and non-null, with the observed JSON
type. Require positive IDs, finite amounts/balances, nonnegative amounts,
and a parseable offset-bearing timestamp. Empty description/reference is
allowed. Raw numeric codes survive; no string-to-number coercion applies.
Codes 1 and 2 were credits; code 5 was a debit. Descriptions associate them
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
| `description` | string | `Description string` |
| `createTimeStamp` | string; exact syntax missing | `CreatedAt string` |

Require modeled fields with their observed types, positive ID, and finite
nonnegative amount. The timestamp parser requires an offset-bearing value;
its exact raw form is a probe gate. Unknown gateway/status/type values stay
raw. Do not assign names from the unverified guide labels.

The following were null and remain unmodeled: `payGateTransactionID`,
`payGateName`, `bankCode`, `bankName`, `merchantCode`, `actualAmount`,
`paymentRefID`, `paymentOptions`, `executorID`, `executorName`. The summary
does not establish that a payment row joins a wallet row. Do not assert
reference equality or use payments to reconcile wallet spending.

## Error statuses

No published status table exists for these paths. Only the transactions
unauthenticated 401 is observed. Other rows below specify deterministic
handling under the existing transport contract, not captured behavior.

| Case | History result; CLI exit |
|-|-|
| Local input validation | Wrap `ErrInvalidInput`; 2, zero requests |
| HTTP 400 | `APIError`, status-derived `BadRequest`; 1 |
| HTTP 401 after recovery | `APIError`, `ErrAuth`, `Unauthorized`; 3 |
| HTTP 403 | `APIError`, `ErrPermission`, `Forbidden`; 1 |
| HTTP 404 | `APIError`, `ErrNotFound`, `NotFound`; 1 |
| HTTP 429 exhausted | `APIError`, `ErrRateLimited`, `Throttled`; 1 |
| HTTP 500/502/503/504 exhausted | `APIError`, `ServerError`; 1 |
| Unexpected 2xx or any 3xx | `APIError`, `InvalidResponse`; 1 |
| Other 4xx/5xx | `APIError`, safe status-derived code; 1 |
| HTTP 200, failing numeric envelope code | `APIError`, decimal code; 1 |
| Unsupported envelope code or malformed data | `InvalidResponse`; 1 |
| Walk bound or changed coverage | `ErrHistoryIncomplete`; 1 |
| Context canceled/deadline | Preserve context identity; 1 |

Preserve HTTP status, Operation, safe cause chain, and transport Retryable.
History withholds peer messages and arbitrary peer codes, including budget
not-found text. Numeric failing envelope codes do not become HTTP status
sentinels: an HTTP 200 envelope code 403 remains status 200, code `403`.
Never turn an envelope failure into a successful empty output.

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

This is a manager-run read-only probe specification, not authorization or
an existing executable. It requires an explicitly approved account/profile
and run. No worker reads credentials or makes these calls. No purchases,
policy changes, activation, synthetic transactions, or resource cleanup are
permitted to obtain witnesses. Only temporary probe files are removed.

### Runner and bounds

The manager prepares a temporary Go program under an already git-ignored
path inside the module, with no public SDK bypass added to the CLI. Load
`vngcloud.LoadConfig(ctx, vngcloud.WithProfile("default"),
vngcloud.WithRegion("hcm-3"), vngcloud.WithRetry(0, 0))`; `default` is the
captured IAM-user profile and region is only a Config requirement. Confirm
this explicit profile is the approved identity; if not, substitute the
approved IAM-user profile as a recorded runner argument. Never print its
contents, copy a token, or inspect `.env`. Use no endpoint overrides,
custom transports, debug logger, or response-capture hook.

Use `core.ClientOf(cfg).RouteURL` with `routes.ProductBilling` and the path
segments above, then `core.ClientOf(cfg).DoRaw`. Set GET, `Once=true`,
`NoRedirect=true`, `MaxBody=8<<20`, and fixed withheld error text. This
allows shared token acquisition but no application resend on 401. Record a
401 as a failure; do not silently refresh into a positive probe result.
One request has a 20-second context; the run has a ten-minute context and
at most 64 history GETs, serially, with no reruns. Stop at the first 401,
403, 429, 5xx, redirect, malformed body, size overflow, or deadline. Expected
400s on exploratory size/date/page variants are recorded without retries.

Write each raw body with its observed status under private
`examples/basic/output/raw/billing/`, wrapped under `body` per live-data
rules. Use mode 0600 files and a private directory; never overwrite a
capture. Save request parameters and per-step results privately without
headers. Print only step labels and pass/fail, never IDs, values, bodies,
errors, or private paths. The summary is not a fixture. Remove the temporary
program after the run; captures remain private for manager sanitation.

### Exact request sequence

Freeze `N = time.Now().UTC()` once. Let D be N's UTC calendar date,
S = D minus 90 calendar days, E = D plus 2 calendar days. Format all probe
query dates with `02/01/2006`. These broad exploratory bounds deliberately
make no claim about the eventual public date contract.

Run the following for transactions, then independently for payments:

1. GET page 1, size 10, start S, end E. Require HTTP/envelope success and
   record required field presence/types, metadata, and timestamp syntax.
2. Repeat page 1 at size 100 then 500, holding dates fixed. Candidate C
   qualifies only with HTTP/envelope success, `pageable.size == C`, coherent
   metadata, and all rows decodable. A 400 or clamped size does not qualify.
   Choose the largest qualifying candidate from 10, 100, 500. This proves
   the SDK cap, never the upstream maximum. If none qualifies, keep gated.
3. Using size 10 and S/E, request page 2, page 0, and page P+1, where P is
   the baseline `totalPages`. Page 2 must be nonempty to prove the response
   number base and distinct IDs. Record exact page-0 and beyond-end forms,
   including 400 if returned. Do not assume an empty wallet proves paging.
4. With page 1 and size 10, omit only `dateEnd`, then omit both dates.
   Record success/error and private row membership. This is evidence only;
   the chosen SDK rule always sends explicit bounds.
5. Walk S/E using the proved size from step 2, within the run budget and
   design consistency limits. Stop rather than declare success on drift.
   Preserve raw pages. Privately select the latest row older than two UTC
   days with timestamp UTC hour at least 17. This witness has different
   UTC and UTC+07:00 dates. Ties select the smallest ID. No witness means
   the timezone gate stays open; do not generate a transaction.
6. Let U be that witness's UTC date and L its UTC+07:00 date. For each
   distinct d in {U,L}, GET these `(dateStart,dateEnd)` pairs at page 1
   with the proved size: `(d-1,d)`, `(d,d)`, `(d,d+1)`, `(d+1,d+1)`.
   Walk each successful result to completion to test witness membership;
   absence on page 1 alone proves nothing. Reuse identical requests within
   this step. Record memberships by ID privately and check all returned
   timestamps. Require exactly one consistent candidate calendar and
   boundary interpretation across rows; ambiguity keeps the gate open.
7. Query `(E+1,E+2)` at page 1 and the proved size as a candidate empty
   range. If it contains rows, it is not empty evidence. A 400 is also not
   empty evidence. Record the exact successful empty shape if available.

The candidate zones are hypotheses to distinguish, not the only possible
zones. Witnesses away from midnight cannot establish exact midnight
inclusivity. If the results do not distinguish whole-day inclusive bounds
from midnight cutoffs, obtain first-party documentation or additional
existing boundary witnesses in another approved run. Never choose a rule
by majority or infer it from the console's local display.

Stop when any budget is exhausted and list each unresolved step. Record
per endpoint: proved cap, numbering/empty convention, timestamp syntax,
date filtering zone, and inclusive-public-to-wire conversion. Each fact
needs raw request/response provenance; update this document before coding.
Service-account probing is optional and needs a separately approved profile
and run; repeat baseline only. Do not create credentials or widen policy.

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
30-calendar-day range through today under the verified conversion, page 1,
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
- Empty end uses one clock read in the proved zone across midnight/pages;
  host local zone changes nothing. Check UTC versus local date transitions.
- Decode every field through the SDK. Missing/null false/zero fields,
  wrong types, negative amounts, bad offsets, overflow, empty body,
  unenveloped body, and null data/content/metadata fail with no Output.
- Exercise each error-table row on both endpoints, including HTTP 200
  failing envelopes. Assert `errors.Is`, `errors.As`, status, fixed message,
  code, Retryable, stdout emptiness, and absence of peer values in chains.
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

Implementation waits for SDK IAM-user access, both endpoints' proved caps,
response numbering and coherent multi-page/empty raw fixtures, payment
timestamp syntax, exact date zone and boundary conversion. `GetSpend` also
waits for raw known-code rows and their direction/amount semantics. A probe
that lacks suitable existing rows leaves the relevant gate unresolved.

Nonblocking unknowns remain explicit: service-account permissions, new
transaction codes, unmodeled null fields, payment type/status/gateway
meanings, reference joins, description grammar, historical VAT basis, equal
instant ledger ordering, and snapshot guarantees. No document move, owner
decision, or approval turns these into verified facts.
