# Billing and Pricing Design

Status: Approved (2026-09-25).

This design adds budgets, cost reads, balances, and price quotes to the SDK
and CLI. Budgets and price quotes ship before any paid write, so a user can
cap spend and price a resource before creating it. Budget writes are the
first write APIs; the conventions they set for every later write API are in
[ADR 0002](../adr/0002-write-api-conventions.md).

It builds on [SDK and CLI](sdk-and-cli.md): package per service,
`Method(ctx, *Input) (*Output, error)`, operation tables, and the error
model.

## Goals

- Create, change, pause, and delete budgets and their alert thresholds.
- Read the current period's cost, the cost explorer overview, and the
  per-resource cost list.
- Read account balances.
- Get a price quote for a resource before creating it.

## Deferred

Each of these gets its own design when needed:

- Payments, invoices (`/v1/billings`), transactions, and credits. Credits
  return 403 to IAM Users.
- Orders. A quote never places an order.
- Price quotes for existing resources: renew, recover, and match-end-time.
  They need resource IDs and belong with the design for each resource's
  renewal.
- Billing resources (`/v1/resources`) and the product list (`/v1/products`).
- The cost explorer `table` block, which repeats the series in a UI layout.
- `GET /v1/budgets/{uuid}/status`. The budget summary in `ListBudgets`
  already holds the same costs.
