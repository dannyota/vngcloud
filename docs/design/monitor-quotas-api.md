# vMonitor check state and quotas: API evidence and checks

This file owns wire evidence, fixture provenance, and checks for
[check state and quotas](monitor-quotas.md). The design owns public types,
safety rules, and owner decisions. Nothing here authorizes a live call.

## Provenance and limits

The manager's sanitized response record comes from live calls at the
Monitor root `https://vmonitor.console.greennode.ai/`, as the IAM User
except where noted. Quota writes sent: the free Email order, the paid SMS
run (order, auto-renew enable and disable, delete), and the early synthetic
renewal attempt as the renew-only service account, and the IAM synthetic
recovery described below. Unlisted write responses remain unverified.

Console sources are public Angular bundles from that same root, read as
untrusted text: uptime creation in chunk 478, billing flows in chunk 816,
quota guidance in chunk 439, state filters in chunk 580, and the common
classes and billing service methods. Older v1 and current v2 synthetic
flows coexist; use the specified v2 renewal. This is first-party
implementation evidence, not a published API guarantee.

Labels mean **live** response status and shape, **console** code never
sent, or **inferred** behavior established by neither. Evidence for one
kind or principal never verifies another. Approval promotes no label;
captures close each gate, and mock fixtures never close a live gate.

## Check state and history

**Live:** `GET vmonitor-uptime-manager/v1/uptimes/{id}` adds
`monitor_status`, `history_alarm`, numeric `user_id`, and null `deleted_at`.
ALARM and OK were observed; console filters also name UNDETERMINED.
`history_alarm` looks up the definition and is not a location alarm ID. A
deleted check reads 404 `Not found uptime '<id>'.` The console creates a
check with one call carrying `notifications`; no alarm or trigger call.

**Live:** `GET vmonitor-api/api/v1/alarms/synthetic/metrics/mona/{id}`
uses history_alarm and returns `{"data": {...}}`. Console method:
`getAlarmSyntheticMetricDefinitionById`. Definition keys:

- `id` differs from the lookup history_alarm; `name` has the form
  `syntt-his-<check-name>-<suffix>`; `description`, `expression_syntax`.
- `expression.sub_expressions[]`: `function` AVG, `metric_name`
  syntt.http.failure, `dimensions{syntt_id}`, `operator` GTE,
  `threshold` 1.0, `period` 60, `periods`, `deterministic` false;
  `expression.operators[]` was empty.
- `match_bys` contains location_id; `severity` LOW;
  `alarm_actions`, `ok_actions`, `undetermined_actions` are arrays.
  Empty alarm_actions despite Email notifications confirms the uptime
  notifications map carries the wiring; copying channels here is wrong.
- `status {status, status_updated_at}`; `alarms[]` holds one per location.
  Two locations produced two items; this is not a documented maximum.

Each location alarm has `id`, `links[]` containing internal cluster URLs,
`metrics[0] {id:null, name, dimensions}`, `state`, null `link`,
`alarm_definition {id, links, name, severity}`, null `lifecycle_state`,
`state_updated_timestamp`, `updated_timestamp`, and `created_timestamp`.
Dimensions include location_name, syntt_id, user_id, syntt_name, location_id.
Timestamps use an RFC 3339 shape with fractional seconds and +07:00 offset.
Never expose internal links or raw dimension maps.

**Live:** the same alarm base path with `/{alarm-id}/histories` relative to
`mona` takes `start_time` and `end_time` as epoch milliseconds and `interval`
as the check's test_frequency in minutes. It uses an alarms[].id, not the
lookup or definition ID. Console method: `getAlarmSyntheticMetricHistoryById`.
Result: `{"data": [...]}`. Each item has digit-string `id`, `metrics[]`,
`reason` text containing syntt_id, `timestamp`, `alarm_id`, `old_state`,
`new_state`, null `reason_date`, and `sub_alarms[]` with null
`subAlarmExpression`, `sub_alarm_state`, `current_values[]`.

