# Public NAT IAM purchase amendment

Status: Accepted (2026-10-11). This amendment replaces the NAT parts of
the [accepted write design](network-writes.md) where they differ. The
owner approved each choice marked **Approved**. The
[API evidence](network-writes-api.md) distinguishes live observations from
console code.

## Purchase and auth

**Approved.** Use the verified direct NAT collection
POST for the first purchase release. Send the same priced resourceInfo,
including `isEnableAutoRenew: false` and `isBuyMorePoc: false`, plus
`tagDetails: []`. Send neither `period` nor `paymentType`. The default term
is the observed one-month PREPAID purchase. Keep root checkout out of scope.
A quote needs HTTP 200, numeric `code: 0`, and `success: true`. A create
needs HTTP 201, numeric `code: 0`, and `success: true`; the observed
reply contains no data or ID. No second order or payment fallback exists.

**Approved.** NAT writes fail closed unless
auth uses the SDK IAM-user login provider and the selected region is HAN.
That is the only live-verified write auth mode. Root, service-account,
static bearer, and custom providers remain unverified, even though console
code selects AUTO for non-root users. Reject unsupported or unknown auth
provenance with `ErrInvalidConfig` before mutation. Do not infer auth mode
from a successful inventory read, caller-supplied switch, or decoded token.
If the configured provider cannot establish provenance, do not send.
Existing inventory auth contracts stay unchanged. HCM write support still
requires its own successful lifecycle; never fall back to another region.

## Inputs and discovery

**Approved.** Require `AvailabilityZoneID string`
on `CreateNATInstanceInput`, in addition to `Name`, `ZoneID`, `PackageID`,
`VPCID`, and `MaxPrice`. Keep `ZoneID`: existing callers use it for the
region-level vNetwork path segment, shown as `{regionId}` in this evidence.
An availability zone is a distinct identifier such as `HAN01-1B`; it must
never replace `ZoneID` in a path. Renaming `ZoneID` would break consistency
with the accepted read input without removing the need for two identifiers.

`PackageID` means the selected catalog row's `uuid`, sent as `packageUuid`,
not the catalog's separate `packageId` field. Require an exact unique zone
with `zoneType: AVAILABILITY` and `isEnabled: true`. Fetch packages with
`zoneUuid=AvailabilityZoneID` and require one exact UUID match. Do not choose
`isDefault` automatically or fetch packages without a zone. Validate all
IDs before discovery. Quote and create use the same zone/package resolver.

Use the NAT VPC picker to require that the named VPC's `zones` contains the
selected availability-zone UUID, as well as the shared project and region
guards. Reject missing, ambiguous, or inconsistent membership. Complete
picker scans need the same page and total checks as inventory guards.
The order has no availability-zone field: the selected package implies it.
Resolve once per call; quote immediately before the single order.

**Approved.** Add `ListNATZones` and
`ListNATPackages` to `network.Client` with CLI commands `list-nat-zones` and
`list-nat-packages` in the same feature release. Both use the standard
pointer Input/Output method shape and are reads in read-only profiles.
`ListNATZonesInput` has `ZoneID`, resolved like the inventory read's scope.
`ListNATZonesOutput` is `core.List[NATAvailabilityZone]`: the verified zones
reply is an unpaged array, so the SDK sends the console's single query with
size 1000 and exposes no paging. `ListNATPackagesInput` has `ZoneID` and
required `AvailabilityZoneID`; `ListNATPackagesOutput` is
`core.List[NATPackageOffer]`, since the package reply is also unpaged.
Reject nil package input and an omitted AZ.

`NATAvailabilityZone` exposes `UUID`, `Name`, `ZoneType`, `IsEnabled`,
`IsDefault`, and `Description` with their wire names; flags are booleans,
other fields strings. Require explicit presence for guard fields. The
package offer exposes string `UUID`, `Name`, `PackageID`, `ResourceServiceID`,
`BillingSKU`, `ServiceName`, `Description`, `CurrencyUnit`, and `CreatedAt`,
boolean `IsDefault`, numeric `MonthlyPrice`, and a typed `Price` with the
four observed numeric price fields. `Description` is nullable, so it is a
`*string`; the other strings were non-null in the evidence. Omit the offer's
`image`, whose discovery shape is not established; never expose license
keys. Do not change the separate inventory `NATPackage` model.

Catalog prices help callers discover offers; they never replace the fresh
quote for `MaxPrice`. Preserve unknown zone types in reads but refuse them
for purchase. Both read decoders reject duplicate keys, malformed envelopes,
and absent guard fields.
No public VPC-picker or whitelist method is needed for this release.

**Approved.** Use the V3-only no-subnet contract. Before
quote or create, require a valid whitelist reply with explicit
`data.enabledForAll: true`. False, absent, null, or malformed values stop
before pricing or ordering. Do not inspect, retain, or output
`whitelistedPortalUserIds`, and do not infer eligibility from membership.
No NAT `SubnetID` input or legacy subnet fallback exists. A known false
flag is unsupported configuration; malformed replies are `InvalidResponse`.

## Identity and payment

The direct reply cannot identify a NAT. Take the complete baseline before
ordering and require exactly one new exact-name row, absent by ID from that
baseline, in the named VPC. Match project, resolved region-level scope, and
package UUID. Verify availability-zone membership through the catalog and
VPC join, not by equating inventory `zoneUuid` with `AvailabilityZoneID`.
An ambiguous row never authorizes a renewal write or automatic deletion.

