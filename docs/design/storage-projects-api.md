# vStorage projects: API evidence

Status: Accepted (2026-10-10) for project pricing; purchase and cleanup
accepted, gated on the paid check

Evidence for [vStorage projects](storage-projects.md). The manager supplied
read-only discovery from an IAM console session on 2026-10-10, including
price probes and public console code. No order was sent during discovery.
The earlier delete/refund observation below is separate evidence. This
revision makes no API calls. Public research and commit references follow
below; console routes take precedence over different external API shapes.

## Console contract

Every path below is relative to the SDK's `Storage` endpoint,
`https://vstorage.console.greennode.ai/`. Console code names `internal/v1`
`VOS_API_URL` and `internal/v2` `VOS_API_V2_URL`. All calls use the IAM
bearer token. The create form is `/storage/projects/create-project`.

Existing storage calls send `region` and `region_id` headers with the same
vStorage region UUID; retain them on the new storage-scoped calls.
`ListRegions` retains its exception. Query `region_id` on the calls below
is also that UUID, resolved by the storage region read. Do not substitute
a vServer region ID or send account-ID headers.

Responses use `{code, success, datas | data, errorMsg}`. HTTP 200 alone is
not success. Preserve existing storage envelope errors and credential
redaction. Billing price is also enveloped; its prices are in `data`.
`storage.exchange` currently lacks an `Idempotent` option; add quote read
retry support without changing write retry rules.

## Configuration and purchase types

The form reads `GET billing-api/v1/configurations?keys=...`, with either
`project_id=undefined` or `region_id=<uuid>`. The SDK uses the latter and
requests one key per GET: `keys=<key>&region_id=<uuid>`. Do not send the
console's literal undefined project ID. Responses contain
`datas: [{key, value}]`, with both fields strings.

| Key | Observed string value |
|-|-|
| `vos_billing_normal_min_quota` | `"30"` |
| `vos_billing_normal_max_quota` | `"2000000"` |
| `max_project_per_user_per_region` | `"20"` |
| `enable_iam_checkout` | `"true"`, per region |
| `trial_period_days` | `"7"` |
| `vos_trial_quota` | `"100"` |
| `billing_payg_default_quota` | `"200"` |
| `storage_base` | `"1024"` |
| `vng_storage_base` | `"1000"` |
| `iam_enable_feature` | `"false"` |

Only the first four keys are needed for this design. Limits are dynamic;
these values are observations, not constants. Trial and PAYG configuration
never changes the monthly-order defaults. The storage-base settings do not
require converting the quota: the confirmed request expresses GB directly.

`GET internal/v1/billing/purchase_types` returns `datas` items with `id`,
`name`, `title`, `status`, `descriptionEn`, `descriptionVi`, `order`, and
`isDefault`. The observed item has ID 4, name `Normal`, title `Pay monthly`,
status 1, order 1, and `isDefault: true`.

The form also reads `GET billing-api/v1/users/info` and
`GET billing-api/v1/users/poc-info?resourceType=object_storage`. Those
responses hold account data, including user ID/name and billing state.
Do not call or model them for catalog pricing or create construction.

## Project types

`GET internal/v1/billing/project_types` returns `datas` items with these
keys: `id`, `name`, `title`, `group`, `descriptionEn`, `descriptionVi`,
`status`, `storageClass`, `allowPeriod`, `sku`, `skuMappings`,
`billingUnitPrice`, `billableResources`, `order`, and `isDefault`.

Gold has ID 1, name `Gold`, title `Gold Type`, group `Gold`, status 1, and
`storageClass.storagePolicy: "Gold"`. Its `allowPeriod` is
`[1,3,6,12,24,36]` months. `sku` is `["Gold-PAYG","vStorage-Gold"]`.
Its billable resource is:

```json
{
  "id": 6,
  "name": "Normal - Gold",
  "priceKey": "objng-quota",
  "purchaseTypeId": 4,
  "projectTypeId": 1,
  "period": null
}
```

`billingUnitPrice["vStorage-Gold"]` reports these numeric metadata values:

| Key | Value |
|-|-|
| `traffic_free_multiples` | 10 |
| `traffic_unit_price` | 280 |
| `request_unlimited` | 1 |
| `request_block` | 1000 |
| `excess_domestic_traffic_all` | 280 |
| `excess_international_traffic_1_100` | 580 |
| `excess_international_traffic_101_200` | 510 |
| `excess_international_traffic_201_500` | 420 |
| `excess_international_traffic_501_1000` | 370 |
| `excess_international_traffic_gte1001` | 330 |