One transition per alarm was observed in a six-hour window:
UNDETERMINED to ALARM and UNDETERMINED to OK.

**Live range probe** on one alarm with one transition in the last day:
24-hour, 7-day, and 90-day windows each returned 200 with that one item,
so no maximum range appeared up to 90 days; `interval=1`, `interval=60`,
and no `interval` returned the same item. A reversed range returned 200
with an empty list (the design still refuses it locally), and a window
with no transition returned empty: history reports only transitions
inside the window, never the state at its start. An unknown alarm UUID on
`/histories` returned 200 empty, not 404; an unknown definition UUID on
`mona/{id}` returned 500 with `timestamp` and `message`, passed through.
Paging and truncation stay unknown: one transition, no paging keys.

## Billing reads

All paths below are relative to `billing-api/`. Successes listed as live
were 200. Bare arrays have no observed pagination envelope.

**Live composite usage:** `GET v1/quotas/usages` returns logUsages[],
metricUsages[], syntheticUsages[], emailUsages[], smsUsages[], freeEmailQuota,
freeSmsQuota, logSeverity, metricSeverity, syntheticSeverity, emailSeverity,
smsSeverity, notificationSeverity. Only syntheticUsages had items:
`{api, location, email, sms, slack, webhook, telegram, teams}`. After the
free Email order, emailUsages held `[{amount: 0}]` with emailSeverity and
notificationSeverity "good"; freeEmailQuota stayed 0, so it counts only
metric-package grants. Log, metric, and SMS item shapes remain unknown.

**Live Email absence:** `GET v1/email/quota` returned 404 with
`{"message":"Not found email quota"}`. `GET v1/email/quota/usages` returned
404 with `{"message":"Not found the Resource"}`. The first response is the
only evidenced create precondition. **Live SMS absence:** `GET
v1/sms/quota` returned 404 `{"message":"Not found sms quota"}`. `GET
v1/metric/quota` also returned 404; metric quota operations remain outside
this design.

**Live Email quota** (after the owner-chosen free order): `GET
v1/email/quota` returned `{id, name "email quota", type "EMAIL", status
"ACTIVE", packageId <emailfree>, startTime, endTime, createdAt, updatedAt,
system false, autoRenew 0, autoRenewPeriod 0, poc 0}`, the synthetic quota
shape, with endTime at startTime plus 30 days. `GET v1/email/quota/usages`
returned `{"usage":{"amount":0},"description":{"amount":{name, specs,
unit}}}`; introspect (null body) returned 200 `{"active":true}`, out of scope.

**Live SMS quota** (paid `sms5` order, later deleted): the same shape with
type "SMS", name "sms quota", endTime at startTime plus 30 days; CREATING
at 3 s with `updatedAt` null, ACTIVE at 6 s. SMS usage was not read and
stays inferred; Email's CREATING `updatedAt` is likely null, unverified.

**Live delivery test** after the free order: the owner received all four
alert emails, the first In-alarm one about one minute after a check
entered ALARM; before the quota an ALARM transition sent nothing and usage
stayed 0. On this account, with no Email quota and zero free allowance,
the missing quota is the confirmed cause. Email usage read `amount 4`,
matching the four emails, while the location history showed three
transitions, one right after an `update-check` URL change; that gap stays
open. Synthetic usage `email` stayed 0: alerts count against Email usage.

**Live packages:** `GET v1/email/packages` and `GET v1/sms/packages` return
`[{id, name, price, amount}]` with VND numeric prices. Email's emailfree has
a UUID ID, price 0, amount 20; email5k has non-UUID ID
`vMonitor-Platform-Notification-email5k`, price 110000, amount 5000.
Other paid Email IDs also use names. SMS's smsfree has a UUID ID, 0, 20;
sms5 has a UUID ID, 55000, 50. Never apply UUID validation to packages.
`GET v1/email/package-description` returns amount metadata with name,
specs, unit; specs says maximum emails per month. No operation exposes it.

