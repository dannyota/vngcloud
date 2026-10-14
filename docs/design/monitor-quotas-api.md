# vMonitor check state and quotas: API evidence and checks

Wire evidence, fixture rules, deterministic checks, and the live checks
before code for [vMonitor check state and quotas](monitor-quotas.md). This
file owns the wire contracts; the design owns the rules.

## Provenance

The manager collected every live response below read-only on the test
account with an SDK-issued IAM User bearer token and no extra header,
against the `Monitor` endpoint root `https://vmonitor.console.greennode.ai/`.
Console flows come from the public vMonitor Angular bundle, read as data.
The designer had no live access. Each row carries one of these marks:

| Mark | Meaning |
|-|-|
| live | Status and body shape seen in a live response |
| live 404 | Only the not-found response was seen; the 200 shape is inferred |
| console | Read from console code; never sent |
| inferred | Derived from a sibling shape or console behavior; unverified |

No document move or approval upgrades an inferred row. Every inferred row
has a live check below, and an operation that depends on one stays gated
until that check runs.

## Check state

| Call | Mark | Shape |
|-|-|-|
| `GET vmonitor-uptime-manager/v1/uptimes/{id}` | live | Adds `monitor_status` (`OK`, `ALARM`, `UNDETERMINED`), `history_alarm` (UUID), `user_id` (number, account data), `deleted_at` (null) |
| `GET vmonitor-api/api/v1/alarms/synthetic/metrics/mona/{history_alarm}` | live | `{"data": {...}}`, below |
| `GET vmonitor-api/api/v1/alarms/synthetic/metrics/mona/{alarm}/histories?start_time=&end_time=&interval=` | live | `{"data": [...]}`, below |

Definition `data` keys: `id` (differs from `history_alarm`), `name`
(`syntt-his-<check name>-<5 chars>`), `description`, `expression`
(`sub_expressions[]` of `function`, `metric_name`, `dimensions{syntt_id}`,
`operator`, `threshold` (number), `period`, `periods`, `deterministic`;
`operators []`), `expression_syntax`, `match_bys ["location_id"]`,
`severity`, `alarm_actions []`, `ok_actions []`, `undetermined_actions []`,
`alarms[]`, and `status {status, status_updated_at}`. `alarm_actions` was
empty although the check named an Email channel: the uptime manager's
`notifications` map carries the wiring, not the alarm definition.

Each `alarms[]` item is one location: `id`, `links[]` (internal cluster
URLs), `metrics[0] {id null, name, dimensions {location_name, syntt_id,
user_id, syntt_name, location_id}}`, `state`, `link` (null),
`alarm_definition {id, links, name, severity}`, `lifecycle_state` (null),
`state_updated_timestamp`, `updated_timestamp`, `created_timestamp`.
Timestamps follow the layout `2006-01-02T15:04:05.000-07:00` with a
`+07:00` offset; `time.RFC3339` parses them. A check with two locations
had two items.

History items: `id` (digit string), `metrics[]` (as above), `reason`
(text naming `syntt_id`), `timestamp`, `alarm_id`, `old_state`,
`new_state`, `reason_date` (null), `sub_alarms[] {subAlarmExpression null,
sub_alarm_state, current_values[]}`. The console sends the check's
`test_frequency` as `interval` and the chart range in epoch milliseconds.
One item came back per alarm in a six-hour window. Paging, maximum range,
and the effect of `interval` are unknown.

## Quota reads

