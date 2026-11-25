# vMonitor diagnostics, dashboards, and quota quotes

Status: Accepted for the dashboard reads. The resize quote is next and
needs its own evidence. The other sections are held.

Ship dashboard reads and resize quotes independently. Hold location metrics,
dashboard delete, and saved-channel test send pending evidence.

## Evidence and shared rules

Paths are relative to the existing [Monitor endpoint](monitor.md#endpoint).
The [public API reference][api], [official MCP source][mcp], and
[public console JavaScript][console] are not live verification. MCP files
are under `src/vmonitor-mcp-server/greennode/vmonitor_mcp_server/`; see
`config.py` for prefixes. **Console code only** marks bundle-only details.

Use `monitor.Method(ctx, *MethodInput) (*MethodOutput, error)`, operation
`monitor.Method`, and matching kebab-case CLI commands. Extend the existing
package and operation table without changing current types or dependencies.
Validate path IDs, encode query values, and bound JSON reads. Never follow
links from a response.

**Fail closed.** A field used for identity, classification, scope, or price
must appear exactly once under its verified name and with its verified JSON
type, and must agree with the request where applicable. Otherwise stop
before the next request and return no partial Output. Use a body-free
`InvalidResponse` for bad peer data unless a fixed refusal is specified
below. Never infer custom or free from a Go zero value.

Use sensitive requests with fixed or status-derived errors, no peer error
text or codes, and no raw capture. As in `backup.GetPolicy`, check every
retained Output string before returning against all nonempty credentials
sent across attempts and steps, including after refresh. Check strings used
in later requests before sending. A hit returns fixed `InvalidResponse`
and nil Output. Apply this in the SDK before any CLI format or query.
See [security](../../instructions/security.md) and
[write conventions](../adr/0002-write-api-conventions.md).

## Check per-location diagnostics

**Partly found.** The [API reference][api] and MCP `statistic_handler.py`
confirm `GET vmonitor-api/api/v1/statistics/synthetics`, no body. Console
`634.7eff525cc086dd8f.js` and `245.db0f3f6e59fd44cf.js` send `name`,
`dimensions`, `statistics`, `start_time`, `end_time`, `group_by`, and
`alarm`; only the reference lists `period`. Console code only:
`478.527c60df14590d84.js` sends `syntt.http.response_time`,
`syntt_id:{check}`, `avg`, epoch milliseconds, `location_id`, and the
check's `history_alarm`. It reads `dimensions.location_id` and parses the
public schema's `statistics[].key`/`value` strings as times and numbers.

`GET vmonitor-uptime-manager/v1/uptimes/{check}` supplies `id`, `subtype`,
`locations`, and `history_alarm`. The manager's IAM probe of `GET
vmonitor-uptime-manager/v1/locations` returned a bare array with `id`,
`name`, and other keys. Neither GET needs a query or body. ID-only Output
needs no catalog GET. Existing alarm methods cover state and transitions,
not test results ([live evidence][check-evidence]).

No stored per-run GET was found. The [Synthetic guide][synthetics], MCP
`synthetic_uptime_handler.py`, and console `common.c7da0c5236a4c24a.js` show
**Run Test**, `POST vmonitor-uptime-manager/v1/uptimes/test`, no query.
Console code only: sends `type`, `subtype`, `config`, `options`, `locations`;
reads `location_id`, `metrics` (status code, success, response time), and
`meta_data.response_headers`/`response_body`. This runs a new probe. Instead,
observe GETs when opening a past row or changing location on a saved check.
Do not click **Run Test**. Without a stored-result route, keep per-run held.

The fixed HTTP response-time average moved to
[check response times](monitor-check-response-times.md), with check scope
verified by a two-check sample. Per-run results, other metrics, and status
code averages stay held; do not treat an average status code as an
individual response.

## Dashboards

**Found.** Under `vmonitor-api/api/v1`, the [API reference][api] and MCP
`dashboard_handler.py` confirm `GET /dashboards`, `GET /dashboards/{dashboard}`,
and `DELETE /dashboards/{dashboard}`. `479.a88874a06b0dea36.js` and
`873.5ff23a0f9762b7b2.js` bind `/v1/dashboards` and `/${id}` to those methods.
There is no body on any of these calls or query on the ID calls. The reference
shows 200/204 without a documented delete result; the console ignores its body.

- List sends `searching-text`, `searching-field`, `filter`, `page` (1-based),
  and `size`. The console reads `lstData`, `page`, `pageSize`, `totalItem`,
  and computes page count. The reference also lists `totalPage`. List rows in
  `189.9f4fe293c6602c65.js` read `id`, `name`, `system`, `favorite`,
  `updatedAt`, and `createdAt`.
- Get uses a bare dashboard, not a `data` wrapper. The detail UI reads `id`,
  `name`, `system`, `refreshActive`, `refreshInterval`, `timeRange`,
  `viewSelectedId`, and `widgets`. The reference also gives `createdAt`,
  `updatedAt`, `favorite`, `darkMode`, `timeRangeType`, and `createdUser`.
- Console code only: the user-created filter is `system:false`; resource
  list links use `systemType:productVng`. The list hides Delete based on
  `!row.system` and uses loose `0`/`1` comparisons in other cells. The
  published list and detail schemas declare boolean `system` but do not make
  it required. The [Dashboard guide][dash-guide] documents system-created
  default dashboards and custom-only deletion, not enforcement by DELETE.

**Observed with the IAM bearer** (read-only probes; types and counts only):
both list filters return 200 with integer `page`, `pageSize`, `totalItem`,
and `totalPage`. `page` and `pageSize` echo the request. With the generated
filter at size 1, pages 1 and 2 each returned one row with `system` true,
and both reported the same `totalItem` and an equal `totalPage`, so totals
count the filtered set. The custom filter returned no rows and zero totals
on both pages. Generated rows have
string `id`, `name`, `createdAt`, `updatedAt`, `timeRange`, `timeRangeType`;
boolean `system`, `favorite`, `refreshActive`, and sometimes `darkMode`;
integer `createdUser` and `refreshInterval`. `GET /dashboards/{id}` for a
generated dashboard returned a bare object with the same fields, `system`
true, and a `widgets` array. Custom rows and custom detail are unobserved.

**Proposed reads:** `ListDashboardsInput` has `Page, Size int` (defaults 1,
10) and `System bool` (`--system`). Custom is the default (`system:false`);
`--system` lists GreenNode-generated resource dashboards with
`systemType:productVng`, not the separate host or integration categories.
Never accept an unfiltered or free-form filter. Use blank search keys.
Each row's explicit boolean `system` must equal requested `System` or the
whole page fails with `InvalidResponse` and nil Output. Validate page
metadata against the request and row count. `ListDashboardsOutput` is
`core.PagedList[Dashboard]`; the size 1 probe verified filtered totals.
`Size` is 1 to 100; the service limit is unverified, so a larger value is
refused locally.

`GetDashboardInput` requires `DashboardID`. `GetDashboardOutput` holds a
`Dashboard` of either kind, after matching the ID and verifying the explicit
boolean `system`. Both reads project `ID`, `Name`, `CreatedAt`, `UpdatedAt`,
`Favorite`, and `System`; omit widgets, expressions, and creator/resource IDs.
`monitor list-dashboards` (`--page`, `--size`, `--system`) and
`monitor get-dashboard --dashboard-id ...` are Reads, no `--yes`.

**Held write:** `monitor delete-dashboard` stays out of the SDK and the CLI.
A fresh get followed by DELETE is not atomic, and nothing shows that the
service refuses to delete a generated dashboard or that a dashboard's class
and ID are stable. Deleting a dashboard the caller just created would not
prove either. Reopen it only with provider evidence of a server-side
refusal or an immutable class. Never send DELETE for a generated dashboard.

The reference, MCP source, and console also define `POST /dashboards` with a
JSON `name`. No create command is proposed, and no create-then-delete run is
planned: a create that returns no ID cannot be proven owned by name alone.
Custom rows and custom detail therefore stay unobserved. The reads accept
them only through the strict rules above, so an unexpected custom shape
fails with `InvalidResponse` instead of returning wrong data.

## Existing notification quota resize quote

**Found.** The console billing service in `main.cf29979d32ded065.js` sends
`POST billing-api/v2/{email|sms}/prices/resized-price`, no query, body
`{redirectUrl:"", packageId, quantity:1, resourceId}`. The resize dialogs in
`816.74dca5a88cd6daa9.js` quote the current package for comparison and the
selected target package, both with `resourceId` from the corresponding quota
`id`. MCP `quota_price_handler.py` confirms the v2 mapping and that a quote
places no order. Use this exact request, not `created-price` or the separate
`POST billing-api/v1/{email|sms}/quota/resize` order. Do not use `pay`,
`monthPeriod`, or an order redirect in the quote. Renewal is separate.

Console code only: the current-package result supplies `originalPrice`,
and the target supplies `originalPrice`, `discountPrice`, `optimumPrice`.
The UI labels a negative total as a refund, without proving entitlement.
`439.1ec476d151455f31.js` uses legacy `POST
billing-api/v1/{email|sms}/prices/resized-price?package_id=...`, null body,
no ID. It reads `originalCost`, `discountCost`, `discountRatio`,
`discountPercent`, `totalCost`, `packagePrice`, and `originalPackagePrice`.
Do not fall back. The [quota guides][notifications] describe upgrades only.

**Proposed:** `QuoteResizeNotificationQuotaInput` requires `Kind`,
`ExpectedQuotaID`, and target `PackageID`. `QuoteResizeNotificationQuotaOutput`
holds `Kind`, `ResourceID`, `CurrentPackageID`, `PackageID`, `Currency` (VND),
and required signed `OptimumPrice`, not the create/renew `QuotaQuote` guard.
`monitor quote-resize-notification-quota` takes `--kind`,
`--expected-quota-id`, and `--package-id`. It is Read, with no `--yes` even
though it uses POST. Quota and package GETs have no query or body; the
[live evidence](monitor-quotas-api.md#billing-reads) owns their fields.

Resolve kind to fixed `email`/`sms`; compare the current quota's `id` with
`ExpectedQuotaID`, `type` with `EMAIL`/`SMS` for that route, and `status`
with `ACTIVE`. In that kind's catalog require exactly one current package
matching `packageId` and exactly one target matching input `PackageID`.
They must differ and have non-negative integral `amount` values; the target
must be strictly higher. A well-formed quota or package eligibility guard
mismatch returns `ErrQuotaChanged`. Contradictory or malformed peer data,
including a kind mismatch or missing/duplicate catalog match, returns
`InvalidResponse`. Both paths return nil Output and send no quote POST.
Package IDs are opaque body values, not UUIDs.

Send only the evidenced method, path, and body with `NoRedirect`; no fallback
to create, renew, recover, or order. Set `Idempotent` for bounded read
retries only after the no-order semantics are verified live. Preserve signed
finite `OptimumPrice`, reject missing/null totals, and distinguish explicit
zero from absence. Do not subtract quotes client-side, present negative
amounts as approved refunds, or use the quote as an order budget. Omit
payment URLs, property text, raw responses, and reflected credentials.

**Manager quote observation (not a shape probe):** On an existing active
quota, inspect the upgrade dialog without confirming an order. For each kind
with a distinct higher package, send at most one exact price POST with
`Once` and `NoRedirect`; do not follow a 3xx, replay, or try another endpoint.
Record sanitized status, numeric field types and the total's sign. Verify
unchanged quota and no order or debit with read-only checks. Do not click the
order confirmation or buy a prerequisite. If billing or principal scope
cannot be verified, keep that kind held. Tax, proration, and refund
eligibility remain open; no quote authorizes a charge or refund.

## Saved-channel test send

**Not found.** `118.553307f85e686881.js` and `698.7ad87edd69bfa8b4.js`
implement `/v1/notification` CRUD and OTP, not a saved-channel test. MCP
`notification_handler.py` and the [Notification guides][notifications] show
no method, path, query/body fields, or response keys for a test send.
`POST notification-gateway/api/v1/notification/otps` sends a verification
code and `POST .../otps/validate` validates it. Neither tests a saved channel.
Do not substitute OTP, a check mutation, or a request to the recipient.

Inspect a saved channel's menu or its handler without triggering send. If
there is no test control or handler, ask the provider.

**Held:** `TestChannelInput{ChannelID}`, `TestChannelOutput{Accepted bool}`
if acknowledgment is defined. `monitor test-channel --channel-id ... --yes`
is a Write with destructive confirmation. Refuse read-only mode and any
message, address, header, or OTP override. Require evidenced type, quota
impact, cost and owner consent. Use `Once`; never resend, including after
401, redirect, or lost reply. Check receipt, not just acceptance. Never
expose recipient, headers, contents, or peer text.

## Manager shape probes

GET only. Captures stay private and feed later reads, never writes. Print
allowlisted key names and path-scoped booleans, bounded integers, or enums;
never print IDs or arbitrary values. Check ID matches, counts, and duplicate
keys locally without printing values. Without a valid capture, skip dependent
reads. Sample HTTP series only for a verified HTTP snapshot. Use
`{day_ago_ms}` instead of `{hour_ago_ms}` if the hour is empty.

```json
[
  {"label":"checks","product":"monitor","version":"vmonitor-uptime-manager/v1",
   "parts":["uptimes"],"query":{},"allowKeys":["id"],"reveal":[],
   "capture":{"check":"[0].id"}},
  {"label":"check-snapshot","product":"monitor",
   "version":"vmonitor-uptime-manager/v1","parts":["uptimes","{check}"],
   "query":{},"allowKeys":["id","subtype","locations","history_alarm"],
   "reveal":[{"path":"subtype","as":"enum","values":["HTTP"]}],
   "capture":{"history_alarm":"history_alarm"}},
  {"label":"http-response-time","product":"monitor",
   "version":"vmonitor-api/api/v1","parts":["statistics","synthetics"],
   "query":{"name":"syntt.http.response_time","dimensions":"syntt_id:{check}",
            "statistics":"avg","group_by":"location_id",
            "alarm":"{history_alarm}","start_time":"{hour_ago_ms}",
            "end_time":"{now_ms}"},
   "allowKeys":["statistics","key","value"],"reveal":[]},
  {"label":"custom-p1","product":"monitor",
   "version":"vmonitor-api/api/v1","parts":["dashboards"],
   "query":{"searching-text":"","searching-field":"","filter":"system:false",
            "page":"1","size":"1"},
   "allowKeys":["lstData","id","system","page","pageSize","totalItem",
                "totalPage"],
   "reveal":[{"path":"lstData[].system","as":"bool"},
             {"path":"page","as":"int","max":10},
             {"path":"pageSize","as":"int","max":10},
             {"path":"totalItem","as":"int","max":10000},
             {"path":"totalPage","as":"int","max":10000}]},
  {"label":"custom-p2","product":"monitor",
   "version":"vmonitor-api/api/v1","parts":["dashboards"],
   "query":{"searching-text":"","searching-field":"","filter":"system:false",
            "page":"2","size":"1"},
   "allowKeys":["lstData","id","system","page","pageSize","totalItem",
                "totalPage"],
   "reveal":[{"path":"lstData[].system","as":"bool"},
             {"path":"page","as":"int","max":10},
             {"path":"pageSize","as":"int","max":10},
             {"path":"totalItem","as":"int","max":10000},
             {"path":"totalPage","as":"int","max":10000}]},
  {"label":"generated-p1","product":"monitor",
   "version":"vmonitor-api/api/v1","parts":["dashboards"],
   "query":{"searching-text":"","searching-field":"","page":"1","size":"1",
            "filter":"systemType:productVng"},
   "allowKeys":["lstData","id","system","page","pageSize","totalItem",
                "totalPage"],
   "reveal":[{"path":"lstData[].system","as":"bool"},
             {"path":"page","as":"int","max":10},
             {"path":"pageSize","as":"int","max":10},
             {"path":"totalItem","as":"int","max":10000},
             {"path":"totalPage","as":"int","max":10000}],
   "capture":{"dashboard":"lstData[0].id"}},
  {"label":"generated-p2","product":"monitor",
   "version":"vmonitor-api/api/v1","parts":["dashboards"],
   "query":{"searching-text":"","searching-field":"","page":"2","size":"1",
            "filter":"systemType:productVng"},
   "allowKeys":["lstData","id","system","page","pageSize","totalItem",
                "totalPage"],
   "reveal":[{"path":"lstData[].system","as":"bool"},
             {"path":"page","as":"int","max":10},
             {"path":"pageSize","as":"int","max":10},
             {"path":"totalItem","as":"int","max":10000},
             {"path":"totalPage","as":"int","max":10000}]},
  {"label":"generated-detail","product":"monitor",
   "version":"vmonitor-api/api/v1","parts":["dashboards","{dashboard}"],
   "query":{},"allowKeys":["id","name","system","favorite","createdAt",
                           "updatedAt"],
   "reveal":[{"path":"system","as":"bool"}]},
  {"label":"email-quota","product":"monitor","version":"billing-api/v1",
   "parts":["email","quota"],"query":{},
   "allowKeys":["id","type","status","packageId"],
   "reveal":[{"path":"type","as":"enum","values":["EMAIL"]},
             {"path":"status","as":"enum","values":["ACTIVE"]}]},
  {"label":"email-packages","product":"monitor","version":"billing-api/v1",
   "parts":["email","packages"],"query":{},"allowKeys":["id","amount"],
   "reveal":[{"path":"[].amount","as":"int","max":10000}]},
  {"label":"sms-quota","product":"monitor","version":"billing-api/v1",
   "parts":["sms","quota"],"query":{},
   "allowKeys":["id","type","status","packageId"],
   "reveal":[{"path":"type","as":"enum","values":["SMS"]},
             {"path":"status","as":"enum","values":["ACTIVE"]}]},
  {"label":"sms-packages","product":"monitor","version":"billing-api/v1",
   "parts":["sms","packages"],"query":{},"allowKeys":["id","amount"],
   "reveal":[{"path":"[].amount","as":"int","max":10000}]}
]
```

Pass criteria in addition to 200 and the shared rule:

- `checks`: path-safe ID captured. `check-snapshot`: matching HTTP check,
  valid alarm and location IDs. `http-response-time`: points in the window
  attributable to this check/alarm; repeat for shared-location evidence.
- `custom-p1`, `custom-p2`, `generated-p1`, `generated-p2`: requested page
  and size, correct `system` value on each row, distinct IDs or empty after
  exhaustion, consistent totals matching a known filtered set. Empty custom
  pages alone do not qualify custom rows or nonempty paging.
- `generated-detail`: same captured ID, `system:true`, verified key types.
- `email-quota`, `sms-quota`: expected `EMAIL`/`SMS` type, `ACTIVE`, valid
  quota and package IDs. `email-packages`, `sms-packages`: one current and
  one distinct higher target per kind with non-negative integral allowances.
  Verify IDs and counts privately. No active quota means no quote sample.

## Release order and decisions

1. Dashboard list and get first. Hold list if filtered totals are unproven.
2. Resize quote for one kind at a time after its exact POST observation.
   Quote upgrades only; do not order or buy a missing quota to test.
3. Hold location metrics until sampling proves value and scope. Keep per-run
   results open without a stored-result route. Hold dashboard delete and
   channel test separately pending evidence, owner approval of each write
   contract and live test, and independent adversarial review.

Use synthetic fixtures and loopback tests for bounds, rule violations, and
read-only flags. Assert nil Output on bad dashboard rows and totals, zero
DELETEs on unsafe classification, zero POSTs on guard or shape failure, and
no other route or redirect on quote. Test reflected credentials across
attempts and steps, including tokens first sent after refresh. Assert fixed
errors and no leak in SDK Output, CLI json/table/text/`--query`, debug logs,
error strings, and capture hooks. For held writes, test no second send after
ambiguity. Check wiki consistency when code lands.

[check-evidence]: monitor-quotas-api.md#check-state-and-history
[api]: <https://docs.api.greennode.ai/service-docs/vmonitor-api.html>
[console]: <https://vmonitor.console.greennode.ai/>
[mcp]: <https://github.com/GreenNodeHub/greennode-mcp>
[dash-guide]: <https://docs.greennode.ai/vmonitor/dashboards/dashboard>
[synthetics]: <https://docs.greennode.ai/vmonitor/dashboards/synthetics>
[notifications]: <https://docs.greennode.ai/vmonitor/dashboards/notification>
