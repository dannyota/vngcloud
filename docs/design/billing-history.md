# Billing History Design

Status: Proposed. Owner decisions and evidence gates are listed at the end;
approval of this document authorizes no live run.

This design adds credit wallet history and payment history reads to the
`billing` package and CLI, plus one spend summary that answers "what did we
spend since I bought credits" in a single command. It also ranks
improvements to the existing billing commands. Wire evidence, field tables,
error statuses, fixtures, and deterministic checks live in
[billing history API](billing-history-api.md).

It builds on [billing](billing.md): same package, same `Billing` endpoint,
same envelope decoder, same money and date rules. Cost explorer decoding
belongs to a separate fix and is out of scope here; only CLI follow-ups for
after that fix appear in [a later section](#after-the-cost-explorer-fix).

## Goals

- List credit wallet transactions and payments for a date range, with typed
  rows and a bounded way to read every row.
- Summarize spend for a range: purchases, charges, refunds, net spend, and
  opening and closing balance.
- Keep the auth facts in [billing](billing.md) consistent with the evidence.

## Non-goals

- Invoices (`/v1/billings`), credits (`/v1/credits`), orders, and the PDF
  export the console offers.
- Parsing product, resource type, or resource ID out of free-text
  descriptions; the format is not captured, see [descriptions](#descriptions).
- Any write. Every operation here is a read.

## Evidence summary

Published guides describe the credit history page (every wallet balance
change; actions Deposit balance, Refund balance, Buy resource) and the
payment history page (transaction ID, payment type, gateway, date, details,
amount in VND). The console calls `GET gateway/api/v1/transactions` and
`GET gateway/api/v1/payments` on the `Billing` host with the IAM user's
bearer token and the budgets envelope. Field names, numeric codes, and the
paging block are console evidence, not a published contract, and the code
mapping is inference from row descriptions; the
[API document](billing-history-api.md#sources) marks each one.

## Auth facts

[Billing](billing.md#deferred) recorded that credits return 403 to IAM
Users. That fact stands for `/v1/credits`. The transactions and payments
endpoints answered 200 to the IAM user's token in the console, and 401
without a token. The SDK has not sent that token to these paths yet, so the
first [free verification call](billing-history-api.md#free-verification-call)
must confirm it before implementation. Service-account access is untested;
a 403 keeps `ErrPermission` and exit 1 as
[service-account auth](service-account-auth.md#errors-and-secret-handling)
defines, and the wiki states it as unverified until a run shows otherwise.

## SDK surface

All three operations live in `billing`, follow
`Method(ctx, *Input) (*Output, error)`, and call `do` from
`billing/billing.go`, so envelope codes, redaction, and the not-found
mapping behave as for budgets. Each request sets `MaxBody` to 8 MiB, as
server reads do; the current billing reads set none and
[gain it as an improvement](#ranked-improvements).

### Dates

Public inputs stay `YYYY-MM-DD` strings checked by `core.CheckDate`, the
format every other billing date uses. The SDK converts to the wire form
`DD/MM/YYYY` after the check, so callers never see the wire format and the
CLI keeps one date format. `StartDate` after `EndDate` is `ErrInvalidInput`
before any request.

`StartDate` is required. `EndDate` is optional: empty means "through
today". How the SDK expresses that on the wire depends on the
[date gate](billing-history-api.md#open-evidence): omit `dateEnd` if the
API accepts its absence, otherwise send the UTC calendar date of now plus
one day, or plus two days when the bound proves exclusive. The chosen rule
is deterministic, uses the SDK's injected clock in tests, and is recorded in
the API document once verified. Whether the server filters by UTC or by its
local day is unknown; the wiki says so.

### ListTransactions

| Field | Type | Rule |
|-|-|-|
| `StartDate` | string | (r) `YYYY-MM-DD` |
| `EndDate` | string | Optional; see [dates](#dates) |
| `Page` | int | 1-based; nonpositive selects page 1 |
| `Size` | int | Nonpositive sends the default in the API document |
| `All` | bool | Read every page; see [paging](#paging) |

`ListTransactionsOutput` is `core.PagedList[Transaction]`. A `Transaction`
holds:

| Field | Type | Source |
|-|-|-|
| `ID` | int64 | `transactionID` |
| `Type` | string | Derived from `transactionType`: `PURCHASE` (1), `REFUND` (2), `CHARGE` (5); empty for any other code |
| `TypeCode` | int | `transactionType` as sent, so an unknown code stays visible |
| `Direction` | string | `CREDIT` when `creditStatus` is true, `DEBIT` when false |
| `Amount` | float64 | `transactionValue`, VND, never negative |
| `Balance` | float64 | `cumulativeValue`, the wallet balance after this row |
| `SourceReference` | string | `sourceReference`, a payment or bill number |
| `Description` | string | `description`, free text |
| `CreatedAt` | string | `createTimeStamp` as sent, ISO 8601 with offset |

The package exports `TransactionPurchase`, `TransactionRefund`,
`TransactionCharge`, `DirectionCredit`, and `DirectionDebit`. `Type` names
the three published actions in the order deposit, refund, buy resource;
the numeric mapping is inference, so `TypeCode` stays beside it. A code the
SDK does not know decodes with an empty `Type`; no row fails the list.
`userServiceReference` and `invoiceId` were null in every observed row and
are omitted until a capture shows a value, so adding them later breaks no
caller.

### ListPayments

Same Input fields as `ListTransactions`. `ListPaymentsOutput` is
`core.PagedList[Payment]`. A `Payment` holds `ID` (`paymentID`),
`TransactionID` (`transactionID`, a string), `Gateway` (`payGate`),
`Status` (`paymentStatus`, a string as sent), `Amount` (`paymentAmount`),
`TypeCode` (`paymentType`), `Description`, and `CreatedAt`. The package
exports `GatewayBalance` for the one observed gateway, `use_balance`.
The published Momo and ZaloPay gateways and the six published payment types
have no observed wire values, so the SDK adds no constants or derived
`Type` for them; the raw `Gateway`, `Status`, and `TypeCode` stay visible.
The ten fields that were null in every observed row are omitted until a
capture shows them.

### Paging

The API pages with 1-based `pageNumber`, `pageSize`, and a `pageable`
block holding `totalPages`, `totalElements`, `size`, and `number`. The
public `Page` is the API's `pageNumber`; the SDK returns `number + 1` as
`Page` only if the free read shows `number` is 0-based, otherwise it
returns `number`. The API document records which.

`All` makes one operation read every page. It is a bounded walk inside the
SDK, not a CLI loop, because `GetSpend` needs the same walk and a loop in
the CLI would duplicate it:

1. Request page 1 with the caller's `Size`, or the default.
2. Continue while `Page < TotalPage`, at most `maxHistoryPages` (100)
   pages. Every page uses the same dates and size.
3. Drop a row whose `ID` an earlier page already returned; a row that lands
   during the walk shifts later pages.
4. Return `Items` in API order with `Page` 1, `PageSize` set to the row
   count, `TotalPage` 1, and `TotalItem` from the last page.

The walk fails closed: when the bound stops it before the last page, or the
final row count differs from the last page's `totalElements`, it returns no
Output and an error wrapping `billing.ErrHistoryIncomplete`, so a caller
never treats a partial wallet history as complete. A rerun usually
succeeds; a wallet with more rows than the bound needs a narrower range. An
`*APIError` on any page returns that error unchanged. Page metadata that
cannot be trusted, such as a negative `totalPages`, is an `*APIError` with
code `InvalidResponse` and the body withheld, as `ListCostResources` does
at `billing/cost.go:191`.

### GetSpend

`GetSpend(ctx, *GetSpendInput)` takes `StartDate` (r) and `EndDate`, walks
every transaction page as `All` does, sorts rows by `CreatedAt` then `ID`,
and returns:

| Field | Type | Meaning |
|-|-|-|
| `StartDate`, `EndDate` | string | The range as sent, `YYYY-MM-DD` |
| `Purchases` | float64 | Sum of `Amount` where `Type` is `PURCHASE` |
| `Refunds` | float64 | Sum where `Type` is `REFUND` |
| `Charges` | float64 | Sum where `Type` is `CHARGE` |
| `Other` | float64 | Signed sum of rows with an empty `Type`: credits add, debits subtract |
| `NetSpend` | float64 | `Charges - Refunds` |
| `OpeningBalance` | float64 | The earliest row's `Balance` reversed by its own `Amount` and `Direction` |
| `ClosingBalance` | float64 | The latest row's `Balance` |
| `Reconciled` | bool | True when every consecutive pair of rows satisfies `Balance = previous Balance ± Amount` within 0.5 VND |
| `Transactions` | int | Row count |
| `ByType` | []SpendGroup | One `{Type, TypeCode, Direction, Count, Amount}` per distinct `TypeCode`, API codes ascending |

`Other` and `Reconciled` exist so an unknown code or a gap in the wallet
ledger shows up instead of silently distorting `NetSpend`. A range with no
rows returns zero sums, zero balances, `Reconciled` true, and no error; the
CLI prints it, and the wiki says that a zero closing balance with zero rows
means "no rows", not "empty wallet". Grouping is by what the API states,
`TypeCode`; grouping by product or resource type waits for
[description evidence](#descriptions).

`GetSpend` belongs in the SDK rather than only in the CLI. The CLI calls one
SDK operation per command, as [CLI](cli.md#commands) sets, and
`storage.GetProjectAutoRenew` and pricing discovery already place joined
reads in the SDK. An SDK method also serves the owner's agents that use the
Go module directly. The alternative, a CLI-only command that loops over
`ListTransactions`, would need the same bounded walk in a second place and
a CLI-only command precedent the layout does not have.

Amounts are VND as the API reports them. [Published updates][vat] state
that wallet credits and deductions are pre-VAT from 1 March 2026 and
VAT-included before; the SDK does not convert either way, and the wiki
states it.

[vat]: https://docs.greennode.ai/overview/product-updates-all/2026

### Descriptions

`Description` is free text that carries resource names, IDs, products, and
periods. The SDK returns it as sent, and it is account data under
[live data](../../instructions/live-data.md): fixtures replace names and IDs
with placeholders, and no error message echoes it. The observed leading
tags (`[CREATE]`, `[RENEW]`, `[RESIZE]`, `[RECOVER]`, `[REFUND]`) and the
`Resource <product> - <type> ...` fragment are not captured verbatim, so
this release derives nothing from them. A later slice may add an `Action`
field and product grouping once a sanitized fixture pins the exact formats;
until then, `--query "Items[?contains(Description, 'vserver')]"` filters
rows without the SDK guessing.

### Errors

| Case | Result |
|-|-|
| Missing `StartDate`, bad date, `StartDate` after `EndDate` | `ErrInvalidInput`, no request, exit 2 |
| 401 after the transport's one retry | `ErrAuth`, exit 3 |
| 403 (service account, or a policy that denies billing) | `ErrPermission`, code `Forbidden`, exit 1 |
| Envelope `code` outside 200 to 299 on an HTTP 2xx | `*APIError` with that code, exit 1 |
| Bound hit or row count mismatch under `All` or in `GetSpend` | `ErrHistoryIncomplete`, no Output, CLI code `HistoryIncomplete`, exit 1 |
| Untrusted page metadata, body over 8 MiB, undecodable body | `*APIError`, code `InvalidResponse` or the transport's error, exit 1 |

`ErrHistoryIncomplete` is a new sentinel in `billing`, re-exported from
`vngcloud.go` like `ErrPriceAboveMax`. The CLI code `HistoryIncomplete`
joins the class list in [CLI errors](cli.md#errors-and-exit-codes) in the
slice that implements it. Each other documented status is checked with a
fixture per the [API document](billing-history-api.md#error-statuses).

## CLI

| Command | Kind | Notes |
|-|-|-|
| `billing list-transactions` | Read | `--start-date` (r), `--end-date`, `--page`, `--size`, `--all` |
| `billing list-payments` | Read | Same flags |
| `billing get-spend` | Read | `--start-date` (r), `--end-date`; `--query` root is the Output itself |

Flags derive from the Input fields, so `StartDate` becomes `--start-date`
as on the cost commands, and `All` becomes `--all`. The owner's example
`--since` is a rename choice; see [decisions](#owner-decisions). Every
command runs under a read-only profile. The `billing` service description
becomes "Budgets, cost, balances, and history".

Output follows [list output shape](cli-usability.md#list-output-shape):
JSON has `Items` plus page metadata; `table` and `text` show the rows. Table
views, the pattern `list-resources` uses in `internal/cli/svc_billing.go`,
pick the columns:

- `list-transactions`: `CreatedAt`, `Type`, `Direction`, `Amount`,
  `Balance`, `SourceReference`, `Description`.
- `list-payments`: `CreatedAt`, `Gateway`, `Status`, `TypeCode`, `Amount`,
  `Description`.
- `get-spend`: one row of the sums and balances, then `ByType` rows under
  `table` only when `--query` is empty, as table views already do.

Help text for each command states the `YYYY-MM-DD` date format, that
`--end-date` defaults to today, that amounts are VND as reported, the
`--query` root, and that `--all` fails rather than print a partial history.
`gen-docs` writes them to `docs/wiki/CLI-Billing.md`, and the
`Billing-and-Pricing.md` page gains a "Wallet history" section with the
spend example, the pre-VAT note, and the unverified service-account access.

## Ranked improvements

Each item is one slice. The first three do not depend on the cost explorer
fix; items under [after the cost explorer fix](#after-the-cost-explorer-fix)
wait for it.

1. Bound every `billing` read with `MaxBody` 8 MiB. `billing/budgets.go`,
   `billing/cost.go`, and `billing/resources.go` set none, while
   `pricing/pricing.go:144` sets 1 MiB; a hostile host can send an
   unbounded body to a read. Pure hardening, no surface change.
2. State the `--query` root in every billing command's help and wiki
   entry: `Balances`, `Budget`, `PeriodCost`, `Threshold`, and `Items` for
   lists. Today only the generated example line shows it. This is the
   billing part of the open TODO item on output roots.
3. Add table views for `list-budgets` (name, type, period, limit, actual
   cost, percentage, status) and `get-balances` (one row, with `null`
   printed as `none` so an absent balance is not read as zero). Both tables
   print every struct field today; the budget table has 22 columns.
4. List the allowed values in help for `create-budget --period-type`,
   `--type`, and `--status`, and for `create-budget-threshold
   --threshold-type`, from the exported constants. The server rejects a
   wrong value with a 400 after a round trip.
5. Render `list-resources` epoch-millisecond timestamps as ISO 8601 in the
   table view only; JSON keeps the raw numbers. The current note tells the
   user to convert them by hand.
6. Add `ListBudgetsInput.Status` validation (`ACTIVE` or `PAUSED`) before
   the request, consistent with the date and UUID checks, so a typo exits 2
   without a call.

### After the cost explorer fix

- Give `get-cost-overview` and `list-cost-resources` the same optional
  `EndDate` rule as history, so "this month so far" needs one date.
- Add `All` to `ListCostResources` with the same bounded walk and
  `ErrHistoryIncomplete`, replacing the manual page loop the wiki shows.
- Add table views for both cost commands once their decoded shape is
  settled.

## Security

Every operation is a read with no body, no path parameter, and no new
credential path. The five threat models traced through the proposed paths,
with existing code where it already exists:

- Leaked tokens. Requests reuse `do` in `billing/billing.go:137-140`, which
  collects sent credentials and redacts them from envelope errors; decode
  errors pass `transport.SanitizeDecodeError`. The transport logs method,
  path, status, and duration, never the query string or body
  ([logging](sdk-and-cli.md#logging)), so dates never reach logs either.
  Descriptions reach stdout because the user asked for them; they never
  enter an error message. Severity if broken: high. Check: a fixture whose
  description equals the bearer token must not appear in any error.
- Hostile responses. New: `MaxBody` 8 MiB on each request; `totalPages`,
  `totalElements`, and `number` validated as non-negative and `totalPages`
  bounded by `maxHistoryPages` before any loop; unknown codes decode to an
  empty `Type`; `ErrHistoryIncomplete` instead of partial sums. Existing:
  envelope code mapping in `billing/billing.go:96-121`. Severity: medium
  (memory, wrong totals). Checks: oversized body, negative and huge
  `totalPages`, shifting pages, mismatched `totalElements`.
- Foreign host. The `Billing` endpoint is the dashboard host, and the shared
  transport keeps TLS verification on and refuses cross-host redirects
  ([security](../../instructions/security.md#secure-defaults)); an endpoint
  override moves the whole package, never one operation. No new path.
- Tampered local files. No new file, cache, or profile key. The token cache
  and credential file rules in [SDK and CLI](sdk-and-cli.md#token-cache) are
  unchanged.
- Repeated or concurrent callers. No write exists. `All` and `GetSpend`
  send at most 100 GETs per call; the transport's read retry policy applies
  per page and no page is retried by the walk itself. A charge landing
  mid-walk is detected by the `totalElements` comparison and ID
  deduplication rather than double counted. Severity: low.

These are design obligations for the independent review, not a verdict.

## Testing

Sanitized raw [fixtures](billing-history-api.md#fixtures) decoded through
the SDK, request and walk tests, `GetSpend` tests, CLI golden tests, and
the `make live` reads are listed in the
[API document](billing-history-api.md#deterministic-checks). Every unit
test uses an `httptest` server or a fixture and never calls the real API.

## Release order

1. Hardening and help: improvements 1 and 2, no public surface change.
2. `ListTransactions` and `ListPayments` with `All`, fixtures, wiki
   sections, and release notes, after the verification call clears the
   gates.
3. `GetSpend` and its CLI command, once step 2 has a live-confirmed
   fixture, because its sums depend on the type mapping.
4. Improvements 3 to 6 in any order, then the cost explorer follow-ups
   after that fix lands.

## Owner decisions

The manager holds delegated authority for these; each names options and a
recommendation.

1. Flag names: derived `--start-date` and `--end-date`, or `Since` and
   `Until` Input fields giving `--since` and `--until` on `get-spend`.
   Recommend the derived names, so every billing date flag matches and no
   rename table entry is needed; help text says "since".
2. `EndDate` optional with the "through today" rule, or required as on the
   cost commands. Recommend optional: the owner's question is always "since
   a date until now", and the rule is deterministic once the date gate
   resolves.
3. `GetSpend` in the SDK or CLI only. Recommend SDK, for the reasons in
   [GetSpend](#getspend).
4. Fail closed on an incomplete walk with no Output, or return partial rows
   plus the error as `monitor.GetQuotaStatus` does. Recommend fail closed:
   a partial ledger yields wrong balances, and a narrower range always
   works.
5. Derive `Type` names from the published actions (`PURCHASE`, `REFUND`,
   `CHARGE`) or expose only `TypeCode`. Recommend both, as specified; the
   wiki marks the numeric mapping as inferred from descriptions until the
   fixture capture confirms all three codes.
6. Default `Size` and the cap, after the verification call reports what the
   API accepts. Recommend the largest size that returns 200 with a matching
   `pageable.size`, capped there as `ListCostResources` caps at 200.
7. Whether `make live` adds the two reads. Recommend yes; both are free and
   log only counts.

## Evidence gates

Implementation of step 2 waits for the
[free verification call](billing-history-api.md#free-verification-call) to
settle: IAM-user access through the SDK's token, page size default and cap,
`pageNumber` base and the `number` base in `pageable`, `dateEnd` omission
and bound inclusivity, and the shape of an empty page. Service-account
access, Momo and ZaloPay gateway values, payment type codes, and the ten
null payment fields stay open and are listed in
[open evidence](billing-history-api.md#open-evidence). No gate here
authorizes a purchase, a write, or cleanup.