Instant Archive has ID 7, name `Instant-Archive-2`, title
`Instant Archive Type`, group `Instant Archive`, and status 1. Its storage
policy is `Instant Archive`; `allowPeriod` is `[1,3,6,12,24,36]` months.
`sku` is `["Instant-Archive-PAYG","Instant-Archive-2"]`. Its billable
resource has ID 21, name `Normal - Instant Archive 2`, price key
`Instant-archive-2`, purchase type ID 4, and project type ID 7. No period
value was supplied for this item; preserve missing/null as nullable.

The list has no per-GB storage price or quota-step field. Join the quota
configuration and monthly purchase type, then quote the minimum package
for each active monthly offering to provide prices in `ListProjectTypes`.
Do not derive storage cost from `billingUnitPrice` traffic values. Preserve
uninterpreted metadata such as `skuMappings` without inventing a shape.
The console defines `GET internal/v1/billing/project_types/{id}/details`,
but discovery did not read it and the SDK does not need it here.

## Price

Send `POST billing-api/v2/price?region_id=<uuid>` with this price-only body:

```json
{
  "resourceType": "object_storage",
  "action": "create",
  "resourceInfo": {
    "quota": 30,
    "purchaseTypeId": 4,
    "projectType": 1
  }
}
```

The console price-request class allows `resourceInfo` fields `isPoc`,
`period`, `purchaseTypeId`, `projectType`, `quota`, `trialDays`, `startTime`,
and `endTime`. The SDK needs only the three fields above for this scope.
The price POST is idempotent and places no order.

Envelope `data` holds `optimumPrice`, `discountPercent`, `originalPrice`,
`discountPrice`, and `propertiesPrice`. Each property holds `optimumPrice`,
`monthlyPrice`, `discountPercent`, `name`, and `description`. Decode prices
as numbers with presence checks; preserve nullable discounts and text.
`data.optimumPrice` is VND per month, not a whole-term price. Currency is
known from this contract; do not require a nonexistent currency field.

The manager's price probes returned:

| `resourceInfo` | Monthly `optimumPrice`, VND |
|-|-|
| Gold ID 1, quota 30, purchase ID 4 | 30000 |
| Gold, quota 31 | 31000 |
| Gold, quota 29 | 29000 |
| Instant Archive ID 7, quota 30 | 15900 |
| Gold, quota 30, `period: 12` | 30000 |
| Gold, quota 30, `monthPeriod: 12` | 30000 |
| Gold, quota 30, order-only fields added | 0 |

The final row adds `archivePeriod: 0` and `billingTimeType: "block"`:
all prices become zero and property name is null. Never price the order
body. The positive 29 GB quote proves pricing does not enforce the minimum;
both SDK quote and create must enforce the configured min/max quota.
The 31 GB result is not a documented general step constraint.

The first write release fixes every order to one month, so `MonthlyPrice`
and `TotalPrice` both equal `optimumPrice`. Keep both output fields and
catalog `AllowPeriod`, but expose no Input `Period` or CLI `--period`.
A later release can add months with `TotalPrice = MonthlyPrice * Period`
and `MaxPrice` compared to that total. Reject zero or missing monthly
prices and non-finite totals.

Quote immediately before the single order and compare `MaxPrice` to the
quoted total, following the accepted paid-write convention. The captured
order carries no quote lock or spending cap; the quote-to-order price gap
remains and must be documented.

## Order

Console `createOrder()` sets `paymentType` to `"auto"` only for an IAM
user when `enable_iam_checkout` is false; otherwise it uses `"manual"`.
The observed region currently enables IAM checkout, so the browser would
choose manual. The SDK deliberately chooses auto first to test direct
balance payment. It does not change the configuration or fall back to a
second manual order if refused.