| Call | Mark | Shape |
|-|-|-|
| `GET billing-api/v1/quotas/usages` | live | `logUsages[]`, `metricUsages[]`, `syntheticUsages[{api, location, email, sms, slack, webhook, telegram, teams}]`, `emailUsages[]`, `smsUsages[]`, `freeEmailQuota`, `freeSmsQuota`, and `logSeverity`, `metricSeverity`, `syntheticSeverity`, `emailSeverity`, `smsSeverity`, `notificationSeverity` strings |
| `GET billing-api/v1/email/quota` | live 404 | `{"message":"Not found email quota"}`; 200 inferred as the synthetic quota shape |
| `GET billing-api/v1/email/quota/usages` | live 404 | `{"message":"Not found the Resource"}`; 200 inferred as the synthetic usage shape |
| `GET billing-api/v1/sms/quota`, `.../usages` | console | Same paths as email; never sent |
| `GET billing-api/v1/email/packages` | live | `[{id, name, price, amount}]`: `emailfree` (UUID id, 0, 20) and paid ids such as `vMonitor-Platform-Notification-email5k` (110000, 5000) up to `email200k` |
| `GET billing-api/v1/sms/packages` | live | Same shape: `smsfree` (UUID, 0, 20), paid UUID ids from `sms5` (55000, 50) to `sms100` (1100000, 1000) |
| `GET billing-api/v1/email/package-description` | live | `{"amount":{name, specs, unit}}`; not modeled |
| `GET billing-api/v1/synthetic/quota` | live | `{id, name, type "SYNTHETIC_TEST", status "ACTIVE", packageId, startTime, endTime (ISO, +00:00), createdAt, updatedAt, system false, autoRenew 0, autoRenewPeriod 0, poc 0}` |
| `GET billing-api/v1/synthetic/quota/usages` | live | `{usage {api, location, email, sms, slack, webhook, telegram, teams}, description {<key>: {name, specs, unit}}}` |
| `GET billing-api/v2/synthetic/{id}/quota-detail` | live | `{id, name, price 0.0, location 1, api 10, tierId, tierName, email 0, sms 0, slack, webhook, telegram, teams, resourceId, classId, className "Basic", packageId}` |
| `GET billing-api/v1/settings` | live | `{paymentMethod "PRE-PAID", monthPeriods [1,3,6,12,24,36], maxMonthPeriodForFreeQuota 1, maxMonthlyResetNotificationBillingAction 3, hidePeriod true}` |
| `GET billing-api/v1/trash/quotas` | live | `[]`; the item shape is unseen |
| `GET billing-api/v1/synthetic/packages`, `GET billing-api/v2/synthetic/quota-class` | live | Basic `free10` (price 0, location 1, api 10); Pro and Enterprise `DISABLED`; not modeled |

The composite usage counted `api 2` and every notification counter 0 after
the alarm fired, with `freeEmailQuota 0`. The synthetic detail's `email 0`
shows the Basic synthetic class grants no free email. The console text
reads: "You don't have any Paid email notification quota. Buy a package to
extend your Notification quota. You can have Free notification quota
provided with your Metric packages," and "At one time, you can only own
one Email Notification quota."

## Quotes

| Call | Mark | Body and result |
|-|-|-|
| `POST billing-api/v2/email/prices/created-price` | live | `{"redirectUrl":"","packageId":<id>,"quantity":1}` -> `{optimumPrice, discountPercent, originalPrice, discountPrice, propertiesPrice[{optimumPrice, discountPercent, originalPrice, discountPrice, name "monitor-platform-email", description}]}`; `emailfree` 0; `email5k` 100000 against a list price of 110000 |
| `POST billing-api/v2/email/prices/created-price` with `resourceId` | console | Renewal quote: `{"redirectUrl":"","packageId":<quota packageId>,"quantity":1,"resourceId":<quota id>}` |
| `POST billing-api/v2/sms/prices/created-price` | console | Same bodies as email |
| `POST billing-api/v2/synthetic/prices/created-price` | live | `{"redirectUrl":"","packageId":<quota packageId>,"quantity":<api total>,"resourceId":<quota id>}` -> 200, `optimumPrice` 0 for the free quota |
| `POST billing-api/v1/synthetic/prices/time-extension-price?month_period=1` | live | 500; not used |

The quote response is the log project quote shape, so the SDK reuses that
decoder with the same nil-price refusal.

## Writes

All bodies come from console code. `pay` is true for an IAM user, which
charges the balance at once; the console opens `paymentUrl` when the
response has one. `PAYMENT_REDIRECT_URL` is
`https://vmonitor.console.vngcloud.vn/quota-usage/quota` and each flow
replaces `quota-usage/quota` with its page path.

