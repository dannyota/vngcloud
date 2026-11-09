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

Prices are VND totals that include value-added tax (VAT); the API gives no
VAT breakdown or rate. GreenNode's [invoice documentation][invoice-management]
states that prepaid unit prices include VAT when an action is performed on
a resource.

[invoice-management]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/invoice-management

`pricing.GetQuote` asks what a resource would cost to create or resize. It
changes nothing and places no order:

```go
quote, err := priceClient.GetQuote(ctx, &pricing.GetQuoteInput{
	ResourceType: pricing.ResourceSnapshot,
})
if err != nil { log.Fatal(err) }
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
if err != nil { log.Fatal(err) }
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
and `volume.ResizeVolumeInput`. A resize `OptimumPrice` is a signed quoted
adjustment in VND for the remaining period. A negative amount is an
expected credit, not a confirmed refund. The exact gateway formula is
unverified. JSON, table, and text retain the minus sign. `QuoteResizeVolume`
reads the volume fresh
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

`pricing.GetQuote` accepts a finite negative `optimumPrice` only for exact
`Action: pricing.ActionResize`, for every resource type. Empty action sends
`create`; create, unknown, whitespace, and case variants refuse negatives
as `*vngcloud.APIError` with nil Output. Missing, null, malformed, NaN, and
infinite prices also fail with nil Output. Either signed zero is a valid
quote read. Each pricing response is limited to 1 MiB. Quote commands run
in CLI read-only mode.

`GetQuote` rejects a successful response that reflects any credential sent
by the call, including earlier or refreshed tokens and JSON-escaped strings.
The check also covers decoded numbers, including exponent notation and
rounding, before returning a quote or formatting an invalid-price error.
The check includes unknown fields and returns nil Output with
`*vngcloud.APIError`. Routine SDK response captures are suppressed for quotes,
including failed responses. A sent credential with numeric syntax, after
trimming whitespace, fails with nil Output and `*vngcloud.APIError` with
`Code: "InvalidResponse"` before any quote or price-bearing error is returned.
Unknown raw fields such as `artifactPrices`,
`originalCurrentPrice`, and `originalNewPrice` have no public model; keep
authorized raw evidence separately from decoded SDK output. Missing or null
`originalPrice`, `discountPrice`, and `discountPercent` retain their existing
zero defaults, which do not prove that the API supplied those totals.
Malformed JSON retains a sanitized `*json.SyntaxError` and its offset while
withholding response text and captures.

A paid write compares its quote with `MaxPrice` (default 0): monthly VND
for creates, the quoted adjustment for resizes. An excess wraps
`vngcloud.ErrPriceAboveMax`. `ResizeServer` and `ResizeLoadBalancer` accept
a credit at every valid cap but refuse zero with `vngcloud.ErrUnpriced`.
`ResizeVolume` refuses every nonpositive quote, naming the amount. Create
quote reads refuse negatives; paid server, volume, and load balancer
creates also refuse zero. `MonthlyPrice` retains its name and signed
`float64` type; it holds the quote, not the final charge or refund.

The API cannot enforce `MaxPrice` on the final charge. A concurrent
resource, price, or discount change can turn a quoted credit into a charge
even with `MaxPrice` 0. Never subtract expected credits from a run's spend
or use them to fund another operation. After an uncertain server resize,
read `GetServer` before considering another write; do not resend blindly.

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

Every paid create or resize in `compute` and `volume` follows the quote
policy above. The CLI requires `--yes` for server resize because it
restarts the server; a credit needs no `--max-price`. Deleting a server or
volume also requires `--yes`, since a delete cannot be undone.

If a server or volume is managed by OpenTofu or Terraform, a write made
through the SDK drifts from that state; keep such a resource's writes in
its own tool.

## Discovery metadata

`pricing.DiscoveryPrice` keeps unknown prices and discounts as nil. A quoted
price belongs to the account and uses VND with tax as returned. Discovery
metadata records the region, logical request count, completion coverage, safe
failures, and pricing notes. `QuotedAt` is UTC completion time, not an atomic
snapshot. Unread excluded catalogs have a nil `ExcludedGroups` count.

`pricing.PricingNote` distinguishes published, observed, conflicting, and
unverified evidence. `pricing.DiscoveryNotes()` returns reviewed source notes
without fetching websites. Published programs do not establish account
eligibility. Observed term comparisons do not promise universal discounts.
Effective storage rates apply at the quoted size. Unknown RAM units, bandwidth,
egress, and other excluded charges remain unknown.

Partial discovery returns `*pricing.DiscoveryError` with the validated output.
Use `errors.Is(err, pricing.ErrDiscoveryIncomplete)` to detect incomplete
coverage. `errors.As` still finds the original `*vngcloud.APIError`, and
cancellation remains visible through `errors.Is`. A failure before any usable
row returns the original error without partial output.
