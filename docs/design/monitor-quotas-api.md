# vMonitor check state and quotas: API evidence and checks

This file owns wire evidence, fixture provenance, and checks for
[check state and quotas](monitor-quotas.md). The design owns public types,
safety rules, and owner decisions. Nothing here authorizes a live call.

## Provenance and limits

The manager's sanitized response record comes from read-only calls using an
SDK-issued IAM User bearer token without extra headers at the Monitor root,
`https://vmonitor.console.greennode.ai/`. No write in this file was sent.
The design improver has no live access and made no network calls.

Console sources are public Angular bundles from that same root, read as
untrusted text: uptime creation in chunk 478, billing flows in chunk 816,
quota guidance in chunk 439, state filters in chunk 580, and the common
classes and billing service methods. Older v1 and current v2 synthetic
flows coexist; use the specified v2 renewal. This is first-party
implementation evidence, not a published API guarantee.

Labels mean **live** response status and shape, **console** code never
sent, or **inferred** behavior established by neither. Email evidence does
not verify SMS. Document approval promotes no label; manager-approved
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
so no maximum range appeared up to 90 days. `interval=1`, `interval=60`,
and no `interval` returned the same item. A reversed range returned 200
with an empty list; the design still refuses it locally. A window with no
transition returned empty: history reports only transitions inside the
window, never the state at its start. An unknown alarm UUID on
`/histories` returned 200 with an empty list, not 404. An unknown
definition UUID on the `mona/{id}` read returned 500 with a `timestamp`
and `message`, not 404; the SDK passes that error through. Paging and
truncation remain unknown: no alarm had more than one transition and the
response carries no paging keys.

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
metric-package grants. Log, metric, and SMS item shapes remain unknown and
cannot become public raw JSON models. Before the order the account showed
freeEmailQuota 0 and no counted notification after the alarm.

**Live Email absence:** `GET v1/email/quota` returned 404 with
`{"message":"Not found email quota"}`. `GET v1/email/quota/usages` returned
404 with `{"message":"Not found the Resource"}`. The first response is the
only evidenced create precondition. **Console:** corresponding SMS paths
exist; they were not called, and SMS quota and usage 200 shapes stay
**inferred**. `GET v1/metric/quota` also returned 404; metric quota
operations remain outside this design.

**Live Email quota** (after the owner-chosen free order): `GET
v1/email/quota` returned `{id, name "email quota", type "EMAIL", status
"ACTIVE", packageId <emailfree>, startTime, endTime, createdAt, updatedAt,
system false, autoRenew 0, autoRenewPeriod 0, poc 0}`, the synthetic quota
shape, with endTime at startTime plus 30 days. `GET v1/email/quota/usages`
returned `{"usage":{"amount":0},"description":{"amount":{name, specs,
unit}}}`. `POST v1/email/quota/introspect` with a null body returned 200
`{"active":true}`; it stays out of scope.

**Live delivery test** after the free order: an In-alarm email reached the
owner about one minute after a check entered ALARM; before the quota, an
ALARM transition sent nothing and usage stayed 0. This confirms the missing
quota as the cause. `update-check` replayed a transition (a URL change gave
an extra ALARM to OK). Email usage then read `amount 4` for three observed
transitions; one count is unexplained, and whether both Up emails arrived
is unconfirmed. Synthetic usage `email` stayed 0: alert emails count
against the Email quota usage, not the synthetic counter.

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
use RFC 3339 with +00:00. The zero flag values are live evidence; a
nonzero encoding is inferred from console truthiness and stays gated.
`GET v1/synthetic/quota/usages` returns
`{usage:{api,location,email,sms,slack,webhook,telegram,teams},description}`;
description maps counter names to `{name,specs,unit}`.

**Live detail:** `GET v2/synthetic/{quota-id}/quota-detail` returns id, name,
price 0.0, location 1, api 10, tierId, tierName, email 0, sms 0, slack,
webhook, telegram, teams, resourceId, classId, className Basic, packageId.
The zero email allowance applies to this Basic synthetic quota. It does
not prove every account lacks free messages: the console says Metric
packages can supply them and only one Email quota can exist at a time.

