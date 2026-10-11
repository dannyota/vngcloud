# Public NAT

`network.Client.ListNATInstances` reads one page of Public NAT inventory.
It supports `hcm-3` and `han-1` through their regional vNetwork origins.
Use the same login and selected project as other SDK services.

```go
client := network.New(cfg)
out, err := client.ListNATInstances(ctx, &network.ListNATInstancesInput{
    Page: 1,
    Size: 10,
})
if err != nil {
    return err
}
for _, nat := range out.Items {
    fmt.Println(nat.NATName, nat.Status)
}
```

A nil input or zero `Page` and `Size` requests page 1 with size 10.
Positive values pass through unchanged. Negative values fail before any
request. Each call requests one page and preserves the server's `Page`,
`PageSize`, `TotalPage`, and `TotalItem`. Page advancement and a server size
cap have not been verified live. The SDK does not automatically fetch more
pages or expose search and sort inputs.

Set `ZoneID` to use an explicit zone. Otherwise, discovery must return one
zone whose dashboard or gateway matches the selected region's verified
origin. A missing, ambiguous, or conflicting mapping returns
`vngcloud.ErrInvalidConfig`. Discovery errors reach the caller. The SDK
never substitutes a region name for a zone UUID. Zone and project IDs must
pass path validation before resource requests.

HCM uses `https://hcm-3-vnetwork.console.greennode.ai`; HAN uses
`https://han-1-vnetwork.console.greennode.ai`. An `Endpoints.VNetwork`
override controls both zone discovery and the NAT request. Discovery cannot
replace the override or select an arbitrary credential destination.

`ListNATInstancesOutput` holds `[]NATInstance` and page metadata.
`NATInstance` includes identity, name, status, timestamps, project and zone
UUIDs, a `NATPackage`, and a `NATVPC`. `NATPackage` includes a `NATImage`
with a `NATPackageLimit`. CPU, memory, and disk limit integers retain the
API's values; their units are unverified.

`NATGatewayIP`, `PublicIP`, `DeletedAt`, and `BillingStatus` are `*string`.
Null stays distinct from an empty string. Status and timestamp fields stay
raw strings. Unknown status values are preserved. A provisioning status
does not prove traffic flow.

The models omit account identifiers, `visible`, `message`, `subnet`,
null-only fields, `elasticIps`, and `image.licenseKey`. They also omit
`monthlyPrice`: the API's zero is not a usable package price. Unknown fields
are discarded.

A successful response needs `success: true`, positive page and size,
nonnegative totals, and an array of rows with nonempty UUIDs. A verified
empty response may omit `data` only when both totals are explicitly zero.
`data: null`, missing metadata, wrong types, and malformed bodies return
`InvalidResponse`. NAT keeps the shared API errors, cancellation, and GET
retry policy. NAT detail and rule methods are not exposed.

## Catalogs and purchase

`ListNATZones` returns an unpaged `Items []NATAvailabilityZone` catalog.
A nil input discovers the region-level `ZoneID` as inventory reads do.
`ListNATPackages` returns an unpaged `Items []NATPackageOffer` catalog.
Its input requires `AvailabilityZoneID`; `ZoneID` can be discovered.
Both catalogs request size 1000. Package reads always send `zoneUuid`.
Unknown zone types remain visible. Package descriptions are nullable;
package images and license keys are omitted.

`ZoneID` is the region-level vNetwork path scope. `AvailabilityZoneID`
identifies an availability zone within that scope. `PackageID` is the
selected offer's `UUID`, not its separate `PackageID` catalog field.
Quote, create, and delete require an explicit `ZoneID`.
Inventory `ZoneUUID` is an availability-zone ID, not the region path ID.
Inventory `ProjectUUID` identifies the public project; `VPC.ProjectID`
identifies a separate backend project. Scope checks use `ProjectUUID` and
`VPC.RegionID`. Placement checks use the catalog and the VPC picker zones.

```go
zones, err := client.ListNATZones(ctx, &network.ListNATZonesInput{
    ZoneID: zoneID,
})
if err != nil {
    return err
}
// Select one enabled AVAILABILITY zone from zones.Items.
packages, err := client.ListNATPackages(ctx, &network.ListNATPackagesInput{
    ZoneID: zoneID,
    AvailabilityZoneID: availabilityZoneID,
})
if err != nil {
    return err
}
// Select an offer's UUID from packages.Items.
input := &network.CreateNATInstanceInput{
    Name: "example-nat",
    ZoneID: zoneID,
    AvailabilityZoneID: availabilityZoneID,
    PackageID: packageUUID,
    VPCID: disposableVPCID,
    MaxPrice: 1000000,
}
quote, err := client.QuoteCreateNATInstance(ctx, input)
if err != nil {
    return err
}
fmt.Println(quote.MonthlyPrice, quote.TotalPrice, quote.Currency)
created, err := client.CreateNATInstance(ctx, input)
if err != nil {
    return err
}
```

