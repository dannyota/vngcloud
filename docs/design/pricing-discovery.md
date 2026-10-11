# Pricing and options discovery

Status: Accepted. The owner approved the six decisions below. An independent
plan review precedes implementation. This design approves no live run.

Four read-only SDK helpers and CLI commands compare regional compute, volume,
object storage, and network options. Unknown charges stay unknown. Existing
quote inputs, paid-write guards, outputs, and retries remain unchanged.

## Scope and authorities

[API evidence](pricing-discovery-api.md) owns sources and probe results.
Authorities: [SDK layout](sdk-and-cli.md#service-packages),
[billing](billing.md#price-quotes), [paid API](vserver-paid-writes-api.md),
[lookups](cli-usability.md), [encryption](encrypted-volumes.md),
[storage](storage-projects-api.md#price), and [NAT](network-writes-api.md).
Accepted quote designs and current code establish `Action` support despite
older billing text saying every quote sends `create`.

Provisioning, checkout, renewals, promotions, balance and resource inventory,
and eligibility reads are excluded. This catalog reserves no price or capacity.
Windows, marketplace licensing, non-vServer GPU services, and negotiated
contracts are excluded. Returned GPU families remain visible without inferred
prices. The helpers do not estimate a complete architecture.

## SDK surface

Methods use `(context.Context, *Input) (*Output, error)` on existing service
clients. Input/Output names append those suffixes to the method name. Nil input
means defaults. Additions preserve existing callers. The SDK remains standard
library only; the CLI uses only public SDK methods.

| Method | Input fields beyond common fields | `Items` element |
|-|-|-|
| `compute.ListFlavorPrices` | `ZoneID`, `Family`, `Platform`, `Name` string; `MinRAM`, `MaxRAM` int64; `IncludeNonPublic`, `AvailableOnly` bool | `FlavorPrice` |
| `volume.ListVolumePrices` | `ZoneID`, `VolumeTypeID` string; `IOPS`, `Size` int; `Encryption` string | `VolumePrice` |
| `storage.ListStoragePrices` | `Region`, `Type` string; `QuotaGB` int64 | `StoragePrice` |
| `network.ListNetworkPrices` | None | `NetworkPrice` |

Common fields are `MaxRequests int`, `AllowPartial bool`. The first release has
no `Periods` input or term projection. Server and volume quotes send period 1;
storage retains its existing monthly body without a period key. Existing create,
renewal, and generic quote inputs do not change.

`ZoneID` empty means all returned availability zones in the configured vServer
region, not every region. `Region` follows existing storage resolution,
including HCM04 for the normal hcm-3 mapping; output always names the resolved
storage region. Do not equate HCM04 with HCM03 or a vServer availability zone.
Validate explicit zones against `ListZones`. Preserve disabled-zone rows and
status but skip their quotes with `Status=inapplicable` and a note. Zone
observations belong in
[catalog evidence](pricing-discovery-api.md#catalog-results).

`Family`, `Platform`, `Name`, and `Type` are case-sensitive exact matches
against returned keys or names, with no substring or guessed ID lookup.
`MinRAM`/`MaxRAM` compare the raw integer `memory`, whose unit is unknown; zero
means unbounded. Reject negatives and minimum greater than maximum.
`Size`/`QuotaGB` zero uses each offering's minimum; positive values select a
comparison size. Reject negative sizes, negative IOPS, bad identifier shapes,
conflicting bounds, and unknown enum values before any request.
Catalog-dependent validation follows the catalog read, before any quote. A
requested size outside one offering's range produces an `inapplicable` row
without a quote, not a fabricated price or a failed whole command.

`Encryption` is `all` (default), `none`, or a returned encryption `key`. The
existing SDK normalizes `key` to its encryption type ID. Unknown exact-match
filters return an empty complete list; an explicit unknown zone is invalid
input. Keep sold-out
flavors unless `AvailableOnly`; availability uses both `IsSoldOut` and
`RemainingVMs`: available means not sold out and remaining VMs greater than
zero, without promising future capacity.

### Rows and metadata

New rows are derived models using Go JSON field names. They contain only catalog
IDs, specifications, prices, and sanitized diagnostics. Reuse existing models by
composition, not by changing their fields or JSON tags.

| Row | Fields |
|-|-|
| `FlavorPrice` | `Flavor Flavor`, `ZoneID`, `ZoneName`, `Family`, `FamilyName`, `Platform`, `Visibility`, `RAMUnit` string; `ZoneEnabled` *bool; `RAMGiB`, `PricePerGiB`, `PricePerRAMUnit` *float64; `Reference *FlavorPriceReference`; `Price pricing.DiscoveryPrice` |
| `VolumePrice` | `VolumeType VolumeType`, `ZoneID`, `ZoneName`, `EncryptionTypeID` string; `ZoneEnabled` *bool; `SizeGB` int; `PricePerGB` *float64; `Price pricing.DiscoveryPrice` |
| `StoragePrice` | `Region`, `Type`, `PurchaseType`, `PriceKey` string; `TypeID`, `PurchaseTypeID` int; `QuotaGB`, `MinQuotaGB`, `MaxQuotaGB` int64; `AllowPeriod` []int; `PricePerGB` *float64; `Price pricing.DiscoveryPrice` |
| `NetworkPrice` | `Kind`, `Name`, `Type`, `Scope`, `Unit` string; `Price pricing.DiscoveryPrice` |

`FlavorPriceReference` contains `ImageID`, `ImageType`, `ImageVersion`,
`RootDiskTypeID` string and `RootDiskSizeGB` int. It records the selected quote
inputs, not credentials or the image catalog's `licenseKey`.

`pricing.DiscoveryPrice` contains `Status`, `Basis`, `Currency`, `Tax`, `Source`
string; `MonthlyPrice`, `OriginalPrice`, `DiscountPrice`, `DiscountPercent`
*float64. Status is `quoted`, `catalog`, `unknown`, `inapplicable`, or `failed`.
Source is a stable operation name or first-party document URL, never a
credential-bearing request URL. Network uses `Unit=IP-month`; bandwidth and
traffic tariffs remain notes, not priced rows in the first release.

Each Output holds `Items []Row` and `Metadata pricing.DiscoveryMetadata`.
Fields: `Region string`, `Complete bool`, `Requests int`, `QuotedAt time.Time`,
`Failures []pricing.DiscoveryFailure`, `Notes []pricing.PricingNote`, and
`ExcludedGroups *int` (nil for unread excluded catalogs). `QuotedAt` is UTC
completion time, not an atomic snapshot. Empty lists use `[]`. `Requests`
counts logical reads/quotes, excluding transport retries and login.

`DiscoveryFailure` holds `Operation`, `ItemKey`, `Code` string and `Status int`;
no server message or raw body. `PricingNote` holds `Topic`, `Scope`, `Text`,
`SourceURL`, `Evidence` string. Evidence is `published`, `observed`,
`conflicting`, or `unverified`. Notes cover exclusions, units, egress, terms,
and programs. Maintain a small reviewed source table in the SDK; do not scrape
websites at runtime or represent that table as a fresh account quote. Ship
source changes with documentation updates.

## Composition and visibility

Compute reads zones, platform codes, and each selected zone's family list,
enumerating every pair from `condition.codes`. Treat family keys as open-ended;
keep names as returned without a derived `Class` or parser. New readers stay
private. [Catalog evidence](pricing-discovery-api.md#catalog-results) owns the
family types, host-aggregate conditions, and absent flavor-to-type join.

Default visibility is `console-family`: families with `key`, excluding
`custom`, partner/custom, and legacy branches. It certifies no sale eligibility.
`IncludeNonPublic` adds `flavor_zones/product`, `flavor_zones/customs`, their
flavor-zone lists, and `flavors/customs`, labelled `custom` or `unclassified`.
Report known excluded counts and scope. No name blacklist, partner-name
heuristic, guessed IDs, or privilege bypass. Non-public branches retain the
[fixture gates](pricing-discovery-api.md#remaining-checks).

Keep distinct `(zone, flavor-zone, flavor-ID, family, platform)` rows;
deduplicate exact duplicate tuples only. Memoize identical quote bodies within
the call, so shared rows do not multiply quotes. A conflicting flavor ID or
flavor catalog zone is a row error, not silently reassigned.

### Reference server configuration

Use the full `server/create` body from
[server quote evidence](pricing-discovery-api.md#server-quote-results), with
`period:1`, `isPoc:false`, selected zone/flavor/image, root disk size and type,
and `encryptionVolume:false`. Use the existing priced-field builder; send no
data disk, network, login, or order-only fields.

Apply the
[reference rule](pricing-discovery-api.md#deterministic-reference-selection).
Filter candidates by flavor image support before quoting under that rule. Read
images once per enabled zone, reuse catalogs, and record `Reference`. Never
escalate to other candidates, OS types, or disk tiers after a refused quote.

No eligible pair produces `Status=unknown`, nil price and reference, and a
`reference-unavailable` note. A refused selected reference is a failed row.

Require exactly one property named `INSTANCE TYPE`, with present, non-null,
positive, finite `monthlyPrice` and `optimumPrice` that agree. Set compute
`MonthlyPrice` from that line and `Basis=instance-type-line`. Exclude `ROOT
DISK` from output prices and ratios; show the reference inputs and an exclusion
note. Never subtract a disk estimate from the server total. Leave compute
`OriginalPrice` and `DiscountPrice` nil; copy `DiscountPercent` only when
present on the instance line.

Label each price as the component of its stated reference. The bounded
[cross-image result](pricing-discovery-api.md#image-compatibility-evidence)
does not establish universal image independence. Compute remains gated on
sanitized selector fixtures and their SDK decode tests.

### Volume, storage, and network

Volume reads zones, unfiltered `ListVolumeTypeZones(nil)`, and one child
`ListVolumeTypes(VolumeTypeZoneID)` call per returned type zone. Treat type
zones as region-wide, ignoring their `zone` field; follow the
[types](pricing-discovery-api.md#volume-catalog-reconciliation-and-prices).
Retain both type ID fields; quote `VolumeTypeID`, falling back to `ID`. Read
encryption types once. For each enabled selected zone, quote every filtered,
valid-size type/encryption choice with that zone's ID. A refusal is a failed
row. Row `ZoneID` names the selected zone, not the catalog's zone metadata.

Storage reuses `ListProjectTypes`' private catalog and price helpers without
changing its fail-fast contract. Read once and quote each selected monthly
offering at its chosen quota once; never list then requote every row. Unknown
purchase types and PAYG remain notes. Require `AllowPeriod` containing 1.
Keep the price-only serializer and omit project-type details, per
[storage evidence](pricing-discovery-api.md#storage-and-network-results).

Network quotes `public-vip` types `public`, `public-vm`, and `public-mkp`
separately through `ProductPortal`, honoring the existing Portal override.
Defer bandwidth catalog calls, capacity fields, and the `Kind` input. Publish
an exclusion note with unknown bandwidth cost; the
[future endpoint rule](pricing-discovery-api.md#bandwidth-endpoint-contract)
requires separate approval. Floating IP, external-interface, and reservation
charges keep unknown rows. Never treat a VIP quote as every public IP's price.
Link to existing NAT catalog/quote commands; add no NAT cost automatically.

## Prices and periods

Return VND with VAT as the quote contract establishes; never add VAT again.
Use `Tax=as-returned`, or `Tax=unknown` for public tariffs with unstated tax.
Quotes are account-scoped. Preserve returned discounts without attributing them
to a program. Copy percentages only with proven field presence, not float64
zero defaults.

Use `optimumPrice` as the monthly total for volume, storage, and VIP; compute
uses the instance line above. Never divide by a term. Publish a note that tested
1, 3, 6, 12, 24, and 36 month quotes showed no term discount on this account.
This is not a universal discount policy or proof that a term was honored. Exact
probe coverage is in the evidence file.

Compute price excludes root/data disks, CES, software licensing, IPs, bandwidth
purchases, traffic, snapshots, backups, and NAT. Quote an actual server
configuration with the existing `quote-create-server` command. Standalone
encrypted volume quotes do not establish encrypted-server cost; the existing
server quote includes a separate CES component.

`PricePerGB = MonthlyPrice / quoted provisioned GB` for block and object
storage. Call this an effective rate at that size, not a marginal tariff;
minimum packages, tiering, and account discounts may prevent scaling it. The
helper does not quote a forbidden 1 GB package to obtain a unit rate.

`PricePerRAMUnit = compute monthly price / Flavor.Memory` for positive memory.
Set `RAMUnit=unknown` and leave `RAMGiB`/`PricePerGiB` nil until a first-party
byte definition exists. Confirmed GiB uses memory unchanged; decimal GB converts
by `1e9 / 2^30`. Never relabel units silently. Keep float64 precision, round
only table displays to two decimals, and sort unrounded values.

Reject missing, null, negative, or non-finite required prices. For compute,
block, and monthly storage, zero is unpriced and produces a failed row, matching
paid-quote safety. A zero network tariff is valid only when an explicit free
catalog entry establishes that meaning. New private decoders check field
presence without changing existing `GetQuoteOutput` fields.

## Bounds, errors, and cancellation

Let Z be selected zones, A enabled selected zones, P family/platform pairs, F
extra flavor zones, N unique eligible reference quote bodies, T returned
region-wide volume type zones, V volume types after filters, E encryption
choices including plain, and O active monthly storage offerings. These are
logical calls before retry/authentication overhead:

| Helper | Upper bound after catalogs are known |
|-|-|
| Compute | `3 + Z + P + A + T + N`; non-public adds `1 + 2*Z + F` |
| Volume | `3 + T + A*V*E` |
| Storage | `7 + O`: one region and six catalog/config reads, then quotes |
| Network | `3`: three public-VIP quotes |

Compute's base includes zones, platform codes, unfiltered volume type zones, and
A image reads. Volume's base includes zones, unfiltered type zones, and
encryption types. Both reuse each type-zone read once. Skip image and
quote reads for disabled zones. Unknown/inapplicable rows cost no quote.

Add at most one `GET v1/projects` to compute, volume, and network when project
resolution is uncached; count it in the budget and retain ambiguity errors.
Cached storage region resolution saves one read. Filters can reduce counts;
large catalogs need not fit the default budget.

Count actual selected offers and bodies before the quote phase. More than
`MaxRequests` refuses that phase with an error and its required count; never
silently truncate. Default 500, range 1..2000. Catalog fan-out also spends this
budget and stops at the limit. Limit collected rows to 10000 and each HTTP body
to 8 MiB. Cap violations are explicit incomplete results. Established arrays are
unpaged; reject new paging envelopes as schema errors instead of guessing page
requests.

Run serially, with at least 500 ms between starts, including retries. Honor
transport backoff and Retry-After. No helper-level retry loop. A logical call
can send at most `2*(RetryCount+1)` attempts with one 401 refresh, excluding
login. Refuse redirects on the new discovery call path so redirect replay does
not evade its request scope. Apply limits through private per-invocation
helpers, never mutate shared client settings. A ten-minute child deadline and
earlier caller cancellation stop scheduling, sleeps, and requests, subject to
the shared-wait exception below. No prompt-return deadline is promised.

### Shared waits and cancellation

Discovery can wait behind another caller's in-flight project lookup or token
refresh beyond its own deadline. No finite shared-wait bound is promised:
HTTP timeouts bound individual requests, not queued operations or custom
providers. Locks span network/token work at `internal/core/client.go:211`,
`internal/core/project.go:68`, `internal/transport/discovery.go:125` and `:164`,
and `internal/core/auth.go:70`.

After each acquisition, check context before cache changes, token invalidation,
login, requests, or other work; return the context error if cancelled. Preserve
single-flight refresh, project ambiguity rules, and token identity. Cancellation
must not clear another caller's token or invalidate a token the call never sent.

Contention tests hold project lookup, initial refresh, and 401 refresh beyond
the waiter's deadline, then release the holder. Require a context error after
acquisition, no later work or token changes, and preserved token identity.
Include shared IAM authentication; do not assert prompt return during a wait.

Cache catalogs and identical successful quotes within one invocation only.
Existing token and project resolution caches stay intact.

Without `AllowPartial`, the first failed catalog or quote returns nil and the
original error. With it, independent branches continue, failed rows retain nil
prices, and catalog failures name their missing scope. Return the output and
`*pricing.DiscoveryError`, which unwraps the underlying errors and matches
`pricing.ErrDiscoveryIncomplete` through `errors.Is`. `errors.As` still finds
`*vngcloud.APIError`; cancellation remains visible. An API error keeps its
actual status; do not translate HTTP 500 to not-found or depend on a 4xx.
HTTP 500 gets one attempt, preserving `errors.As` identity in both partial
modes. Exhaustion tests use 429, 502, 503, and 504, per
`internal/transport/errors.go:45`; discovery changes no retry statuses.
Authentication failures, exhausted throttling, and
cancellation stop all remaining calls. Failures before any usable row return nil
and the cause. Expected `unknown` or `inapplicable` rows do not mean transport
failure; `Complete` means the requested discovery finished, not every cost is
known.

## CLI and output

Commands: `compute list-flavor-prices`, `volume list-volume-prices`,
`storage list-storage-prices`, and `network list-network-prices`, under
`vngcloud`. All retain global `--region` and `--output` flags.

Input names produce kebab-case flags, including `--family`, `--zone-id`,
`--min-ram`, `--max-ram`, `--include-non-public`, `--encryption`,
`--allow-partial`, and `--max-requests`. There is no `--periods` flag. Storage
`Region` keeps the existing JSON-only service-region override; the global
`--region` remains the vServer configuration region. Add local `--sort-by`
(`name`, `ram`, `monthly-price`, `unit-price`) and `--descending`; reject keys
irrelevant to a command before API work. Sorting is a presentation concern and
does not change the SDK Input.

JSON always contains `Items` and `Metadata`, including failures and notes.
Table/text render Items with region/zone, family/type, IDs, specifications,
status, source, monthly price, and unit price. Compute includes reference inputs
and `PricePerGiB` even when unknown; `RAMUnit` explains the RAM column. Volume
includes IOPS, encryption, and quoted GB. Storage includes quota and purchase
type. Network shows only IP-month rows. Nil values
print `unknown`, not 0 or a blank; text is tab-separated. For table/text, print
notes and incomplete coverage on stderr. JSON keeps notes in Metadata. `--query`
uses existing behavior; it may hide metadata but never clears a failure exit
code.

Default stable order is zone, family/type, platform, RAM, CPU, ID, then
encryption where applicable; absent fields are skipped. Explicit numeric sorts
put unknowns last in either direction and use the default order to break ties.
SDK rows use the default order too. Sort before `--query`. Compute unit-price
sorts use `PricePerRAMUnit`; volume/storage use `PricePerGB`. Network rejects
`unit-price` sorting; use `monthly-price` for its IP-month rows.

All commands register as reads and work under `--read-only`; no `--yes`,
`--max-price`, or confirmation prompt. Exit codes remain 0 for complete, 2 for
invalid input, 3 for auth, 4 for standalone not-found, otherwise 1. Only these
new commands opt into rendering `DiscoveryError` partial output on stdout before
the existing JSON error on stderr, exit 1. Rendering/query failures also exit 1.

## Security, tests, and release gates

The threat model and probe hygiene are in the [evidence
file](pricing-discovery-api.md#security-obligations). Allow only catalog GETs
and fixed `create` price POSTs. No order factory, renewal, resource mutation,
cleanup, cookies, account-ID header discovery, or response-directed URL call is
permitted. Unknown endpoints remain gated.

Require SDK decode tests with manager-sanitized raw fixtures and provenance;
label synthetic variants. Test routes/bodies, no writes, IAM refusal,
400/401/403/404, the retry cases above, malformed or oversized bodies,
and cancellation. Cover catalog completeness, duplicate IDs, wrong/disabled
zones, region-wide types in enabled zones, encryption, sizes, units, prices,
conflicts, sorting, budgets, empty/sold-out rows, and invocation-only caching.
Reference tests cover deterministic selection, nullable licence, missing or
duplicate instance lines, and disk exclusion. Inject time for pacing, deadlines,
and `QuotedAt`. CLI goldens cover formats, partial exits, query errors, flags,
secret redaction, and unchanged existing output. All services must pass the
[credential-reflection checks](pricing-discovery-api.md#response-security).

Land response security and post-acquisition checks, then volume/storage,
compute, and IP-only network discovery. Defer bandwidth and period comparison.
Each helper needs sanitized raw fixtures, owner approval, and independent design
and plan review. GiB fields stay gated; non-public branches need populated
fixtures. Implementation updates wiki and generated CLI pages. The manager runs
`make check` before committing and obtains independent review under
[workflow](../../instructions/workflow.md#reviews).

## Owner decisions and open questions

1. Recommend four service helpers with shared pricing metadata, fail-fast
   defaults, opt-in partial errors, bounded serial calls, and invocation-only
   caching. Require response security and post-acquisition context checks;
   preserve quote/write bodies and retry statuses.
2. Recommend all-zone catalogs with disabled-zone quotes skipped, console-family
   defaults, explicit non-public scope, and unchanged flavor names. No derived
   Class or claim of public purchase eligibility.
3. Recommend deterministic flavor-supported Ubuntu/3000 IOPS references and
   only the instance component as compute price. Exclude root-disk charges,
   disclose reference inputs, and gate compute on sanitized selector fixtures.
   Keep GiB and universal image-independence claims gated.
4. Recommend monthly-only discovery without `Periods` or projections,
   minimum-size effective rates, all encryption choices, and a published
   account-scoped no-term-discount note. Defer bandwidth discovery and retain
   an unknown-cost note; never infer a full architecture total.
5. Recommend the release order above, preserving fixture, compatibility, unit,
   and tariff gates. Use reviewed first-party notes without runtime scraping,
   eligibility reads, or automatic promotion application.
6. Choose the shared-wait exception above (recommended), without prompt return
   at the deadline, or require cancellable shared coordination as a separate
   core change before release. The latter needs its own brief and independent
   design/code review, with tests proving prompt cancellation under contention.

Open facts: [probe evidence](pricing-discovery-api.md#remaining-checks).
Design approval establishes no API facts and grants no live-run authority.
