# Price Quotes

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
same priced configuration. A quote ignores the Input's `MaxPrice` and
`NoWait`, which govern only the write, and never sends user data.

With `CreateServerInput.AttachFloatingIP: true`, `QuoteCreateServer` prices
only the server. The quote, guard, and `MonthlyPrice` equal the server
price: the 347,800 VND reference stays 347,800 with the included IP.
Server totals and properties stay unchanged. One `FLOATING IP` line has
optimum and monthly price 0, discount percent 0, nil current price, and
description `Included in server order for the verified configuration`.
Missing, nonfinite, or inconsistent server totals give no partial quote.
Nonpositive server prices remain `vngcloud.ErrUnpriced`.

Both default and public opt-in make one server price request and no
exposure reads; `AllowOpenLogin` is ignored on quotes. The response retains
its 1 MiB bound and credential protection. `pricing get-quote` with
`--resource-type elastic-ip` remains available unchanged; server create
does not use that quote. The API cannot bind an order to this quote or
`MaxPrice`. Inclusion covers the verified create terms; retention,
reservation, delayed charges, and IP refunds remain unconfirmed. See
[Floating IP create](Compute-Servers.md#floating-ip-create).

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
