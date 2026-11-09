# Billing history fixture provenance

The `ListTransactions*.json` and `ListPayments*.json` files are sanitized raw
response bodies of bodyless `GET gateway/api/v1/transactions` and
`GET gateway/api/v1/payments` calls made with an IAM user token. Each file is
one HTTP 200 response.

|Files|Response|
|-|-|
|`ListTransactions.json`, `ListTransactionsPage2.json`|Consecutive pages 1 and 2 of one range, size 10; together they contain transaction types 1, 2, and 5|
|`ListPayments.json`, `ListPaymentsPage2.json`|Consecutive pages 1 and 2 of one range, size 10|
|`*BeyondEnd.json`|A page past the last one: empty `content`, nonzero totals, the requested page echoed|
|`*Empty.json`|A range with no rows: `totalPages` and `totalElements` 0|
|`*InvalidPageSize.json`|Page size 100: envelope `code` 400 with a `{type,field,errorMsg}` array|

All data is synthetic except the shape. Sanitizing kept every key, key order,
JSON type, null, envelope and page metadata value, enum value
(`transactionType`, `creditStatus`, `payGate`, `paymentStatus`,
`paymentType`), and validation message. It replaced identifiers and references
with order-preserving synthetic values of the same JSON type, descriptions,
executor names, and `paymentOptions` with synthetic text, and money with
synthetic amounts. Transaction balances are re-derived so that, newest first
across both pages, each row's `cumulativeValue` equals the next older row's
balance plus `transactionValue` for a credit or minus it for a debit, as in
the raw pages. Timestamps moved by a whole number of weeks, keeping time of
day, milliseconds, offset syntax, and order. The evidence these responses
support is in [billing history API](../../docs/design/billing-history-api.md).