**Live synthetic quota:** `GET v1/synthetic/quota` returns id, name, type
SYNTHETIC_TEST, status ACTIVE, packageId, startTime, endTime, createdAt,
updatedAt, system false, autoRenew 0, autoRenewPeriod 0, poc 0. Time strings
use RFC 3339 with +00:00. `autoRenew` 0 and 1 are live (the SMS flip);
`poc` 1 is unverified; any other encoding fails closed.
`GET v1/synthetic/quota/usages` returns
`{usage:{api,location,email,sms,slack,webhook,telegram,teams},description}`;
description maps counter names to `{name,specs,unit}`.

**Live detail:** `GET v2/synthetic/{quota-id}/quota-detail` returns id, name,
price 0.0, location 1, api 10, tierId, tierName, email 0, sms 0, slack,
webhook, telegram, teams, resourceId, classId, className Basic, packageId.
Slack, webhook, telegram, and teams are null, not zero. The Telegram usage
description says maximum messages per month; neither its 0 usage nor its
null limit proves whether delivery needs notification quota. The zero
email allowance applies to this Basic quota only: the console says
Metric packages can grant free messages, one Email quota at a time.

**Live synthetic catalogs (manager raw reread):** `v1/synthetic/packages`
lists free10, price 0, tierId 24, location 1, api 10. Its id differs from
Basic config.packages and config.locations[].packageId in
`v2/synthetic/quota-class`. Quota/detail and the console renewal quote use
the class-side packageId; detail.classId identifies Basic. No v1 ID join is
evidenced. `GET v2/synthetic/packages` returns 404, not a fallback catalog.
Basic is ACTIVE, priority 1, location amount 1, min/maxApi 10, step 1.
Pro and Enterprise `(Coming soon)` are DISABLED, description `Coming soon.`,
no config. No priced ACTIVE option is evidenced; v1 price is not its price.

**Live settings:** `GET v1/settings` returns paymentMethod PRE-PAID,
monthPeriods [1,3,6,12,24,36], maxMonthPeriodForFreeQuota 1,
maxMonthlyResetNotificationBillingAction 3, hidePeriod true.
`GET v1/trash/quotas` returned `[]`; no nonempty item shape is verified.
`POST v1/trash/count-quotas`, no body, returned `{"total":1}` while the
synthetic quota was in trash. It is a read despite POST. A count proves no
identity; `list-quotas-in-trash` waits for an item shape.

## Quote bodies

All quote calls here are `POST`, semantically read-only. The intended SDK
uses Idempotent with the configured bounded read retries, never a write
retry loop. All paths below are relative to `billing-api/`.

- **Live Email create:** `v2/email/prices/created-price` with
  `{"redirectUrl":"","packageId":"<package-id>","quantity":1}`.
  Bare result has optimumPrice, discountPercent, originalPrice,
  discountPrice, propertiesPrice[] with the same price keys plus name and
  description. emailfree quoted 0; email5k quoted 100000 against list
  110000. Email quotes are pre-VAT by inference; the actual debit can
  exceed the quote, and no debit or tax evidence gates a paid write.
- **Live SMS create:** `v2/sms/prices/created-price` with the same body
  quoted sms5 at 50000 against list 55000; smsfree was not quoted on v2.
  The v1 query quote `?package_id=<sms5>&month_period=1` returned
  totalCost 55000, equal to the list price, so the v1/v2 agreement and the
  VAT note hold for Email only. v2 stays the price source (decision 10).
- **Console Email and SMS renewal:** the v2 call and body with
  `"resourceId":"<quota-id>"` added; neither was sent.
- **Live synthetic renewal:** `v2/synthetic/prices/created-price` with
  `{"redirectUrl":"","packageId":"<package-id>","quantity":10,`
  `"resourceId":"<quota-id>"}` quoted optimumPrice 0. Quantity came from
  the detail's total api capacity, not current consumed usage.
