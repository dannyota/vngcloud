# Billing and Pricing

`billing` and `pricing` are separate packages, `danny.vn/vngcloud/billing`
and `danny.vn/vngcloud/pricing`. They are not clients on the root client from
[Services](Services.md); each has its own `New(cfg)`.

Budget, cost, balance, history, and price amounts are VND. Resource `Cost` has no
established meaning or currency.

Budget, cost, balance, and history calls are per account: they
send no project ID and ignore the region in `Config`. Price quotes (the
`pricing` package) use the region, because they call a regional gateway. A
`Config` always needs a region, since the rest of the SDK needs one.

## Setup

Use the standard `context` and `log` packages, plus `danny.vn/vngcloud`,
`danny.vn/vngcloud/billing`, and `danny.vn/vngcloud/pricing`:

```go
ctx := context.Background()
cfg, err := vngcloud.NewConfig(
    vngcloud.WithRegion("hcm-3"),
    vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
        RootEmail: "<root-email>", Username: "<iam-username>",
        Password: "<password>",
    }),
)
if err != nil { log.Fatal(err) }
client, priceClient := billing.New(cfg), pricing.New(cfg)
```

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
if err != nil { log.Fatal(err) }

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

All six budget and threshold writes send once, refuse redirects, and cap
responses at 8 MiB. A lost, malformed, or oversized reply can leave the
outcome unconfirmed. Read budgets or thresholds and reconcile the state
before deliberately retrying. Empty update and delete replies remain valid.
Budget limits decode exactly as int64; fractional or overflowing values fail.

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
if err != nil { log.Fatal(err) }

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

