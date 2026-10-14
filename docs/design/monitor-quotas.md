# vMonitor check state and quotas

Status: Proposed. Nothing here approves code, a purchase, or a live run.

A check created with `monitor create-check` and an Email channel went to
alarm and sent no email. The account had no Email Notification quota and a
free email allowance of 0. The CLI could not show the check's alarm state,
order the free email package, or renew the free Synthetic Test quota that
expires every month and stops every check when it lapses. This design adds
those reads and writes to `monitor`. Wire evidence, fixtures, deterministic
checks, and the live checks before code are in [the API and checks
sibling](monitor-quotas-api.md).

It extends [vMonitor](monitor.md) and [vMonitor Alerts](monitor-alerts.md)
and follows [SDK and CLI](sdk-and-cli.md), [CLI](cli.md), and [ADR
0002](../adr/0002-write-api-conventions.md). Orders copy the log project
pattern: one body for quote and order, `MaxPrice` default 0,
`ErrPriceAboveMax`, no retry after an ambiguous failure, read-only refusal,
and a bounded wait with `NoWait`.

## Scope

In scope:

- `Check.MonitorStatus` and `Check.HistoryAlarmID`.
- A check's backing alarm definition with per-location state, and its
  state history over a time range.
- Composite quota usage, the Email and SMS quota and usage, their package
  lists, and the Synthetic Test quota with its end time and auto-renew
  state.
- Ordering an Email or SMS quota by package, free by default, and renewing
  the Email, SMS, and Synthetic Test quotas with a quote and price guard.

Out of scope, each needing its own design: metric quotas, log quota
renewal and resize, `buyWith` bundles, a new Synthetic Test order or a
paid synthetic class, quota deletion and purge, auto-renew settings, Slack,
Telegram, Webhook, and Teams (usage counters only, no package), the
`time-extension-price` quote (500 live), payment-URL checkout (`pay: false`),
the `introspect` calls, and billing settings as an operation.

## Decisions

| Topic | Decision |
|-|-|
| Package | Extend `monitor`; quota paths share its host and token |
| Check fields | `MonitorStatus` and `HistoryAlarmID`, plain strings |
| Alarm reads | Two operations keyed by `CheckID`; cluster `links` dropped |
| History range | `Start` and `End` as `time.Time`; default the last 24 hours |
| Kind | `Kind` is `Email` or `SMS`, mapped to a fixed path segment |
| No quota | `GetNotificationQuota` returns the not-found sentinel |
| Order default | Empty `PackageID` orders the one package priced 0 |
| Price guard | `MaxPrice` against the higher of quote and list price |
| Zero quote | A 0 quote for a package the list prices above 0 is `ErrUnpriced` |
| Renewal path | Extend an active or expired quota; recover one in trash |
| Early renewal | Allowed; `MaxDaysLeft` lets a cron job skip it |
| Period | Always one month; the free quota allows no more |
| Payment | `pay: true`; the payment URL is never exposed or followed |

## SDK

"(r)" marks `vngcloud:"required"`. Operation names are `monitor.<Method>`.
Every ID that reaches a path passes `core.CheckPathID` before any request.
`Kind` never reaches a path as typed: the SDK maps `Email` and `SMS`, case
insensitive, to `email` and `sms`, and refuses any other value with
`ErrInvalidInput`.

### Check state

`Check` gains `MonitorStatus string` (`monitor_status`) and `HistoryAlarmID
string` (`history_alarm`). Constants `MonitorStatusOK`, `MonitorStatusAlarm`,
and `MonitorStatusUndetermined` name the three values the console filters
on; an unknown value reaches the caller unchanged. `user_id` and
`deleted_at` stay dropped. Both additions break no caller.

| Operation | Input | Output |
|-|-|-|
| `GetCheckAlarm` | `CheckID` (r) | `{Alarm CheckAlarm}` |
| `ListCheckAlarmHistory` | `CheckID` (r), `Start`, `End`, `LocationID` | `{Items []CheckAlarmTransition; Start, End time.Time}` |

`GetCheckAlarm` reads the check, then the definition named by its
`HistoryAlarmID`. An empty `HistoryAlarmID` returns the not-found sentinel
and sends no second request. `CheckAlarm` holds `ID`, `Name`,
`Description`, `Severity`, `Status`, `StatusUpdatedAt`, `MatchBys`,
`ExpressionSyntax`, `Expression` (`[]CheckAlarmSubExpression` with
`Function`, `MetricName`, `Operator`, `Threshold`, `Period`, `Periods`,
`Deterministic`), and `Locations []CheckLocationAlarm`. Each location holds
`AlarmID`, `LocationID`, `LocationName`, `State`, `StateUpdatedAt`,
`UpdatedAt`, and `CreatedAt`. The wire `links`, `link`, `alarm_definition`,
the three action lists, and the metric `dimensions` map, which carries the
account ID, are not modeled. Timestamps decode as `time.Time` from RFC
3339; a value in another shape fails the read with a decode error, which
the fixture test guards against.