| Call | Mark | Body |
|-|-|-|
| `POST billing-api/v1/email/quota` | console | `{monthPeriod: 1, packageId, redirectUrl: ".../quota-usages/notification", pay}` |
| `POST billing-api/v1/sms/quota` | console | Same body |
| `POST billing-api/v1/email/quota/extend-time`, `POST billing-api/v1/trash/email/quota` | console | `{monthPeriod: 1, redirectUrl: ".../quota-usages/notification", pay}`; trash when `endTime` passed and status `IN-TRASH`, else extend |
| `POST billing-api/v1/sms/quota/extend-time`, `POST billing-api/v1/trash/sms/quota` | console | Same rule and body |
| `POST billing-api/v2/synthetic/quota/extend-time`, `POST billing-api/v2/trash/synthetic/quota` | console | `{redirectUrl: ".../quota-usages/synthetic-test", monthPeriod: 1, pay}`; same rule |
| Order response | inferred | `{amount, orderId, paymentUrl}` as the log order returns; `orderId` may be empty for a free order |
| `POST billing-api/v1/{synthetic\|email\|sms}/quota/{id}/update-auto-renew` | console | `{resourceId, enabled, period: 1}`; hidden for free and POC quotas; out of scope |
| `DELETE billing-api/v1/{kind}/quota`, `DELETE billing-api/v1/trash/{kind}/quota` | console | No body, no ID; out of scope |
| `POST billing-api/v1/{kind}/quota/introspect` | console | Null body, `{active}`; out of scope |

The console preselects the first paid email package; the SDK defaults to
the free one instead. The console's new-expiry preview adds the period to
the current `endTime`, which is the basis for the inferred rule that an
early renewal extends rather than restarts the term. The 409 "Exceeded
quota" after three Basic log orders a month and
`maxMonthlyResetNotificationBillingAction 3` are the basis for the
inferred three-actions rule on notification quotas.

## Fixtures

Each fixture under `testdata/monitor/` is a sanitized raw response and
decodes through the SDK; decoded SDK output is never fixture evidence.
Replace check and alarm IDs with `<id>`, the account ID in `user_id` and
in every `dimensions.user_id` with `<account>`, each `links[]` entry and
`alarm_definition.links[]` entry with `<internal-url>`, check and
`syntt_name` values with synthetic names, and quota, order, and package
UUIDs with `<id>`. Keep every key, `links` and `user_id` included, so the
decode test proves the model drops them. Keep statuses, severities, the
number shapes (`threshold 1.0`, `autoRenew 0`, `poc 0`), the `+07:00` and
`+00:00` timestamp shapes, and the `Not found email quota` message.

| Fixture | Source |
|-|-|
| `GetCheck.json` | Already holds `monitor_status` and `history_alarm`; the decode test starts reading them |
| `GetCheckAlarm.json` | The live definition with two locations |
| `ListCheckAlarmHistory.json` | The live `UNDETERMINED -> ALARM` item |
| `GetQuotaUsage.json` | The live composite usage |
| `GetNotificationQuotaNotFound.json` | The live 404 body |
| `ListNotificationPackagesEmail.json`, `...SMS.json` | The two live lists, prices kept |
| `QuoteCreateNotificationQuota.json` | The live `email5k` quote |
| `GetSyntheticQuota.json`, `GetSyntheticQuotaDetail.json`, `GetSyntheticQuotaUsage.json` | The three live synthetic reads |
| `QuoteRenewSyntheticQuota.json` | The live renewal quote |
| `GetNotificationQuota.json`, `GetNotificationQuotaUsage.json`, `CreateNotificationQuota.json` | Gated on live check 2 |
| `RenewSyntheticQuota.json`, `RenewNotificationQuota.json` | Gated on live checks 3 and 4 |

## Deterministic checks

Every check runs against `httptest` with the injected clock and counts
requests; a fake server fails the test on a second order or renewal.