`GetCostOverview` returns `Summary.Breakdown` entries with `Key`, `Cost`,
and `Percent`. `Cost` reads the API's `value` amount in VND; zero is a valid
amount. `GroupedSeries` holds each group's `Key`, `Color`, and `Points`.
Each point has `Date` (the API's bucket label `x`, kept unchanged) and
`Cost` (the numeric `y` amount). Groups retain their own point counts and
order; the SDK does not fill gaps or combine groups.

`Series []CostSeries` remains available for legacy flat `date`/`cost`
responses. Captured grouped responses populate `GroupedSeries` and leave
`Series` nil. Existing Go fields and types remain available. Consumers of
the live trend data should read `GroupedSeries`; in the CLI:

```sh
vngcloud billing get-cost-overview --start-date 2026-11-01 \
  --end-date 2026-11-09 --group-by product --interval daily \
  --query GroupedSeries
```

Daily, weekly, and monthly requests with product or resource-type grouping
return the same grouped shape. For the tested month-long range, hourly
requests and resource-ID grouping returned HTTP 400 from the API. The exact
refusal reasons and shorter-range behavior remain unverified. The SDK sends
these options unchanged and returns the API error. The console's `table`
block remains outside the SDK output.

Populated `ListBudgetAlerts` data remain unverified; treat a zero alert field
as unconfirmed.

`GetCurrentPeriodCost` reports the account's current billing period:

```go
period, err := client.GetCurrentPeriodCost(ctx, nil)
if err != nil { log.Fatal(err) }
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

Each `CostResource` item holds `ResourceID`, `ResourceName`, `ResourceType`,
`Product`, `Cost`, `DailyAverage`, and `StartDate`. `Cost` is the resource's
total cost over the requested range, in VND, as the API reports it through
`totalCost`; the SDK does not use `cost` as a fallback. `DailyAverage` holds
the reported `dailyAvg`, and `StartDate` holds the reported `startDate`
string in `YYYY-MM-DD` format. The SDK omits `region` and the account's
`userId`.

Cost-resource pagination requires `page`, `size`, `total`, and `totalPages`;
its summary requires `totalAll`, `activeCount`, and `avgPerResource`. Missing
or null fields return `InvalidResponse` with no output. Empty resource lists
with explicit zero totals remain valid.

`GetBalances` reads the account's cash and POC (pay-on-credit) balances.
Each field is nil when the account has none of that kind:

```go
balances, err := client.GetBalances(ctx, nil)
if err != nil { log.Fatal(err) }
if balances.Balances.Cash != nil {
	log.Printf("cash: %.0f", *balances.Balances.Cash)
}
```

Cost, balance, budget, threshold, and alert reads accept responses up to
8 MiB. A larger 2xx response returns `InvalidResponse` with its HTTP
status; a larger 4xx or 5xx response keeps its status-based error but is
not retried and does not refresh the token.

Missing or null read data, ambiguous JSON keys, missing cost values, and
nonfinite balances return `InvalidResponse` with the observed HTTP status.
Explicit empty lists and null balance amounts remain valid. Successful
responses reflecting a sent bearer token are withheld, including raw resource
fields and credentials used before a refresh. Response bodies stay out of
routine captures.

## Transaction and payment history

`ListTransactions` and `ListPayments` are account reads verified with IAM
users. Service-account access remains unverified. Both Inputs require
`StartDate`; `EndDate` is optional. For example:

```go
transactions, err := client.ListTransactions(ctx, &billing.ListTransactionsInput{
    StartDate: "2024-11-01", EndDate: "2024-11-30", All: true,
})
if err != nil { log.Fatal(err) }
log.Println(len(transactions.Items))
```

```sh
vngcloud billing list-transactions --start-date 2024-11-01 --all
vngcloud billing list-payments --start-date 2024-11-01 \
  --end-date 2024-11-30 --query 'Items[].Amount' --output text
```

Dates are inclusive `YYYY-MM-DD` in fixed UTC+07:00, a manager choice fitting
observed filtering. The exact server zone and midnight behavior remain
unproved. Empty end means today in that zone, resolved once per call; the
wire end is the next calendar day. Timestamps keep their explicit offsets.
`Page` is 1-based. `Size` defaults to 50 and is capped at 50. `All` starts at
page 1 and rejects `Page > 1`. Outputs keep `Items` and paging metadata.

Complete walks have a 100-page limit, at most 100 times the verified page
size rows, 8 MiB per response, 32 MiB of decoded JSON values across pages,
and two minutes including authentication and retries. Surrounding whitespace
does not count toward the walk limit. Incomplete walks return
`ErrHistoryIncomplete`, nil Output, and CLI `HistoryIncomplete`, exit 1.
No error prints a report. A complete walk is not an atomic account snapshot;
a narrower range may help but cannot guarantee success.

JSON, text, and explicit queries can expose descriptions, references, IDs,
times, and amounts. Default tables omit descriptions and references. These
reads work with read-only profiles without prompts. Unknown transaction codes
keep `TypeCode` and an empty `Type`; unknown payment values remain raw.
Redaction covers verbatim reflection, not deliberate encoding.
Values retain the API's monetary basis. Historical VAT normalization is
unverified: wallet credits need not equal tax-inclusive cash paid. Payments
may describe the same economic activity as wallet charges; do not add them.

## Prepaid resources

`ListResources(ctx, nil)` reads the unfiltered central resource list across
products. It takes no filters or pagination parameters and sends no region
or project headers. `Items` contains resource rows; nullable counts and
`Extra` preserve the server summaries and raw warning/alarm thresholds.
Counts do not establish completeness. A row can outlive its resource. Two
deleted vMonitor log projects remained listed as `active` in the observed
account. Confirm that a resource exists with its product's own read.

```go
resources, err := client.ListResources(ctx, nil)
if err != nil {
    log.Fatal(err)
}
log.Println(len(resources.Items))
```

`Resource` retains artifact identity, product, billing type, renewal type,
nullable channel, renewal period, dates, and `IsRenewing`. Times are epoch
milliseconds. `RenewTypeNonRenewable`, `RenewTypeManual`, and
`RenewTypeAutoRenew` name the known strings; unknown strings remain visible.
Status fields, billing element quantity, and metadata preserve raw JSON.
Creator tags are omitted from the public model and routine captures.

Missing, null, or malformed envelope/list structure returns
`*vngcloud.APIError`. An empty array is valid. `Cost` is never a price or
estimated charge. The list does not quote every resource. For a vStorage
project's joined state and price, use
[Storage: Projects](Storage-Projects.md#auto-renew).

## Price quotes

See [Price quotes](Price-Quotes.md#price-quotes).

### Quoting a paid write

See [Quoting a paid write](Price-Quotes.md#quoting-a-paid-write).

## vServer prices

See [vServer prices](Price-Quotes.md#vserver-prices).

## Discovery metadata

See [Discovery metadata](Price-Quotes.md#discovery-metadata).

## Setup estimates

`pricing.New(cfg).Estimate(ctx, spec)` returns monthly quotes for specified
servers, separate volumes, public-VIP products, and object storage packages.
It makes read-only catalog and price calls. It places no order and reserves
neither capacity nor a future price.

```go
out, err := priceClient.Estimate(ctx, &pricing.EstimateInput{
    Servers: []pricing.EstimateServer{{
        Key: "web", Quantity: 2, ZoneID: "zone-1",
        FlavorID: "flavor-1", ImageID: "image-1",
        RootDiskSize: 20, RootDiskTypeID: "type-1",
        DataDiskSize: 10, DataDiskTypeID: "type-1",
    }},
    Volumes: []pricing.EstimateVolume{{
        Key: "extra", ServerKey: "web", Quantity: 3,
        ZoneID: "zone-1", VolumeTypeID: "type-1", Size: 50,
    }},
    PublicIPs: []pricing.EstimatePublicIP{{
        Key: "ips", Kind: "public-vip", Type: "public-vm", Quantity: 2,
    }},
    Storage: []pricing.EstimateStorage{{
        Key: "objects", Type: "Gold", QuotaGB: 30, Quantity: 2,
    }},
    AllowPartial: true, MaxRequests: 100,
})
if out != nil {
    log.Printf("processing complete: %t", out.Metadata.Complete)
    log.Printf("requested components complete: %t", out.Total.RequestedComplete)
}
if err != nil { log.Print(err) }
```

Replace synthetic selectors with returned catalog IDs. Servers require an
explicit unlicensed Ubuntu image with a usable minimum root disk size. Flavor
IDs go to the quote unchanged; success proves no deployment feasibility.
Server and volume lines name their zone, so a zone the catalog marks
disabled is still quoted; a refusal produces a failed line. Other images can
yield `unknown`.
Invalid explicit selectors or sizes reject the whole setup before any quote,
including with `AllowPartial`.

Each server quote includes its root and optional bundled data disk, plus CES
when returned for encryption. `Breakdown` shows per-unit included charges;
never add those lines to the server amount. `Volumes` describes additional
disks. Its quantity is independent of server quantity; `ServerKey` records
intent without attaching anything. An encrypted associated volume requires
an encrypted server root. Encryption selectors are returned type keys.

Quantities are required, from 1 to 1000 per row. They project independent unit
quotes without bulk discounts or shared allowances. Storage quantity counts
packages, each at `QuotaGB`. Empty storage region resolves from `Config`, with
`hcm-3` normally using HCM04. Unit prices remain in `Price.MonthlyPrice`;
`MonthlyAmount` includes quantity. Amounts use VND and VAT as returned.

`KnownMonthlySubtotal` sums valid extended amounts and is nil when none can
be summed. `RequestedMonthlyTotal` is nil unless every requested row is quoted
and processing completes. Display either sum with: "monthly, VAT as returned,
account-scoped estimate; not a commitment". Inspect `Coverage` beside both
sums. `CostComplete` is always false. Traffic, object egress and requests,
licences, bandwidth packages, NAT, load balancers, backups, snapshots, PAYG,
and negotiated discounts or credits remain unknown or excluded.

`floating-ip`, `external-interface`, and `reservation` return unknown prices
without quotes and forbid `Type`. `public-vip` requires `public`, `public-vm`,
or `public-mkp`. Unspecified IP costs are excluded, never assumed free.

Fail-fast calls return nil output and the cause. `AllowPartial` retains all
rows when a valid row survives, with `*pricing.DiscoveryError` preserving
causes. Authentication, exhausted throttling, cancellation, and limits stop
scheduling. Unscheduled rows have nil amounts and `not-attempted` coverage.
Use `errors.Is` for `pricing.ErrDiscoveryIncomplete`,
`pricing.ErrEstimateLimit`, and `pricing.ErrEstimateOverflow`; use `errors.As`
for `*pricing.EstimateArithmeticError` or `*vngcloud.APIError`. Multiplication
overflow fails its row; subtotal overflow clears both sums and preserves
valid rows in partial output. Neither can serialize a non-finite amount.

One invocation shares a default 500-request limit, at most 2000 logical calls,
an 8 MiB response cap, a 10000-row budget, a ten-minute deadline, and serial
attempt pacing of at least 500 ms. At most 1000 input rows and 10000 summed
quantities are accepted. Quotes are preflighted together; prices are cached
only within that invocation. `QuotedAt` is UTC completion time.

The basic example includes these reads only with `-estimate`. The
[estimate contract](https://github.com/dannyota/vngcloud/blob/master/docs/design/cost-estimate.md)
and [raw fixture provenance](https://github.com/dannyota/vngcloud/blob/master/testdata/pricing/estimate-provenance.md)
record scope and observed response shapes.