`ListCheckAlarmHistory` runs these steps, with no request before step 3:

1. Check `CheckID` and, when set, `LocationID` with `core.CheckPathID`.
2. Resolve the range. A zero `End` is now from the client clock. A zero
   `Start` is `End` minus 24 hours. `Start` at or after `End` returns
   `ErrInvalidInput`. The server owns any maximum range (ADR 0002 rule 5).
3. Read the check and its alarm definition as `GetCheckAlarm` does.
4. For each location alarm, or only the one whose `LocationID` matches,
   send one history read with `start_time` and `end_time` in epoch
   milliseconds and `interval` set to the check's `TestFrequency` in
   minutes, at least 1, as the console does. Requests run one at a time
   and stop at the first error or a cancelled `ctx`; there is no partial
   output. A `LocationID` that matches no location returns the not-found
   sentinel.
5. Merge the items, tag each with its `AlarmID`, `LocationID`, and
   `LocationName`, and sort by `Timestamp` then `LocationName`.

Each history read sets `MaxBody` to 16 MiB. The API shows no paging, so a
wide range on a flapping check is the only unbounded read here; past the
cap the error names a shorter range. `CheckAlarmTransition` holds `ID`,
`AlarmID`, `LocationID`, `LocationName`, `Timestamp`, `OldState`,
`NewState`, `Reason`, and `SubAlarms []CheckSubAlarmState` with `State` and
`CurrentValues []float64`. The wire `metrics`, `reason_date`, and
`subAlarmExpression` are not modeled.

### Quota reads

| Operation | Call | Input |
|-|-|-|
| `GetQuotaUsage` | `GET billing-api/v1/quotas/usages` | none |
| `GetNotificationQuota` | `GET billing-api/v1/{kind}/quota` | `Kind` (r) |
| `GetNotificationQuotaUsage` | `GET billing-api/v1/{kind}/quota/usages` | `Kind` (r) |
| `ListNotificationPackages` | `GET billing-api/v1/{kind}/packages` | `Kind` (r) |
| `GetSyntheticQuota` | `GET billing-api/v1/synthetic/quota`, then `GET billing-api/v2/synthetic/{id}/quota-detail` | none |
| `GetSyntheticQuotaUsage` | `GET billing-api/v1/synthetic/quota/usages` | none |

- `QuotaUsage` holds `SyntheticUsages []UsageCounts`, `FreeEmailQuota`,
  `FreeSMSQuota`, and the six severity strings. `UsageCounts` has `API`,
  `Location`, `Email`, `SMS`, `Slack`, `Webhook`, `Telegram`, and `Teams`.
  `LogUsages`, `MetricUsages`, `EmailUsages`, and `SMSUsages` were empty
  live, so their item shape is unknown; the model keeps them as
  `[]json.RawMessage` until a fixture shows an item.
- `Quota` is one billing quota: `ID`, `Name`, `Type`, `Status`,
  `PackageID`, `StartTime`, `EndTime` (`time.Time`), `AutoRenew bool`
  (true for any non-zero wire value), `AutoRenewPeriod` (months), `POC
  bool`, `System bool`, `CreatedAt`, and `UpdatedAt`. Constants
  `QuotaStatusActive` and `QuotaStatusInTrash` name the two statuses the
  console acts on. The synthetic read establishes this shape; the Email and
  SMS quota shape is inferred from it until an order yields a fixture.
- `GetNotificationQuota` maps the live 404 to the not-found sentinel with a
  message naming the kind and `CreateNotificationQuota`, so the CLI exits
  4 and a script can tell "no quota" from a failed call.
  `GetNotificationQuotaUsage` maps its 404 the same way. Its 200 shape is
  inferred to match the synthetic usage.
- `NotificationPackage` holds `ID`, `Name`, `Price` (VND), and `Amount`
  (messages a month). Package IDs are not all UUIDs; they travel only in
  request bodies, never in a path.
- `GetSyntheticQuotaOutput` holds `Quota` and `Detail` (`API`, `Location`,
  `Email`, `SMS`, `Slack`, `Webhook`, `Telegram`, `Teams`, `ClassName`,
  `TierName`, `Price`). A 404 on the quota returns the not-found sentinel.
  Both requests must succeed; there is no partial output.