**Live synthetic packages:** `GET v1/synthetic/packages` lists free10 with
id, name, price 0, tierId, location 1, api 10. `GET v2/synthetic/quota-class`
shows Basic ACTIVE with config.locations entries amount 1, minApi 10,
maxApi 10, step 1, packageId; Pro and Enterprise are DISABLED. Package
lookup for free renewal is internal, not a new public list operation.

**Live settings:** `GET v1/settings` returns paymentMethod PRE-PAID,
monthPeriods [1,3,6,12,24,36], maxMonthPeriodForFreeQuota 1,
maxMonthlyResetNotificationBillingAction 3, hidePeriod true.
`GET v1/trash/quotas` returned `[]`; no trash item or lookup is verified.

## Quote bodies

All quote calls here are `POST`, semantically read-only. The intended SDK
uses Idempotent with the configured bounded read retries, never a write
retry loop. All paths below are relative to `billing-api/`.

- **Live Email create:** `v2/email/prices/created-price` with
  `{"redirectUrl":"","packageId":"<package-id>","quantity":1}`.
  Bare result has optimumPrice, discountPercent, originalPrice,
  discountPrice, propertiesPrice[] with the same price keys plus name and
  description. emailfree quoted 0; email5k quoted 100000 against list
  110000. Pre-VAT pricing is an inference, not a verified tax rule.
- **Console Email renewal:** that same endpoint and body with
  `"resourceId":"<quota-id>"` added. SMS uses `v2/sms/prices/created-price`
  and the corresponding create or renewal body; neither SMS quote was sent.
- **Live synthetic renewal:** `v2/synthetic/prices/created-price` with
  `{"redirectUrl":"","packageId":"<package-id>","quantity":10,`
  `"resourceId":"<quota-id>"}` quoted optimumPrice 0. Quantity came from
  the detail's total api capacity, not current consumed usage.
- **Live failed alternative:** `v1/synthetic/prices/time-extension-price`
  with query `month_period=1`, null body, returned 500. Do not use it.

Similar price keys permit a private decoder patterned on the log project
quote, but missing/null optimumPrice must never mean free.

**Live quote probe** (quotes only, no order sent):

- The v2 Email quote returned 500 for the create body
  `{monthPeriod:1, packageId, redirectUrl, pay:true}`, free or paid. With
  `quantity:1` added it returned 200; `monthPeriod` 1 or 3 priced the same
  100000. The v2 quote ignores the period. `{redirectUrl:"", packageId,
  quantity:1}` priced emailfree 0 and email5k 100000.
- The v1 query quote `POST v1/email/prices/created-price?package_id=
  <id>&month_period=<n>` with a null body returned, for email5k and period
  1, `{originalCost 100000, discountCost 0, discountRatio 0.0,
  discountPercent "0.00 %", totalCost 100000, packagePrice 100000,
  originalPackagePrice 100000}`; period 3 returned totalCost 300000. It
  returned 500 for emailfree and 400 "The email package does not exist."
  for an unknown package. The SMS route `POST v1/sms/prices/created-price?
  package_id=<smsfree>&month_period=1` also returned 500.
- The v2 synthetic quote returned 400 "packageId: must not be null" for the
  extension body `{redirectUrl, monthPeriod:1, pay:true}`.

No quote endpoint accepts a mutation body. The design therefore maps one
frozen set of terms to each quote request and mutation body; at period 1
the v1 and v2 quotes agree on a paid package, and v2 is the price source.
Preserve both observed bodies; never relabel them as identical.

## Writes and unresolved semantics

All but the free Email create are **console only** under `billing-api/`,
with unverified responses. For IAM users the console sets pay true and may
open paymentUrl. The SDK must not open it. The configured base redirect is
`https://vmonitor.console.vngcloud.vn/quota-usage/quota`; flows replace its
last path with the appropriate page. Do not modernize its hostname by guess.

Notification page: `https://vmonitor.console.vngcloud.vn/` plus
`quota-usages/notification`. Synthetic page: the same origin plus
`quota-usages/synthetic-test`. These are fixed body values, not fetched URLs.