The request is `POST internal/v2/orders?region_id=<uuid>`. The order class
has `resourceInfo` fields `projectName`, `purchaseTypeId`, `projectType`,
`projectTypeGroup`, `quota`, `period`, `isTrial`, `trialDays`, `isPoc`,
`enableAutoRenew`, `autoRenewPeriod`, `startTime`, and `endTime`.
Its defaults include `isTrial: false`, `isPoc: false`, and the unsafe
`enableAutoRenew: true`. `createOrder()` adds `billingTimeType: "block"`
and sets `archivePeriod` to the selected period. This selected-period
rule supersedes the older zero in [Purchase](storage-api.md#purchase).

The SDK's one-month Gold 30 GB order is explicitly constructed as:

```json
{
  "resourceType": "object_storage",
  "action": "create",
  "paymentType": "auto",
  "resourceInfo": {
    "projectName": "<name>",
    "purchaseTypeId": 4,
    "projectType": 1,
    "projectTypeGroup": "Gold",
    "quota": 30,
    "archivePeriod": 0,
    "billingTimeType": "block",
    "isTrial": false,
    "isPoc": false,
    "enableAutoRenew": false,
    "autoRenewPeriod": 0
  }
}
```

The SDK sends `archivePeriod: 0` and omits `period`, as the console order
that bought a one-month project on 2026-10-09 did (see
[vStorage API](storage-api.md#purchase)); it takes group from the selected
type. It omits trialDays, startTime, and
endTime because this is neither a trial nor a scheduled purchase.
`autoRenewPeriod: 0` is the SDK's explicit disabled setting, consistent
with the existing project read; order acceptance is tested once live.
The request above is the chosen SDK contract from console fields, not a
claim that an order with these values has already succeeded.

On envelope success the browser navigates to `data.redirectUrl`; on
failure it displays `errorMsg`. Neither branch proves debit or resource
creation. Auto's exact `data` shape and acceptance with IAM checkout enabled
remain for the paid check. Manual's exact response is also unverified; no
separate manual probe is authorized or required for an auto-only release.
Do not send vMonitor's `pay` field or follow the redirect automatically.
If auto is refused, return the refusal without retrying and defer create.
If only a checkout URL is returned and no paid project exists, return
`ErrPaymentRequired` without retrying and defer create. Neither outcome
permits shipping create until the design changes. Never send a manual
order. Reconcile any uncertain side effects from the same attempt, as
specified in the [recorded decisions](storage-projects.md#recorded-decisions).

Console resize uses
`PUT internal/v2/orders/{projectId}/resize?region_id=<uuid>`; it is out
of scope.

## Delete and project confirmation

Console delete is
`DELETE internal/v1/projects/{projectId}?region_id=<uuid>`, with JSON `{}`.
The response is the envelope and the console checks `success`. The typed
`delete me` confirmation is UI-only. There is no confirmation string in
the request. The SDK uses `Once`, its bucket guard, and read confirmation;
it does not assume that repeat deletes or refunds are safe to replay.

An earlier live delete refunded unused value at once: 29,895 VND from a
30,000 VND purchase after about 9.5 hours. This confirms a refund path,
not a constant amount to assert in the new run. The paid SDK check reads
its own balance and transaction history after cleanup.

Existing project reads show numeric `projectType: 1`, `purchaseTypeId: 4`,
names `Gold` and `Pay monthly`, GB `totalQuota: 30.0`, status 1, and
`enableAutoRenew: false`, `autoRenewPeriod: 0`. `period` can be null.
`paymentMethod: 1` alone does not prove a debit. Compare the uniquely
identified new project and balance history; never treat an arbitrary
redirect or HTTP 200 as a paid, active project.

## Product documentation

The [HCM04 project guide][project-guide] documents choosing billing type,
quota, period, and auto-renew, then checkout. It describes deletion with
`delete me`, seven days of free trash retention, later permanent deletion,
and refund for deletion before the prepaid term ends. It documents enabling
and disabling auto-renew in the UI, not the API body.

The same guide says HCM04 allows ten projects, while
[HCM04 getting started][getting-started] says one. Both describe only
Instant Archive in the purchase steps. The
[pricing page][storage-pricing] lists Gold and Instant Archive packages,
with 30 GB minima and rates of 1,000 and 530 VND/GB/month. It also lists
pay-as-you-go offerings. These pages disagree with each other and with
the recorded account catalog. Read the actual regional offerings and
quota controls rather than encoding a public-page limit.

Gold 30 GB at 1,000 VND/GB/month agrees with the captured 30,000 VND
monthly quote. The SDK uses the API quote, not public tariff arithmetic.
The live configuration reports 20 projects per region, resolving the
public guides' conflicting limits for this account. Neither public tariff
nor captured metadata establishes a general quota-step rule.

The [online payment guide][online-payment] supports paying a whole order
from sufficient credit. Successful payment provisions resources and adds
payment and credit-history entries. It documents top-up and third-party
methods too, which this design excludes. A shared payment link lasts three
days; the guide does not give the underlying unpaid order's lifetime or
cancellation API. No balance-payment request shape appears there.

The [resource deletion guide][delete-resource] describes refunds of unused
value, with minute-based calculation and product-specific exceptions.
It directs users to payment history. It does not prove an exact vStorage
refund amount or settlement time. The [credit hold guide][credit-hold]
describes other services' holds, but supplies no vStorage monthly-order
contract. Do not infer that vStorage orders are harmless while unpaid.

## Public API reference

The [HCM04 external API][external-api] documents
`POST /api/v1/projects` on `hcm04-api.vstorage.vngcloud.vn`. Its body is:

| Field | Public schema |
|-|-|
| `projectName` | Required string |
| `projectType` | Required string, example `Gold\|Instant Archive` |
| `quotaInGBytes` | Required int64 |
| `archivePeriod` | Optional int32 |
| `enableAutoRenew` | Optional boolean |
| `isPoc` | Optional boolean |

Success statuses are 200 and 201. The 200 schema is
`ProjectCreatingResponse`, with `success`, `code`, `errorMsg`, and `data`
referencing `ProjectCreatingDTO`. Documented errors are 400, 401, 403,
404, and 500. That DTO contains `description`, `projectId`, `projectName`,
and integer `status`, with no payment URL or debit result. The spec also
has project list, details, quota, and resize.
No project DELETE path, internal order path, or payment call was found
in this HCM04 reference. These are documentation findings, not live tests.

The body differs from the console: `quotaInGBytes` versus `quota`, a string
type versus a numeric type, and no `paymentType`. The existing
[management API record](storage-api.md#management-apis) says the external
API needs a service-account token and IAM User reads return an empty 200.
Do not replace the console order with this external create. The internal
order's explicit auto-renew field is now independently confirmed by console
code, as recorded above; external field names are not its authority.

## Public repositories

Recursive file inventories, README files, and relevant text matches were
read at these commits. Searches covered `vstorage`, `object_storage`,
`internal/v2/orders`, and `billing-api/v2/price` in source and documentation.
No implementation buying or deleting a vStorage project was found in this
set. This is a bounded search, not a claim about every GreenNode repository.

- [vngcloud/docs][docs-tree], commit
  `0c10ca7a4521d5348dab82937443ed27f784d73c`: HCM04 project guide, getting
  started, storage pricing, and HAN02 project guide. The HCM04 one-versus-ten
  conflict also exists at this commit. Links to the three HCM04 source
  files are [project][project-source], [start][start-source], and
  [pricing][pricing-source]. The [HAN02 source][han02-source] also describes
  checkout, auto-renew, trash, and refunds; it is not evidence for HCM04
  payment request fields.
- [vngcloud/vngcloud-go-sdk][sdk-tree], commit
  `a76d5fc0fdbd31663c51a7ba5cb71c15b3604188`: vStorage references in
  network endpoint IDs and tests, not project purchase.
- [vngcloud/terraform-provider-vngcloud][tf-tree], commit
  `230bd7d346853b2aba33a9ec06f8c544b06fa8a2`: backup-location models and
  examples using an existing vStorage bucket for Terraform state; no
  vStorage project resource found.
- [vngcloud/greennode-mcp][mcp-tree], commit
  `c8029935de6f6432a34fb346d041fdc324fdb593`: VKS server and shared core;
  vStorage mentioned as an example future server.
- [GreenNodeHub/vngcloud-go-sdk][hub-sdk-tree], commit
  `c0431aacc942d58555b495bfc46854a452d3effa`: the same network-endpoint
  references, no project purchase found.
- [GreenNodeHub/greennode-cli][cli-tree], commit
  `713367a7e4e348e9c259ce2048e9149663d2e187`: no vStorage source match.
- [GreenNodeHub/greennode-mcp][hub-mcp-tree], commit
  `ca770f29e4b6eb2fe72248c6da6564a585ee4195`: vStorage backup destinations,
  monitoring hosts, and log mappings. Deleting a monitoring host is not
  deleting the underlying storage project.

The product and API websites have no exposed commit SHA. The pinned docs
repository above supplies source history for the named storage pages;
website billing pages were read separately and are dated observations.

## Local implementation references

- [Project reads](../../storage/projects.go),
  [region resolution and envelopes](../../storage/storage.go), and
  [bucket listing](../../storage/buckets.go): current region and decoding
  contracts. Missing list data currently decodes as an empty list, which
  requires extra presence checks in a destructive pre-read.
- [Bucket delete](../../storage/buckets_delete.go): guard, clock, and
  `ErrNotSettled`. Project deletion must refuse even an empty bucket.
- [vMonitor quote](../../monitor/logprojects.go) and
  [order](../../monitor/logprojects_write.go): one shared resource builder,
  `pay: true`, duplicate-name check, and post-order reads. The design's
  [payment account](monitor-alerts.md#log-projects) states that `pay: true`
  debits balance for IAM users and false returns a payment URL. Neither
  establishes a vStorage field or endpoint.
- [vLB create](../../loadbalancer/create_load_balancer.go) and
  [price guard](../../loadbalancer/price_guard.go): positive price checks,
  nullable price decoding, `Once`, and ambiguous-outcome recovery.
- [CLI registration](../../internal/cli/svc_storage.go): Input `Region`
  has no local flag; delete `ProjectID` uses the global flag.
- [Paid writes](vserver-paid-writes.md),
  [ADR 0002](../adr/0002-write-api-conventions.md), and
  [ADR 0003](../adr/0003-toggle-writes.md): quote classification, input
  sharing, destructive consent, and no resend with `Once`.

## Paid check record to complete

The SDK live test sends the first and only approved auto order. Record its
acceptance or refusal with IAM checkout enabled, exact `data` fields,
redirect meaning, readiness timing, and any refused/unpaid leftovers.
Manual data is recorded only if exposed by that same attempt; otherwise
it remains outside the supported auto path. No second order or checkout
payment is allowed to complete discovery. See
[Discovery](storage-projects.md#discovery) for each response branch.

The same test reads back the project, deletes it, and reconciles its refund.
Store raw and decoded output privately per
[live-data](../../instructions/live-data.md). Public fixtures replace
account IDs, order IDs, project names, checkout URLs, balances, cookies,
and tokens. Retain field types, error codes, and state transitions only.

[project-guide]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/cac-tinh-nang-cua-object-storage/lam-viec-voi-project.md
[getting-started]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/bat-dau-voi-object-storage/buoc-1-khoi-tao-project.md
[storage-pricing]: https://docs.greennode.ai/vstorage/object-storage/cach-tinh-phi.md
[online-payment]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment/online-payment.md
[delete-resource]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/resource-lifecycle-management/delete-resource.md
[credit-hold]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/payment/credit-hold.md
[external-api]: https://docs.api.greennode.ai/service-docs/vstorage-hcm04-api.html
[docs-tree]: https://github.com/vngcloud/docs/tree/0c10ca7a4521d
[sdk-tree]: https://github.com/vngcloud/vngcloud-go-sdk/tree/a76d5fc0fdbd
[tf-tree]: https://github.com/vngcloud/terraform-provider-vngcloud/tree/230bd7d34685
[mcp-tree]: https://github.com/vngcloud/greennode-mcp/tree/c8029935de6f
[hub-sdk-tree]: https://github.com/GreenNodeHub/vngcloud-go-sdk/tree/c0431aacc942
[cli-tree]: https://github.com/GreenNodeHub/greennode-cli/tree/713367a7e4e3
[hub-mcp-tree]: https://github.com/GreenNodeHub/greennode-mcp/tree/ca770f29e4b6
[project-source]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d/English/vstorage/object-storage/object-storage-hcm04/cac-tinh-nang-cua-object-storage/lam-viec-voi-project.md
[start-source]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d/English/vstorage/object-storage/object-storage-hcm04/bat-dau-voi-object-storage/buoc-1-khoi-tao-project.md
[pricing-source]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d/English/vstorage/object-storage/cach-tinh-phi.md
[han02-source]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d/English/vstorage/object-storage/object-storage-han02/features-of-object-storage/working-with-project.md
