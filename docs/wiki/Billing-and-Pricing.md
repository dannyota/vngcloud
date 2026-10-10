# Billing and Pricing

`billing` and `pricing` are separate packages, `danny.vn/vngcloud/billing`
and `danny.vn/vngcloud/pricing`. They are not clients on the root client from
[Services](Services.md); each has its own `New(cfg)`.

All amounts are VND. Field names carry no currency.

Budget, cost, and balance calls (the `billing` package) are per account: they
send no project ID and ignore the region in `Config`. Price quotes (the
`pricing` package) use the region, because they call a regional gateway. A
`Config` always needs a region, since the rest of the SDK needs one.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/pricing"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := billing.New(cfg)
	priceClient := pricing.New(cfg)
	_ = ctx
	_ = client
	_ = priceClient
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := billing.New(cfg)`.

## Budgets

A budget caps spend for one period type, `MONTHLY` or `QUARTERLY`, and one
budget type, `ACTUAL` or `FORECASTED`. The account can hold at most one
budget per type; the server rejects a second `CreateBudget` of a type it
already has.

Create a budget, then pause it with `UpdateBudget`. Pausing sends only the
`Status` field, so build it with `vngcloud.Ptr`:

```go
created, err := client.CreateBudget(ctx, &billing.CreateBudgetInput{
	Name:        "monthly-actual",
	PeriodType:  billing.PeriodMonthly,
	Type:        billing.TypeActual,
	LimitAmount: 5_000_000,
})
if err != nil {
	log.Fatal(err)
}

if _, err := client.UpdateBudget(ctx, &billing.UpdateBudgetInput{
	BudgetUUID: created.Budget.UUID,
	Status:     vngcloud.Ptr(billing.StatusPaused),
}); err != nil {
	log.Fatal(err)
}
```

`UpdateBudget` sends only the fields you set; every other field of the
budget stays as it was. There is no separate pause operation.
`UpdateBudget` and `UpdateBudgetThreshold` need at least one field set;
calling either with none is an error before any request goes out.
`CreateBudget` and `CreateBudgetThreshold` return the new object's UUID; a
create response with no UUID is an error too, rather than a budget or
threshold the SDK cannot name.

### Thresholds

A threshold triggers an alert once a budget's spend crosses a percentage.
The server starts a new threshold enabled, whatever the create request
sends; disable it with `UpdateBudgetThreshold`:

```go
threshold, err := client.CreateBudgetThreshold(ctx, &billing.CreateBudgetThresholdInput{
	BudgetUUID:          created.Budget.UUID,
	ThresholdType:       billing.TypeActual,
	ThresholdPercentage: 80,
})
if err != nil {
	log.Fatal(err)
}

