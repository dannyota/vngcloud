# vStorage projects

Status: Accepted (2026-10-10). The paid check passed on 2026-10-10; see
[paid check record](storage-projects-api.md#paid-check-record).

Add regional project prices, a create quote, a guarded purchase, and an
empty-project delete to `storage` and the CLI. The owner's approval replaces
[storage decision 3](storage-decisions.md#owner-decisions). Resizing, manual
renewal orders, restoring, purging, pay as you go, trials, proof-of-concept
orders, and automatic growth remain out of scope. Creation sets auto-renew
off; settings for existing projects follow [auto-renew](auto-renew.md).

[API evidence](storage-projects-api.md) owns the exact routes, query keys,
wire fields, configuration values, and price probes. The manager supplied
read-only console discovery on 2026-10-10; no order was sent. The SDK uses
`paymentType: "auto"` for its first and only order attempt, even when the
regional `enable_iam_checkout` configuration is `"true"`.

## Release order

1. Project pricing: `ListProjectTypes`, `QuoteCreateProject`, CLI commands,
   sanitized fixtures, example reads, and SDK and CLI wiki pages. The read
   contract is accepted for implementation independently of the paid check.
2. Project purchase and cleanup: `CreateProject`, `DeleteProject`, CLI,
   fixtures, and wiki pages. Implement and unit-test the candidate before
   the paid check. The SDK live test supplies the first and only order and
   records the remaining response contract. Ship after that test passes,
   the design incorporates its results, and an independent adversarial
   review passes. Checkout-only behavior defers create as described below.

Each release gets its own feature tag and green CI on the exact commit.
Existing public methods keep their contracts. New `Project` fields are
additive; retain existing types and JSON names. Service types stay in
`storage`; the root package adds no service re-exports.

## SDK reads

All methods live on `storage.Client`, with `storage.<Method>` operation
names. Each Input has `Region string`, using the existing region resolver:
`hcm-3` maps to `HCM04`, `han-1` to `HAN02`. Resolve to the vStorage UUID,
not a vServer region ID. An unknown region fails, with no fallback.

Every route uses the configured `Storage` endpoint and IAM bearer token.
Keep the existing `region` and `region_id` headers. Quote, order, delete,
and configuration calls also send the UUID as query `region_id`. Never
send literal `project_id=undefined`. `ListRegions` keeps its header exception.
All new responses use the storage envelope, including the quote's `data`.

### Project types with prices

`ListProjectTypes(*ListProjectTypesInput)` returns
`*ListProjectTypesOutput`, a `core.List[ProjectType]`; nil Input uses the
configured region. It joins these fresh reads for that region:

- `GET internal/v1/billing/project_types` supplies types and allowed periods.
- `GET internal/v1/billing/purchase_types` supplies the purchase names.
- `GET billing-api/v1/configurations?keys=<key>&region_id=<uuid>` supplies
  each required configuration. Request one key per call, avoiding any
  dependency on an undocumented multi-key separator.
- One price-only quote per active monthly offering at the minimum quota
  supplies its monthly price. There is no storage price in the type list.

Read `vos_billing_normal_min_quota`, `vos_billing_normal_max_quota`,
`max_project_per_user_per_region`, and `enable_iam_checkout`. Values are
strings; parse positive integer limits and exact `"true"`/`"false"` booleans.
Missing, duplicate, malformed, or inconsistent required limits fail closed.
Do not cache catalog prices or configuration. The configuration currently
sets 30 GB minimum, 2,000,000 GB maximum, and 20 projects per region.
These live values take precedence over conflicting public guides.

Each `ProjectType` exposes `ID`, `Name`, `Title`, `Group`, `Status`,
`StoragePolicy`, `AllowPeriod []int`, and `Offers []ProjectOffer`.
Each offer preserves `PurchaseTypeID`, `PurchaseTypeName` (the title),
`PriceKey`, nullable billable-resource `Period`, `MinQuotaGB`, `MaxQuotaGB`,
nullable `StepQuotaGB`, `QuotedQuotaGB`, `MonthlyPrice`, and `Currency`.
Use int64 for GB limits, float64 for VND prices, and `"VND"` from the
verified price contract. `QuotedQuotaGB` equals the configured minimum;
`MonthlyPrice` is that package's price, not a per-GB storage tariff.

`StepQuotaGB` is null: no recorded configuration or catalog field supplies
it. The Gold 31 GB probe proves that increment works; it does not establish
a general quota-step constraint. Integer GB is the SDK input granularity.
Keep SKU, SKU mappings, unit-price metadata, descriptions, and billable
resources in the decoded catalog model, with their API JSON names. Do not
interpret traffic unit prices as storage prices. Preserve metadata whose
shape is not specified in the evidence as `json.RawMessage`.

Match active purchase type `name: "Normal"`, title `"Pay monthly"`, and
billable resources with that purchase ID. Unknown purchase types remain
visible without a fabricated quote. A failed or unpriced supported offer
fails the operation rather than silently displaying zero. No type-details,
account-info, or proof-of-concept-info request is needed.

### Create Input and quote

`CreateProjectInput` has these fields:

| Field | Meaning |
|-|-|
| `Region string` | vStorage region; empty uses the configured mapping |
| `Name string` | Required for create only; sent as `projectName` |
| `Type string` | Required exact catalog `name`, such as `Gold` |
| `QuotaGB int64` | Required positive GB; no rounding or implicit minimum |
| `MaxPrice float64` | Maximum quoted total VND for the selected term |
| `NoWait bool` | Skip only the post-order readiness wait |

Every order in the first write release is for one month. `CreateProjectInput`
has no `Period` field. Keep `AllowPeriod` in the catalog read and require
that the selected type permits one month. Resolve the exact type name and
monthly purchase ID once. Gold is currently ID 1;
Instant Archive's input name is `Instant-Archive-2`, ID 7. Unknown,
disabled, or ambiguous types and purchases fail without an order.

Both quote and create reject quota below the configured minimum or above
the maximum locally, before the price POST. This is an explicit exception
to ADR 0002's server-owned limits: the price API prices Gold 29 GB despite
the 30 GB minimum. Missing limits are an error, not permission to order.

`QuoteCreateProject(*CreateProjectInput)` returns
`*QuoteCreateProjectOutput`. `Name` is optional and never sent to pricing;
`MaxPrice` and `NoWait` are ignored. Validate type, quota, and one-month
availability as for create, without duplicate-name or project-count reads.
The method is a read under [ADR 0002](../adr/0002-write-api-conventions.md),
sets `Idempotent`, and works with read-only profiles.

One resolver builds a purchase specification. Two separate serializers
project that specification into the price body and order body. Quote and
create use the same price serializer. Never send the order body to pricing:
`archivePeriod` and `billingTimeType` make its price zero. The price body
contains only `resourceType`, `action`, and `resourceInfo` with `quota`,
`purchaseTypeId`, and `projectType`. It needs no name or period.

Decode the envelope's `data.optimumPrice` with presence tracking. Missing,
null, malformed, or non-finite values fail with `*APIError`; zero and
negative values fail with `vngcloud.ErrUnpriced`. The output exposes:

- `OptimumPrice`, `OriginalPrice`, `DiscountPrice`, nullable
  `DiscountPercent`, and `Properties` from the response. Each property
  preserves `OptimumPrice`, `MonthlyPrice`, nullable `DiscountPercent`,
  nullable `Name`, and nullable `Description`.
- `MonthlyPrice`, equal to `OptimumPrice`, `TotalPrice`, equal to
  `MonthlyPrice` for the one-month term, and `Currency`, fixed to `"VND"`.

Compare `MaxPrice` to `TotalPrice` and reject a non-finite total. Gold 30 GB
quotes 30,000 VND for one month. The price API returns a monthly price even
when sent a longer period; `monthPeriod` is not a supported price selector.

A later release can add `Period` to the Input and CLI, validate it against
`AllowPeriod`, and compare `MaxPrice` to `MonthlyPrice * Period`. Keeping
both output prices now makes that addition additive. A future 12-month
Gold 30 GB order would have a quoted total of 360,000 VND; this release
cannot request it. No multi-month debit has been verified live.

## Purchase

`CreateProjectOutput` holds `Project *Project`, `OrderID string`,
`MonthlyPrice float64`, and `TotalPrice float64`. Nil `Project` means no
read confirmed it. Populate `OrderID` only from an identified response
field; do not infer it from a redirect URL. Preserve known fields in
partial outputs on post-order errors. A redirect is not purchase success.

Extend `Project` with these observed fields:

| Go field | API JSON field |
|-|-|
| `ProjectType int` | `projectType` |
| `ProjectTypeName string` | `projectTypeName` |
| `PurchaseTypeID int` | `purchaseTypeId` |
| `PurchaseTypeName string` | `purchaseTypeName` |
| `EnableAutoRenew *bool` | `enableAutoRenew` |
| `AutoRenewPeriod *int` | `autoRenewPeriod` |

Pointers distinguish missing renewal settings from false or zero. Preserve
raw presence for identity, type, and quota checks. Existing `Period` zero,
which can represent null, does not establish a one-month term.

1. The CLI rejects a read-only profile before authentication or any request.
   The SDK checks required fields and a finite nonnegative `MaxPrice`
   before any request. Default `MaxPrice` is 0 and cannot buy a project.
2. Resolve region and fresh configuration/catalog. Check quota and that
   the type permits one month.
   Read the region's complete project list. Refuse an exact duplicate name
   or count at the configured maximum. Failed or incomplete reads stop.
   Never remove an existing project to make room.
3. Build one resolved specification. Send its price-only body and require
   a positive monthly quote and finite total. If total exceeds `MaxPrice`,
   return `vngcloud.ErrPriceAboveMax` naming total and cap. Equality passes.
4. Send the [order body](storage-projects-api.md#order) with
   `paymentType: "auto"`, `enableAutoRenew: false`, `autoRenewPeriod: 0`,
   `archivePeriod: 0`, and no `period`, matching the console order that
   bought a one-month project. Always send
   `isTrial: false`, `isPoc: false`, and `billingTimeType: "block"`.
   No configuration may turn renewal on or select manual checkout instead.
5. Set `transport.Request.Once`: no resend after any status, failed dial,
   network loss, 401, 429, or redirect. No SDK or CLI outer retry exists.
6. Interpret the result through the branches in Discovery. Confirm the
   project's identity, active state, requested region/type/quota, and
   auto-renew false by reads. Do not follow `data.redirectUrl`, submit a
   manual order, make a separate payment, or open a browser.

The order carries no price cap or quote token in the captured contract.
Quote immediately before the single order and compare `MaxPrice` to the
quoted total, following the existing paid-write convention. This is not a
server-enforced debit limit; the quote-to-order race remains. Do not
promise an atomic spending cap. Record a different observed debit as a
failed paid check, clean up only the created empty project, and defer
release until the difference is explained and the design is corrected.

### Recovery and waiting

A lost, empty, malformed, or unrecognized response may hide an order.
Return `storage.ErrNotSettled` with the underlying error and the instruction:
"Order may exist; run storage list-projects in the same region before
retrying. Check pending orders and payment history if no project appears.
Do not submit another order while payment or provisioning is unresolved."
An empty project list alone is not proof that an order failed.

Prefer a returned project ID whose field has been confirmed. Otherwise
require one new exact-name match in the same region, absent before the
order, with matching type and quota. More than one match fails confirmation.
Duplicate-name guards do not provide cross-client idempotency. Do not
select or delete another caller's resource to settle uncertainty.

Use the existing observed active project status 1 as the candidate success
state, checking identity and explicit auto-renew false as well. The paid
check records its transitions. Poll every 2 seconds for at most 120 seconds
after create and 60 seconds after delete, with context and injected clock.
Unknown statuses keep polling; failed reads are not proof of success.
Timeout, cancellation, or failed confirmation returns partial output and
`ErrNotSettled`, with no resend. Any proven terminal failure returns
`storage.ErrFailed`; do not invent a failure enum before observing one.

`NoWait` skips readiness polling only. It does not skip the quote, guards,
or response classification. Return the accepted order's partial output
without claiming the project is active; the order always explicitly
disables renewal. If a returned project reports renewal true, fail even
with `NoWait`. Default waiting requires a project read showing false.

## Delete

`DeleteProjectInput` has `Region`, required `ProjectID`, and `NoWait`.
`DeleteProjectOutput` is empty. Send
`DELETE internal/v1/projects/{projectId}?region_id=<uuid>` with JSON `{}`.
Require an envelope with `success: true`; do not mistake HTTP success or
an empty body for acceptance. The console's `delete me` is UI-only and
never enters the body. The candidate accepts HTTP 200 with this envelope;
any other status is handled as an error until recorded by the paid check.

The CLI requires `--yes` and a writable profile before any request. Check
`ProjectID` with `core.CheckPathID` before region lookup. Find the ID in a
complete regional project list; confirmed absence returns `ErrNotFound`.
A malformed or incomplete response is not absence.

List all buckets using the existing `limit=1000` request. Any bucket, even
an empty one, returns `storage.ErrProjectNotEmpty`, with no DELETE. Failed
reads, `isNext: true`, absent/null lists, or malformed envelopes also stop.
Use strict presence checking inside this guard without changing existing
`ListBuckets` behavior. No force, cascade, or implicit bucket deletion.

Stop bucket writers before deleting: a bucket created after the pre-read
can race the DELETE. No server precondition is established by the captured
call. Send DELETE with `Once` and confirm by reads only. A lost response
returns `ErrNotSettled`; list projects and inspect billing before another
attempt. A subsequent independent call repeats all pre-reads.

An absent ID in a complete active-project list confirms removal there,
not immediate purge. Public docs describe seven days in free trash. A
prior live delete refunded unused value immediately, 29,895 of 30,000 VND
after about 9.5 hours. This is evidence of refund behavior, not a fixed
refund formula. The new paid check reconciles its own balance. Do not
purge, restore, or delete shared keys and service accounts implicitly.

## CLI and errors

Commands are `storage list-project-types`, `quote-create-project`,
`create-project`, and `delete-project`. Quotes and catalog pricing are
reads despite their price POSTs. Both writes use the read-only gate.
Create uses `--max-price` as consent; delete requires `--yes`. No prompts.

Keep the existing storage region convention: global `--region hcm-3` maps
to `HCM04`; Input `Region` is available through `--cli-input-json`, without
a colliding local flag. Delete maps global `--project-id`; create and quote
need no existing project ID. There is no `--period` flag in this release.

```sh
vngcloud storage list-project-types --region hcm-3
vngcloud storage quote-create-project --region hcm-3 \
  --type Gold --quota-gb 30
vngcloud storage create-project --region hcm-3 \
  --name example-storage --type Gold --quota-gb 30 --max-price 30000
vngcloud storage delete-project --region hcm-3 --project-id <id> --yes
```

Tables show the offered type, purchase type, min/max/step quota, allowed
periods, quoted quota, monthly price, and currency. Null step means not
reported. Quotes show monthly and term totals separately and place no order.
Use normal JSON, text, table, and query output.

| Condition | Result and CLI exit |
|-|-|
| Invalid input, quota range, missing `--yes`, read-only | 2 |
| Invalid/missing required configuration | `*APIError`, 1 |
| Zero or negative quote | `vngcloud.ErrUnpriced`, `Unpriced`, 1 |
| Missing, null, malformed, non-finite price or total | `*APIError`, 1 |
| Quoted total above cap | `vngcloud.ErrPriceAboveMax`, 1 |
| Project holds any bucket | `storage.ErrProjectNotEmpty`, 1 |
| Confirmed missing project | `ErrNotFound`, `NotFound`, 4 |
| Accepted or ambiguous order not confirmed | `ErrNotSettled`, 1 |
| Proven failed provisioning | `storage.ErrFailed`, `WriteFailed`, 1 |
| Checkout required instead of direct purchase | `ErrPaymentRequired`, 1 |
| Confirmed server refusal | Existing storage `*APIError` mapping |

CLI codes include `InvalidUsage`, `PriceAboveMax`, `ProjectNotEmpty`,
`NotSettled`, and `PaymentRequired`. Broaden `storage.ErrNotSettled`'s wording
while preserving its value and `errors.Is` compatibility. Never infer
payment state from English error text alone. Treat order responses as
sensitive: capture privately for this check, never expose checkout tokens
through debug, public fixtures, or routine output.

## Discovery

The paid check on 2026-10-10 answered question 1 with acceptance and
question 3 with an immediately active project; see the
[paid check record](storage-projects-api.md#paid-check-record).

1. **Does IAM `auto` debit balance with `enable_iam_checkout: "true"`?**
   If it accepts and provisions the paid project, confirm by reads and
   return it. If auto is refused, return the refusal without retrying and
   defer create. If only a checkout URL is returned and no paid project
   exists, return `ErrPaymentRequired` without retrying and defer create.
   Neither outcome permits shipping create until the design changes.
   Never send a manual order. If a debit, hold, or project appears despite
   a refusal, preserve the refusal under `ErrNotSettled` and reconcile the
   same attempt before any future purchase; create remains deferred.
2. **What does `data` contain for auto and manual checkout?**
   Record the auto response's exact ID fields and redirect destination.
   An ID supports direct lookup; no ID uses the guarded name lookup. A
   service-page redirect plus a confirmed paid project can succeed without
   following the URL. A checkout redirect alone is `ErrPaymentRequired`
   only when the absence of a paid project is established; otherwise the
   result is `ErrNotSettled`. An unrecognized response fails closed. The
   console reads manual `data.redirectUrl`, but its exact payload is not
   live-verified. Infer nothing about it from auto. If auto exposes that
   checkout response, record it; otherwise manual remains untested and out
   of scope. No manual probe is required to ship a working auto path.
3. **How long until the new project is active?**
   If it is already active, return after confirmation. If it is pending,
   poll within the stated bound. A confirmed failure returns `ErrFailed`;
   unknown status, timeout, missing renewal state, or a failed read returns
   `ErrNotSettled`, preserving known IDs. Never resend to shorten the wait.
4. **What does a refused or unpaid order leave behind?**
   Read the same attempt's project, order, and credit state privately. A
   proven clean rejection leaves only its error. A pending order, credit
   hold, charge, or unconfirmed expiry is a named leftover and blocks
   create release. A known empty project can be deleted by its ID. If an
   unpaid order expires harmlessly, record its lifetime and proof; if it
   needs cancellation, use only a verified recovery path under the run's
   scope. An unexplained obligation stops the test for owner handling.
   Never claim "no charge" solely from a URL, refusal, or empty list.

## Paid live check

The SDK live test is the first and only order under the approval: one Gold
30 GB project in `HCM04`, one month, `paymentType: "auto"`, with
`MaxPrice: 30000`. It performs discovery and live SDK verification in the
same attempt. No preceding console purchase, separate manual probe, or
second order is allowed. CLI request mapping is covered by unit tests.

Run only the dedicated `TestLiveWriteStorageProject` with
`VNGCLOUD_LIVE_WRITE=1` and `VNGCLOUD_LIVE_STORAGE_PROJECT=1`, following
[live-data](../../instructions/live-data.md). Keep the account private.

1. Record baseline projects, balance, and relevant transactions privately.
   Pause unrelated spending for reconciliation. Read fresh configuration
   and catalog; confirm room under the regional project limit. Use a
   unique `vngcloud-live-<8 hex>` name. Create no buckets, keys, or objects.
2. Quote Gold 30 GB for one month through the SDK. Require exactly 30,000
   VND and a valid quota range. Inspect the candidate request construction
   in unit tests: auto, one-month fields, and explicit auto-renew false.
   Call SDK `CreateProject` once; its own fresh quote must still pass.
3. Capture the order response privately, read back the new project, and
   reconcile a single debit. Check identity, region, Gold, 30 GB, active
   status, and auto-renew false. Register cleanup as soon as the project
   is uniquely identified, including after a partial or unexpected result.
4. If auto is refused, returns checkout, times out, or behaves unexpectedly,
   do not retry, select manual, follow a URL, or pay in the browser. Record
   the attempt's known IDs and state; run read-only reconciliation. Clean
   up only a uniquely identified new empty project. Do not delete a
   guessed match or any baseline project. Fail the test even if cleanup
   succeeds. A clean refusal consumes the attempt; no automatic rerun.
5. For the successful path, call SDK `DeleteProject` on that ID after its
   bucket guard. Confirm absence from the active list. The CLI's `--yes`
   requirement is tested separately; the SDK has no interactive consent.
6. Reconcile initial balance minus debit plus refund, using the order's
   transaction history. Poll refund reads for at most 10 minutes. Expect
   unused-value refund, not exactly 30,000 VND; the earlier refund is not
   this run's expected constant. Missing or unexplained credit is a failed
   check with a named leftover, not a reason to repeat DELETE or create.

The private leftover report names project/order IDs when known, attempted
name and region otherwise, last status, charge or hold, renewal state,
and cleanup outcome. An unpaid order counts even without a project.
Uncertain cancellation/expiry, outstanding credit, or an undeleted project
stays open for the owner. Free trash is reported and left to its documented
automatic expiry; no purge or restore. Public output contains counts and
statuses only. Unresolved writes defer release, not trigger another order.

## Verification

Use sanitized raw fixtures and `httptest`. Test catalog joins and quote
fan-out, configuration string parsing, missing and conflicting limits,
region headers and query UUIDs, and envelope refusals. Gold 29 GB must
fail locally despite its positive server quote; test 30, 31, both quota
bounds, and values outside them. Do not fabricate a quota step.

Prove that quote and create share priced fields but send different bodies.
Assert the price body excludes all order-only fields. Cover missing/null,
zero, negative, malformed, and non-finite prices; one-month availability;
equal monthly and total prices; exactly-at-cap and over-cap totals.
Assert every order has `archivePeriod: 0`, no `period`, auto, and explicit
false renewal, trial, and POC. Verify the CLI exposes no `--period` flag.

Count order attempts for 401, 429, 5xx, dial failure, lost response,
redirect, cancellation, and malformed success: at most one, with no manual
fallback or payment request. Cover response branches, partial outputs,
wait bounds, `NoWait`, name ambiguity, and every bucket-delete guard.
Both CLI write gates must run before login or any request; quotes run
read-only. Test `--yes`, outputs, errors, and secret redaction. Update wiki
pages and run `make check` during implementation. Independent write review
checks price races, bucket races, no resends, and leftover handling.

## Recorded decisions

The manager recorded these decisions under the owner's feature approval
on 2026-10-10.

1. Periods are not exposed in the first write release. Every order is one
   month, with no Input `Period` or CLI `--period`. Keep catalog
   `AllowPeriod` and both output prices, equal for one month. A later
   release can add periods with `MaxPrice` compared to monthly price times
   months.
2. If auto is refused or returns only checkout with no paid project, defer
   create. Return the refusal or `ErrPaymentRequired`, without retrying.
   Never send a manual order. Do not ship create until the design changes.
3. Follow the existing paid-write price convention: quote immediately
   before the single order, compare `MaxPrice` to the quoted total, and
   document the quote-to-order price gap. The guard is not an atomic cap
   on the server's debit.
