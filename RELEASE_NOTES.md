# Release Notes

## v0.94.0 - Billing history

- Add SDK `billing.ListTransactions` and `billing.ListPayments`, plus
  `billing list-transactions` and `billing list-payments`. Dates are inclusive
  in fixed UTC+07:00; an omitted end means today. Both reads support bounded
  complete walks and read-only profiles. Existing billing commands and
  JSON/query roots retain their contracts.
- Incomplete history walks return `ErrHistoryIncomplete` with nil Output;
  the CLI reports `HistoryIncomplete`, exit 1, with no report. Complete
  walks detect coverage changes but do not establish an atomic snapshot.
- JSON, text, and explicit queries can contain account data. Default tables
  omit descriptions and references. Values preserve the API's monetary
  basis; historical VAT normalization and service-account access remain
  unverified. Register both reads in the example and live suite.

## v0.93.3 - Bounded billing reads

- The eight billing reads (`get-cost-overview`, `list-cost-resources`,
  `get-balances`, `list-budgets`, `get-budget`, `get-current-period-cost`,
  `list-budget-thresholds`, and `list-budget-alerts`) now refuse response
  bodies over 8 MiB. An oversized 2xx response returns `InvalidResponse`
  with its HTTP status and no body text; an oversized 4xx or 5xx response
  keeps its status-based error but fails without retry or token refresh.
- Responses within the limit, budget writes, and output shapes are
  unchanged.

## v0.93.2 - Cost explorer overview fix

- `billing get-cost-overview` (SDK `GetCostOverview`) showed every
  breakdown cost as 0 and empty series, because the live API sends
  `value` and `percent` in each breakdown row and groups series by key
  with `x`/`y` points. `Summary.Breakdown[].Cost` now reads `value` and
  gains `Percent`; the new `GroupedSeries` holds one series per group with
  its points. Responses in the old flat shape still fill `Series`.
- The API refuses `--interval hourly` and `--group-by resourceId` with HTTP
  400; the wiki notes this.

## v0.93.1 - Quote response safety

- `pricing get-quote` (SDK `pricing.GetQuote`) and every command that
  quotes before a paid write now refuse a quote response that echoes a
  credential the call sent, raw or JSON-escaped and including a refreshed
  token, before returning a price; the error text never contains it.
  Routine quote responses are no longer passed to `WithResponseCapture`.
- If a credential the call sent could be read as a number, quotes fail
  closed with `InvalidResponse`. Real bearer tokens are long opaque strings,
  so normal use is unaffected.
- A malformed quote response still yields a `*json.SyntaxError` with its
  offset through `errors.As`, without the response text.

## v0.93.0 - Server public address

- Server reads (`compute get-server`, `list-servers`,
  `network list-servers-by-security-group`, and every write that returns
  a server) now show `PublicIP`: the server's floating IP, taken from its
  internal interfaces, so `--query Server.PublicIP --output text` answers
  "what is this server's public address" in one command. It is empty when
  the server has no valid public floating address; an empty value does not
  prove that no IP is allocated.
- Compatibility: `compute.Server` gains the `PublicIP` field, which breaks
  unkeyed `compute.Server{...}` literals, and CLI JSON for servers gains a
  `PublicIP` key. The SDK's own JSON encoding of `Server` is unchanged
  (`json:"-"`).
- Server responses are bounded at 8 MiB, and server reads and writes
  refuse a response that echoes a credential the operation sent, without
  returning the response. Redacted errors keep their identity for
  `errors.Is`, and expose `*APIError` through `errors.As` only when it
  holds no credential.
- The response scanner shared with NAT reads now fails closed on JSON it
  cannot scan completely.

## v0.92.0 - Resize quote credits

- `compute quote-resize-server` (SDK `QuoteResizeServer`) and
  `pricing get-quote --action resize` now return a server downsize quote.
  GreenNode prices a downsize as a negative prorated credit, which the SDK
  used to refuse as an invalid price. `OptimumPrice` and `MonthlyPrice` are
  signed: a negative value is a credit, and JSON, table, and text output
  show the minus sign. Create quotes still refuse a negative price, and a
  missing, null, NaN, or infinite price is still refused for every action.
- `compute resize-server` (SDK `ResizeServer`) now lets a quoted credit
  pass at every valid `--max-price`, including the default 0; a quote of
  exactly 0 still fails as unpriced. `--max-price` bounds the quote, not
  the final charge: the resize request carries no price, so if the server
  or prices change between the quote and the send, a quoted credit can
  become a charge.
