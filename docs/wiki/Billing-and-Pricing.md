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

`pricing.GetQuote` asks what a resource would cost to create. It changes
nothing and places no order:

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
call's own shape; leave it nil for a resource type that needs none.
`pricing.ResourceSnapshot` and `pricing.ResourcePublicVIP` are the verified
resource types. Other types work through the plain string.
