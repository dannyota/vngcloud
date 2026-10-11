# Pricing discovery: evidence and probes

Evidence for [pricing discovery](pricing-discovery.md); accepted rules govern.
Console code shows UI construction; manager IAM evidence is account-scoped.

## Existing contracts and code

| Authority | What it establishes |
|-|-|
| [Paid API](vserver-paid-writes-api.md#billing-model) | Regional `POST v1/price`; create quotes are monthly; periods 1, 3, 12 gave the same price; 0 gave 400 and omitted period gave 500 |
| [Paid design](vserver-paid-writes.md#quote-before-a-paid-write) | Shared quote/create priced fields; existing price guards remain unchanged |
| [CLI usability](cli-usability.md) | Fan-out zone lookups, exact-name filters, fail-fast reads, `Items` output, quote-only required fields |
| [Billing](billing.md#price-quotes), [pricing code](../../pricing/pricing.go) | Quotes are read-only POSTs; top-level prices; current code supports create and resize, validates total price presence, and retries quotes |
| [Encryption](encrypted-volumes.md#live-results) | Standalone encrypted volume price equalled plain in the recorded case; encrypted server had a separate CES charge |
| [Storage API](storage-projects-api.md#price) | Monthly package quote, six allowed periods in metadata, ignored period 12 in one probe, order-only keys can yield zero |
| [NAT API](network-writes-api.md#nat-price-and-direct-order) | Separate vNetwork price route and envelope; a regional billing server quote is not a NAT quote |

Catalog, quote, transport, endpoint, and CLI tests establish code, not live
behavior. Flavor and volume models have no price; `storage.ListProjectTypes`
quotes monthly minima after six catalog/config reads and one region read.

## Public console inspection

The source record inspects credential-free assets as text with TLS verification
and no cookies, tokens, or redirects. Console claims are construction evidence.
Sources: [vServer][vs], [dashboard][dash], [vServer main][vm], [runtime][vr],
[configuration][vc], [flavor service][common], [quote builders][builders],
[server][server], [volume][vol], [IP][ip], [bandwidth service][bwservice],
[bandwidth UI][bwui], [dashboard main][dm], [dashboard runtime][dr].

The public configuration sets these same-origin paths:

| Symbol here | Value under `https://hcm-3.console.greennode.ai` |
|-|-|
| `V` | `/vserver/iam-vserver-gateway` |
| `B` | `/vserver/iam-billing-gateway` |
| `W` | `/vserver/bandwidth-api` |

### Flavor lists and prices

The shared service builds the following GETs under `V/v1/{projectId}`. No
request has a body. Query `zoneId` is appended when supplied.

| Path | Console usage; response evidence |
|-|-|
| `/flavor_zones/codes` | Platform list; UI reads `key` and description |
| `/flavor_zones/families?zoneId={zone}` | Array consumed directly; rows use `key`, `value`, `description`, `condition.codes` |
| `/flavor_zones/customs?zoneId={zone}` | Array consumed directly; rows use `id`, `name`, `description` |
| `/flavors/families/{family}/platforms/{code}?zoneId={zone}` | Array consumed directly by the flavor table |
| `/flavors/customs?zoneId={zone}` | Special family key `custom`; array consumed directly |
| `/{flavorZoneId}/flavors` | Wrapper `flavors`, already implemented |

Family `key` versus custom `id` is a UI distinction, not sale eligibility.
The picker exposes specifications, not bulk prices; RAM displays as GB without
a byte definition. The summary posts to `B/v1/price`, matching instance and
CES properties by name. Accepted request bodies live in the
[paid API](vserver-paid-writes-api.md#quote-requests); probe results below own
wire types, nullability, and permission observations.

### Volume and IP prices

Volume UI uses the catalog routes and wrappers in the volume results below.
Quotes use `POST B/v1/price`, `resourceType=volume`, `action=create`,
and `resourceInfo` with `period:1`, `isPoc:false`, size, type, and zone.
Encryption follows the [accepted body](encrypted-volumes.md#body-keys).

The IP form quotes `POST B/v1/price` with:

```json
{"resourceType":"public-vip","action":"create",
 "resourceInfo":{"isBuyMorePo":false,"isPoc":false,"type":"public-vm"}}
```

`isBuyMorePo` is the quote spelling. The other UI type is `public-mkp`;
existing generic examples use `public`. These names establish no equivalence
with Floating IP or External Interface. Create bodies are out of scope.

### Bandwidth endpoint contract

Bandwidth UI reads `GET W/v1/projects/{projectId}/bandwidth/package-config`.
It references `price`, but only the probe results establish returned fields.
`internal/endpoints/endpoints.go:46` and `internal/core/client.go:253` have
no bandwidth override or product mapping. Neither existing network gateway
owns W. Defer bandwidth catalog discovery from the first release.

For a separately approved extension, add public `EndpointOverrides.Bandwidth`,
matching internal Overrides/Set fields, and `routes.ProductBandwidth` mapped
by `core.Client.Endpoint`. Normalize and join paths like other product bases.
Use only the verified hcm-3 default, W in the table above; no guessed han-1
or other regional default. A nonempty override is authoritative for every
request. Without an override or verified default, return `ErrInvalidConfig`
before login or API work. Never derive W from VServer/VNetwork, replace an
override after a failure, or fall back to production. Test a caller's base
path, failure with no fallback, unknown region, and cross-host redirects.

Dashboard browser headers, orders, package inventory, attachments, and usage
reads are excluded. Renewal/invoice fields supply no discount schedule.

## Published tariffs and conflicts

These first-party publication observations are not account quotes. Tax and
region applicability are unknown unless stated by the source.

| Source | Published fact and limit |
|-|-|
| [Pricing hub][prices] | CPU from 238392 VND/30 days; block from 920 VND/GB; object from 530. Starting prices do not identify full configurations |
| [CPU product][cpu] | General, Standard, High CPU, High Memory; starts at 238392, 800352, 361267, 1386598 VND respectively. No complete size table or account scope |
| [Flavor guide][flavordoc] | Flavor names encode platform, resource ratio, vCPU and RAM; RAM called GB, with no byte definition |
| [Block product][block] | 3000 IOPS starts at 3200 VND/GB/30 days; no minimum commitment. Hub's 920 starting price lacks matching type/zone evidence |
| [vServer pricing guide][vpricing] | Calculator estimates include VAT; does not publish a full flavor tariff or term-discount matrix |
| [Storage charging guide][storageprice] | All listed object regions: Gold package 1000, PAYG 1400; Instant Archive package 530, PAYG 742 VND/GB/month. Package minimum 30 GB; PAYG minimum 1 GB |
| [Storage charging guide][storageprice] | Unlimited requests and upload; Gold download allowance 10 times storage, Archive 3 TB/month; excess domestic 280 VND/GB, international 330..580 |
| [Object product][object] | Gold 1400 and Archive 530 VND/GB; domestic free traffic Gold x10, Archive x2, international 1 TB. Conflicts with charging guide's Archive and international rules |
| [Bandwidth overview][bandwidth], [free package][bwfree] | Default VNG Dedicated: up to 300 Mbps domestic and 5 Mbps international. These are rates, not monthly GB allowances |
| [Bandwidth PAYG][bwpayg] | PAYG-ALL, PAYG-DOMESTIC, PAYG-INTERNATIONAL; charges beyond the free package, no numeric tariff on this page |
| [Dedicated bandwidth][bwdedicated] | Domestic 500/1000/2000 Mbps, international 10/20/50 Mbps options; no quoted monthly price on the inspected page |
| [Instance IP guide][ipdoc], [Floating IP][fip] | Floating IP and External Interface are different products; reservation affects FIP billing; detached External Interface keeps billing until release. No numeric rate or free-IP count established |
| [Partner discounts][partner] | Discounts require registration, approval, activation, and apply to customer orders. No universal percentage or commitment schedule |
| [AgentBase survey policy][credits] | Expired survey offer; exclude from current cost reductions |
| [GPU launch announcement][inception] | NVIDIA Inception partnership mentioned; no CPU startup credit amount, eligibility guarantee, or hcm-3 discount |

Do not mix PAYG prices and package allowances without a matching source.
[Storage metadata](storage-projects-api.md#project-types) leaves tier progress,
allowance sharing, unit conversion, and VAT unresolved. Report sourced rules,
not an egress bill. This source set establishes no universal CPU startup credit
or term-savings schedule; Sales must confirm eligibility and negotiated rates.

## Manager read-only probes

Manager IAM probes cover catalog GETs and price POSTs in hcm-3 and vStorage HCM.
Summaries are not raw fixtures; SDK decoding needs sanitized raw responses
under [live data](../../instructions/live-data.md).

### Catalog results

The 65 catalog GETs returned HTTP 200 without IAM refusal. Four zones returned
catalogs; only HCM03-1C has `isEnabled:true`. Disabled zones are HCM03-1A,
HCM03-1B, and HCM03-BKK-01. Catalog visibility does not establish that the
account can create resources there.

`flavor_zones/codes` returns 12 entries with string `key`, `value`, and
`description`. The family array returns only `general-purpose` and `gpu`, with
string `key`, `value`, `description` and `condition.codes` arrays.
General-purpose declares code-s2, code-s, and code-a; GPU declares eight
platforms. The manager reports general-purpose `types`: `standard`, `general`,
`high-cpu`, `high-memory`, labelled Standard, General, High CPU, High Memory.
Their host-aggregate `condition` keys are `shared`,
`aggregateInstanceExtraSpecsCpuNcore`, and `aggregateInstanceExtraSpecsMemory`:
General: shared true; high-cpu: CpuNcore high; high-memory: Memory high.
GPU has empty `types`. Flavors have no row-to-type join; name segments are
`standard`, `general`, `highcpu`, `highmem`. Some carry `-n10` without changing
CPU or memory. No exact, tested name parser exists; preserve returned names.

In HCM03-1C, family/platform reads return plain arrays: code-s2 has 33 flavors,
code-s has 37, code-a and all GPU platforms are empty. Flavor fields include
numeric `cpu`, `memory`, `gpu`, `gpuMemory`, `bandwidth`, `remainingVms`; string
`flavorId`, `flavorZoneId`, `name`, `group`, `cpuPlatformDescription`,
`bandwidthUnit`, `metaData`; boolean `isSoldOut`; and array `flavorZones`. In
the 33-row code-s2 sample, these are non-null and `zoneId` is null throughout.
`bandwidthUnit` is `Gbps`. No price or memory-unit field exists. Derive zone
scope from the request; reject a conflicting non-null zone. Integer memory does
not establish GB or GiB. Each row in the committed
[flavor fixture](../../testdata/compute/discovery_family_flavors.json) has
JSON-string `metaData` with a nonempty string array `imageTypeSupport`,
including `Ubuntu`; `Flavor.Metadata` retains the string.

Both custom paths return empty arrays in every zone. The product envelope
contains `errorCode`, `errorMsg`, `extra`, and 69 `flavorZones` entries with
`id`, `name`, `description`, `zoneId`; five descriptions are null. Only five
legacy flavor-zone branches fit the probe cap. The first returns the same 33
code-s2 rows. This does not establish all branch membership or populated custom
schemas.

### Server quote results

The flavor-only `server/create` body with `period`, `isPoc`, `zoneId`,
`flavorId`, and `encryptionVolume` returns HTTP 400 for three flavors. Adding a
compatible Ubuntu image and a 20 GB, 3000 IOPS root disk returns HTTP 200 with
exactly `INSTANCE TYPE` and `ROOT DISK` properties:

```json
{"resourceType":"server","action":"create","resourceInfo":{
  "period":1,"isPoc":false,"zoneId":"<zone-id>",
  "flavorId":"<flavor-id>","imageId":"<image-id>",
  "rootDiskSize":20,"rootDiskTypeId":"<volume-type-id>",
  "encryptionVolume":false}}
```

| Quote | INSTANCE TYPE | ROOT DISK | Monthly total, VND |
|-|-|-|-|
| s2-general-1x2 | 283800 | 64000 | 347800 |
| Larger code-s2 flavor | 43587634 | 64000 | 43651634 |
| Code-s flavor | 2906624 | 64000 | 2970624 |

Both properties have equal `monthlyPrice` and `optimumPrice`, null
`currentPrice`, and zero `discountPercent`. Top-level `optimumPrice` is monthly.
Periods 1, 3, 6, 12, 24, 36 on the smallest full body give the same total and
lines. Two identical bodies repeat the same result. A nonexistent flavor in a
full body returns HTTP 500 `ServerError`. No result establishes a 400/404-only
invalid-ID contract.

The image read returns an `images` envelope with 45 entries. `id`, `imageType`,
`imageVersion`, `zoneId`, `description` are non-null strings; `flavorZoneIds`
and `defaultTagIds` are arrays. `packageLimit` has numeric `cpu`, `memory`,
`diskSize`; a compatible Ubuntu image requires 20 GB. `licence` is boolean or
null (nine nulls); `licenseKey` is string or null (40 nulls). Never expose
`licenseKey` in discovery rows or diagnostics.

### Image compatibility evidence

The manager's cross-image check uses HCM03-1C, period 1, a 20 GB 3000 IOPS
root, and two flavors. The harness image, Ubuntu 22.04 x64, and Ubuntu 24.04
x64 give identical instance lines: 283800 for s2-general-1x2 and 2906624 for
the code-s flavor. Every root line is 64000. The two named Ubuntu images have
`licence:false` and minimum disk 20 GB. Equality applies only to those tested
configurations; it proves no universal independence from image choice.

Image queries for HCM03-1A and HCM03-1C return the same 45 images, all marked
HCM03-1A. For each named Ubuntu image, 12 of 15 `flavorZoneIds` are absent
from the 69-row flavor-zone catalog; three identify HCM03-1A partner groups.
None matches HCM03-1C, yet the full quotes there succeed. Neither image
`zoneId` nor `flavorZoneIds` establishes quote compatibility; invent no alias.
Manager reports include SDK field checks; raw selector fixtures remain required.

### Volume catalog reconciliation and prices

The filtered `volume_type_zones?zoneId=<zone-id>` probe returns zero rows.
The unfiltered catalog smoke returns NVME and SSD; both report `zone.uuid`
HCM03-1A, a disabled zone, and neither reports HCM03-1C. Their types quote
successfully in enabled HCM03-1C. Parent `zone` metadata therefore cannot
establish quote compatibility; treat returned type zones as region-wide.
`volume/volume_types.go:75` uses filtered parents for `ListVolumeTypes(ZoneID)`;
discovery instead reads unfiltered parents, then each direct child at
`GET V/v1/{projectId}/{typeZoneId}/volume_types`. Never embed type-zone IDs.
Quote each filtered type in each enabled selected zone; a refusal is a failed
row. Required fixtures cover misleading parent zones and quote validation.

The type envelope is `volumeTypes`; entries contain `id`, `name`, `iops`,
`minSize`, `maxSize`, and `zoneId`, without `volumeTypeId`. Use `id` as the
quote's type ID in this shape. The four quoted types allow 1 to 5000 GB.
`volumes/encryption_types` returns two entries with non-null string `key` and
`displayKey`, not `id` and `name`; existing normalization in
`volume.ListEncryptionTypes` fills the SDK aliases from `key`.

| IOPS | VND per GB-month at both 1 GB and 100 GB |
|-|-|
| 3000 | 3200 |
| 3200 | 7680 |
| 6400 | 10880 |
| 10000 | 14560 |

Quotes use the accepted volume body, period 1, with `encryptionType` omitted for
plain or set to the returned key. Each returns one `Volume` property. Both
encryption types cost the same as plain in the tested cases. Linear prices at
the two sizes support effective rates, not a universal linear tariff. The 20 GB
server root equals 20 times 3200 VND. Plain periods 1, 3, 6, 12, 24, 36 and
encrypted periods 1, 3, 6, 12 have unchanged monthly prices. Encrypted 24/36 are
outside the probe cap. A nonexistent type ID returns HTTP 500.

### Storage and network results

Existing `ListProjectTypes` already supplies offers and billing metadata. The
region list has two entries; purchase types has one `Normal` entry; project
types has two, both allowing `[1,3,6,12,24,36]`. The four existing configuration
reads succeed. `billingUnitPrice` and `billableResources` retain the
traffic/request keys described in [storage
evidence](storage-projects-api.md#project-types).

Gold quotes 30000 VND at 30 GB and 31000 at 31 GB; Instant Archive quotes 15900
and 16430. Effective rates are 1000 and 530 VND/GB-month. Properties are named
`vStorage-Gold` and `Instant-Archive-2`. Gold periods 1, 3, 6, 12, 24, 36 all
quote 30000 monthly. This does not establish term discounts or Archive's full
period matrix. `GET internal/v1/billing/project_types/{id}/details` returns HTTP
403 for both types under IAM. Drop that route; do not bypass the permission
check.

All three `public-vip` types, `public`, `public-vm`, and `public-mkp`, quote
120000 VND with one `Public Virtual Ip Address` line. Treat them as separate
quote types; equal amounts do not establish interchangeable products. Floating
IP and external-interface prices remain unverified.

Bandwidth `package-config` returns HTTP 200 with `data` containing 17 packages.
Fields are string `uuid`, `name`, `type`, `description`, and numeric
`bandwidth`, all non-null. Types are `Domestic` or `International`. There is no
price field. Names mention Mbps, but no explicit unit field establishes the
numeric `bandwidth` unit. Prices and capacity units remain unknown; no tariff
can be parsed from a name.

### Deterministic reference selection

This selection rule is not an API promise. Read images once per enabled zone
and reuse the volume catalog path above. Select returned IDs only:

1. Parse the flavor's JSON-string `metaData` as an object. Require nonempty
   `imageTypeSupport` as an array of nonempty strings. Missing, null, malformed,
   wrong-type, or empty metadata/list/entries give unknown price, nil reference,
   and a `reference-unavailable` note for unusable image support; do not quote
   or guess. Require the candidate's `ImageType` to match an entry exactly,
   case-sensitive as returned, without trimming or normalization. Also require
   Ubuntu ignoring ASCII case and explicit `licence:false`. Ignore image
   `zoneId` and `flavorZoneIds`. Require present integer package limits:
   nonnegative CPU/memory no greater than the flavor and positive disk size.
   Only the quote validates the selected pair.
2. Use a region-wide 3000 IOPS volume type with positive minimum and
   consistent maximum size. Root size is the greater of that minimum and
   `packageLimit.diskSize`; reject pairs exceeding the disk maximum.
3. Sort by root size, then bytewise image version, image ID, and disk type ID,
   all ascending. Pick first; version order does not promise the newest image.

No eligible pair means unknown price; a refused selected pair is a failed row.
Never try another OS, licensed image, higher IOPS tier, guessed ID, or trial
quotes. Eligibility needs private presence-aware decoding, not zero values.

### Remaining checks

No live run is authorized. Compute needs SDK decoding of sanitized raw
image/catalog and successful quote fixtures: verify selector fields, known
candidates, null-licence exclusion, and independence from image-zone metadata.
Test exact support matches; case mismatches, unsupported images, and unusable
metadata/list shapes must yield unknown rows without quotes.

Only three flavors have quote evidence. Remaining legacy branches, populated
custom/GPU catalogs, reference selection outside the observed zone, and
encrypted 24/36-month quotes are unverified. RAM needs a first-party byte
definition; no price probe proves GiB. Bandwidth tariff and unit, object egress
conflicts, and negotiated discount or credit eligibility need first-party
clarification. NAT remains governed by its Hanoi evidence; this scope requires
no NAT, order, or account-data calls.

## Response security

Every discovery exchange, including project/region resolution and quotes,
must reject reflected credentials before schema validation, row decoding,
caching, server-derived errors, or output. `DoJSONStatus` at
`internal/transport/transport.go:236` decodes successes unchanged. Use the
NAT pattern (`network/nat_catalog.go:120`, `:134` and
`network/nat_response_security.go:11`) with bounded raw bodies, the credential
actually sent, `Sensitive`, and static withheld messages. Check raw bytes and
JSON-decoded strings, including escaped names, IDs, metadata, and property text.

The discovery-only response guard must run on each attempt before status/error
interpretation or retry, including 401 and 5xx; ordinary DoJSON alone is not
sufficient. Reject reflection as a non-retryable `*APIError` with actual status,
`Code=InvalidResponse`, and static text. No body, token, or offending field may
enter that error or a partial failure key; use trusted request scope instead.
Keep existing non-discovery exchange behavior and nested-string limits intact.

Canary tests cover every service and auxiliary read, success/error envelopes,
JSON escapes, token rotation, caches, debug/capture hooks, `errors.As`, both
partial modes, CLI formats, query output, and stderr. Only independently
validated rows may survive a failed branch; no reflected scalar may reach any
output. Require this exchange support before releasing a helper.

## Security obligations

These are constraints for independent review, not a review verdict. Compute
and network discovery helpers remain unimplemented.

| Attacker and severity | Existing evidence | Required invariant and check |
|-|-|-|
| Log reader steals bearer material, high | `internal/transport/transport.go:276` attaches tokens; CLI error serialization in `internal/cli/errors.go` | Enforce [response security](#response-security) before decoded data escapes; retain redaction and test cookies/headers as well as token reflections |
| Malicious API peer fabricates free prices or huge catalogs, high | `pricing/pricing.go:137` validates the top-level price; `internal/transport/transport.go:122` makes body limits opt-in | New private decoders require price presence and keep unknown units explicit; enforce 8 MiB/10000-row budgets, duplicate/zone checks, and partial coverage tests |
| Peer redirects price POST to a mutation or foreign host, high | `internal/transport/transport.go:395` applies redirect policy; `NoRedirect` exists | Preserve TLS verification and caller-hook policy; discovery refuses redirects; endpoints come from trusted configuration, never catalog URLs; test same-host mutation and foreign-host redirects |
| Local attacker tampers with profile or cache, high | `internal/core/load.go:49` resolves profiles; `internal/cli/config.go:81` checks cache permissions; `internal/tokencache/cache.go:42` binds identity | Reuse credential validation, 0600 files, 0700 cache directories, and identity-bound token reuse; add no credential/cache format or persistent price cache; test profile separation and no credential attachment to documentation URLs |
| Concurrent caller causes paid action or stale-price purchase, high | `pricing/pricing.go:131` marks quote POST idempotent; accepted paid-write quote guard is separate | Fixed read/quote allowlist, no generic action input or order fallback; test duplicate calls issue only reads; discovery never substitutes for the write's fresh guard; honor the [shared-wait exception](pricing-discovery.md#shared-waits-and-cancellation), post-acquisition context checks, and cancellation/token-identity contention tests |

Source confirms catalog GETs, read-only quote POSTs, and fail-fast existing
reads. Runtime security tests, live calls, and independent review of these
changes are not performed here.

[vs]: https://hcm-3.console.greennode.ai/
[dash]: https://dashboard.console.greennode.ai/
[vm]: https://hcm-3.console.greennode.ai/vserver/main.d140e1f2810ed7c0.js
[vr]: https://hcm-3.console.greennode.ai/vserver/runtime.e60847e23362c82d.js
[vc]: https://hcm-3.console.greennode.ai/vserver/assets/config/vserver-portal.json
[common]: https://hcm-3.console.greennode.ai/vserver/common.7ad6cbb47c60c53e.js
[builders]: https://hcm-3.console.greennode.ai/vserver/50.73b740cda668cc22.js
[server]: https://hcm-3.console.greennode.ai/vserver/517.5431c328c7ea7dc8.js
[vol]: https://hcm-3.console.greennode.ai/vserver/226.f6fdc45f207d787a.js
[ip]: https://hcm-3.console.greennode.ai/vserver/807.0e452e9661f43b9c.js
[bwservice]: https://hcm-3.console.greennode.ai/vserver/924.b1a79a02a55197fb.js
[bwui]: https://hcm-3.console.greennode.ai/vserver/346.b753a0fae0878e45.js
[dm]: https://dashboard.console.greennode.ai/main.92dc99a321f44d75.js
[dr]: https://dashboard.console.greennode.ai/runtime.06f455901f63b7d8.js
[prices]: https://greennode.ai/pricing
[cpu]: https://greennode.ai/product/cpu-instances
[flavordoc]: https://docs.greennode.ai/vserver/compute-hcm03-1a/instance/flavor.md
[block]: https://greennode.ai/product/block-storage
[vpricing]: https://docs.greennode.ai/vserver/compute-hcm03-1a/pricing.md
[storageprice]: https://docs.greennode.ai/vstorage/object-storage/cach-tinh-phi.md
[object]: https://greennode.ai/product/object-storage
[bandwidth]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/bandwidth.md
[bwfree]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/bandwidth/datatransfers-bandwidth-service.md
[bwpayg]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/bandwidth/dedicated-bandwidth-service.md
[bwdedicated]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/bandwidth/package-bandwidth-dedicated.md
[ipdoc]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/instance-ip-address.md
[fip]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/floating-ip.md
[partner]: https://docs.greennode.ai/partner-portal-user-guide/registration-of-partner-discount.md
[credits]: https://greennode.ai/agentbase-credit-program-policy
[inception]: https://greennode.ai/blog/launch-hyper-scale-ai-gpu-cluster