- `volume resize-volume` still refuses a quote at or below 0 and now names
  the amount.
- A resize that fails before its request is sent, for example while
  getting a token, now returns no Output and the original error instead of
  reporting an accepted but unsettled write.
- Quote responses are bounded at 1 MiB.
- Bool flag help shows `[=true|false]` and invalid flag values give
  shorter errors, from pflag v1.1.0; usage errors still exit 2.

## v0.91.2 - Clearer IAM permission confirmations

- Policy attach and detach for service accounts, users, and groups, and
  adding or removing a user to or from a group, now say "grants
  permissions" or "removes permissions" when `--yes` is missing, instead
  of "is destructive". `--yes` stays required on the same commands, still
  with exit code 2 and no request sent.
- Help for `iam get-service-account` and `iam list-service-accounts` names
  `iam list-service-account-policies`; help for `iam get-user` and
  `iam list-users` names `iam list-user-policies` and
  `iam list-user-groups`.
- The global `--yes` help reads "confirm a destructive or
  permission-changing operation".

## v0.91.1 - Flavor prices with GPU catalogs

- `compute list-flavor-prices` (SDK `ListFlavorPrices`) works again.
  GreenNode now lists GPU flavors in the `gpu` family, which discovery
  rejected as an invalid response, so every call failed with
  `InvalidResponse` from `compute.ListFamilyFlavors`. GPU family scopes are
  now skipped with an `excluded-family` note; GPU flavors are neither listed
  nor priced, and malformed rows in other families still fail.

## v0.91.0 - vMonitor ensure-synthetic-quota

- SDK `EnsureSyntheticQuota` and CLI `monitor ensure-synthetic-quota` keep
  the free Synthetic Test quota alive from one command, for cron and CI.
  From one fresh read it renews when the quota is ACTIVE inside
  `--max-days-left` (CLI default 7), recovers when it is expired in trash,
  skips when it is not due, and refuses anything else. It never creates or
  deletes and never falls back from one action to another. It reuses the
  renewal and recovery guards (`--max-price` default 0, optional
  `--expected-quota-id`), sends once, and confirms by read-back.
- The output adds `Action` to the shared `Sent`, `Stage`, and `Error`
  outcome. An identity that may renew but not recover gets a clear
  `Forbidden` refusal with `Action` `recover` and exit 1.
- `quota-status` now suggests `monitor renew-synthetic-quota` or
  `monitor recover-synthetic-quota` when one applies. Tables show time left
  in days, hours, and minutes, and leave the error column blank on healthy
  rows; JSON fields are unchanged.
- When a call reconciles an earlier unresolved write on the same client,
  its error now names the operation that was called.
- The wiki moves Synthetic create, recovery, renewal, ensure, delete, and
  the Unattended CI guidance to a new Monitor Synthetic Quota page.

## v0.90.0 - vMonitor Synthetic quota create and delete

- SDK `CreateSyntheticQuota` and `QuoteCreateSyntheticQuota`, and CLI
  `monitor create-synthetic-quota` and `monitor quote-create-synthetic-quota`,
  order a free Synthetic Test quota when none exists. They require an
  explicit class, package, and quantity (API tests), refuse paid classes,
  price the order as unit price times quantity against `--max-price`
  (default 0), never bundle paid notification packages, send once, and
  confirm by read-back of an ACTIVE quota with the chosen package.
- SDK `DeleteSyntheticQuota` and CLI `monitor delete-synthetic-quota` now
  delete the quota instead of refusing. The command is destructive: it
  needs `--yes` and `--expected-quota-id`, re-reads the quota first, sends
  once, and settles when the quota read returns not found. Deleting the
  Synthetic Test quota permanently deletes every synthetic check; help and
  every output format say so. Purge stays unsupported.
- `list-synthetic-packages` adds each class's unit packages (from the
  class packages read), with unit price, units, and tier; this is the
  package ID that create, renewal, and recovery use.
- All three writes share the `Sent`, `Stage`, and `Error` outcome model and
  exit table from v0.89.0.
- Compatibility: `DeleteSyntheticQuotaInput.ExpectedQuotaID` is now
  required, and a call that refused before can now delete. New fields on
  `SyntheticQuotaClass` break unkeyed literals. The create response body is
  not verified live; success comes only from the read-back.

Older releases are in [docs/release-notes-archive-v0.89.md](docs/release-notes-archive-v0.89.md).