- `QuotaUsageDetail` holds `Usage UsageCounts` and `Descriptions` keyed by
  counter name.

### Notification quota order

`CreateNotificationQuotaInput` has `Kind` (r), `PackageID`, `MaxPrice`
(VND a month, default 0), and `NoWait`. `QuoteCreateNotificationQuota`
takes the same Input and ignores the last two (ADR 0002 rule 8). In
order, with no request before step 3:

1. Check `Kind` and `MaxPrice`, which must be finite and not negative.
2. Resolve nothing else locally; the server owns name and limit rules.
3. List the packages. An empty `PackageID` selects the single package
   priced 0; none or more than one returns `ErrInvalidInput` naming
   `PackageID`. A set `PackageID` must appear in the list.
4. Read the quota. A 200 returns `ErrInvalidInput`: the account can hold
   one quota per kind, so renewal is the right call. Only a 404 continues.
5. Quote with `POST billing-api/v2/{kind}/prices/created-price` and body
   `{"redirectUrl":"","packageId":<id>,"quantity":1}`, `Idempotent`. A
   response without `optimumPrice` is an `*APIError` and orders nothing.
6. Guard. Let `price` be the higher of `optimumPrice` and the package's
   list `Price`: the live quote for a paid package was below its list
   price, so the quote looks pre-VAT and the list price is the safer
   bound. `price > MaxPrice` returns `ErrPriceAboveMax` naming both. A
   quote of 0 for a package whose list price is above 0 returns
   `ErrUnpriced`. A free package quotes 0 and passes at `MaxPrice` 0.
7. Order once with `POST billing-api/v1/{kind}/quota` and body
   `{"monthPeriod":1,"packageId":<id>,"redirectUrl":<notification
   page>,"pay":true}`, with `NoRedirect` and no retry after a 5xx or a
   lost response. The error then says to read the quota before ordering
   again. `pay: true` debits the balance at once for a paid package.
8. Decode `orderId` and `paymentUrl`. The Output exposes `OrderID` and
   `PaymentPending bool`, true when `paymentUrl` is non-empty; the URL
   itself can carry a token and is never returned, logged, or opened.
9. Unless `NoWait`, poll `GetNotificationQuota` every 2 seconds for up to
   120 seconds until it returns a quota with status `ACTIVE`, then return
   it. The bound returns `dns.ErrNotSettled`, as the log project wait does.

The console's fixed `redirectUrl` is
`https://vmonitor.console.vngcloud.vn/quota-usages/notification`. Whether
the order endpoint demands it, as the log order does, is a live check.

### Renewals

`RenewNotificationQuotaInput` has `Kind` (r), `MaxPrice`, `MaxDaysLeft`,
and `NoWait`; `RenewSyntheticQuotaInput` has the last three. Each has a
`Quote` twin that ignores `MaxPrice`, `MaxDaysLeft`, and `NoWait`.

1. Check the input as the order does.
2. Read the quota. A 404 returns the not-found sentinel whose message
   names the order call; a quota that left trash is not found this way.
   For the synthetic quota, also read the detail for its `API` count.
3. When `MaxDaysLeft` is above 0 and `EndTime` is later than now plus
   that many days, return `{Renewed: false, Quota}` and send nothing.
4. Choose the path as the console does: `EndTime` in the past and status
   `IN-TRASH` recovers with `POST .../trash/{kind}/quota`; anything else
   extends with `POST .../{kind}/quota/extend-time`. Email and SMS use
   `billing-api/v1`; the synthetic quota uses `billing-api/v2`.
5. Quote with the `created-price` call and body `{"redirectUrl":"",
   "packageId":<quota PackageID>,"quantity":<1, or the synthetic API
   count>,"resourceId":<quota ID>}`, then guard as the order does, with the
   list price looked up by `PackageID` when the package list has it.
6. Send the renewal once with body `{"redirectUrl":<page>,
   "monthPeriod":1,"pay":true}`, `NoRedirect`, no retry after an
   ambiguous failure. The error says to read the quota and compare its
   `EndTime` with the one printed before retrying.
7. Unless `NoWait`, poll the quota every 2 seconds for up to 120 seconds
   until `EndTime` is later than the pre-renewal read, or the status left
   `IN-TRASH`. The Output holds `Renewed: true`, `OrderID`,
   `PaymentPending`, and the quota.

A renewal of an active quota extends it from its current `EndTime`, as the
console's expiry preview computes; the live check confirms this. Billing
settings cap a free quota at one month a renewal and allow three
notification orders or recoveries a month; the fourth gets the server's
409 unchanged. The SDK never resends after a 409, and `MaxDaysLeft` keeps
a daily job from spending those actions early.

