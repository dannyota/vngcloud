# Setup cost estimate

Status: Accepted. The owner approved an automatic architecture total for this
command and the six decisions below. That exception supersedes the no-total
decision in
[pricing discovery](pricing-discovery.md#owner-decisions-and-open-questions)
only for this surface. This design authorizes no live run, order, or cleanup.

`pricing.Client.Estimate` and `vngcloud pricing estimate` price a specified
setup in one invocation. Each server, volume, public-IP product, and object
storage package has a monthly quote. Their sum is an estimate of specified
recurring charges, with unknown and excluded costs visible beside the sum.
One invocation can make multiple catalog reads and quote POSTs.

## Authorities and scope

Reuse [pricing discovery](pricing-discovery.md), its
[response guard](pricing-discovery-api.md#response-security),
[paid quote bodies](vserver-paid-writes-api.md#quote-requests),
[encryption](encrypted-volumes.md#body-keys), and
[storage pricing](storage-projects-api.md#price). The
[evidence delta](cost-estimate-api.md) records dependencies, gaps, and security
obligations. Those sources retain ownership of tariffs and live observations.

The first surface covers one configured account and vServer region, explicit
server images and flavors, root disks, one bundled data disk per server,
additional standalone volumes, named public-IP products, and monthly object
storage packages. Storage uses its own resolved region names. It does not
build, validate deployment feasibility, reserve capacity, or purchase a setup.
No inventory, balance, permission escalation, order, attachment, or usage call
is needed. NAT, load balancers, backups, snapshots, software licensing, PAYG
storage, bandwidth packages, traffic, and negotiated programs stay excluded.

## SDK surface and reuse

Add to `danny.vn/vngcloud/pricing`:

```go
func (c *Client) Estimate(
    ctx context.Context, in *EstimateInput,
) (*EstimateOutput, error)
```

The SDK remains standard-library only. Existing methods, import paths, public
models, JSON tags, paid-write guards, and discovery outputs stay compatible.
The CLI calls this public method; it has no cloud HTTP path of its own.

`compute`, `volume`, and `storage` already import `pricing`; importing them
from `pricing` would form cycles. Extract only reusable private mechanisms:
priced-field builders into `internal/quoteinput`, guarded catalog selection
into `internal/pricecatalog`, and presence-aware quote decoding into
`internal/pricequote`. These packages may use core, transport, and discovery,
but import no public service package or public pricing model. Existing service
adapters retain their input validation and ordinary exchange behavior.
Estimate converts private results to public pricing models. Do not copy bodies
or call the four public discovery helpers with four independent sessions.
Builder drift tests must prove unchanged existing requests and guard prices.

All types below belong to `pricing`. JSON uses the exact Go field names.
No input accepts raw resource maps, action, endpoint, headers, account ID,
credentials, login data, period, promotions, or order options.

```go
type EstimateInput struct {
    Servers      []EstimateServer
    Volumes      []EstimateVolume
    PublicIPs    []EstimatePublicIP
    Storage      []EstimateStorage
    MaxRequests  int
    AllowPartial bool
}
type EstimateServer struct {
    Key, ZoneID, FlavorID, ImageID string
    Quantity                     int
    RootDiskSize                 int
    RootDiskTypeID               string
    RootDiskEncryptionTypeID     string
    DataDiskSize                 int
    DataDiskTypeID               string
    DataDiskEncryptionTypeID     string
}
type EstimateVolume struct {
    Key, ServerKey, ZoneID, VolumeTypeID string
    Quantity, Size                     int
    EncryptionTypeID                   string
}
type EstimatePublicIP struct {
    Key, Kind, Type string
    Quantity        int
}
type EstimateStorage struct {
    Key, Region, Type string
    Quantity          int
    QuotaGB           int64
}
```

`Key` is a caller-chosen component label, unique across all arrays. `Quantity`
is required, 1..1000; omission is invalid. A row describes identical units,
not aggregate disk capacity. Each server unit includes its specified disks.
Volume `Quantity` is the total number of separate volumes, even when
`ServerKey` names a server group. It is never multiplied by server quantity.
`ServerKey` records intended association only; it allocates or attaches nothing.
Omit that key for an independent volume. Omitted arrays mean not requested.

Server requires zone, flavor, image, positive root size, and root type. Optional
data size and type must appear together; data encryption requires that pair.
Additional disks use `Volumes` and are not also placed in the server quote.
Volume requires explicit zone, type, and positive size. Empty encryption means
plain. Use returned encryption keys, not key material.

IP `Kind` is `public-vip`, `floating-ip`, `external-interface`, or
`reservation`.
Only `public-vip` requires `Type`, exactly `public`, `public-vm`, or
`public-mkp`; other kinds forbid `Type` and yield unknown amounts. No generic
"public IP" default and no free-IP deduction is permitted.

Storage requires exact returned project-type `Type` and positive `QuotaGB`.
Empty `Region` uses existing storage resolution; output records the resolved
name, including HCM04 for the normal hcm-3 mapping. Quantity means separate
packages, each at that quota. Never quote combined quota and divide it.

### Validation and catalog selection

Before authentication or requests, reject nil/empty input, duplicate or invalid
keys, invalid enums, missing required fields, malformed IDs, negative values,
invalid pairs, and exceeded input bounds as `vngcloud.ErrInvalidInput`.
Keys use `^[A-Za-z0-9_-]{1,64}$`. Resource IDs use existing path-ID checks;
encryption keys use existing type-ID checks. A `ServerKey` must resolve, share
the volume zone, and, for an encrypted volume, name a server with an encrypted
root. This conservative association rule makes no attachment guarantee.
Check the full input before processing any component; partial mode cannot
turn a malformed spec into a different setup.

Read only needed catalogs through the shared session. For servers and volumes,
reuse zones and region-wide volume types, preserving both type-ID fields and
the existing fallback. Do not filter parent type zones by their misleading
zone metadata. Validate specified sizes against type limits. Read encryption
keys once when requested. Unknown explicit zone, type, or encryption key and
out-of-range sizes are invalid input after catalogs, before quotes. A disabled
zone yields `inapplicable`, nil amount, and an explanation.

For each enabled server zone, read images once. Require the exact image ID,
explicit `licence:false`, Ubuntu ignoring ASCII case, and a present positive
minimum root size. A requested root below that minimum is invalid input;
an absent or unusable minimum yields unknown. Other image types or
unknown licensing yield `unknown` with no server quote. Missing explicit image
IDs are invalid input. Reuse safe image decoding; never retain `licenseKey`.
Do not select a reference image, replace a flavor, infer RAM units, or trust
image zone metadata as compatibility proof. The supplied flavor ID goes to the
quote unchanged; no broad flavor discovery is necessary. A quote refusal is
an error, not a signal to try alternatives. A successful quote proves neither
flavor/image deployability nor capacity.

Storage reuses guarded region and six catalog/config reads per selected region,
active monthly-offering selection, `AllowPeriod` containing 1, and configured
quota limits. An unknown explicit type or invalid quota is invalid input.
Catalog ambiguity or malformed catalogs are response errors. Do not call
project-type details, account information, or checkout. These checks constrain
only this new helper; they do not tighten existing create-quote inputs.

## Quotes and component accounting

Use the exact accepted builders and routes, detailed in the
[evidence delta](cost-estimate-api.md#request-and-response-contracts).

- Server: `server/create`, period 1, full specified root disk and optional
  bundled data disk; `encryptionVolume` is true when either encryption ID is
  set. Type keys never enter the server quote. Use top-level `optimumPrice`,
  which includes compute, root, bundled data disk, and CES when quoted.
- Volume: `volume/create`, period 1, explicit size/type/zone, and optional
  `encryptionType`. Use the returned monthly total. An encrypted standalone
  volume quote does not include server CES.
- Public VIP: one `public-vip/create` quote per requested distinct type with
  `isBuyMorePo:false` and `isPoc:false`. Unknown IP kinds make no quote call.
- Storage: the price-only `object_storage/create` body with resolved type,
  monthly purchase ID, and quota; no period or order-only keys. Use enveloped
  `data.optimumPrice` and require a successful storage envelope.

Keep the full server quote as one counted component. Its `INSTANCE TYPE`,
`ROOT DISK`, data disk, and CES properties are informational breakdowns, never
additional summed rows. Do not add discovery's compute-only price or quote the
same root/bundled disk again as a volume. Separate volumes are counted once.
Input documentation must distinguish bundled and additional data disks.

A unit quote is not a bulk quote. Multiply each validated unit amount by its
quantity, then sum extended amounts once in input order. Memoize identical
successful bodies within the invocation, including endpoint and storage region
headers, while retaining each distinct input row. Do not retry the same failed
body for duplicate rows; propagate its failure to every dependent row.
`Quantity > 1` is explicitly an independent-unit projection. Do not infer bulk
discounts, shared free allowances, marginal rates, or combined-order
eligibility.
Never multiply a per-GB rate from a minimum-size discovery result.

Amounts use existing float64 semantics, without intermediate rounding. Reject
missing, null, negative, zero, or non-finite required monthly prices. No current
quote product has established zero as free. Multiplication overflow fails its
component; subtotal overflow invalidates both totals. The
[arithmetic contract](cost-estimate-api.md#arithmetic-failures) defines error
identity, partial output, and completeness. Preserve optional discounts only
when present; never sum percentages or apply another discount. A mismatched
currency fails the component; no conversion or mixed-currency total is allowed.
Round only display to two decimals. This is not invoice arithmetic.

## Output and completeness

```go
type EstimateOutput struct {
    Items    []EstimateItem
    Total    EstimateTotal
    Coverage []EstimateCoverage
    Metadata DiscoveryMetadata
}
type EstimateItem struct {
    Key, Kind, ServerKey, Region, ZoneID string
    Quantity                           int
    Price                              DiscoveryPrice
    MonthlyAmount                      *float64
    Breakdown                          []EstimateCharge
}
type EstimateCharge struct {
    Name         string
    MonthlyPrice *float64
}
type EstimateTotal struct {
    KnownMonthlySubtotal, RequestedMonthlyTotal *float64
    Currency, Tax, Basis, Scope                 string
    RequestedComplete, CostComplete            bool
}
type EstimateCoverage struct {
    Topic, Scope, Status, Reason string
}
```

`Price.MonthlyPrice` is the unit quote; `MonthlyAmount` includes quantity.
`Kind` is `server`, `volume`, an IP kind, or `object-storage`.
`Price.Status` reuses `quoted`, `unknown`, `inapplicable`, and `failed`.
A failed or unscheduled row has nil amounts. Record `not-attempted` as its
coverage reason when a terminal error stops scheduling. Preserve all input
rows whenever returning output, ordered Servers, Volumes, PublicIPs, Storage,
then original array order. Breakdown amounts are per unit; unknown line amounts
remain nil. Missing breakdowns do not invalidate a valid top-level quote.
Do not publish property descriptions or catalog blobs. Arrays use `[]`.

`KnownMonthlySubtotal` sums valid components, or is nil if none or overflowed.
`RequestedMonthlyTotal` is present only when every requested row is quoted and
processing completed. Otherwise it is nil; never silently total unknown as
zero. `RequestedComplete` describes that same condition. `Metadata.Complete`
means processing completed, including deliberate unknown/inapplicable rows;
it is false for operational or arithmetic failures. `CostComplete` is always
false in this release because usage and excluded products are not priced.
No field asserts a complete architecture bill.

Total labels are `Currency=VND`, `Tax=as-returned`,
`Basis=sum-of-independent-monthly-quotes`, and
`Scope=specified-recurring-components`. VAT means value-added tax; retain the
quote's tax basis and never add VAT again. Report "monthly, VAT as returned,
account-scoped estimate; not a commitment" beside every displayed sum.
The account scope follows Config without exposing account identity. `QuotedAt`
is UTC completion time, not an atomic snapshot or price lock. No hourly,
annual, term-discount, pro-rata, or quote-to-order projection is offered.

Always include machine-readable `Coverage` entries for bandwidth packages,
vServer traffic, object egress/requests, software licences, NAT, load balancers,
backups, snapshots, PAYG, and negotiated discounts/credits. Use `excluded` for
unsupported products and `unknown` for applicable unpriced usage or requested
unverified IP kinds. Name requested keys in scope and use `setup` for general
exclusions. Explain absent IP selection as "no IP product specified; any
required IP cost is excluded" when servers are present without PublicIPs.
Omission is a scope choice, not evidence of a free or unnecessary service.
Reuse `PricingNote` sources and evidence labels for tariff conflicts, region
limits, and no observed term discount. Do not scrape or guess usage tariffs.

## Bounds, errors, and cancellation

Use one `internal/discovery.Session` across every auxiliary read and quote.
Reuse its 500 default and 1..2000 request limit, 8 MiB bodies, 10000 collected
rows, serial 500 ms attempt pacing, ten-minute child deadline, invocation cache,
and [shared-wait exception](pricing-discovery.md#shared-waits-and-cancellation).
No helper retry or resource wait exists. Preserve transport retry statuses,
Retry-After, at most `2*(RetryCount+1)` attempts per logical call with one 401
refresh, and one attempt for HTTP 500. Authentication is outside request counts.
Existing token/project caches retain their rules. No persistent price cache.

New input bounds: at most 1000 component rows, 10000 summed quantities, and
32 distinct resolved storage regions; count rows without expanding quantity.
Use checked integer addition. Count catalog and breakdown rows against the
session's 10000-row limit as well. Established catalogs are unpaged; reject
unexpected paging metadata rather than invent pagination.

The logical upper bound is `P + D*(2+T) + A + E + L + 6*R + Q`: P is at most
one uncached project lookup when regional products need it; D is 1 when servers
or volumes need zones and type-zone parents, else 0; T is returned type zones;
A is enabled server zones needing images; E is 1 if encryption keys are needed;
L is at most one storage region-list read; R is selected storage regions; Q is
distinct eligible quote bodies. Existing cached resolution can reduce reads.
Quantity adds no calls. Unknown-only IP specs need no project lookup.

Resolve catalogs and construct all eligible bodies before any quote. Preflight
Q against the remaining budget and refuse the whole quote phase if it cannot
fit. Return the required logical count in the limit error. Budget, row, and
cancellation limits never silently truncate. Expose new
`pricing.ErrEstimateLimit` wrapping the private limit cause via `errors.Is`;
use it for runtime request/row limits, not malformed input bounds.

Default is fail-fast: first error returns nil output and its cause. With
`AllowPartial`, continue independent branches after ordinary catalog or quote
errors; mark all affected rows failed and keep amounts nil. Authentication,
exhausted throttling, cancellation, and limits stop scheduling globally. A
catalog-dependent invalid input aborts the whole call even in partial mode.
If any valid row survives, return output plus `*pricing.DiscoveryError`;
otherwise return nil and the cause. Deliberate unknown rows are valid results.
Arithmetic failures follow the linked contract and its subtotal exception.
An entirely unknown successful estimate returns output, nil error, and nil sums.

Reuse `DiscoveryFailure` with trusted component keys, static classification,
and actual HTTP status. Preserve `errors.Is` for `ErrDiscoveryIncomplete`,
`ErrInvalidInput`, auth/rate limits, the estimate limit, and context errors;
`errors.As` must still find `*vngcloud.APIError`. Schema errors are static
`InvalidResponse`, with actual status. HTTP 500 is not converted to not-found.
No raw response or reflected text enters failure keys, errors, or partial rows.

## CLI, dependencies, and documentation

```sh
vngcloud pricing estimate --cli-input-json file://setup.json
vngcloud pricing estimate --cli-input-json file://setup.json \
  --allow-partial --max-requests 100 --output table
```

Example spec; replace synthetic IDs with catalog IDs:

```json
{"Servers": [{"Key": "web", "Quantity": 2, "ZoneID": "zone-1",
  "FlavorID": "flavor-1", "ImageID": "image-1",
  "RootDiskSize": 20, "RootDiskTypeID": "type-1"}],
 "Storage": [{"Key": "objects", "Quantity": 1,
  "Type": "Gold", "QuotaGB": 30}]}
```

Use the existing JSON input facility, including inline JSON, with only
`--allow-partial` and `--max-requests` as component-independent local flags.
Explicit flags override JSON controls. Global profile, region, output, debug,
and query behavior remain available. Repeated nested resource flags and YAML
would add ambiguity and parsing work; recommend neither. Add no dependency:
`encoding/json` and existing CLI libraries suffice. Any future dependency must
pass [dependency policy](../../instructions/security.md#dependencies).

For this command, open `file://` paths with `openRegularFileNonBlocking`,
then call `f.Stat()` and reject non-regular descriptors before reading. Close
on every path. Read at most 1 MiB plus one byte from that same descriptor;
validate and decode those same bytes, never reopen the path. Refusals exit 2
before authentication. Require the deterministic FIFO test in the evidence file.
Validate the original JSON before map decoding: cap file and
inline input at 1 MiB, require one object, reject duplicate keys, unknown keys,
case mismatches at every depth, and trailing values. Limit nesting to 16.
Use static field-path errors without echoing input values. No file includes,
environment expansion, URL fetching, stdin convention, or templating is added.
A local spec is data, never an authority to change credentials or endpoints.

JSON contains the whole Output. Table/text list key, kind, region/zone,
quantity, status, unit monthly price, extended amount, currency, and tax;
text is tab-separated. Show breakdowns separately labelled "included in server
quote". Render nil as `unknown`. Label sums "Known monthly subtotal" and
"Requested monthly total (specified components only)". Always print coverage
and the estimate caveat on stderr for table/text and whenever `--query` can
hide metadata, including JSON queries. Escape control characters everywhere.

Exit 0 means processing completed, not complete cost coverage. Invalid input
exits 2; auth exits 3; standalone not-found exits 4; other errors exit 1.
Partial output is printed on stdout with the existing structured error on
stderr, exit 1. Queries and rendering cannot clear that failure. No `--yes`,
`--max-price`, interactive consent, or write fallback exists.

Implementation updates SDK pricing guidance, `docs/wiki/CLI-Pricing.md`, the
CLI operation/help source and generated docs, with examples covering quantities,
bundled versus separate disks, unknown IPs, exclusions, and partial failures.
Do not edit generated pages independently. Release private reuse and guarded
SDK composition before wiring the CLI, in one public feature release after
owner approval and independent design, plan, and code review. Require the
[evidence checks](cost-estimate-api.md#required-checks); no live run is implied.

## Owner decisions

1. Recommend `pricing.Client.Estimate` and `pricing estimate`, with private
   shared mechanisms to avoid import cycles and duplicate quote construction.
2. Recommend the existing JSON spec input, explicit IDs/image, quantities, one
   configured vServer region, and named storage regions. No new dependency.
3. Recommend one counted full-server quote plus separately specified volumes;
   retain bundled data disk and encryption support behind raw-fixture gates.
4. Recommend independent-unit quantity projections, monthly-only VND with VAT
   as returned, no inferred discounts, and no complete-bill claim.
5. Recommend both known subtotal and nullable requested total, mandatory
   coverage, distinct processing/cost completeness, and exit 0 for deliberate
   unknowns. Automation must test `RequestedComplete` and inspect Coverage.
6. Recommend discovery's bounds, fail-fast default, optional partial errors,
   and shared-wait exception, with the new input limits above. Keep unknown
   products unknown; any broader quote surface needs its own evidence and brief.