Quote and create require an enabled AVAILABILITY zone, an exact package
UUID in that zone, and a VPC in the selected project that lists the zone.
Create refuses an existing NAT in that VPC, an exact duplicate name, and
incomplete or changing inventory scans. Multi-page behavior has not been
verified live. The SDK checks scan consistency and fails closed. Serialize creates in
the same VPC. Guards cannot prevent a concurrent order; the API has no
idempotency key or conditional create.
The V3 whitelist must explicitly enable all users. There is no subnet
input, membership check, or legacy fallback.

Purchase and delete support only `han-1` with the SDK's IAM-user login.
Static bearer tokens and custom credentials providers cannot establish
that login provenance. Paid-write provenance trusts the SDK token cache,
protected by file mode 0600 in a 0700 directory. Unsupported auth or region
returns
`vngcloud.ErrInvalidConfig` before a request. Quotes, catalogs, and inventory
reads retain their HCM and HAN auth support. Quotes use the same placement
and V3 guards as create.

`NetworkQuoteOutput` exposes optimum, original, and discount prices,
nullable discount percent, typed price properties, monthly price, total
price, and currency. Property `CurrentPrice` is a nullable `*float64`;
null remains nil, and an omitted field is invalid. Monthly and total prices
equal the fresh one-month quote in VND. Catalog prices never authorize a
purchase. Create quotes
again immediately before ordering. `MaxPrice` must be finite and
nonnegative. Zero refuses a positive quote; equality passes. An above-cap
quote returns `vngcloud.ErrPriceAboveMax`. A nonpositive quote returns
`vngcloud.ErrUnpriced`. The cap is local; the server has no price lock or
atomic debit limit.

Creating NAT adds a `0.0.0.0/0` egress route for every VM in the named VPC.
Use a disposable VPC for tests. Do not test in a production VPC.

The SDK sends one order with renewal false, no period or payment type,
and an empty tag list. The service starts the purchase with renewal
**enabled** despite that flag. Create waits for one new exact-name NAT
matching the baseline, VPC, package, project, and region-level scope.
ACTIVE plus a matching active PREPAID billing row establishes the paid
state. The SDK then reads fresh billing account identity, sends one
renewal-off PUT, and confirms MANUAL with an explicit null renewal period.
An already MANUAL/null row needs no PUT. No order or renewal write is resent.

Create polls every 5 seconds for at most 15 minutes after the order
attempt. Renewal confirmation reads immediately, then every 2 seconds
for at most 30 seconds within that overall bound. A shorter context wins.
Create has no `NoWait`, period, or renewal input. Another actor can change
renewal after confirmation; the billing API has no conditional update.

`CreateNATInstanceOutput` retains `NATInstance`, quote prices, `Currency`,
and `AutoRenew`. `OrderID` stays empty because the order returns no ID.
`AutoRenew` is false only after a valid MANUAL/null billing observation,
true after a valid AUTO-RENEW observation, and nil when unknown.

An observed ERROR returns `network.ErrFailed` with partial output.
Lost or malformed order replies, read failures, timeouts, ambiguous
identity, or renewal failures return `network.ErrNotSettled` with safe
partial output. A definite HTTP 4xx refusal, except 408 and 429, triggers one complete
inventory scan. If no new exact-name row exists, create returns the safe
API error immediately. A new row keeps the outcome unconfirmed.
A failed order reply never authorizes the renewal PUT.
A failed renewal PUT remains an error even if a later read shows MANUAL.
The SDK never deletes automatically.

After an uncertain result, run `network list-nat-instances` in the same
scope and `billing list-resources`, matching the known NAT ID. If no NAT
appears, inspect payment history without ordering again. Renewal may remain
enabled. Use the billing console for a deliberate disable and confirm by
a billing read. Do not repeat create while purchase or renewal is
unresolved. Do not use the storage renewal command for NAT.

## Delete

```go
_, err = client.DeleteNATInstance(ctx, &network.DeleteNATInstanceInput{
    ZoneID: zoneID,
    VPCID: disposableVPCID,
    NATID: created.NATInstance.UUID,
})
```

Delete verifies the VPC and exact NAT identity before sending one DELETE
with `{}`. Confirmed absence before send returns `vngcloud.ErrNotFound`.
A VPC mismatch refuses the write. Default waiting polls complete inventory
every 5 seconds for at most 10 minutes after the attempt. `NoWait` confirms
only a valid acceptance. Confirmed absence settles deletion even after a
lost, malformed, or server-error reply. A definite HTTP 4xx refusal, except
408 and 429, triggers one complete scan. A still-present NAT returns the
safe API error; absence contradicts the refusal and returns
`network.ErrNotSettled`. Other unconfirmed outcomes also return that error.
Delete can interrupt VPC egress. Absence does not guarantee a refund or
restoration of earlier routes. Deletion is a separate deliberate action;
create failures do not authorize it automatically.

Catalogs, purchase guards, quotes, and writes reject duplicate keys and
case-variant wire names. Guard fields require explicit presence.
Their raw responses and server messages are withheld from captures and
errors. NAT and billing responses are capped at 4 MiB. NAT rejects replies
that reflect the sent credential, including escaped JSON strings.
Outputs omit account IDs, portal IDs, tokens, and billing rows.

See [Network](Network.md) for other network operations and
[Errors](Errors.md) for shared error handling.