### Client state

`Client` gains `quotaMu`, held across the read, quote, and send of every
order and renewal, so two goroutines on one `Client` cannot both pass step
4 of the order on the same 404. Across processes the server's one-quota
rule and the caller's own serialization apply; the API has no conditional
request.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Bad `Kind`, ID, range, or `MaxPrice`; unknown or ambiguous package | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| A quota already exists on create | `ErrInvalidInput`, no order | `InvalidUsage`, 2 |
| No quota, no alarm definition, no such location | Not-found sentinel | `NotFound`, 4 |
| Price above the guard | `ErrPriceAboveMax`, no order | `PriceAboveMax`, 1 |
| Zero quote for a priced package | `ErrUnpriced`, no order | `Unpriced`, 1 |
| Wait ran out or failed | `dns.ErrNotSettled` with the Output | `NotSettled`, 1 |
| Server refuses, including the monthly 409 | The `*APIError` | 1, or 4 on 404 |

No new sentinel or CLI code is added. `monitor.ErrUnpriced` aliases
`vngcloud.ErrUnpriced` as `ErrPriceAboveMax` already does.

## CLI

| Command | Kind | `--yes` |
|-|-|-|
| `monitor get-check-alarm`, `list-check-alarm-history` | Read | No |
| `monitor get-quota-usage`, `get-notification-quota`, `get-notification-quota-usage`, `list-notification-packages`, `get-synthetic-quota`, `get-synthetic-quota-usage` | Read | No |
| `monitor quote-create-notification-quota`, `quote-renew-notification-quota`, `quote-renew-synthetic-quota` | Read | No |
| `monitor create-notification-quota` | Write | No |
| `monitor renew-notification-quota`, `renew-synthetic-quota` | Write | No |

- A read-only profile refuses the three writes before any request. None
  is destructive under ADR 0002 rule 6: a paid order spends money, but the
  existing paid creates take consent through `--max-price`, and these do
  the same. Without `--max-price` only a package priced 0 is ordered.
- `list-check-alarm-history` takes `--start` and `--end` as RFC 3339
  times. `internal/cli/flags.go` gains `time.Time` as a flag type, parsed
  with `time.RFC3339`, so the main use needs no `--cli-input-json`;
  `--cli-input-json` still accepts the same RFC 3339 strings. `--location-id`
  narrows to one location.
- `--kind Email|SMS` reaches every notification command. `--max-days-left`
  and `--no-wait` reach the renewals; `--package-id` the order.
- The quote commands hide `MaxPrice`, `MaxDaysLeft`, and `NoWait` with
  `NoFlag`, as `quote-create-log-project` does.
- Tables show `EndTime` in UTC with its zone, `AutoRenew`, and `Status`,
  and never render a missing price as 0.

## Security

- The alarm definition and its history carry the account ID in metric
  dimensions and the cluster's internal URLs in `links`. Neither is
  modeled; fixtures replace them with `<account>` and `<internal-url>`
  and keep the keys so the decode test proves they are dropped.
- Every path is built with `RouteURL` under the `Monitor` endpoint. The
  order and renewal `POST` set `NoRedirect`, so a redirect can never replay
  a paid body elsewhere, and send at most one attempt after any response.
- `--debug` logs method, path, status, and duration only
  (`internal/transport/transport.go:564`). No error message carries a
  price token, payment URL, or account ID; the ambiguous-order error wraps
  the transport error, which redacts the bearer token.
- `MaxPrice` is a local check of today's quote. It is not sent and does
  not bind a later renewal or a recurring charge.
- The adversarial review before each write release checks: one order or
  renewal per call at every layer, redirects included; the guard runs on
  the same body the order sends; `MaxDaysLeft` and the pre-order quota read
  send nothing when they refuse; the mutex covers read to send; no
  `paymentUrl` in output, errors, or logs; read-only refusal; and the
  `Kind` map that keeps caller text out of paths.

## Threat trace