- **Live** create Email: `POST v1/email/quota`, body
  `{monthPeriod:1,packageId,redirectUrl:<notification-page>,pay:true}`,
  sent once for emailfree after a 404 quota read and a 0 quote, returned
  200 `{"orderId":null,"paymentUrl":<the sent redirectUrl>,"amount":null}`.
  A free order echoes the redirect as paymentUrl; there is no checkout.
  Reads then gave 404 at once, CREATING at about 3 s, ACTIVE at about 5 s.
  SMS: `POST v1/sms/quota`, same shape, console only. The console
  preselects a paid package; a paid order is unverified.
- Email extension: `POST v1/email/quota/extend-time`; recovery:
  `POST v1/trash/email/quota`. Both send
  `{monthPeriod:1,redirectUrl:<notification-page>,pay:true}`.
  SMS substitutes sms in both paths. No resource ID appears in these paths
  or write bodies; the account's current quota is the target.
- Synthetic extension: `POST v2/synthetic/quota/extend-time`; recovery:
  `POST v2/trash/synthetic/quota`. Body:
  `{redirectUrl:<synthetic-page>,monthPeriod:1,pay:true}`.
  Use renewSyntheticOrderV2/recoverSyntheticOrderV2, not the v1 alternatives.
- Each renew flow recovers only when endTime has passed AND status is
  IN-TRASH; otherwise it extends. Known-state refusal in the SDK further
  narrows eligibility until each path is evidenced.
- **Response:** live for the free Email create as above. For every other
  write the `{amount, orderId, paymentUrl}` envelope, status, nullability,
  and paid-order orderId stay inferred. Capture each supported write.
- **Inferred expiry:** console preview adds a period to current endTime.
  Verify active extension before promising that it adds a month. Recovery
  may use different identity and expiry semantics; it remains gated.
- Auto-renew `POST v1/{kind}/quota/{id}/update-auto-renew` sends
  `{resourceId,enabled,period:1}`. Console hides controls for isFree and POC;
  this does not prove free auto-renew support. Out of scope.
- Delete `DELETE v1/{kind}/quota` and `DELETE v1/trash/{kind}/quota` use
  no body/ID. Introspect `POST v1/{kind}/quota/introspect` uses null body
  and the console reads active. All remain out of scope.

The setting 3 and a Basic log quota 409 suggest a three-actions-a-month
notification budget; what it counts (orders, extension, recovery, delete,
failed attempts, shared Email/SMS) is unverified. Serialize billing probes,
reserve one action per mutation attempt, assume a shared budget of three,
stop when the remaining budget is unknown, and never probe a fourth write.

## Fixtures

The manager supplies sanitized raw responses, never decoded SDK output,
under `testdata/monitor/` with provenance (operation, HTTP status, live or
console, substitutions). Keep original keys and types. Replace numeric
account IDs with a synthetic number and string dimensions.user_id with
`<account>`; resources with synthetic IDs such as check-1 or quota-1 that
pass path checks; links with `<internal-url>`; names with synthetic names;
credential or payment URLs with placeholders. Preserve non-UUID package
IDs, integral decimals, nulls, offsets, and unknown keys.

Required fixture families, all decoded through the public SDK:

- Existing GetCheck plus definition with two locations and each location's
  history; a manager-supplied raw list response for ListChecks additions.
- Composite usage, Email and SMS package lists, synthetic quota/detail/usage
  and package list, Email quota and usage 404s.
- Email free and paid quote responses and synthetic renewal quote.
- Email v2 quotes for the free and a paid package, the v1 paid quote at
  periods 1 and 3, the live Email quota and usage successes, the free
  Email order response, the composite usage with an emailUsages item, and
  each gated SMS, extend, or recover success. Never reuse Email proof for
  SMS. Fixtures stay synthetic: no test assumes the account owns a quota.

Check required-key presence, not just json.Unmarshal success: null or empty
bodies cannot be a zero-valued success. Sanitation must keep valid route IDs
so path validation does not prevent the intended decode test. New SDK
Sensitive requests suppress ordinary capture hooks; only the manager's
separately authorized private capture produces raw fixture material.

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
  partial Output on a later error. Test every response and aggregate cap,
  fan-out refusal, and the manager-established pagination contract.