The live evidence combines an immediate quote-sized cash debit with the
later billing row. At runtime, the unique ACTIVE NAT and a matching PREPAID
billing row establish the supported paid state; the SDK does not add a
wallet scan or use a global balance change to identify a resource. Join
billing by exactly `(product: vserver, artifactType: nat, artifactId: NATID)`.
Require one row, `status: active`, and the selected catalog billing SKU with
quantity 1. Missing billing data keeps polling within the create bound;
conflicting or malformed data returns `NotSettled`. Inventory billingStatus
alone proves neither payment nor renewal. Billing `cost: 100` has unknown
meaning and must never be used as a price, debit, or guard.

The live runs reconcile the actual debit separately. Runtime `MonthlyPrice`
and `TotalPrice` remain the fresh quote, not a claimed measured debit.
Keep the accepted quote-to-debit race and `MaxPrice` behavior.

## Renewal off

**Approved.** Create includes a documented renewal-off step.
The order flag does not disable renewal. After the uniquely matched
NAT is ACTIVE and its billing row exists, perform these steps. Require a
validated order acceptance first; a lost or malformed order reply permits
read-only reconciliation and returns NotSettled, with no renewal PUT.

1. Require PREPAID, a present numeric channel, and known renewal state.
   Copy the row's channel; never default it or accept one from the caller.
   Refuse `isRenewing: true`; preserve the billing precedent for null or
   absent isRenewing. If already MANUAL with explicit null renewPeriod,
   confirm off and send no redundant write. Unknown or inconsistent state
   returns `NotSettled`.
2. For AUTO-RENEW, require the observed one-month renewPeriod. Resolve fresh
   account identity with `internal/billingresources.Account`, following
   [auto-renew](auto-renew.md#account-identity-and-privacy). Use the same
   core client and configured Billing endpoint. Never use a public service
   dependency, caller-supplied portal ID, or persisted identity cache.
3. Call `internal/billingresources.Put` exactly once for the matched NAT,
   using product `vserver`, artifact type `nat`, its ID and channel, and
   `autoRenewInfo: {isEnable: false, period: 43200}`. The helper sends the
   `portal-user-id` header and enforces `Once`, `NoRedirect`, safe errors,
   HTTP 200, code 200, true successAll, and an empty error array.
4. Read billing immediately, then every 2 seconds for at most 30 seconds,
   also bounded by the remaining 15-minute create deadline and context.
   Require the same unique row with `renewType: MANUAL` and explicit null
   `renewPeriod`. Set output `AutoRenew` false only from that observation.

If identity lookup, the PUT, or confirmation fails, return
`network.ErrNotSettled` with safe partial output and recovery text. A failed
PUT remains an error even if later reconciliation sees MANUAL. Never
resend the PUT or order, and never delete automatically. Preserve the last
valid renewal observation: true for AUTO-RENEW, false for MANUAL/null, nil
when unknown. No raw billing rows, account values, or error entries enter
NAT output. A later actor can change renewal after confirmation; no
conditional update exists.

This replaces the accepted prohibition on correcting NAT renewal with a
second write. The write is an explicit part of create and its help text.
It reuses the transport behind `storage put-project-auto-renew`, with NAT
identity and eligibility guards. It does not add a generic billing write
or authorize use of the storage command on a NAT.

The owner rejected two alternatives: leaving AUTO-RENEW enabled and
reporting true, which abandons the renewal-off guarantee and permits future
charges; and deferring create until an order can disable renewal, which
blocks this purchase release. The live probe confirmed MANUAL immediately
after the billing write.

## Waiting and recovery

**Approved.** Remove `NoWait` from NAT create's
input and CLI. Keep it on NAT delete. A create cannot confirm renewal before
ACTIVE because billing has no row during PROVISIONING. Keep the accepted
5-second provisioning poll and 15-minute bound starting after the order
attempt, including the renewal step. A shorter context deadline wins.
Deletion retains its 5-second poll and 10-minute bound; `NoWait` confirms
acceptance only. VPN's input is unaffected.

### Recovery

Recovery text says to run `network list-nat-instances` in the same scope and
`billing list-resources`, matching the known NAT ID. If no NAT appears,
inspect payment history without ordering again. If renewal is true or
unknown, warn that renewal may remain enabled and direct the caller to the
billing console for a deliberate disable and a confirming billing read.
Never suggest `storage put-project-auto-renew` for NAT. Keep safe known IDs,
last resource state, quote, and renewal observation in partial output.
Do not repeat create while purchase or renewal is unresolved. Deletion is
a separate deliberate action subject to its VPC guards and consent.

## CLI

Add `--availability-zone-id` to NAT quote and create. Keep `--zone-id` for
the region-level path scope. Help names both meanings and explains that
`--package-id` is the offer's UUID. Show zone discovery followed by packages
for that zone before a quote. Catalog reads need no `--yes` or price cap.
NAT create keeps both `--yes` and `--max-price` and its route-effect warning.

Create help must also say: "The purchase starts with auto-renew enabled.
After the NAT is ACTIVE, this command disables renewal through billing and
confirms it. The command waits for both steps. If it fails after purchase,
renewal may remain enabled; inspect the NAT and billing before taking
another action." There is no create `--no-wait`, period, renewal toggle,
raw order body, or auth bypass. Delete keeps `--no-wait` and `--yes`.

## Owner decisions

The owner approved these choices on 2026-10-11:

1. Purchase and auth: use direct collection POST; limit writes to verified
   IAM-user SDK login in HAN and fail closed for other auth modes.
2. Renewal: include the one billing renewal-off write and confirmation;
   preserve partial output and return NotSettled on failure, without resend
   or deletion.
3. Waiting: remove NAT create NoWait, retaining delete NoWait.
4. Placement: require AvailabilityZoneID, validate its enabled state, package
   and VPC membership, and preserve the existing ZoneID meaning.
5. Discovery: add ListNATZones and ListNATPackages with matching CLI reads.
6. V3: require enabledForAll true and expose no subnet fallback.
