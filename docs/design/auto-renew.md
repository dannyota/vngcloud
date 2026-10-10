# Prepaid resource auto-renew

Status: Accepted (2026-10-10).

Add a central billing resource read and a guarded vStorage auto-renew
setting. Users can inspect a project's end date, renewal state, period,
and estimated next charge, then enable, change period, or disable renewal.
Manual one-period renewal orders remain deferred. No toggle places an
order, extends the current term, or guarantees that a later renewal succeeds.

This design accepts the billing resource read deferred in
[billing](billing.md#deferred) and the auto-renew setting deferred in
[vStorage projects](storage-projects.md). Creation still sets auto-renew
off. Existing create, quote, project list, and delete contracts stay intact.
Production resource details and account identifiers do not belong here.

## Surface and ownership

Choose `billing.ListResources` plus `storage.GetProjectAutoRenew` and
`storage.PutProjectAutoRenew`. The billing list describes resources across
products. The storage methods join the right regional project, apply its
eligibility rules, and price its quota without asking users for billing
implementation fields. A dedicated read supplies the CLI's complete view
without changing `ListProjects` into a billing and pricing fan-out.

Do not expose `billing.PutResourceAutoRenew` in this release. A generic
write would promise eligibility, periods, and prices for products whose
write contracts are unverified. A later vMonitor setting can reuse the
internal billing transport after its own design and live check.

Use a small `internal/billingresources` helper for the resource envelope,
list, account lookup, and single-resource PUT. Both service clients use
the same `core.Client` from their Config. The helper owns no public write
surface and imports no public service package. Service types remain in
`billing` and `storage`; root `vngcloud` adds no service re-exports. Do not
change existing IAM caller-identity behavior or its guards' cache.

All methods follow `Method(ctx, *Input) (*Output, error)`. Operation names
are `billing.ListResources`, `storage.GetProjectAutoRenew`, and
`storage.PutProjectAutoRenew`. Nested calls retain useful read operation
names; the PUT and its errors name the storage write.

## Wire evidence and boundaries

The manager verified the following on a disposable project on 2026-10-10.
The vStorage console opens the central billing console for auto-renew.
The resource list also includes vMonitor log and synthetic resources.
All billing paths below are relative to the configured `Billing` endpoint,
which defaults to `https://dashboard.console.greennode.ai/`.

| Action | Method and path |
|-|-|
| List | `GET gateway/api/v1/resources` |
| Resolve account | `GET gateway/api/v1/home/user-info` |
| Set auto-renew | `PUT gateway/api/v1/resources/autoRenew` |

Use the IAM bearer token, normal TLS verification, and no redirects. These
calls send no regional or project headers. Regional storage reads and
quotes keep their existing endpoint, region headers, and UUID query rules.

The resource response is an envelope with `code`, `message`, and `data`.
Inside `data`, the `data` array contains resource rows, followed by summary
counts and `extra` thresholds. No pagination or filter request contract is
established. Request the observed unfiltered list once per read; do not
invent page parameters or infer completeness from the status counts.

A PUT carries `portal-user-id` and a JSON array with exactly one object:

```json
[
  {
    "product": "vstorage",
    "artifactType": "object-storage",
    "artifactId": "project-1",
    "channel": 1,
    "autoRenewInfo": {"isEnable": true, "period": 43200}
  }
]
```

`channel: 1` is synthetic. Copy the target row's actual numeric channel,
including zero if explicitly present. Never default a missing channel or
accept a caller-supplied channel. Period is `43200 * months` in minutes;
this wire conversion uses 30 days per month, not calendar arithmetic.
Disable always sends `isEnable: false, period: 43200`.

Both verified settings returned HTTP 200, envelope code 200, and
`data: {"successAll": true, "errorAutoRenewResources": []}`. Enabling one
month yielded billing `AUTO-RENEW` with `renewPeriod: 1` within 2.5 seconds.
The storage read immediately showed true with period 1. Disabling yielded
billing `MANUAL` with null period. End time stayed unchanged and the toggle
caused no observed charge. Longer periods and period updates have console
evidence but still need the implementation's live check.

## Billing resource read

`ListResourcesInput` is empty; nil is allowed. `ListResourcesOutput` has
`Items []Resource`, nullable int64 counts `TotalAutoRenews`,
`TotalGoodResources`, `TotalWarningResources`, `TotalAlertedResources`,
`TotalExpiredResources`, and `Extra ResourceThresholds`.

`Resource` retains API JSON field names and these Go fields:

- Strings: `ArtifactID`, `ArtifactName`, `ArtifactType`, `Product`,
  `RenewType`, and `BillingType`.
- Nullable int64 values: `StartBillingTime`, `EndBillingTime`, `DateLeft`,
  `RenewPeriod`, and `Channel`. Times are epoch milliseconds. Presence is
  required where a storage guard uses a value; zero is not absence.
- `IsRenewing *bool` and `Cost *float64`. Cost has unknown meaning and
  currency; it is never a price, quote, next charge, or eligibility guard.
- `Status` and `StatusUI` as `json.RawMessage`, because the supplied
  evidence does not establish their types or complete enums.
- `BillingElements []ResourceBillingElement`, with string `SKU`, and
  `Quantity` and `MetaKey` as `json.RawMessage` until fixtures establish
  their shapes. Threshold fields `WarningThresholds` and
  `AlarmThresholds` also preserve raw JSON.

Drop `tags` from the public model and routine output. Tags contain creator
IAM identifiers and are unnecessary for the feature. Retain the wire keys
in sanitized raw fixtures to prove decoding safely ignores them.

Export constants for `NON-RENEWABLE`, `MANUAL`, and `AUTO-RENEW`. Preserve
unknown strings in reads. Require HTTP 200, numeric envelope code 200,
non-null object data, and a present non-null array at `data.data`; `[]` is
valid. Missing/null/malformed structure fails with `*APIError`. Do not use
the existing billing fallback for unenveloped responses on this route.
Unknown resource types remain readable. A malformed typed value fails the
read rather than becoming a false state, zero price, or absent project.

## Project read and output

`GetProjectAutoRenewInput` has required `ProjectID string`, `Region string`
(empty uses configured region), and optional `PeriodMonths *int` for a
price preview. Region resolution follows storage: `hcm-3` maps to `HCM04`.
Validate the ID with `core.CheckPathID` before any request. Never select a
project by name or default to another project when the ID is absent.

Read a complete regional project list and the billing resource list. Match
exactly one row by the tuple `(vstorage, object-storage, ProjectID)` and
exactly one regional project by ID. Verify region identity. Duplicate
matches fail. Missing regional project returns `ErrNotFound`; a project
with no matching billing row returns `*APIError`, not permission to write.
A partial, malformed, or conflicting list cannot establish absence.

Both storage methods return a `State *ProjectAutoRenew` field. The write
also returns `Changed bool`, true only after a sent request is confirmed.
Nil State means no valid joined observation exists. On later errors retain
the last valid observation; never fill state from the requested values.
`ProjectAutoRenew` exposes:

| Field | Meaning |
|-|-|
| `ProjectID`, `Region`, `ProjectName` | Regional project identity |
| `EndBillingTime int64` | Billing end time in epoch milliseconds |
| `EndTime time.Time` | The same instant, JSON in UTC RFC 3339 |
| `RenewType string` | Billing renewal enum |
| `Enabled *bool` | True for AUTO-RENEW, false for MANUAL; unknown otherwise |
| `PeriodMonths *int` | Configured months; null when off |
| `QuotePeriodMonths int` | Period used for the price preview |
| `MonthlyPrice *float64` | Fresh monthly project quote, including VAT |
| `QuotedRenewalCharge *float64` | Monthly price times quote period |
| `NextCharge *float64` | Estimate for the configured enabled period |
| `Currency string` | VND when priced; empty when unavailable |
| `PriceStatus string` | `Quoted` or `Unavailable` |
| `PriceErrorCode string` | Safe stable error code when unavailable |

Require a present valid billing end timestamp; do not use `dateLeft` to
calculate an end date or a charge date. Present UTC with its zone in table
output. Do not promise the scheduler charges exactly at that instant.
Require explicit storage `EnableAutoRenew` and agreement with billing for
MANUAL and AUTO-RENEW. AUTO-RENEW also requires matching positive periods
in both reads. For MANUAL, normalize the exposed period to null; tolerate
storage period null or zero, but require billing null. Unknown billing
states remain visible with Enabled null and block writes.

The price preview defaults to the current enabled period or one month
when off. An explicit read period changes the preview only. When enabled,
NextCharge always uses the configured period, even if preview differs.
When off or state is unknown, NextCharge is null, never zero. An off
project still shows a quoted renewal charge so the user can choose a cap.

Use `QuoteCreateProject` with the fresh project's region, exact catalog
type name, and quota. Verify the type ID/name and monthly purchase ID
against the catalog. Require a positive integral GB quota without rounding
or truncation. The quote is monthly; multiply by the selected period and
check that the result is finite and positive. Prices include VAT; do not
add tax again. Never substitute billing Cost or traffic prices.

The read returns state successfully when only pricing fails. Prices are
null, PriceStatus is Unavailable, and PriceErrorCode explains the category
without raw response text. Cancellation and failed identity/state reads
remain operation errors. A failed quote never disguises stale pricing as
current pricing. Label NextCharge as an estimate from today's create quote;
renewal discounts, future tariffs, and successful payment are not verified.

## Write input and consent

`PutProjectAutoRenewInput` has required `ProjectID string`, `Region string`,
required `Enabled *bool`, optional `PeriodMonths *int`, and
`MaxPrice float64`. A required pointer makes omission distinct from an
explicit disable, including in CLI input JSON. Nil Enabled fails before
any request. There is no account, channel, product, force, or NoWait input.

When enabling from MANUAL, omitted period means one month. When already
enabled, omitted period keeps the current period; an explicit different
period updates it. Accept 1, 3, 6, or 12 months. Reject a period on disable
rather than silently ignore it. MaxPrice must be finite and nonnegative;
zero is the default and cannot authorize an enable or period change.

The first write supports ordinary `object-storage` projects using the
catalog's active `Normal` monthly purchase. Exclude trial, POC, other
purchase modes, and fixed-period artifacts. Do not map console enum names
`OBJECT_STORAGE_6` or `OBJECT_STORAGE_12` to guessed wire values. Their
mapping and eligibility need separate evidence before write support.
Unknown purchase identities fail closed. Do not infer trial or POC
eligibility from Cost, tags, or an undocumented status value. The server
retains its eligibility checks; never bypass a refusal of the setting.
The renewal period menu is separate from purchase `AllowPeriod`; do not
use that purchase list to invent renewal restrictions.

Require `--max-price` for enable and period changes, compared against a
fresh monthly quote times target months, VAT included. Equality passes.
No `--yes` is required. Under [ADR 0002](../adr/0002-write-api-conventions.md),
a setting reversible by another command is not destructive. A numeric
cap gives clearer consent to future spending than a bare boolean. This
extends the paid-create guard to a setting that authorizes future charges;
it does not reclassify the toggle as an immediate purchase.

The cap is only a local check of today's estimate. It is not sent to the
server, saved for later renewals, or a lifetime spending limit. Auto-renew
can charge repeatedly until disabled. CLI help and wiki examples must say
so. Disable needs neither a quote nor a cap and must work during a pricing
outage. A later disable does not cancel a charge already in progress.

## Read, send once, and confirm

Duplicate JSON keys in billing resource, user-info, and PUT replies, the
vStorage project list, the project catalog, the price quote, and region and
configuration reads fail closed.

1. Refuse read-only CLI profiles before authentication or any request,
   including requests for a no-op. Validate input shape locally.
2. Join fresh project and resource reads as above. Require known renewal
   state. If both reads already show the target state and period, return
   Changed false without PUT. No-op enable needs no new price consent;
   quote failure only marks its output Unavailable.
3. For a change, require PREPAID and a present channel; live rows carry
   `isRenewing: null` when no renewal is in progress, absence also means no
   renewal in progress, and only `isRenewing: true` refuses the change. For
   enable or period change, also require an active storage project, end time
   in the future, and the supported monthly purchase. Reject NON-RENEWABLE and
   unsupported eligibility with `ErrInvalidInput`. Unknown or missing
   guard data returns `*APIError`. Disable does not require an active term,
   current catalog, or successful price lookup.
4. For enable or period change, quote afresh and check MaxPrice. Quote
   failure stops before PUT, preserving state. An above-cap estimate
   returns `ErrPriceAboveMax`. Resolve the account as described below.
5. Send exactly one PUT with `Once` and `NoRedirect`. No resend after 401,
   429, 5xx, failed dial, lost response, cancellation, or redirect. Neither
   CLI nor SDK adds an outer retry. The explicit target avoids a raw flip;
   the send-once rule also avoids acting again on stale reads and prices.
6. Require HTTP 200, envelope code 200, a present true successAll, and a
   present empty errorAutoRenewResources array. False successAll or any
   error entry returns `*APIError` with code `AutoRenewRejected`, even if
   later reads show the target. Never infer success from HTTP alone or
   parse an unknown error entry into public text.
7. After a valid acceptance, confirm through both lists immediately, then
   every 2 seconds for at most 30 seconds. Bound all requests and sleeps
   with the caller context and the confirmation deadline. Both reads must
   agree on the target and period in the same poll. Reads may use their
   usual retry rules within that bound; never resend the PUT. Disable
   requires MANUAL/null and explicit storage false with null or zero period.

A confirmed write returns Changed true and the joined state. Enable and
period-change outputs use the guarded pre-write quote; confirmation does
not require another quote. Disable output leaves prices Unavailable
without making a pricing request. A later read can price the project.
Do not compute or require an extended end date: the setting changes future
renewal, not the current term. Document the read/write race: no conditional
update exists, and another actor can change state after confirmation.

A known HTTP refusal or failed dial returns its error. A lost, malformed,
or unrecognized response may hide a change; return `storage.ErrNotSettled`
with the underlying error. Read-only reconciliation may attach the observed
state but must not erase the failed response. A timeout, cancellation, or
failed confirmation after acceptance also returns ErrNotSettled, retaining
last observed state. Explicit rejection remains an error even if state
changed; wrap it in ErrNotSettled when the outcome is inconsistent.
Recovery is to run GetProjectAutoRenew, reconcile both reads, then issue a
new deliberate setting if needed. Never automatically roll back or resend.

## Account identity and privacy

Resolve identity from authenticated Billing user-info immediately before
each actual PUT: the SDK login carries `data.userId`, which equals the
caller identity's account ID, and the console session carries
`data.accountId`; prefer `accountId` when present and require both fields
to agree when both are present. Require a positive integral int64 and
serialize it in decimal as `portal-user-id`. Do not accept the value from
Input, flags, config, environment, tags, a project user ID, or a browser
cache. IAM GetCallerIdentity.AccountID identifies the same account concept,
but this path avoids a public-service dependency and matches the console.

Cache the account ID only within that operation. Do not persist it or reuse
it across writes, clients, profiles, endpoint overrides, or token-provider
changes. This costs one read per mutation and avoids defining credential
cache invalidation for account IDs. Credentials providers must keep a
stable principal during an operation; a provider switching accounts during
a call has no cross-request identity guarantee. A server refusal never
triggers a fallback header or a retry.

Keep account responses and raw resource tags out of normal output, debug
logs, and errors. Redact the account ID and sent bearer token from PUT
errors, including reflected values. Return a fixed safe rejection message
and error count instead of raw error-array entries. Raw captures remain
private under the live-data rules; never publish user-info payloads.

## CLI

Add `billing list-resources`, `storage get-project-auto-renew`, and
`storage put-project-auto-renew`. The first two are reads. Use the existing
global `--region` and `--project-id` mapping without duplicate local flags.
The write requires explicit `--enabled=true` or `--enabled=false`, or the
same explicit boolean in `--cli-input-json`. All commands support normal
JSON, text, table, and query output. No interactive prompts.

```sh
vngcloud storage get-project-auto-renew --region hcm-3 \
  --project-id project-1
vngcloud storage put-project-auto-renew --region hcm-3 \
  --project-id project-1 --enabled=true --period-months 1 --max-price 30000
vngcloud storage put-project-auto-renew --region hcm-3 \
  --project-id project-1 --enabled=false
```

The cap is illustrative; users read the fresh quote before choosing it.
Tables show end time with zone, state, configured months, preview months,
quoted renewal charge, estimated next charge, currency, and price status.
Show `unavailable` for failed pricing and `none scheduled` for an off
NextCharge; never render null as zero. Billing tables show raw renewal and
end-time data without presenting Cost as a price. Quotes do not fan out
across all rows of billing list-resources.

Use existing exits: invalid input or read-only is 2, NotFound is 4,
PriceAboveMax, Unpriced, AutoRenewRejected, API failures, and NotSettled are
1. A successful read with unavailable pricing exits 0 with explicit price
status. A successful disable also exits 0 without a quote.

## Fixtures, checks, and release

Use sanitized raw fixtures for the nested list, enabled/manual rows, the
user-info account field, and both PUT responses. Replace every resource ID,
name, tag key/value carrying account data, account ID, and IAM ID with
synthetic values. Preserve numeric wire types with synthetic numbers.
Sanitize nested error entries too. Keep relevant unknown fields in the raw
fixture so decode tests detect model omissions; never publish live dumps.

Deterministic tests must cover strict envelopes, null/missing fields,
unknown enums, duplicate joins, wrong region/product/artifact, channel zero,
period conversion, period omission, fixed-period exclusion, and eligibility.
Test exact quota conversion, VAT-inclusive totals, price failures, overflow,
cap equality, stale state, conflicting reads, and no-op behavior. Verify
omitted Enabled fails and explicit false succeeds in flags and JSON.

Count PUT attempts for every refusal and ambiguous outcome, including 401,
429, redirect, and network loss. Test partial failures with both values of
successAll, missing/null error arrays, and non-empty arrays. Verify both
confirmation reads, the 30-second bound, cancellation, and no rollback.
Test per-operation account lookup, endpoint overrides, header reflection,
tag omission, read-only refusal before login, and quote-free disable.
Run `make check`; update SDK and CLI wiki pages and example reads in the
implementation change. Independent adversarial review covers this write,
identity header, future-charge consent, error redaction, and send-once rule.

Ship one feature release containing the read, setting, CLI, and docs, after
the dedicated live check and review pass and CI is green on the exact
commit. Reads precede write implementation; no separate generic write is
required. A refused longer-period update defers that period's write support
until the contract is corrected, not permission to guess another request.

## Implementation live check

The manager obtains run approval naming the account, region, throwaway
project purchase, price cap, and deletion under
[live-data](../../instructions/live-data.md). The feature approval does not
authorize a new paid run or a change to any existing production project.
The manager buys and deletes one empty project solely for this check.
Use a dedicated `TestLiveWriteStorageAutoRenew`, gated by
`VNGCLOUD_LIVE_WRITE=1` and `VNGCLOUD_LIVE_STORAGE_AUTO_RENEW=1`, with
`go test -tags livewrite -count=1 -timeout 60m` with an exact test-name
filter and `./livetest/` as the package.
Run no concurrent storage live test or probe against that project.

1. Record its ID and baseline balance/transactions privately. Confirm
   auto-renew off, its end time, monthly type, and quota through both reads.
2. Enable for one month through the SDK with a fresh quoted cap. Confirm
   AUTO-RENEW/1 and storage true/1. Verify unchanged end time and reconcile
   transactions to show no toggle charge; a balance alone is insufficient.
3. If the resource supports it, update to three months with a fresh cap.
   Confirm period 3 through both reads. An unexplained refusal fails that
   period's check; do not retry or send a different payload speculatively.
4. Disable, confirm MANUAL/null and storage false, and reconcile no toggle
   charge. Cleanup attempts disable on this same project after earlier
   failures, after reading its state; cleanup does not retry the failed
   mutation. The manager deletes only this empty throwaway project.
5. Reconcile purchase debit and deletion refund separately from toggles.
   Report unresolved renewal, charges, or cleanup privately with the known
   ID and last state. Never wait for a billing boundary or touch production
   to prove a renewal debit. No charge is expected from the settings.

## Decisions

The accepted recommendation is a generic read, a storage-specific setting,
a joined storage read, and MaxPrice consent for changes that enable future
charges. Disable stays independent of pricing. Account identity is fresh
per mutation. Manual renewal orders and other products' writes are deferred.
No additional owner product decision is required. A paid implementation
live run still needs its own scoped approval under the live-data rules.
