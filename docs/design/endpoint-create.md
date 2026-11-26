# Create and delete an hcm-3 private endpoint

Draft amendment to the accepted `hcm-3` lifecycle. Quote and create a
monthly Gateway endpoint from the caller's catalog. Delete uses the exact
endpoint record in the caller's scope. Quote, create, and delete ship
together for this scope under the
[per-account purchase rule](network-writes-nat.md#purchase-and-auth).

Keep the HCM vNetwork origin. `han-1` stays blocked because the
[Hanoi subnet read](network-endpoints-api.md#recorded-iam-user-catalog-reads)
establishes no usable subnet and endpoint billing remains held after a
delete left an active prepaid record without a recorded refund.
[Refund evidence](network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe)
· [Billing hold](network-nat-reads.md#surface-and-release-order)

The [broader lifecycle](network-endpoints.md) and
[private data path](network-endpoints-api.md#private-data-plane-gate)
retain their separate evidence gates.

## Scope and alternatives

Use the observed direct collection POST with a fresh quote for the same
resource. A separate order or checkout needs its own payment contract.
Console management and SDK inventory remain available when a catalog or
placement check refuses the operation.

Limit the flow to Gateway endpoints with private DNS off. Request
auto-renew off. Tags, rename, DNS mutation, VPC and subnet creation, VKS
integration, checkout, funding, and renewal correction stay out of scope.
Endpoint readiness confirms network provisioning only. Bucket access and
S3 connectivity have separate requirements.

## SDK and CLI

All methods use `(context.Context, *Input) (*Output, error)` on
`network.Client`. Add no dependency or root re-export.

| SDK method | CLI command | Input |
|-|-|-|
| `QuoteCreateEndpoint` | `network quote-create-endpoint` | `CreateEndpointInput` |
| `CreateEndpoint` | `network create-endpoint` | `CreateEndpointInput` |
| `DeleteEndpoint` | `network delete-endpoint` | `DeleteEndpointInput` |

Quote and create take `Name`, `VPCID`, and `SubnetID` as required strings
(`--name`, `--vpc-id`, `--subnet-id`). Add four optional strings to
`CreateEndpointInput`. Resolve each omitted field in catalog order.

| SDK field | CLI flag | Value when supplied | When omitted |
|-|-|-|-|
| `AvailabilityZoneID` | `--availability-zone-id` | Zone row `uuid` | Exact `HCM03-1C` row |
| `CategoryID` | `--category-id` | Category row `uuid` | Sole category row, marked `isDefault: true` |
| `ServiceID` | `--service-id` | Service row `uuid` for that category and AZ | Exact name `vstorage.hcm03` in that catalog |
| `PackageID` | `--package-id` | Package row `uuid` for that service | Exact name `Standard` in that catalog |

Resolve IDs from the caller's catalog on every call, including when all
fields are omitted. A supplied value must match exactly. Refuse an
unavailable or ambiguous choice without substituting another offer. Empty
SDK strings mean omitted and preserve existing keyed literals and CLI
invocations. Go callers using unkeyed literals must switch to keyed fields.

`MaxPrice float64` remains the create cap. Delete takes `EndpointID` and
`VPCID` as required strings (`--endpoint-id`, `--vpc-id`). Region and project
come from configured selection and trusted discovery. Use the discovered
`regionUuid` for the path and the selected AZ for the catalog and subnet
checks. The SDK exposes no client-supplied routing, DNS, renewal, wait,
force, cascade, or funding controls for these methods.

`QuoteCreateEndpointOutput` has `MonthlyPrice float64` and `Currency string`.
`CreateEndpointOutput` has `EndpointID` and `Status` strings, `MonthlyPrice
float64`, and `Currency string`. The amount is the fresh quote, not the
recorded debit. Output carries no renewal or billing status. Include only a
screened ID and last verified state in partial output; never infer an ID
from a name. `DeleteEndpointOutput` has `EndpointID` and `Deleted bool`.
`Deleted` becomes true only after the confirmation below.

Quote is a read, even though it uses POST, and works in read-only mode.
Create and delete require a writable profile and `--yes`. Create also
requires `--max-price`. Reject missing consent, bad output or query
settings, and invalid input locally before discovery. CLI help describes
catalog UUIDs, the defaults, VND per month, the quote-to-debit race, and
the risk of disrupting service connections on delete. An explicitly
empty selector flag is invalid. Existing inventory commands keep their
contracts. Catalog and picker reads stay internal to quote and create.

## Trusted scope and placement

- Require configured region `hcm-3`, SDK IAM-user login, default auth
  endpoints, default VServer endpoint for `GET /v1/projects`, and the fixed
  `https://hcm-3-vnetwork.console.greennode.ai/vnetwork-gateway/` base. Reject
  endpoint overrides and unknown provenance before discovery. Check any
  additional service origin used to establish billing under the same rule.
  No fallback, browser session, static bearer, or service-account auth.
- Use the default VServer regional project read to confirm one exact
  eligible project. A configured project ID can narrow the result, not
  replace proof of membership. Reject ambiguous discovery, missing regional
  evidence, or a mismatch between that ID and vNetwork scope. Resolve one
  matching `regionUuid` through trusted vNetwork region discovery. Treat
  returned hosts as labels to compare, never credential destinations. Pin
  the resolved project and region for request paths, bodies, and waits.
- Validate caller IDs before discovery and discovered IDs before reuse.
  Apply the [path-ID rule](../adr/0002-write-api-conventions.md#decision)
  to all IDs, including the AZ catalog `uuid`. Require 24 hex for region
  and category, UUIDs for service and package, and `pro-<uuid>`,
  `net-<uuid>`, `sub-<uuid>`, and `enp-<uuid>` for project, VPC, subnet,
  and endpoint respectively. Require a nonblank name without control
  characters. The server owns product naming rules.
- Resolve the offer under the pinned `.../endpoints` collection. Each
  selected ID must match exactly one row in its scoped catalog, including
  IDs found by default name. Default names must also match exactly one
  row. Require nonblank service and package names. Validate each match
  before checking eligibility, so an ineligible duplicate still refuses.
  Fail closed on absent, duplicate, conflicting, malformed, or incomplete
  selection evidence.
  [HCM catalog evidence](network-endpoints-api.md#recorded-hcm-3-catalog-reads)

| Catalog GET suffix | Selection guard |
|-|-|
| `/zones` | Selected `uuid`, `zoneType: AVAILABILITY`, and `isEnabled: true` |
| `/categories` | Selected `uuid` or the omitted-field rule above |
| `/{categoryUuid}/services?zoneUuid=<selected AZ>` | Selected `uuid` and `endpointType: Gateway` |
| `/{serviceUuid}/packages` | Selected `uuid`, `serviceName` equal to the resolved service name, and `currencyUnit: VND` |

- Read a complete `GET .../endpoints/vpcs` result using `status=ACTIVE`,
  `endpointServiceId` set to the resolved service UUID, and
  `zoneType=AVAILABILITY`. Require exactly one row with the caller's VPC
  ID, `status: ACTIVE`, and the pinned `projectUuid` and `regionUuid`.
- Read `GET .../endpoints/vpcs/{vpcUuid}/subnets` with
  `isEndpointUsable=true` and `zoneUuid` set to the selected AZ. Require
  exactly one row with the caller's subnet ID, `status: ACTIVE`, `vpcUuid`
  equal to the verified VPC, and `zone.uuid` equal to the selected AZ.
  That join proves placement in the scoped VPC. An envelope without
  `data` supplies no eligible subnet.

## Quote and create

1. Resolve one typed resource specification. Quote and create serialize
   the same category, service, package, VPC, subnet, region, and project
   UUIDs. The service and subnet selections carry the AZ constraint. Set
   `isBuyMorePoc: false`, `isPoc: false`, `isEnableAutoRenew: false`,
   `enableAZ: true`, and `enableDnsName: false`. Create adds only
   `tagDetails: []`. The request carries no AZ field, `period`, or
   `paymentType`.
   [Request evidence](network-endpoints-api.md#recorded-hcm-3-console-quote-and-create-request)
2. Before create, read a complete endpoint inventory in the pinned project
   and region to establish an ID baseline. Refuse an existing exact name in
   the selected VPC or an unprovable baseline. Quote alone does not take that
   baseline or reserve a price or capacity. Callers serialize mutations in
   the same scope; list checks are not locks.
3. Call the idempotent price read immediately before create. Require HTTP
   200, numeric `code: 0`, `success: true`, and a finite, nonnegative
   `data.optimumPrice` in the observed monthly quote shape. The request
   names the selected package UUID. The observed reply identifies the
   package by name. Require `propertiesPrice` to contain one entry whose
   `name` equals the selected package's catalog name. Refuse a name
   shared by another package in that service's catalog, even for an
   explicit ID. The selected package establishes VND. Its price property
   must have `optimumPrice` and `monthlyPrice` equal to the top-level
   amount. Refuse missing or conflicting required amounts. Use only the
   fresh quote amount for the cap. Zero returns `ErrUnpriced`.
4. As with [NAT](network-writes-nat.md#identity-and-payment), compare the
   top-level `optimumPrice` directly to `MaxPrice`, in VND per month, not
   in minor units or prorated VND. Do not round for the comparison.
   Reject negative, infinite, or NaN caps before discovery. Quote ignores
   a cap of valid shape. Equality passes; zero prevents every purchase.
   Over cap returns `ErrPriceAboveMax` and sends no create. The cap bounds
   the fresh quote, not the actual debit, tax, later usage, or a server-side
   price lock. A quote is not proof of prepaid term, payment, or refund.
5. Send one POST to the endpoint collection. Accept only HTTP 201 with
   `code: 0`, `success: true`, `data.uuid` of `enp-<uuid>` form, and
   `data.name` equal to the requested name. A definitive 4xx refusal returns
   fixed SDK text and nil output; 408 or any response that cannot rule out
   execution returns `ErrNotSettled`. Any other 2xx, a reply whose name
   differs, or an ID already in the baseline is `ErrNotSettled` with no ID
   reported as created. Never resend or adopt a candidate from a name.
6. Wait for the returned ID in the complete scoped list. It must be new
   to the baseline and match the requested name, `vpcId`, `subnet.uuid`,
   resolved `service.uuid` and `service.name`, `packageId.uuid`, pinned
   `project.VServerProjectId`, and selected `zoneUuid`. Require
   `endpointType: Gateway` and `enableDnsName: false`. `ACTIVE` confirms
   readiness. `PROVISIONING` is transitional. Other states return
   `ErrNotSettled`. Output reports readiness only. CLI help describes
   the renewal-off request and directs the caller to billing for the
   observed renewal state.

## Delete

1. Read the complete scoped list and require exactly one row whose `uuid`
   equals `EndpointID`. Require the pinned `project.VServerProjectId`,
   the caller's exact `vpcId`, `status: ACTIVE`, `endpointType: Gateway`,
   and `enableDnsName: false`. Require explicit `endpointResource: null`
   and `endpointDomains: []`. Missing or wrongly typed fields refuse.
   An attachment or domain returns `ErrInUse`. A proven absence before
   dispatch returns `ErrNotFound`. CLI help warns that a cluster can
   still use the endpoint and lose private access on delete.
2. Use the matched row's validated `uuid`, `vpcId`, and `service.uuid`,
   plus the pinned region and project. Send one DELETE to the exact
   detail path with `endpointUuid`, `vpcUuid`, `endpointServiceUuid`,
   `regionUuid`, and `projectUuid`. Catalog availability, AZ, category,
   service name, and package play no part in delete. Accept only HTTP
   200 with `code: 0` and `success: true`.
3. `ACTIVE` and `DELETING` keep the wait pending. Set `Deleted=true`
   only when a complete, consistent scoped list omits the ID. An
   envelope without `data` counts only with `total: 0` and
   `totalPage: 0`. An unproven absence returns `ErrNotSettled` with
   the ID and `Deleted=false`. Check billing separately for refunds
   and active billing records.

## Dispatch, waits, and errors

Set `Once`, `NoRedirect`, and `Sensitive` on writes. There is no resend on
any response or transport failure, including 401, 429, 5xx, failed dial,
lost body, and cancellation. All private discovery, quote, and confirmation
reads use `NoRedirect` and `Sensitive` with bounded read retries. Decode
only typed fields from bounded strict JSON. Reject invalid shapes, duplicate
keys, and missing critical fields. Screen decoded keys and string values
for credential substrings before reuse or output, including credentials sent
on prior steps or retries in the operation. Do not use unguarded public
inventory models for write decisions. No server text, raw errors, bodies,
redirect targets, or payment metadata enter errors, logs, or captures.

Bound each inventory walk to 50 pages and 30 seconds or the remaining
operation deadline, whichever is shorter. Require explicit, consistent
paging metadata, page progress, unique IDs, matching decoded totals, and
proved scope. Refuse incomplete scans and detected changes; use only the
verified empty inventory representation. Bound create and delete confirmation
phases to 15 and 10 minutes respectively, polling immediately and then
every 5 seconds under the caller context. Start the bound after dispatch;
it includes all confirmation reads and delays. Never report late success
after cancellation or expiry. These are client bounds, not service targets.

Pre-dispatch errors and definitive 4xx refusals return nil output. Return
fixed SDK text for every 4xx, with shared status and sentinel classification
where established. A 408 or other reply that cannot rule out execution
remains `ErrNotSettled`. Other uncertain outcomes after dispatch include a
screened endpoint ID when known, and `Deleted` stays false on an uncertain
delete. Use read-only reconciliation and keep an ambiguous dispatch an error.
Preserve cancellation/deadline through safe error causes. Inspect inventory
with `network list-endpoints` and `network get-endpoint` in the same scope,
then inspect billing before another mutation. No automatic cleanup or retry.

Use the shared input, configuration, auth, permission, not-found, unpriced,
and price-cap sentinels and the existing `network.ErrNotSettled` and
`network.ErrFailed`. CLI exits follow the shared rules: usage, read-only,
and missing consent 2; authentication 3; proven not-found 4; other API,
price, and unsettled failures 1. Cancellation retains shared precedence.

## Probe record and release order

The `hcm-3` IAM-user lifecycle is live-verified for `vstorage.hcm03`,
`Standard`, and `HCM03-1C`. The recorded quote, create, provisioning, and
removal establish the flow and delete field mapping. The console probe
recorded a full refund. A later SDK run left the billing record active
without a recorded refund after removal. It also showed `AUTO-RENEW` even
though create requested renewal off. CLI help directs callers to check
billing and use a fresh, unique name for each create.
[Live lifecycle and billing record](network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe)

The catalog includes other Gateway services. Lifecycle behavior for other
AZs, categories, services, packages, and account configurations is
unverified. These selections use the same `hcm-3` flow when the caller's
catalog, placement, and price pass the guards above. Live verification
applies only to the exact combination exercised. Separate authorization
is required for any new paid probe. Refund timing, renewal state, private
connectivity, and VKS ownership remain outside the readiness guarantee.
[Catalog evidence](network-endpoints-api.md#recorded-hcm-3-catalog-reads)

Obtain design approval and independent review before implementation.
Use deterministic fixtures for legacy defaults, explicit alternatives,
missing and duplicate matches, placement joins, quote matching and cap
refusals, delete without catalog reads, and existing write protections.
Require adversarial write review, matching wiki and generated reference
updates, and exact-commit checks before release.

## Owner decisions

1. Support each caller's eligible `hcm-3` catalog choices. Verify their
   account's offer before purchase. Keep Gateway endpoints with private
   DNS off as the supported scope.
2. The cap applies to the fresh monthly quote before dispatch. Actual
   billing can differ. The original probe had a one-month Standard
   quote cap and excluded renewal and funding changes.
3. Create returns the endpoint ID from the acceptance reply or fails as
   `ErrNotSettled`. The API exposes no VKS-managed or reuse marker.
   Delete uses the exact ID the caller names with `--yes` and the
   attachment checks above.
4. The original console probe covered one `hcm-3` create and delete on a
   disposable HCM03-1C subnet without consumers, with the outgoing
   create body set to `isEnableAutoRenew: false`.
   [Probe record](network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe)
