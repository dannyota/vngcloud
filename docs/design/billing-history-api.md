# Billing history: API evidence and checks

Evidence for [billing history](billing-history.md); the design owns the
rules. Each statement is marked as a published contract, console evidence
(sanitized raw responses captured from the GreenNode console signed in as
the IAM user), inference, or missing. Nothing below is live-verified
through the SDK yet.

## Sources

| Source | Kind | Establishes |
|-|-|-|
| [Credit history page guide][credit-doc] | Published | Records every credit wallet balance change; filters by day and by description keyword; three actions: Deposit balance, Refund balance, Buy resource; columns Date, Action, Description, Amount, Balance (balance after the action); PDF export |
| [Payment history page guide][payment-doc] | Published | Columns Transaction ID, Payment type, Payment gateway, Payment date, Details, Amount (VND); gateways Momo, ZaloPay, credit balance; payment types Payment service, Payment bill, Buy Bandwidth, Deposit, Deposit Trial, Advanced; filters by day, gateway, and keyword |
| [vConsole guide][vconsole-doc] | Published | Page URLs `/credit-history` and `/payment-history` on `dashboard.console.greennode.ai` |
| [Payment overview][payment-overview] | Published | Payment history records the amount at payment time for several resources; a prepaid payment appears there at once |
| [Product updates 2026][vat] | Published | From 1 March 2026 wallet credits are displayed and deducted pre-VAT; top-up credits equal the amount divided by 110 percent; before that date amounts included VAT |
| Console capture, credit history | Console evidence | Request shape, envelope, paging block, and row fields below |
| Console capture, payment history | Console evidence | Same for payments |
| [Billing design](billing.md#shared-rules) | Existing contract | Envelope, `Billing` endpoint, money and date rules, IAM User 403 on `/v1/credits` |

[credit-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole/lich-su-tin-dung
[payment-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole/lich-su-thanh-toan
[vconsole-doc]: https://docs.greennode.ai/vn/quan-ly-hoa-don-chi-phi-and-tai-nguyen-tren-vng-cloud/vconsole-kenh-quan-ly-chung-ve-hoa-don-va-tai-nguyen-tren-vng-cloud/trai-nghiem-vconsole
[payment-overview]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment
[vat]: https://docs.greennode.ai/overview/product-updates-all/2026

The published guides are in Vietnamese; the English payment overview does
not describe the credit history page. No published document names the
gateway paths, the field names, or the numeric codes.

## Requests

Both reads are `GET` on the `Billing` host, the same host the budgets API
uses, with the bearer token in `Authorization`. A request without the
header returned 401 in the console. No body, no project ID, no region.

| Operation | Path and query (console evidence) |
|-|-|
| `ListTransactions` | `gateway/api/v1/transactions?pageNumber=1&pageSize=10&dateStart=01/09/2026&dateEnd=10/11/2026` |
| `ListPayments` | `gateway/api/v1/payments?pageNumber=1&pageSize=10&dateStart=01/09/2026&dateEnd=10/11/2026` |

- `pageNumber` is 1-based (console evidence: the first page is 1).
- `dateStart` and `dateEnd` are `DD/MM/YYYY`. The SDK converts from the
  public `YYYY-MM-DD` after `core.CheckDate`.
- The console filters by description keyword and, for payments, by gateway.
  Their query parameters were not captured; the SDK sends neither. Missing.
- Whether `dateStart` or `dateEnd` may be omitted, whether the bounds are
  inclusive, which day the server uses, the default and maximum `pageSize`,
  and the response to a `pageNumber` past the last page are missing; see
  [open evidence](#open-evidence).

## Response envelope and paging

Console evidence, both endpoints:

```json
{"code":200,"message":"Success","data":{
  "content":[...],
  "pageable":{"totalPages":3,"totalElements":25,"size":10,"number":1}}}
```

`code` and `message` follow the
[billing envelope](billing.md#response-envelope). `content` holds the rows.
`pageable.number` was 1 on the first page in the
capture, which suggests it echoes the 1-based `pageNumber`; the SDK confirms
the base with the verification call before returning `Page`. The example
counts above are the shape only; the account's real counts stay private.

## Transaction row

Console evidence, over one hundred rows across one month. Inference
is marked.

| Wire field | Type | Evidence | SDK field |
|-|-|-|-|
| `transactionID` | int | Transaction number | `ID` |
| `transactionType` | int | 1 on rows described as buying credits, 2 on rows starting `[REFUND]`, 5 on rows starting `[CREATE]`, `[RENEW]`, `[RESIZE]`, or `[RECOVER]`. Mapping 1 = Deposit balance, 2 = Refund balance, 5 = Buy resource is inference from those descriptions and the three published actions | `TypeCode`, `Type` |
| `transactionValue` | float | VND, never negative in the capture | `Amount` |
| `cumulativeValue` | float | Wallet balance after the row; the capture reconciled across the month | `Balance` |
| `creditStatus` | bool | True on type 1 and 2 rows, false on type 5 rows | `Direction` |
| `sourceReference` | string | A payment or bill number as a string | `SourceReference` |
| `description` | string | Free text with resource product, type, name, ID, and period | `Description` |
| `createTimeStamp` | string | ISO 8601 with offset, such as `2026-01-15T07:00:00.000+00:00` | `CreatedAt` |
| `userServiceReference` | null | Null in every row; type unknown | Omitted |
| `invoiceId` | null | Null in every row; type unknown | Omitted |

Codes 3 and 4 never appeared. The SDK decodes any other code with an empty
`Type`; `GetSpend` reports such rows under `Other` and in `ByType`.

## Payment row

Console evidence, several dozen rows in the same range.

| Wire field | Type | Evidence | SDK field |
|-|-|-|-|
| `paymentID` | int | Payment number | `ID` |
| `transactionID` | string | Identifier text; sensitive | `TransactionID` |
| `payGate` | string | `use_balance` on every sampled row | `Gateway` |
| `paymentStatus` | string | `"1"` on every sampled row; meaning unverified | `Status` |
| `paymentAmount` | float | VND | `Amount` |
| `paymentType` | int | `8084` on the sampled rows; the published payment type names have no observed code | `TypeCode` |
| `description` | string | Free text | `Description` |
| `createTimeStamp` | string | Timestamp | `CreatedAt` |
| `payGateTransactionID`, `payGateName`, `bankCode`, `bankName`, `merchantCode`, `actualAmount`, `paymentRefID`, `paymentOptions`, `executorID`, `executorName` | null | Null in the sample | Omitted |

A payment paid from the wallet also appears as a type 5 transaction; the
capture did not join the two by reference, so the SDK does not claim that
`Payment.TransactionID` equals a `Transaction.SourceReference`. Missing.

## Error statuses

| Status | Evidence | SDK result |
|-|-|-|
| 401 without a token | Console evidence (browser fetch) | Transport refreshes once; then `ErrAuth`, code `Unauthorized` |
| 403 | Missing for these paths; `/v1/credits` returned 403 to the IAM User | `ErrPermission`, code `Forbidden`, exit 1 |
| 400 on a bad date or size | Missing | `*APIError` with the server's code, exit 1 |
| 2xx with envelope `code` outside 200 to 299 | Not observed; existing billing rule | `*APIError` with that code |
| 429, 5xx | Not observed | Transport read retry policy, then `*APIError` |

The verification call records each status it meets; fixtures pin them.
A date the server rejects is expected to be a 400, but the SDK checks the
public format first, so a malformed date never reaches the server.

## Free verification call

Read-only, free, manager-run, before implementation of the list reads.
The SDK has no public raw request, so the manager runs a throwaway program
under a git-ignored path inside the module that uses the shared transport
with the IAM user profile, as [live data](../../instructions/live-data.md)
allows for throwaway live programs, and deletes it afterwards. Raw bodies go
under `examples/basic/output/raw/billing/`; nothing from them enters chat.
Each request below is one `GET`; record status, envelope code, `pageable`,
and row count only.

| Step | Request | Settles |
|-|-|-|
| 1 | `transactions?pageNumber=1&pageSize=10&dateStart=<30 days ago>&dateEnd=<tomorrow>` | IAM-user access with the SDK's token; baseline rows |
| 2 | Same with `pageSize=100`, then `pageSize=500` | Default and cap; `pageable.size` echo or a 400 |
| 3 | Same as 1 without `dateEnd`, then without both dates | Whether dates may be omitted |
| 4 | `dateStart` and `dateEnd` both set to the date of a known row | Inclusive bounds and the server's day |
| 5 | `pageNumber=0`, then `pageNumber` past the last page | 1-based confirmation and the empty-page shape |
| 6 | `payments` for steps 1 and 3 | Same facts for payments; any other `payGate` or `paymentType` value |
| 7 | Steps 1 and 6 with the service-account profile | Service-account access or 403 |

Step 4 also decides the "through today" rule in
[dates](billing-history.md#dates). The results are recorded here, replacing
the missing marks, and in the implementation plan.

## Fixtures

Sanitized raw responses in `testdata/billing/`, captured by the steps
above and decoded through the SDK in tests:

| Fixture | From |
|-|-|
| `ListTransactions.json` | Step 1, first page, at least one row of each known type |
| `ListTransactionsPage2.json` | Step 1 with `pageNumber=2`, for the walk test |
| `ListTransactionsEmpty.json` | Step 5, the past-the-end page |
| `ListPayments.json` | Step 6, first page |

Sanitize every value under the
[live data rules](../../instructions/live-data.md#sanitizing-fixtures):
`transactionID`, `paymentID`, `sourceReference`, and `transactionID` strings
become `<id>` or short synthetic numbers; descriptions keep their leading
tag and structure but replace names and IDs with `<name>` and `<id>`;
amounts become round synthetic values that still reconcile, so the
`GetSpend` balance test holds; timestamps keep their format with synthetic
dates. Keep every null field so a later type change is visible.

After the owner approves the read-only run, `make live` gains
`ListTransactions` and `ListPayments` for the last 30 days with `Size` 10,
logging only row counts.

## Deterministic checks

Each check is a unit test with an `httptest` server or a fixture; none
calls the real API.

| Check | Expect |
|-|-|
| Query for `StartDate` `2026-10-01`, `EndDate` `2026-11-09`, `Page` 2, `Size` 50 | `pageNumber=2&pageSize=50&dateStart=01/10/2026&dateEnd=09/11/2026` |
| Empty `EndDate` | The verified rule: no `dateEnd`, or the clock-derived date |
| `StartDate` after `EndDate`, bad format, empty `StartDate` | `ErrInvalidInput`, zero requests |
| `Size` 0 and `Size` above the cap | The default and the cap on the wire |
| Fixture decode, transactions | `Type`, `Direction`, `Amount`, `Balance`, `CreatedAt` per row; code 7 gives empty `Type` and `TypeCode` 7 |
| Fixture decode, payments | `Gateway` `use_balance`, `Status` `"1"`, `TypeCode` 8084 |
| Body of 8 MiB plus one byte | `*APIError` wrapping `transport.ErrBodyTooLarge`, no Output |
| `totalPages` negative, or above `maxHistoryPages` with `All` | `InvalidResponse` or `ErrHistoryIncomplete`, no Output |
| `All` over two pages where page 2 repeats one ID from page 1 | One copy; `TotalItem` from page 2; `Page` 1, `TotalPage` 1 |
| `All` where the merged count differs from `totalElements` | `ErrHistoryIncomplete`, no Output |
| `All` where page 2 returns 403 | `ErrPermission` through `errors.Is`, `*APIError` through `errors.As`, one request for page 2 |
| Context cancelled after page 1 | `context.Canceled` in the chain, no page 3 request |
| `GetSpend` over shuffled rows | Sums, `NetSpend`, `OpeningBalance`, `ClosingBalance`, `Reconciled` true |
| `GetSpend` with one row's `Balance` off by 1 VND | `Reconciled` false, sums unchanged |
| `GetSpend` with an unknown code | `Other` signed by `Direction`, `ByType` row with empty `Type` |
| `GetSpend` over an empty range | Zero values, `Reconciled` true, nil error |
| Error envelope whose message holds the bearer token | Message and code redacted; `ErrHistoryIncomplete` not involved |
| CLI `list-transactions --all` with a mismatched count | stderr `{"error":{"code":"HistoryIncomplete",...}}`, exit 1, empty stdout |
| CLI `get-spend --output table` | One summary row then `ByType` rows; `--query NetSpend --output text` prints one number |
| CLI read-only profile | All three commands run |

## Open evidence

- IAM-user access through the SDK token: console evidence only; step 1.
- Service-account access: missing; step 7.
- `pageSize` default and cap: missing; step 2.
- `pageable.number` base and the empty-page shape: step 5.
- Omitted dates and bound inclusivity: missing; steps 3 and 4.
- Transaction codes 3 and 4, and the exact description formats: missing;
  no derived `Action` or product grouping until a fixture shows them.
- Payment gateway values for Momo and ZaloPay, payment type codes for the
  six published names, `paymentStatus` values, and the ten null payment
  fields: missing; the SDK keeps raw values and omits the null fields.
- The join between a payment and its wallet transaction: missing.
- Whether `transactionValue` can be negative or `cumulativeValue` can be
  null: not observed; `GetSpend` treats a negative amount as a reconciling
  input and reports `Reconciled` false when the ledger does not add up.