- Quota reads: nil Inputs only where empty, fixed Kind routing, unknown
  statuses preserved, numeric flags/counts validated, unknown usage kinds
  reported without raw JSON. Detail failures return nil Output. Check
  quota/detail identities and absence versus unknown endpoint 404s.
- Pricing: opaque package IDs, absent/null/negative/nonfinite prices,
  duplicate/unknown packages, ambiguous free default, invalid MaxPrice with
  zero requests, and synthetic quantity from capacity. Freeze one set of
  terms: the builder's quote request and mutation body both derive from
  it, `monthPeriod` is always 1, no mutated caller Input or later catalog
  read changes either, and no quote-only field reaches a mutation body.
  Decode the v2 response failing closed on a missing total. Compare 100000
  quote/110000 list at both budgets; free zero passes and zero quote on a
  priced package is Unpriced. Standalone quote ignores write-only controls
  and never sends an order.
- Writes: every supported mutation body and success status, 400/401/403/404/
  409/429, 500/502/503/504, dial failure, disconnect after send, cancellation,
  malformed/oversized 2xx, and every redirect including 307/308. Each makes
  at most one mutation attempt; 401 must not refresh and resend. Unseen
  status cases are deterministic robustness probes, not live claims.
- Renewal: required ExpectedEndTime, instant-equivalent offsets, stale
  expiry refusal, MaxDaysLeft read-only skip, overflow, positive versus zero
  threshold, expiry crossing during quote, ACTIVE extension, expired AND
  IN-TRASH recovery only when gated evidence permits it. No creation on 404,
  no guessed trash lookup, and no POC/auto-renew/unknown-status mutation.
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
  calls, and read reconciliation that stays unknown. Assert no second send.
  Attempt identity: after an unresolved renewal from E, a renewal with
  baseline E1 and a create on that kind each reconcile first and, once the
  prior renewal is confirmed, return ErrInvalidInput naming it, not
  `Renewed: true` or a new send; a matching renewal from E returns the
  confirmed outcome; an inconclusive read returns ErrNotSettled for both.
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

## Live checks before code

The manager gets approval per run under
[live data](../../instructions/live-data.md), naming account, resource,
maximum cost, action consumption, and retention. A kept free quota is not
disposable. No code brief consumes inferred schemas; SDK live verification
after implementation needs separate approval.

1. Read prerequisites: obtain existing raw fixtures and inspect all required
   fields/types. The range, interval, reversed-range, empty-window, and
   unknown-ID probes are done. Paging stays unknown until an alarm holds
   more than one transition; the read ships with that documented limit
   under owner decision 9. Do not rerun captured reads without need.
2. Pricing prerequisites: done for Email (v2 free and paid, v1 paid at
   periods 1 and 3, v1 500 on free). Still open: the SMS v2 quotes and the
   Email extension quote with `resourceId`. Do not try an order whose
   quote mapping is unestablished.
3. Free Email: done once, owner-chosen and kept; order, settlement, quota,
   usage, and delivery are recorded above. Still open: billing-side
   confirmation of zero debit and of the action budget, and the unexplained
   fourth usage count. Do not order again on this account.
4. Free synthetic extension: approve one active-quota extension, quote it,
   capture success response and old/new EndTime, identity, and settlement.
   Verify month arithmetic and actual debit. Free Email extension follows
   only with separate approval and a known remaining action budget.
5. Recovery: wait for a naturally lapsed, owner-approved quota. Capture its
   ordinary GET and trash listing before any recovery; establish resource
   identity, quote, response, and settlement. No deliberate delete/expiry of
   production monitoring to obtain evidence. An unevidenced branch stays out.
6. SMS and paid operations: obtain kind-specific reads/quotes first and
   explicit mutation/cost approval. Reconcile actual debit, tax, term, and
   quantity against quote and list; never treat max(list, quote) as proven
   protection without this. Confirm budget accounting without exhausting it.

No listed check is complete beyond the recorded responses. Open: every
write response but the free Email create, SMS and extension quotes, zero
and paid debit from billing, renewal arithmetic, trash recovery, monthly
accounting, history paging and truncation, and the unexplained email count.