- Typed quote inputs per resource type (see [Price quotes](#price-quotes)).

## Endpoints

Two gateways serve this design:

| Name | Default URL | Scope |
|-|-|-|
| Billing | `https://dashboard.console.greennode.ai/` | Global, per account |
| Regional billing gateway | `https://{region}.console.greennode.ai/vserver/iam-billing-gateway/` | Per region |

`endpoints.Set` gains a `Billing` field, and `endpoints.Overrides` gains a
matching `Billing` override. `Billing` defaults to the resolved `Dashboard`
URL, so a `Dashboard` override also moves billing, and a `Billing` override
moves only billing. `Billing` is the dashboard host root, not
`/gateway/api/`, because balances live under a different prefix:

| API | Path under `Billing` |
|-|-|
| Budgets and cost | `gateway/api/v1/...`, `gateway/api/v2/...` |
| Balances | `navbar/balances/v1` |

`internal/routes` gains `ProductBilling`. The regional billing gateway is the
URL that `endpoints.Set` already names `Portal`; price quotes use it, and no
new field is added.

Billing calls are per account. They send no project ID and ignore the
configured region. `Config` still requires a region, because the rest of the
SDK needs one.

## Packages

| Package | Covers | Endpoint |
|-|-|-|
| `billing` | Budgets, thresholds, alerts, cost, balances | `Billing` |
| `pricing` | Price quotes | Regional billing gateway |

Price quotes get their own package for three reasons. They use a regional
endpoint while `billing` is global. Every paid create design adds a quote for
its resource, so the quote surface grows with the SDK, while `billing` grows
with the billing console. And a quote is what a user calls before a write,
which reads best as `pricing.GetQuote`, as in the AWS SDK's `pricing`
package.

`budgets` and `costexplorer` stay in one `billing` package. They share one
gateway, one response envelope, and one console area, and together they are
14 operations.

Both packages follow the service package rules in
[SDK and CLI](sdk-and-cli.md#service-packages): `New(cfg vngcloud.Config)
*Client`, operation names `billing.<Method>` and `pricing.<Method>`, and
`vngcloud:"required"` checks before any request.

## Shared rules

### Response envelope

Every dashboard gateway response is `{"code":<n>,"message":"...","data":...}`.
`billing` decodes `data` into the Output. The balances endpoint is enveloped
today; as a defense, when a response has neither a `code` nor a `data` key,
the decoder decodes the whole body. Fixtures cover both forms.

A success is HTTP 200 with a `code` from 200 to 299: reads send 200 and
creates send 201. The SDK never compares the `message`.

`code` is a number or null. The shared error decoder in `internal/transport`
accepts a numeric `code` and stores its decimal form in `APIError.Code`;
it once expected a string and dropped a number. For a null `code`,
`APIError.Code` stays empty until the status-to-code mapping in
[SDK and CLI](sdk-and-cli.md#errors) ships. A 2xx response whose envelope
`code` is outside 200 to 299 is an error: `billing` returns an `*APIError`
with the actual HTTP status, the envelope code, and the message.

### Identifiers

A budget has both a numeric `id` and a `uuid`. Paths use the `uuid`, so
Inputs name it `BudgetUUID`, and thresholds use `ThresholdUUID`. The CLI
flags are `--budget-uuid` and `--threshold-uuid`.

Before a request, the SDK checks each UUID in a path against
`^[A-Za-z0-9-]+$` and returns an error wrapping `ErrInvalidInput` otherwise.
`routes.URL` already escapes `/`, but not `.` or `..`, and a write must never
reach a different path than its caller named.

### Money and dates

- All amounts are VND. Field names carry no currency.
- `LimitAmount` is `int64`, because the API takes an integer below 1e10.
  Lists return it as a decimal such as `9999999999.0`; the decoder accepts
  an integral decimal and rejects a fractional one.
- Costs, prices, and percentages are `float64`. Costs can be fractional, and
  float64 holds every integer VND value up to 2^53 exactly.
- Numbers that the API may send as null decode to `*float64`.
- Date inputs are strings in `YYYY-MM-DD`, the format the API takes. A string
  avoids a time zone choice; the SDK checks the format before the request.

## billing operations

"(r)" marks `vngcloud:"required"`. "L[T]" is `core.List[T]`, and "P[T]" is
`core.PagedList[T]`.

### Budgets

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListBudgets` | `GET /v1/budgets?view=summary` | `Status` | L[Budget] |
| `GetBudget` | `GET /v1/budgets/{uuid}` | `BudgetUUID` (r) | `{Budget Budget}` |
| `CreateBudget` | `POST /v1/budgets` | see below | `{Budget Budget}` |
| `UpdateBudget` | `PUT /v1/budgets/{uuid}` | see below | `{}` |
| `DeleteBudget` | `DELETE /v1/budgets/{uuid}` | `BudgetUUID` (r) | `{}` |
| `GetCurrentPeriodCost` | `GET /v1/budgets/cost/overview` | none | `{PeriodCost PeriodCost}` |

`CreateBudgetInput`:

| Field | Type | Rule |
|-|-|-|
| `Name` | string | (r). The API requires `^[a-zA-Z][a-zA-Z0-9 _.@-]*$` |
| `PeriodType` | string | (r). `MONTHLY` or `QUARTERLY` |
| `Type` | string | (r). `ACTUAL` or `FORECASTED` |
| `LimitAmount` | int64 | (r). VND, 1 to 1e10 - 1 |
| `Status` | string | `ACTIVE` or `PAUSED`; empty sends `ACTIVE` |

`UpdateBudgetInput` has `BudgetUUID` (r) and pointer fields `Name`,
`PeriodType`, `Type`, `LimitAmount`, and `Status`. The SDK sends only the
non-nil fields. Pausing a budget is `UpdateBudget` with `Status` set to
`PAUSED`; there is no separate pause operation.

The package exports the enum values as constants: `PeriodMonthly`,
`PeriodQuarterly`, `TypeActual`, `TypeForecasted`, `StatusActive`, and
`StatusPaused`.

`Budget` keeps its API JSON tags and holds `UUID`, `ID`, `Name`,
`PeriodType`, `Type`, `LimitAmount`, `Status`, `Currency`, `Alarm`, the
period and timestamp strings, the threshold counts, and the nullable cost
and percentage fields. It leaves out user and creator IDs. `PeriodCost`
holds `PeriodKey`, `PeriodStart`, `PeriodEnd`, `ActualCost`, and
`ForecastedCost`. Period keys are `YYYY-MM`, as in `ListBudgetAlerts`.

### Thresholds and alerts

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListBudgetThresholds` | `GET /v1/budgets/{uuid}/thresholds` | `BudgetUUID` (r) | L[Threshold] |
| `CreateBudgetThreshold` | `POST /v1/budgets/{uuid}/thresholds` | see below | `{Threshold Threshold}` |
| `UpdateBudgetThreshold` | `PUT .../thresholds/{thrUuid}` | see below | `{}` |
| `DeleteBudgetThreshold` | `DELETE .../thresholds/{thrUuid}` | `BudgetUUID` (r), `ThresholdUUID` (r) | `{}` |
| `ListBudgetAlerts` | `GET /v1/budgets/{uuid}/alerts` | `BudgetUUID` (r), `PeriodKey` | L[Alert] |

`CreateBudgetThresholdInput`:

| Field | Type | Rule |
|-|-|-|
| `BudgetUUID` | string | (r) |
| `ThresholdType` | string | (r). `ACTUAL` or `FORECASTED`; the console sends the budget's `Type` |
| `ThresholdPercentage` | int | (r). 1 or more |
| `MaxAlertsPerPeriod` | int | 0 sends the console default, 1 |
| `ReminderIntervalHours` | int | 0 sends the console default, 648 |

The SDK always sends `comparisonOperator: "GTE"`, the only value the console
offers. The server ignores `enabled` on create and starts every threshold
enabled, so the create Input has no `Enabled`; disable with an update.
`UpdateBudgetThresholdInput` has `BudgetUUID` (r), `ThresholdUUID` (r), and
pointer fields for `ThresholdPercentage`, `Enabled`, `MaxAlertsPerPeriod`,
and `ReminderIntervalHours`.

`Alert` holds the delivery status, the triggered value, and `Recipients`.
The API sends recipients as a JSON string that holds an array. The SDK
decodes that string into `Recipients []string`. When the string is not an
array of strings, `Recipients` stays nil and `RecipientsRaw` holds the
string, so one odd row does not fail the list.

### Cost explorer

| Operation | Method and path | Output |
|-|-|-|
| `GetCostOverview` | `GET /v2/cost-explorer/overview` | `{Summary, Series, Interval, StartDate, EndDate, GroupBy}` |
| `ListCostResources` | `GET /v2/cost-explorer/resources` | P[CostResource] plus `Summary` |

Both take `StartDate` (r), `EndDate` (r), `Product`, `ResourceType`,
`ResourceID`, and `Query` (sent as `q`). `GetCostOverview` also takes
`GroupBy` (`product`, `resourceType`, or `resourceId`; empty sends
`product`) and `Interval` (`hourly`, `daily`, `weekly`, or `monthly`; empty
sends `daily`). `ListCostResources` also takes `Page`, `Size`, `Sort`, and
`Order`.

`ListCostResources` is the first API that caps page size: a `size` above
200 returns HTTP 400. Pages start at 1. The SDK sends `Size` 200 when it is
0, and 200 when it is above 200. The output holds one page with the API's
`page`, `size`, `total`, and `totalPages` in the `PagedList` fields.

The SDK does not page for the caller in this release. A caller that needs
every row loops on `Page` up to `TotalPage`. A helper is lasting public API,
and one capped API is too little to design it from.

### Balances

| Operation | Method and path | Output |
|-|-|-|
| `GetBalances` | `GET /navbar/balances/v1` | `{Balances Balances}` |

`Balances` holds `Cash`, `POC`, `CashAvailable`, `CashHolding`, and
`POCHolding`, each `*float64`, because the API returns null when an account
has no balance. Every field was null on the test account, so the non-null
types are unverified; the decoder accepts a number or a numeric string.
`expiresPoc` is left out until a capture shows its shape, rather than kept
as `json.RawMessage`: adding it later breaks no caller.

## Budget write semantics

- Budget and threshold writes are synchronous. The response to a create,
  update, or delete reflects the final state, so no write waits.
- `CreateBudget` and `CreateBudgetThreshold` return the new object from
  `data`, including its `UUID`, as live runs confirm. A create response
  without a UUID is an `*APIError`; the SDK never finds a new budget by
  listing names.
- An update with no field set is `ErrInvalidInput` and sends nothing.
- `UpdateBudget`, `UpdateBudgetThreshold`, and both deletes return an empty
  Output. A caller that needs the new state calls `GetBudget` or
  `ListBudgetThresholds`. This keeps the Output stable whatever the API
  returns.
- The SDK checks required fields, UUID shape, and date format. It does not
  repeat the server's other rules, such as the name pattern or the limit
  range, so a server change never blocks a valid request. The server's error
  reaches the caller as an `*APIError`.
- The server allows one `ACTUAL` and one `FORECASTED` budget per account
  and rejects a second with HTTP 400. The SDK does not check it first.
- `POST` creates are not idempotent. The transport does not retry them after
  a 5xx or a network error that may have reached the server (see
  [ADR 0002](../adr/0002-write-api-conventions.md)). `PUT` and `DELETE` are
  retried as reads are. A `DELETE` retry that finds the budget gone returns
  `NotFound`; the SDK does not hide it.
- Deleting a budget deletes its thresholds on the server. The SDK does not
  delete thresholds first.

## Price quotes

`pricing.GetQuote` asks the regional billing gateway what a resource would
cost to create. It changes nothing and places no order.

| Field | Type | Rule |
|-|-|-|
| `ResourceType` | string | (r). For example `snapshot` or `public-vip` |
| `ResourceInfo` | `map[string]any` | Sent as `resourceInfo`; nil sends none |

The SDK always sends `action: "create"`. `GetQuoteOutput` holds
`OptimumPrice`, `OriginalPrice`, `DiscountPrice`, `DiscountPercent`, and
`Properties`, a list of `{Name, Description, OptimumPrice, MonthlyPrice,
CurrentPrice, DiscountPercent}` from `propertiesPrice`. `artifactPrices` is
left out until a capture shows it filled. The fixture capture records
whether `OptimumPrice` is per month or per billing cycle.

`pricing` exports `ResourceSnapshot` and `ResourcePublicVIP`, the two
verified resource types. Other types work through the string.

### Why a generic map first

Typed Inputs now would each be guessed from the console's create form. A
generic `ResourceInfo` covers every type, and a wrong key costs a wrong
quote, not a wrong write.

Typed quotes arrive with each paid create instead. The design for a paid
create, such as `compute.CreateServer`, adds a quote operation in the same
package that takes the same Input, for example
`compute.QuoteCreateServer(ctx, *CreateServerInput)`. It builds
`resourceInfo` from that Input with the same code the create uses, and calls
`pricing.GetQuote`. One Input then drives both the quote and the create, so
they cannot drift. That
rule is in [ADR 0002](../adr/0002-write-api-conventions.md).

## CLI

| Command | Kind | Needs `--yes` |
|-|-|-|
| `billing list-budgets` | Read | No |
| `billing get-budget` | Read | No |
| `billing create-budget` | Write | No |
| `billing update-budget` | Write | No |
| `billing delete-budget` | Write, destructive | Yes |
| `billing list-budget-thresholds` | Read | No |
| `billing create-budget-threshold` | Write | No |
| `billing update-budget-threshold` | Write | No |
| `billing delete-budget-threshold` | Write, destructive | Yes |
| `billing list-budget-alerts` | Read | No |
| `billing get-current-period-cost` | Read | No |
| `billing get-cost-overview` | Read | No |
| `billing list-cost-resources` | Read | No |
| `billing get-balances` | Read | No |
| `pricing get-quote` | Read | No |

`pricing get-quote` is a read even though it sends a `POST`: the kind
follows the side effect, not the HTTP method. `ResourceInfo` is a map, so it
is set through `--cli-input-json`:

```sh
vngcloud pricing get-quote --cli-input-json \
  '{"ResourceType":"public-vip","ResourceInfo":{"type":"public"}}'
```

Pointer Input fields become flags that the CLI sets only when the user gives
them, so `update-budget --budget-uuid <uuid> --status PAUSED` sends only the
status. `--enabled=false` sends `false`.

### `cli.Write` and `cli.Destructive`

`cli.Write(name, method, opts...)` has the same generic shape as `cli.Read`.
It differs in four ways:

1. `--debug` logs `write started` and `write finished` around the call.
2. Generated docs mark the command as a write, and as destructive when it
   has `cli.Destructive`.
3. The CLI never retries a write itself; only the transport rules in
   [ADR 0002](../adr/0002-write-api-conventions.md) apply.
4. It accepts `cli.Destructive`.

`cli.Destructive` makes the command fail with exit code 2 and a message
naming `--yes` unless `--yes` is given, as
[SDK and CLI](sdk-and-cli.md#destructive-commands) defines. A command is
destructive when it deletes a resource or data that the user cannot restore
with one more command. Pausing a budget is not destructive, because
`update-budget --status ACTIVE` restores it.

`cli.WaitFor` stays undefined. Budget writes finish in their response, so
nothing here needs to wait; the first asynchronous write defines it.

## Errors

Budget and pricing errors use the existing `*APIError` and exit codes. The
cases this design adds:

| Case | Result |
|-|-|
| Missing required field, bad UUID shape, bad date format | `ErrInvalidInput`, no request, exit 2 |
| Envelope `code` outside 200 to 299 on an HTTP 2xx | `*APIError` with that code, exit 1 |
| Unknown budget or threshold UUID | `NotFound`, exit 4 (see below) |
| Name pattern, limit range, or second budget of a type | The server's `*APIError`, exit 1 |
| `/v1/credits` or another billing API an IAM User cannot call | `Forbidden`, exit 1 |

An unknown budget returns HTTP 400 with envelope code 400 and a message
starting `Budget not found`, not a 404. `billing` maps an error whose
message starts with `Budget not found` or `Threshold not found`, on a 400
or inside a 2xx envelope, to an `*APIError` with code `NotFound` that keeps
the HTTP status and wraps `ErrNotFound`, so `IsNotFound` is true. Only
`billing` maps it; other services return real 404s. If the server changes
the text, the error is a plain 400 with an empty code and exit 1, which is
safe; fixture tests pin both texts. The mapping applies only to a 400 or
an error envelope on a 2xx, never to a 401, 403, or 5xx.

The fixture capture records the status and envelope code of each other
server error above, and the tests assert them.

## Security

- Budget and threshold writes change the account's spend alerts. A deleted
  or paused budget silently removes a guard. `delete-budget` and
  `delete-budget-threshold` need `--yes`, and every write gets an
  adversarial review before its release.
- The review checks: no automatic retry of a `POST` after it may have
  reached the server; UUID checks on every write path; `--yes` on both
  deletes; pointer fields sending only what was set; and no token,
  recipient address, or amount in `--debug` output or error messages.
- Alert recipients are email addresses. They are account data: they never
  enter fixtures, logs, or error messages.
- Budgets, costs, and balances are account data. Live output stays under
  the ignored `examples/basic/output/`, per
  [live data](../../instructions/live-data.md).

## Testing

- One sanitized raw fixture per operation in `testdata/billing/` and
  `testdata/pricing/`, named after the operation, captured per
  [live data](../../instructions/live-data.md). Recipients become
  `<account>`, and UUIDs become `<id>` or short synthetic IDs.
- Every write has tests for its request body, its success response, and
  each error status the capture recorded. The `UpdateBudget` test checks that
  nil fields are absent from the body.
- Tests cover the numeric and null envelope `code`, an error envelope on
  HTTP 200, the `Budget not found` mapping, the `ListCostResources` size
  cap, UUID rejection for `..`, `.`, `/`, and empty values, the recipients
  fallback, and a `POST` create that is not retried after a 502.
- `make live` gains read calls: `ListBudgets`, `GetCurrentPeriodCost`,
  `GetBalances`, and a `snapshot` quote.
- A separate live write test runs only when the owner approves the run,
  naming the account, the budget it creates, and the step 1 deletion. The
  manager adds a gate, such as a `livewrite` build tag. The test:
  1. Deletes leftover budgets whose names start with `vngcloud-live-`.
  2. Picks a budget type the account does not use yet, or skips.
  3. Creates `vngcloud-live-<random>` with `Status` `PAUSED` and
     `LimitAmount` 1e10 - 1, so it cannot send an alert. If `CreateBudget`
     fails, it lists budgets and deletes the one with its name, because an
     unretried `POST` may still have acted.
  4. Updates only `LimitAmount`, to another value of at least 1e9, then
     reads the budget and checks every other field is unchanged, `Status`
     `PAUSED` in particular.
  5. Creates a threshold at 100 percent, disables it at once, updates only
     `ReminderIntervalHours`, checks `Enabled` is still false, and deletes
     it. The budget stays paused throughout, so no alert can fire.
  6. Deletes the budget in `t.Cleanup` with its own
     `context.WithTimeout(context.Background(), ...)`, never the test's
     context, then asserts that no `vngcloud-live-` budget remains, logging
     only a count.

## Docs and release

The sdk role adds a Billing and Pricing page to `docs/wiki/` in the release
that ships the packages. It states that amounts are VND, that billing calls
ignore the region, and that a quote places no order. The release order is in
[SDK and CLI](sdk-and-cli.md#releases).

## Open questions

1. Should a profile setting such as `read_only = true` make the CLI refuse
   every `cli.Write` command? It would let the owner hand an agent a profile
   that cannot change anything. Recommendation: yes, in the first CLI
   release, because billing brings the first writes.