- **Live failed alternative:** `v1/synthetic/prices/time-extension-price`
  with query `month_period=1`, null body, returned 500. Do not use it.

Similar price keys permit a private decoder patterned on the log project
quote, but missing/null optimumPrice must never mean free.

**Live quote probe** (quotes only, no order sent): the v2 Email quote
returned 500 for the create body `{monthPeriod:1, packageId, redirectUrl,
pay:true}`, free or paid; with `quantity:1` added it returned 200, and
`monthPeriod` 1 or 3 priced the same 100000, so v2 ignores the period.
`{redirectUrl:"", packageId, quantity:1}` priced emailfree 0 and email5k
100000. The v1 query quote `POST v1/email/prices/created-price?package_id=
<id>&month_period=<n>` with a null body returned, for email5k at period 1,
`{originalCost 100000, discountCost 0, discountRatio 0.0, discountPercent
"0.00 %", totalCost 100000, packagePrice 100000, originalPackagePrice
100000}`; period 3 returned totalCost 300000; emailfree returned 500, and
an unknown package 400 "The email package does not exist." The SMS route
`POST v1/sms/prices/created-price?package_id=<smsfree>&month_period=1`
also returned 500. The v2 synthetic quote returned 400 "packageId: must
not be null" for the extension body `{redirectUrl, monthPeriod:1,
pay:true}`. No quote endpoint accepts a mutation body: the design maps one
frozen set of terms to each quote request and mutation body; at period 1
the v1 and v2 quotes agree on a paid package, and v2 is the price source.
Preserve both observed bodies; never relabel them as identical.

## Writes and unresolved semantics

The free Email create, paid SMS create, and synthetic recovery have live
write evidence under `billing-api/`; recovery's body is unverified. [Quota
lifecycle](monitor-quotas-lifecycle.md#wire-facts-and-evidence) records the
auto-renew and delete evidence. For IAM users the console sets pay true and
may open paymentUrl; the SDK never opens it. Fixed `redirectUrl` body
values, never fetched, hostname as the console has it:
`https://vmonitor.console.vngcloud.vn/quota-usages/notification` for Email
and SMS, the same origin plus `quota-usages/synthetic-test` for synthetic.

- **Live** create Email: `POST v1/email/quota`, body
  `{monthPeriod:1,packageId,redirectUrl:<notification-page>,pay:true}`,
  sent once for emailfree after a 404 quota read and a 0 quote, returned
  200 `{"orderId":null,"paymentUrl":<the sent redirectUrl>,"amount":null}`.
  A free order echoes the redirect as paymentUrl; there is no checkout.
  Reads then gave 404 at once, CREATING at about 3 s, ACTIVE at about 5 s.
  **Live** create SMS: the same body for sms5 returned 200 with the same
  envelope; CREATING at 3 s, ACTIVE at 6 s. The balance was not read between
  order and delete, so the debit itself was not observed; net change 0.
- Email extension: `POST v1/email/quota/extend-time`; recovery:
  `POST v1/trash/email/quota`. Both send
  `{monthPeriod:1,redirectUrl:<notification-page>,pay:true}`.
  SMS substitutes sms in both paths. No resource ID appears in these paths
  or write bodies; the account's current quota is the target.
- Synthetic extension: `POST v2/synthetic/quota/extend-time`; recovery:
  `POST v2/trash/synthetic/quota`. Body:
  `{redirectUrl:<synthetic-page>,monthPeriod:1,pay:true}`.
  Use renewSyntheticOrderV2/recoverSyntheticOrderV2, not the v1 alternatives.
- The console recovers when endTime has passed AND status is IN-TRASH;
  otherwise it extends. The SDK exposes separate methods with the
  parent and coverage eligibility guards, never an automatic fallback.