| Operation | Checks |
|-|-|
| `Check` decode | `MonitorStatus` and `HistoryAlarmID` from the fixture; `user_id` and `deleted_at` still dropped |
| `GetCheckAlarm` | Decode with two locations; `links`, `dimensions`, and action lists absent from the Output; empty `history_alarm` is not-found with one request; 404 on each read; bad `CheckID` sends nothing |
| `ListCheckAlarmHistory` | Zero `End` is the clock's now and zero `Start` is 24 hours before; `Start >= End` sends nothing; query holds millisecond epochs and `interval` = `TestFrequency` (1 when 0); one request per location in order; `LocationID` filter and its not-found; an error on the second location returns no Output; items merged and sorted; a body over 16 MiB is an error; cancelled `ctx` between locations |
| `GetQuotaUsage` | Decode; empty usage arrays; unknown severity strings pass through |
| `GetNotificationQuota`, `GetNotificationQuotaUsage` | `Email` and `sms` map to `email` and `sms`; `Teams` and `../email` are `ErrInvalidInput` with no request; 404 is the not-found sentinel whose message names the kind; the inferred 200 decodes |
| `ListNotificationPackages` | Both fixtures; a non-UUID id decodes |
| `GetSyntheticQuota` | Two requests in order; `autoRenew 0` and `1` and `poc 0` and `1` decode to bools; a 404 on the quota is not-found; a 500 on the detail returns no Output |
| `CreateNotificationQuota` | Free default selected; zero or two free packages refuse; unknown `PackageID` refuses; a 200 pre-order read refuses with no quote; quote and order bodies; `NoRedirect` set; guard on quote 100000 and list 110000 with `MaxPrice` 100000 refusing and 110000 passing; quote 0 with list 110000 is `ErrUnpriced`; quote 0 free passes; nil `optimumPrice` orders nothing; one `POST` on 502 and on a dropped connection with the read-first hint; 400, 401, 403, 404, 409, and 429 returned unchanged; `PaymentPending` from a non-empty `paymentUrl` with the URL absent from Output, error, and log; wait to `ACTIVE`, timeout, and `NoWait` |
| `RenewNotificationQuota`, `RenewSyntheticQuota` | 404 is not-found naming the order; `MaxDaysLeft` skip with no request and `Renewed` false; extend when active, extend when expired but not in trash, recover when expired and `IN-TRASH`; `v1` and `v2` prefixes; quote body with `resourceId` and the synthetic `quantity` from the detail; guard and `ErrUnpriced` as the order; one `POST` on 502; 409 unchanged; wait on `EndTime` moving and on leaving `IN-TRASH`; timeout; `NoWait` |
| `Quote*` | Same body as the write, no order, `MaxPrice` ignored, `Idempotent` retry after a 502 |
| `quotaMu` | Two concurrent orders on one `Client` make one order and one refusal |
| CLI | Golden `json`, `table`, and `text`; `--start` and `--end` parse RFC 3339 and refuse other text with exit 2; read-only refusal of the three writes with no request; exit 4 on no quota; `PriceAboveMax`, `Unpriced`, and `NotSettled` codes; `--debug` output holds no body, query string, or URL |

## Live checks before code

The manager runs these on the test account under [live
data](../../instructions/live-data.md), records only field names, types,
statuses, prices, and timings, and sanitizes every capture before it
becomes a fixture. Notification quota orders, recoveries, and deletions
share a budget of three a month, so the order below spends at most two.

1. Reads for the first release: `GetCheckAlarm` and
   `ListCheckAlarmHistory` on an existing check with two locations over 24
   hours and over 7 days (paging, size, and whether `interval` changes the
   items), the composite usage, both package lists, and the three
   synthetic reads. Done read-only; repeat only if a shape changed.
2. Free email order, gated on owner approval naming the account and the
   `emailfree` package: quote, order, the order response shape, then
   `GET email/quota` and `email/quota/usages` until `ACTIVE`, with
   timings. Try the order once without the console `redirectUrl` only if
   the owner accepts a possible wasted action. Then fire a check into
   alarm and back and record whether an email arrives, which settles the
   root cause. Keep the quota; do not delete it.
3. Synthetic renewal, gated on owner approval: quote with `resourceId`,
   then `extend-time` on the active free quota, and record the new
   `EndTime` against the old one, the response shape, and the time to
   settle. This spends no notification action.
4. Email renewal, gated on owner approval and the budget: `extend-time`
   on the active free email quota from check 2, recording `EndTime`
   before and after. The trash recovery path needs a lapsed quota; when
   the owner prefers not to delete the email quota, recovery stays
   inferred and its code path ships behind the extend path with the
   status rule untested live.
5. Paid amounts: only with explicit owner approval of the cost, order
   `sms5` or `email5k` and reconcile the debit against the quote and the
   list price. Without this check the guard keeps the higher bound and
   the wiki says so.