if _, err := client.UpdateBudgetThreshold(ctx, &billing.UpdateBudgetThresholdInput{
	BudgetUUID:    created.Budget.UUID,
	ThresholdUUID: threshold.Threshold.UUID,
	Enabled:       vngcloud.Ptr(false),
}); err != nil {
	log.Fatal(err)
}
```

### Deletes

`DeleteBudget` and `DeleteBudgetThreshold` need nothing beyond the UUID; the
SDK adds no extra step. Deleting a budget deletes its thresholds on the
server, so the SDK does not delete them first:

```go
if _, err := client.DeleteBudget(ctx, &billing.DeleteBudgetInput{
	BudgetUUID: created.Budget.UUID,
}); err != nil {
	log.Fatal(err)
}
```

## Cost and balances

`GetCostOverview`, `ListCostResources`, and `ListBudgetAlerts` decode the
console's own field names, but no live capture has yet shown a response
with real cost or alert data for these three. Until one does, treat a field
that reads as zero as unconfirmed rather than as a true zero.

`GetCurrentPeriodCost` reports the account's current billing period:

```go
period, err := client.GetCurrentPeriodCost(ctx, nil)
if err != nil {
	log.Fatal(err)
}
log.Printf("actual cost this period: %.0f", period.PeriodCost.ActualCost)
```

`ListCostResources` returns at most 200 rows per page; a size above 200 is
capped to 200. Loop on `Page` up to `TotalPage` to read every row:

```go
var resources []billing.CostResource
for page := 1; ; page++ {
	out, err := client.ListCostResources(ctx, &billing.ListCostResourcesInput{
		StartDate: "2026-09-01",
		EndDate:   "2026-09-30",
		Page:      page,
	})
	if err != nil {
		log.Fatal(err)
	}
	resources = append(resources, out.Items...)
	if page >= out.TotalPage {
		break
	}
}
```

`GetBalances` reads the account's cash and POC (pay-on-credit) balances.
Each field is nil when the account has none of that kind:

```go
balances, err := client.GetBalances(ctx, nil)
if err != nil {
	log.Fatal(err)
}
if balances.Balances.Cash != nil {
	log.Printf("cash: %.0f", *balances.Balances.Cash)
}
```

## Price quotes

`pricing.GetQuote` asks what a resource would cost to create or resize. It
changes nothing and places no order:

```go
quote, err := priceClient.GetQuote(ctx, &pricing.GetQuoteInput{
	ResourceType: pricing.ResourceSnapshot,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("optimum price: %.0f", quote.OptimumPrice)
```

`ResourceInfo` is a `map[string]any` describing the resource in the create
call's own shape; leave it nil for a resource type that needs none. `Action`
is `pricing.ActionCreate` or `pricing.ActionResize`; empty sends
`ActionCreate`, so code written before `Action` existed is unchanged.
`pricing.ResourceSnapshot`, `pricing.ResourcePublicVIP`,
`pricing.ResourceServer`, `pricing.ResourceVolume`, and
`pricing.ResourceLoadBalancer` are the verified resource types. Other types
work through the plain string.

### Quoting a paid write

A paid write in the SDK, such as `monitor.CreateLogProject`, has its own
`Quote...` method that takes the write's own Input and returns
`*pricing.GetQuoteOutput`, so the quote always prices the exact resource the
write would create:

```go
quote, err := computeClient.QuoteCreateServer(ctx, &compute.CreateServerInput{
	ZoneID: "<zone-id>", FlavorID: "<flavor-id>", ImageID: "<image-id>",
	RootDiskSize: 20, RootDiskTypeID: "<volume-type-id>",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("server would cost %.0f VND a month", quote.OptimumPrice)
```

`volumeClient.QuoteCreateVolume` and `lbClient.QuoteCreateLoadBalancer` work
the same way for a volume and a load balancer create. A create quote
requires and sends only the fields that change the price, as the console
does:

| Quote | Required | Optional, never sent |
|-|-|-|
| `QuoteCreateServer` | `ZoneID`, `FlavorID`, `ImageID`, `RootDiskSize`, `RootDiskTypeID`; the data disk pair when set | `Name`, `VPCID`, `SubnetID`, `SecurityGroupIDs`, `SSHKeyID` |
| `QuoteCreateVolume` | `ZoneID`, `Size`, `VolumeTypeID` | `Name` |
| `QuoteCreateLoadBalancer` | `PackageID`, `ZoneID` | `Name`, `Scheme`, `SubnetID`, `Type` |

A quote checks the shape of the IDs and of `Scheme` when they are set, so
a bad value fails there as it will at the create. It does not check `Name`
or `Type`, which it never sends.

Encryption prices differently for the two resources. The server quote does not
send the disk encryption type IDs, only `encryptionVolume`; the volume quote
sends `EncryptionTypeID` when set. A server with an encrypted disk adds a `CES`
line of 30% of the flavor price (85,140 VND on `s2-general-1x2`), so the quote
and the guard price it through `encryptionVolume`. An encrypted volume costs the
same as a plain one (32,000 VND for 10 GB). See
[Compute-Servers](Compute-Servers.md#encrypted-disks) and
[Volume](Volume.md#encrypted-volumes).

The create still requires every field. The create's price guard quotes the
same priced-only body, so a quote and the guard price one request. Both
ignore the Input's `MaxPrice` and `NoWait` fields, which govern only the
write itself once it orders something, and neither sends user data.

`computeClient.QuoteResizeServer` and `volumeClient.QuoteResizeVolume` price
a flavor change or a grow the same way, from `compute.ResizeServerInput`
and `volume.ResizeVolumeInput`. A resize quote is the new configuration's
price for the rest of the current period, prorated to the minute, not the
difference from the old one. `QuoteResizeVolume` reads the volume fresh
on every call to learn its current size and type, independently of
`ResizeVolume`'s own read, the same way `QuoteCreateLogProject` always
rereads its own class list rather than sharing a read with its create.

`lbClient.QuoteResizeLoadBalancer` prices a change to a resource that
already exists, rather than a create: it takes a
`loadbalancer.ResizeLoadBalancerInput` (`LoadBalancerID` and the new
`PackageID`) and sends `Action: pricing.ActionResize` instead of
`ActionCreate`. A resize quote for an unknown `LoadBalancerID` returns the
server's own error unchanged, since the price guard checks input shape, not
that the resource exists.

A paid write refuses to order above its own `MaxPrice` (VND a month, default
0), with an error wrapping `vngcloud.ErrPriceAboveMax`. The load balancer
writes also refuse a create quote of 0 or less, or a resize quote of
exactly 0 (a negative resize quote is a refund and passes), whatever
`MaxPrice` is, with an error wrapping `vngcloud.ErrUnpriced`: nothing in
vLB is free, so such a quote means the gateway could not price the input.

```go
if _, err := monitorClient.CreateLogProject(ctx, in); errors.Is(err, vngcloud.ErrPriceAboveMax) {
	log.Fatal("quote is above the price you approved")
}
```

`monitor.ErrPriceAboveMax` is the same value as `vngcloud.ErrPriceAboveMax`,
so code written against either name still works.

## vServer prices

vServer has no hourly rate: a quote is VND a month, and a prepaid account
pays one month at once from its credit wallet when a server or volume is
created. A delete refunds the unused value, counted to the minute. Prices
are public list prices and can change; a `Quote...` call always returns the
account's current price, so treat this table as a rough guide, not a
guarantee:

| Item | Monthly (VND) |
|-|-|
| Server, 1 vCPU, 2 GB, 20 GB SSD root | ~347,800 |
| Server, 2 vCPU, 4 GB, 20 GB SSD root | ~631,600 |
| SSD volume | ~3,200 a GB |
| Volume, 10 GB SSD | ~32,000 |

Every paid create or resize in `compute` and `volume` refuses to send a
write priced above its own `MaxPrice`, which defaults to 0: setting
`MaxPrice` is the caller's explicit consent to pay up to that amount, the
role the CLI's `--max-price` flag plays for the matching command. A quote
of 0 is refused with `vngcloud.ErrUnpriced`: nothing in vServer is free, so
the gateway could not price the input. A
destructive write, such as deleting a server or a volume, needs no price
consent, since it does not order anything; the CLI instead requires its
`--yes` flag there, since a delete cannot be undone.

If a server or volume is managed by OpenTofu or Terraform, a write made
through the SDK drifts from that state; keep such a resource's writes in
its own tool.