- **Live** early synthetic extension, sent as the renew-only service
  account 11 days before expiry, after the design's guards and a 0 quote:
  `POST v2/synthetic/quota/extend-time`
  returned 409 with the message "You can only renew in the last 7 days
  of a free quota." and 120 s of reads showed no state change. A free
  quota renews only in its last 7 days; the success shape is unverified.
- **Response:** the order envelope is live for Email and SMS creates only.
  Synthetic recovery has live status 200 but no retained body; renewal
  success status and body remain unverified. Use the
  [tolerant policy](monitor-quota-coverage.md#unverified-write-responses).
- **Inferred expiry:** console preview adds a period to current endTime.
  Verify active extension before promising that it adds a month. Recovery
  uses the new-term semantics below; do not transfer them to extension.
- Auto-renew, delete, and purge: evidence and rules in [quota
  lifecycle](monitor-quotas-lifecycle.md#wire-facts-and-evidence).
  Introspect `POST v1/{kind}/quota/introspect` stays out of scope.

Settings show `maxMonthlyResetNotificationBillingAction` 3 and a Basic log
quota got 409 after three orders; five notification actions this month
(two orders, two auto-renew changes, one delete) were not refused, but
only two were orders. What the limit counts stays unknown. Policy for
later probes: at most one notification order or recovery per month beyond
those already approved, unless the owner approves more.

## Synthetic recovery evidence

The manager's sanitized IAM console capture records a naturally lapsed
free Synthetic quota, not a forced expiry. The quota GET shows IN-TRASH,
past EndTime, autoRenew 0, and poc 0; the UI says EXPIRED and offers Renew
and Delete. Its detail is Basic, price 0, api 10, location 1. The dialog
shows original and total price 0 VND. It sends the existing synthetic v2
quote mapping with quantity 10 and resourceId, returning optimumPrice,
originalPrice, and discountPrice 0 with one propertiesPrice named
`monitor-platform-synthetic`.

Confirmation sent exactly one `POST v2/trash/synthetic/quota`, body
`{redirectUrl:<synthetic-page>,monthPeriod:1,pay:true}`, returning HTTP 200
and application/json in about 1 s. Navigation prevented body retention.
A redirect echo is plausible but unverified. At about 1 s the quota still
read IN-TRASH and count 1. At about 1 minute the manager's CLI read showed
the same quota ID ACTIVE, startTime at send time, endTime one month later,
updatedAt about 4 s after send, and auto-renew false. Recovery starts a
new term at recovery time; it does not extend the old expiry. Trash then
read `[]`. The check reported OK on both locations about 2 minutes later.
Quota settlement and check service recovery are separate observations.

## Fixtures

Fixtures in `testdata/monitor/` are sanitized raw responses, never SDK output,
with operation, HTTP status, live/console provenance, and substitutions.
Preserve keys/types, non-UUID package IDs, integral decimals, nulls, offsets,
and unknown keys. Replace account IDs with synthetic numbers, dimensions.user_id
with `<account>`, resources with path-valid synthetic IDs, names with synthetic
names, links with `<internal-url>`, and secrets/checkout URLs with placeholders.
Keep the free Email order's public redirect literal to test echo equality.

Evidence inventory uses basenames only; no private paths or real resource IDs:

| Source | Provenance and use |
|-|-|
| `synthetic-quota-in-trash.json` | Raw quota GET, 200; recovery precondition fixture |
| `synthetic-quota-detail.json` | Raw detail GET, 200; preserve explicit zero and null limits |
| `trash-count-before.json` | Raw count POST, 200; total 1, no item schema |
| `recover-quote-request.json`, `recover-quote-response.json` | Captured request and raw quote 200; shared mapping and decode fixture |
| `recover-request.json` | Mutation request only; body assertion, never a success fixture |
| `synthetic-quota-after-cli.json` | Decoded CLI projection; timing evidence only, not raw fixture input |

The manager reports raw ListSyntheticQuotaClasses.json; supply it and raw
settings/post-recovery responses before dependent decoding. The class fixture
is absent in this worktree; ListSyntheticPackages.json matches the reread.
Preserve distinct catalog IDs and class/quota/detail/quote equality through
consistent substitutions. The CLI projection omits ID/package; same-ID is
manager evidence only. Mock success bodies stay unverified; no recovery or
in-window renewal success body fixture exists.

Required fixture families, all decoded through the public SDK:

- Existing GetCheck plus definition with two locations and each location's
  history; a manager-supplied raw list response for ListChecks additions.
- Composite usage, Email and SMS package lists, synthetic quota/detail/usage
  and package list, Email and SMS quota and usage 404s, the synthetic
  renewal quote, and the early-renewal 409.
- Email and SMS v2 quotes for the free and a paid package, the v1 paid
  quotes, the live Email and SMS quota successes including a raw CREATING
  read with `updatedAt` null, the Email usage, the free Email and paid SMS
  order responses, the composite usage with an emailUsages item, the
  lifecycle responses the sibling records, and each gated extend or
  recover success. Fixtures stay synthetic: no test assumes a quota exists.

Check required-key presence, not just json.Unmarshal success: null or empty
bodies are never a zero-valued success. Sanitation keeps valid route IDs so
path validation does not block the decode test. Sensitive requests suppress
capture hooks; only the manager's private capture yields raw fixtures.

## Deterministic checks

Use httptest, fake clock/sleep, synthetic credentials, and counted requests.
No test calls GreenNode. Run focused tests and `make check`; the plan owns
file assignments, wiki generation, and acceptance order.

- Check fields: preserve run Status and existing create/update wire bodies;
  unknown monitor states survive; omitted additions decode empty; old keyed
  callers still compile. Read and list raw fixtures exercise actual SDK
  decoding. New projections omit user_id and deleted_at.
- Alarm: use history_alarm for definition and alarms[].id for history;
  validate malicious response IDs before a second request, with no echoed
  value. Empty history_alarm is NotFound. Two locations map correctly;
  absent/ambiguous identity or conflicting history alarm_id fails closed.
- History: defaults, independent optional endpoints, RFC 3339 offsets,
  invalid text, epoch-millisecond collapse, invalid frequency, location
  filter/NotFound, cancellation between locations, stable total sort, no
  partial Output on a later error, every response and aggregate cap, and
  fan-out refusal.
- Quota reads: nil Inputs only where empty, fixed Kind routing, unknown
  statuses preserved, numeric flags/counts validated, a CREATING read with
  `updatedAt` null decoding, unknown usage kinds reported without raw JSON,
  the SMS usage read refused with zero requests. Detail failures return nil
  Output. Check quota/detail identities and absence versus other 404s.
- Pricing: distinct synthetic ID spaces, detail price as ListPrice, no v1
  ID substitution or catalog calls; notification duplicate/unknown packages,
  ambiguous free default; invalid/missing prices and MaxPrice; zero requests
  on invalid input, synthetic quantity from detail. Freeze one set of
  terms: quote request and mutation body both derive from it, `monthPeriod`
  is always 1, no later catalog read changes either, and no quote-only
  field reaches a mutation body. The v2 decoder fails closed on a missing
  total. Compare 100000 quote/110000 list at both budgets; free zero passes
  and zero quote on a priced package is Unpriced; a quote never orders.
- Writes: every supported mutation body and success status, 400/401/403/404/
  409/429, 500/502/503/504, dial failure, disconnect after send, cancellation,
  malformed/oversized 2xx, and every redirect including 307/308; at most one
  mutation attempt each, and 401 never refreshes and resends.
- Renewal: required ExpectedEndTime, instant-equivalent offsets, stale
  expiry refusal, MaxDaysLeft read-only skip, overflow, positive versus zero
  threshold, expiry crossing during quote, ACTIVE extension, expired AND
  IN-TRASH in the separate recovery method, and the 409
  early-renewal fixture decoding to a `Conflict` `*APIError` naming the
  renew operation, message withheld, no record, no further request, with
  an unrelated 409 body decoding the same way. No creation on 404,
  no trash lookup dependency, and no POC/auto-renew/unknown-status mutation.
  The pre-send reread repeats the whole predicate: a snapshot that changes
  only `autoRenew` or `poc` during quoting, or whose flag stops decoding,
  aborts with no send; for create, a quota appearing during quoting aborts.
- Wait/uncertainty: selected package and identity checks, unchanged expiry
  despite ACTIVE, leaving trash without extension, delay, read error, hard
  deadline during a slow request, cancellation, NoWait, and paymentUrl.
  Partial Outputs and wrapped causes survive CLI output/error handling.
  Never print the URL or declare Renewed merely on an accepted response.
- Concurrency: lock wait cancellation, delayed visibility, concurrent create
  versus renewal, two same-expiry renewals, repeated unresolved and NoWait
  calls, and read reconciliation that stays unknown; no second send. After
  an unresolved renewal from E, a renewal with baseline E1 and a create on
  that kind each reconcile first. Once the prior renewal is confirmed,
  the guard mismatch returns ErrQuotaChanged; other changed intents,
  including create, return ErrInvalidInput naming the prior attempt.
  Neither returns `Renewed: true` or sends again. A matching renewal from E
  returns the confirmed outcome; an inconclusive read returns ErrNotSettled
  for both. Existing delete/auto-renew guard mismatches keep ErrInvalidInput
  under [lifecycle rules](monitor-quotas-lifecycle.md#errors).
  A separate-client simulation documents the residual race, not a guarantee.
- Security: sent tokens in successful strings, status/code/error text,
  malformed timestamp, nested JSON, paymentUrl, links, dimensions, and
  unseen usage arrays. Assert safe json/table/text, --query, errors.As
  fields, debug logs, and suppressed captures. Test limits before allocation
  and no foreign request from links, redirect, or caller redirect hooks.
- CLI: existing string flag/JSON precedence, NoFlag controls, read-only flag,
  environment and profile refusals before network, cost warning to stderr,
  MaxPrice consent, safe partial output, and exact error identity/exit codes.
  Keep existing transport, cache, credential, and log-project regressions.

## Manager live checks

Only the manager runs separately approved live verification under
[live data](../../instructions/live-data.md), naming resource, principal,
maximum cost, and retention. Serialize writes and privately record attempts
and baselines. Never force production expiry, repeat a successful recovery,
order a prerequisite package, or delete a working quota for verification.
The lifecycle sibling owns notification action budgets and row gates;
[plans](../plans/) track completion, not this durable protocol.

1. Preserve raw recovery evidence and obtain an approved raw recovered-quota
   read. Compare raw and SDK nullable detail, catalog, and status reads;
   decoded CLI output is not a raw fixture and lost bodies stay unverified.
2. During the free synthetic quota's last 7 days, while ACTIVE and unexpired,
   use the renew-only service account and environment-only CLI for one
   approved renewal: fresh ExpectedEndTime, MaxDaysLeft 7, MaxPrice 0, and
   NoWait false. Read quota-status --kind synthetic before and after, with
   no Email/SMS grants. Privately retain quote, baseline ID/package/expiry,
   request, response status/body if available, Sent/Stage/Error, exit code,
   read-back timing, new identity/expiry, and term arithmetic.
3. A definite early 409 ends the run without retry. After uncertainty use
   reads and inspect billing; never resend, create, recover, or delete.
   A 404 or unchanged expiry proves nothing. Renewed requires read-back;
   implementation approval does not verify a missing success body.
4. Read check state separately to verify service recovery. Delayed messages
   authorize no order. Telegram prerequisites remain a separate investigation;
   no Email renewal run is included. Report remaining evidence limits.