| Threat | Existing path | This design |
|-|-|-|
| Token in logs or errors | Transport redacts the sent token in network errors and logs no body; log project order errors wrap it (`monitor/logprojects_write.go:150`) | Same wrap; `paymentUrl` decoded but never surfaced |
| Hostile response | Quote without a price fails closed (`monitor/logprojects.go:527`); `flexibleInt` rejects fractions | `MaxBody` on history; strict `time.Time`; 404 mapped only on the quota reads; status strings pass through |
| Foreign host | `refuseRedirects` on `Once` or `NoRedirect` (`internal/transport/transport.go:411`); endpoint only from config | `NoRedirect` on every order and renewal |
| Tampered profile or cache | Read-only is checked before `LoadConfig` (`internal/cli/readonly.go:24`); unchanged files | No new file, header, or setting |
| Repeated paid write | Price guard before the order (`monitor/logprojects_write.go:137`); no 5xx retry | Pre-order quota read, `quotaMu`, `MaxDaysLeft`, one-month period, 409 passed through |

## Testing

Unit tests use `httptest` and the injected clock. Each operation decodes a
sanitized raw fixture. Writes test the request body with defaults and with
every field, the success response, and 400, 401, 403, 404, 409, and 429.
The checks document lists them per operation, including: the free default
and the ambiguous-package refusal; the guard on both quote and list price;
`ErrUnpriced`; a 200 on the pre-order read; recovery versus extend by
status and `EndTime`; `MaxDaysLeft` sending nothing; one `POST` on 502 and
on a dropped connection; each wait outcome; the history range defaults,
the millisecond query, `interval`, the location filter, the `MaxBody`
error, and the merge order; and CLI read-only refusal, exit codes, and the
RFC 3339 flag.

## Releases

| Release | Content |
|-|-|
| First | `MonitorStatus`, `HistoryAlarmID`, `GetCheckAlarm`, `ListCheckAlarmHistory`, `GetQuotaUsage`, `ListNotificationPackages`, `GetSyntheticQuota`, `GetSyntheticQuotaUsage`, the `time.Time` flag, and their commands |
| Second | `GetNotificationQuota`, `GetNotificationQuotaUsage`, `QuoteCreateNotificationQuota`, `CreateNotificationQuota`, and the wiki correction |
| Third | The three renewals and their quotes |

Reads ship first because every shape in the first release is live
verified. The email order ships in the second release together with the
two notification quota reads rather than after them: their 200 shape can
only be captured once a quota exists, so the order's live check is also
their evidence, and the blocked user needs the order. Renewals come last
because the synthetic renewal must run early against a free quota that
ends soon, and its effect on `EndTime` is still inferred.

## Wiki

The implementer updates, in the same change as each release: `Monitor.md`
(check state fields and the two alarm reads), a new `Monitor-Quotas.md`
SDK page (quota reads, the order, renewals, `MaxPrice`, `MaxDaysLeft`,
`PaymentPending`, the monthly limits, and that a lapsed Synthetic Test
quota stops every check), `CLI-Monitor.md` through the generator notes,
and `Monitor-Alerts.md`. The last three and the send-channel-otp note in
`internal/cli/gendocs_notes_monitor_logalarm.go` drop "the free 20": a
synthetic-only account has no free email or SMS until it orders the free
package, and the free allowance then comes from that package's `Amount`.

## Owner decisions

1. Add `time.Time` as a CLI flag type for `--start` and `--end`.
   Alternatives: string fields parsed in the SDK, or `--cli-input-json`
   only. Recommended: add the type; it is a small reflection case like
   `float64` was, and keeps the SDK idiomatic.
2. Guard `MaxPrice` against the higher of the quote and the list price.
   Alternative: the quote alone, as the log project does. Recommended:
   the higher value, until the live check shows which amount is debited.
3. Keep quota deletion and auto-renew out of this slice. Deletion spends
   one of three monthly actions and the console shows no auto-renew on a
   free quota. Recommended: out; a later design covers paid quotas.
4. Add `MaxDaysLeft` to the renewals. Alternative: always renew and leave
   the check to the caller. Recommended: add it, so a daily job renews
   only when the quota is about to end.
5. Ship the email order with the notification quota reads in the second
   release. Alternative: reads alone first. Recommended: together, for
   the reasons in [Releases](#releases).
6. Refuse an order when a quota of that kind exists, rather than let the
   server decide. Recommended: refuse; the server's response to a second
   order is unknown and could be a second charge.

## Open questions

- The Email and SMS quota, usage, and order response shapes, and whether
  the order needs the console's `redirectUrl`.
- Whether an extension of an active quota adds a month to `EndTime`, and
  whether the amount debited for a paid package is the quote or the list
  price.
- What a lapsed quota looks like: whether `GET {kind}/quota` still returns
  it with status `IN-TRASH`, or only `GET billing-api/v1/trash/quotas`
  lists it, whose item shape is unseen.
- Whether an email arrives once the free package is active and a check
  changes state; the inference that the missing quota caused the silence
  is unproven.
- The history call's paging, maximum range, and what `interval` changes.
