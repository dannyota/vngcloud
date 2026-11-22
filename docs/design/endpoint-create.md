# Create and delete an hcm-3 private endpoint

Status: Proposed with the owner decisions below. Ship quote, create, and
delete together for one vStorage endpoint on the Standard package and an
existing HCM03-1C subnet. The [API evidence](network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe)
records a live create and delete. This slice narrows the
[endpoint lifecycle proposal](network-endpoints.md). Other regions and the
[private data path](network-endpoints-api.md#private-data-plane-gate)
remain gated separately.

## Scope and alternatives

Use the direct collection POST with a fresh quote for the same resource.
This is the console flow observed for the selected offer. An order or
checkout flow adds a separate payment contract and is not a fallback. Keep
console management and existing SDK inventory available while any lifecycle
gate is open. Do not ship create without a safe delete path.

Keep private DNS and auto-renew off. Do not add tags, rename, DNS mutation,
VPC or subnet creation, VKS integration, checkout, credit top-ups, payment
methods, or a second write to correct renewal. An endpoint changes network
reachability; it grants no bucket rights and does not certify S3 connectivity.

## SDK and CLI

All methods use `(context.Context, *Input) (*Output, error)` on
`network.Client`. Add no dependency or root re-export.

| SDK method | CLI command | Input |
|-|-|-|
| `QuoteCreateEndpoint` | `network quote-create-endpoint` | `CreateEndpointInput` |
| `CreateEndpoint` | `network create-endpoint` | `CreateEndpointInput` |
| `DeleteEndpoint` | `network delete-endpoint` | `DeleteEndpointInput` |

`CreateEndpointInput` has required `Name`, `VPCID`, and `SubnetID` strings,
and `MaxPrice float64`. `DeleteEndpointInput` has required `EndpointID` and
`VPCID` strings. Region and project come from configured selection, checked
by discovery. The API's `regionUuid` is not the subnet's availability zone.
Resolve category, service, and package UUIDs from the verified catalog for
the sole supported offer. Do not expose a raw UUID override, DNS or renewal
switch, `NoWait`, or force, cascade, or funding option.

`QuoteCreateEndpointOutput` has `MonthlyPrice float64` and `Currency string`.
`CreateEndpointOutput` has `EndpointID` and `Status` strings, `MonthlyPrice
float64`, `Currency string`, and `AutoRenew *bool`. The amount is the fresh
quote, not the recorded debit. Unknown renewal stays nil. Include only a
screened ID and last verified state in partial output; never infer an ID
from a name. `DeleteEndpointOutput` has `EndpointID` and `Deleted bool`.
`Deleted` becomes true only after the confirmation below.

Quote is a read, even though it uses POST, and works in read-only mode.
Create and delete require a writable profile and `--yes`; create also
requires `--max-price`. Reject missing consent, bad output or query settings,
and invalid input locally before discovery. CLI help states VND per month,
the quote-to-debit race, and the separate risk of disrupting service
connections on delete. Keep `ListEndpoints`, `GetEndpoint`, `ListEndpointTags`,
and existing VPC/subnet inventory behavior unchanged. Endpoint-specific
catalog and picker reads are private in this slice.

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
- Validate all IDs before reuse. Apply [ADR 0002](../adr/0002-write-api-conventions.md)
  to path IDs, then the narrower verified forms where known: 24 hex for
  `regionUuid` and `categoryUuid`, `pro-<uuid>` for project, `net-<uuid>` for
  VPC, `sub-<uuid>` for subnet, and UUIDs for service and package. The
  endpoint ID form needs live evidence. Require a nonblank name without
  control characters; leave product naming rules to the server.
- Require a unique eligible vStorage service and Standard package through
  the category, service, and package catalog joins. Use evidence-backed
  product and package discriminators from the catalog, not display labels
  alone, inferred hostnames, default flags, or hard-coded catalog UUIDs.
  Reject absent, ambiguous, or conflicting rows. The console summary is
  not a catalog discriminator.
- Prove an active, eligible caller-selected VPC in the resolved project and
  region, and an eligible subnet of that VPC in the enabled HCM03-1C zone.
  Verify the console-label-to-zone mapping and the actual picker semantics.
  Ordinary VPC/subnet existence and a successful quote do not prove endpoint
  eligibility. Do not substitute a different VPC, subnet, or zone.

## Quote and create

1. Resolve one typed resource specification. Share the quote/create
   serializer and use the [observed request fields](network-endpoints-api.md#recorded-hcm-3-console-quote-and-create-request).
   Set `isBuyMorePoc: false`, `isPoc: false`, `isEnableAutoRenew: false`,
   `enableAZ: true`, and `enableDnsName: false` in both calls. Create adds
   only `tagDetails: []`. Do not send `period` or `paymentType`. The recorded
   console used renewal on; prove that both routes accept and honor the
   renewal-off contract before shipping. No retry with renewal on.
2. Before create, read a complete endpoint inventory in the pinned project
   and region to establish an ID baseline. Refuse an existing exact name in
   the selected VPC or an unprovable baseline. Quote alone does not take that
   baseline or reserve a price or capacity. Callers serialize mutations in
   the same scope; list checks are not locks.
3. Call the price route immediately before create. Mark this read idempotent.
   Require HTTP 200, numeric `code: 0`, `success: true`, and a strictly
   decoded, finite, nonnegative `data.optimumPrice` in the verified monthly
   VND quote shape. Require exactly one Standard price property with
   `optimumPrice` and `monthlyPrice` equal to the top-level amount, as in
   the observed quote. Refuse missing or conflicting required amounts.
   A null `currentPrice` is not a zero charge. Use neither `originalPrice`
   nor a catalog rate as the cap value. An otherwise valid zero quote is
   unpriced for this slice.
4. As with [NAT](network-writes-nat.md#identity-and-payment), compare the
   top-level `optimumPrice` directly to `MaxPrice`, in VND per month, not
   in minor units or prorated VND. Do not round for the comparison.
   Reject negative, infinite, or NaN caps before discovery. Quote ignores
   a cap of valid shape. Equality passes; zero prevents every purchase.
   Over cap returns `ErrPriceAboveMax` and sends no create. The cap bounds
   the fresh quote, not the actual debit, tax, later usage, or a server-side
   price lock. A quote is not proof of prepaid term, payment, or refund.
5. Send one POST to the endpoint collection. Require HTTP 200 or 202 and
   the live-verified acceptance envelope and endpoint ID. Do not apply
   NAT's 201 or ID-less acceptance here. Never adopt a candidate from a
   name, even with a baseline. If no usable ID is returned, report
   `ErrNotSettled` without an ID; an ID-less response blocks this design
   until a different correlation rule is reviewed.
6. Read the exact returned ID; require that it was absent from the baseline
   and match the name, project, region, VPC, subnet, service, and package
   through verified scoped reads. Confirm an observed ready state, private
   DNS off, and evidence-defined payment settlement with observed renewal
   off for that same ID. A request flag, global balance change, or readiness
   state alone cannot prove billing or renewal. Poll documented transitional
   states only. Return `ErrFailed` only for a matched, verified terminal
   failure and `ErrNotSettled` for unknown or contradictory states. A known
   billing discrepancy is unsettled, not success.

## Delete

1. Read the exact `EndpointID` in the pinned project and region. Require
   exactly one matching row in complete project inventory and a matching
   detail. Match the explicit `VPCID`, supported vStorage/Standard identity,
   and a verified deletable state. A path scope or a filtered row alone
   does not establish ownership. Before dispatch, proven absence is
   `ErrNotFound`; inconsistent scope or identity refuses the write.
2. Require an authoritative platform read to prove the endpoint is user-owned
   and not VKS-managed or reused. A previous create response, names, tags,
   apparent idleness, and `--yes` do not establish that an endpoint is
   currently unused by VKS. Do not query a cluster or delete when provenance
   or consumers are unknown. The operator coordinates other workload use
   and concurrent VKS or endpoint changes; the preflight is not a lock.
3. Send one DELETE to the exact detail path with `endpointUuid`, `vpcUuid`,
   `endpointServiceUuid`, `regionUuid`, and `projectUuid`. Construct the
   body from proven identity and scope. The service field must map from
   verified target data; do not assume it equals create's `serviceUuid` or
   accept it from the caller. Require HTTP 200 or 202 and the verified
   delete acceptance shape.
4. Confirm exact detail absence with a verified not-found response and a
   complete, consistent inventory scan of the same scope that omits the
   ID. A DELETE 404, collection 404, empty page, transitional state, or
   missing payload proves nothing. Also establish the verified end of
   future charges for that ID. If evidence establishes that confirmed
   absence ends charges, no separate runtime billing read is needed for
   delete; otherwise require the authoritative billing confirmation.
   Success promises no refund amount or timing.

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

The [probe record](network-endpoints-api.md#recorded-hcm-3-create-and-delete-probe)
closes the gates this slice needs: renewal-off create is accepted with HTTP
201 and an `enp-<uuid>` ID in `data.uuid`; the list row shape, the
`PROVISIONING`, `ACTIVE`, and `DELETING` states, and absence from the list
are observed; the delete body maps from create's values, with
`endpointServiceUuid` equal to the row's `endpointServiceId`; and the
quoted 712,400 VND is debited at create and fully returned after delete.

Use these values. `ACTIVE` is success; `PROVISIONING` and `DELETING` are
transitional; any other state is NotSettled. `billingStatus` is not a
success signal. Delete confirmation is absence of the ID from a complete,
consistent list of the same project and region; an empty list omits
`data`. The exact detail GET after delete is unobserved, so a detail 404 is
corroboration only. No field marks an endpoint as VKS-managed; delete
follows owner decision 3.

Cover strict envelopes, scope joins, quote/body parity, price refusal, one
dispatch, credential screening, proven absence, partial output, and wait
bounds with deterministic fixtures. Keep existing reads stable. Require
adversarial write review, matching wiki and generated reference updates,
and exact-commit checks before release.

## Owner decisions

1. Ship quote, create, and delete together for the single verified offer,
   with runtime catalog resolution and private DNS fixed off.
2. The monthly quote cap is a pre-dispatch guard, not a debit guarantee.
   The probe may spend up to one month of the quoted Standard price; no
   renewal and no funding change close a gate.
3. Create returns the endpoint ID from the acceptance reply or fails as
   NotSettled. The API exposes no VKS-managed or reuse marker. Delete ships
   for an exact ID the caller names with `--yes`, refuses when
   `endpointResource` is non-null or `endpointDomains` is non-empty, and
   help warns that deleting an endpoint a cluster uses breaks that
   cluster's private access.
4. The manager closes the probe gates with one console create and one
   console delete in `hcm-3`, on a disposable VPC and subnet in HCM03-1C
   with no consumers. The browser rewrites the outgoing create body to
   `isEnableAutoRenew: false`, so the probe also tests the SDK body. Request
   and response shapes, states, the delete body mapping, provenance fields
   and private balance records before and after each step are recorded.
