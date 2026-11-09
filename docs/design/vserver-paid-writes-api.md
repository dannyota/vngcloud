# vServer Paid Writes: API

Status follows [vServer paid writes](vserver-paid-writes.md).

The API shapes, billing model, and prices behind
[vServer paid writes](vserver-paid-writes.md).

## Sources

- The [vServer API reference][vserver-api], sections Change flavor of
  server and Resize volume: request fields and statuses.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`, `services/compute/v2`
  and `services/volume/v2`): create, delete, attach, detach, and resize
  bodies, success statuses, and error messages.
- VNG Cloud's Terraform provider (`vngcloud/terraform-provider-vngcloud`,
  `client/vserver` and `resource/vserver`): start, stop, reboot, resize,
  rename, and the status sets its waits use.
- The vServer console bundle on `hcm-3.console.greennode.ai`: the price
  request each create and resize form sends.
- GreenNode's billing docs on `docs.greennode.ai`: prepaid and postpaid
  users, and the create, resize, delete, and auto-renew pages. Evidence
  for resize pricing is qualified under [resize credits](#resize-credits).
- Read-only probes on the test account in `hcm-3`: flavor
  zones, flavors, images, volume types, quotas, and price quotes. No write
  was sent.
- Manager-supplied observation: read-only `compute quote-resize-server`
  to a smaller flavor returned `quote response had an invalid price:
  -180797`. `pricing/pricing.go:172` emits that value after decoding
  `optimumPrice`. This supports the sign, but supplies no raw response,
  refund evidence, or proof of other fields. See
  [probe](vserver-paid-writes-checks.md#free-checks).

Every call uses the IAM vServer gateway with the `v2/{projectId}/...` paths
the reads use, and no `portal-user-id` header, as the free writes do. A
shape marked "live" was seen in the probes; the rest is inferred until its
[live check](vserver-paid-writes-checks.md#live-runs) passes.

## Calls

Paths are under `v2/{projectId}` unless they start with `v1`.

| Call | Method and path | Success | Source |
|-|-|-|-|
| Flavor zones | `GET v1/{projectId}/flavor_zones/product` | 200 | Live |
| Flavors | `GET v1/{projectId}/{flavorZoneId}/flavors` | 200 | Live |
| Default volume type | `GET v1/{projectId}/volume_default_id?zoneId=` | 200 | Live |
| Create server | `POST /servers` | 202 | Docs, both SDKs |
| Delete server | `DELETE /servers/{serverId}` with a body | 202 | Docs, both SDKs |
| Start server | `PUT /servers/{serverId}/start` | 202 | Docs, Terraform |
| Stop server | `PUT /servers/{serverId}/stop` | 202 | Docs, Terraform |
| Reboot server | `PUT /servers/{serverId}/reboot` | 202 | Docs, Terraform |
| Resize server | `PUT /servers/{serverId}/resize` | 202 | Docs, Terraform |
| Rename server | `PUT /servers/{serverId}/rename` | 200 | Docs, Terraform |
| Volumes of a server | `GET /volumes/servers/{serverId}` | 200 | Docs, both SDKs |
| Create volume | `POST /volumes` | 202 | Docs, both SDKs |
| Delete volume | `DELETE /volumes/{volumeId}` | 202 | Docs, both SDKs |
| Resize volume | `PUT /volumes/{volumeId}/resize` | 202 | Docs, both SDKs |
| Attach volume | `PUT /volumes/{volumeId}/servers/{serverId}/attach` | 202 | Docs, both SDKs |
| Detach volume | `PUT /volumes/{volumeId}/servers/{serverId}/detach` | 202 | Docs, both SDKs |
| Price quote | `POST v1/price` on the regional billing gateway | 200 | Live |

The flavor zone list returns every zone's flavor zones under
`flavorZones`, each with `id`, `name`, `description`, and `zoneId`. The
flavor list returns `flavors`, each with `flavorId`, `name`, `cpu`,
`memory`, `gpu`, `bandwidth`, `bandwidthUnit`, `remainingVms`,
`isSoldOut`, and `flavorZoneId` (live). The default volume type read
returns 404 `Cannot get zone with id <zone>` without a `zoneId` when the
region's first zone is disabled for the account, and
`{volumeTypeZoneId, volumeTypeId}` with one (live).

## Bodies

Every body below also takes `tags` and `zoneId` where the reference lists
them; the SDK sends `zoneId` only on creates.

- Server create: `name`, `zoneId`, `flavorId`, `imageId`, `networkId`
  (the VPC), `subnetId`, `securityGroup` (a list of IDs), `sshKeyId`,
  `rootDiskSize` (GB), `rootDiskTypeId`, `encryptionVolume` (required by
  the reference), and optionally `dataDiskName`, `dataDiskSize`,
  `dataDiskTypeId`, `serverGroupId`, `userData`, `userDataBase64Encoded`,
  and `isEnableAutoRenew`. The reference also lists `attachFloating`,
  `userName`, `userPassword`, `osLicence`, `isPoc`, `hostGroupId`,
  `poolName`, `networks`, `createdFrom`, backup and snapshot restore
  fields, and the encryption types; the SDK never sends them. The API
  takes one data disk. Encryption keys are in
  [encrypted volumes](encrypted-volumes.md#body-keys).
- Server delete: `deleteAllVolume` (bool). Terraform sends `false`.
- Server resize: `flavorId`, `serverId` (the path ID), and an optional
  `hostGroupId` the SDK never sends.
- Server rename: `newName`.
- Start, stop, reboot: no body.
- Volume create: `name`, `size` (GB), `volumeTypeId`, `zoneId`, and
  optionally `isEnableAutoRenew`. The reference also lists `multiAttach`,
  `encryptionType`, `createdFrom`, `configVolumeRestore`, `imageId`,
  `persistentVolume`, and `poolName`. The SDK sends `encryptionType` only
  per [encrypted volumes](encrypted-volumes.md#body-keys), and never the
  rest.
- Volume resize: `newSize` and `newVolumeTypeId`, both required.
- Attach and detach: an empty object `{}` with the JSON content type. A
  bodiless PUT is refused with 400 and the body `{"message":null}` (live).
  The reference lists an optional `persistentVolume`, which the SDK never
  sends.

## Responses (inferred)

- Server create returns `data` holding the server with `uuid`; both SDKs
  read the new ID from `data.uuid`. Its other fields are unverified, so
  the create decodes into a private type and the SDK reads the server
  after it.
- Server delete, start, stop, reboot, and resize return `data` as an
  untyped object. The SDK ignores it and reads the server.
- Rename returns `data` holding the server.
- Volume create returns `data` holding the volume with `uuid`. Volume
  delete, resize, attach, and detach return `data` holding the volume.
- The quote returns `optimumPrice`, `originalPrice`, `discountPrice`,
  `discountPercent`, and `propertiesPrice` at the top level (live), as
  [billing](billing.md#price-quotes) records. The response carries no
  currency field; every price in this design, and every guard compared
  against `MaxPrice`, assumes VND, matching the account's own region and
  the console's own display. Credit evidence and unknown field signs are
  listed under [resize credits](#resize-credits).

## Quote decoding and bounds

Proposed for `pricing.GetQuote`: cap each response at 1 MiB using
`transport.Request.MaxBody`; this is an SDK limit, not a published API
limit. No pagination applies. Oversized success returns `*APIError`, nil
Output, and no partial capture. Oversized HTTP errors retain the existing
status mapping. Reject malformed or absent prices before returning any
Output; use the [signed reader rule](vserver-paid-writes.md#resize-credits).
Keep context cancellation and configured retries (default three retries,
with at most one authentication refresh) in the transport, without a new
service retry loop. The probe must verify the raw fixture fits the cap.

## Statuses

From Terraform's waits and the console's enums.

| Resource | Moving | Settled | Failed |
|-|-|-|-|
| Server create | `CREATING`, `CREATING-BILLING` | `ACTIVE` | `ERROR` |
| Server stop | `TURNING-OFF` | `STOPPED` | `ERROR` |
| Server start | `STARTING` | `ACTIVE` | `ERROR` |
| Server reboot | `REBOOTING` | `ACTIVE` | `ERROR` |
| Server resize | `CHANGING-FLAVOR`, `VERIFYING-FLAVOR` | `ACTIVE` or `STOPPED` | `ERROR` |
| Server delete | `DELETING` | 404 or `DELETED` | `ERROR` |
| Volume create | `CREATING`, `CREATING-BILLING` | `AVAILABLE` | `ERROR` |
| Volume resize | `RESIZING`, `CHANGING-IOPS` | `AVAILABLE` or `IN-USE` | `ERROR` |
| Attach | `AVAILABLE`, `ATTACHING` | `IN-USE` | `ERROR` |
| Detach | `IN-USE`, `DETACHING` | `AVAILABLE` | `ERROR` |
| Volume delete | `DELETING` | 404 or `DELETED` | `ERROR` |

A server whose prepaid period ended reads as expired: VNG Cloud's SDK maps
`server is expired` on a security group change.

## Waits

Each write below waits unless `NoWait` is set
([design](vserver-paid-writes.md#waits)). Each package gets a poll helper
with the injected clock and sleep that `network` uses, and honours `ctx`.

| Write | Settled | Poll | Bound |
|-|-|-|-|
| Server create | `ACTIVE` | 5 s | 15 min |
| Server start, stop | `ACTIVE`, `STOPPED` | 5 s | 5 min |
| Server reboot | `ACTIVE`, read at least 10 s after the `202` | 5 s | 5 min |
| Server resize | New flavor, and `ACTIVE` or `STOPPED` | 5 s | 15 min |
| Server delete | 404 or `DELETED` | 5 s | 10 min |
| Volume create | `AVAILABLE` | 2 s | 5 min |
| Volume resize | New size, and `AVAILABLE` or `IN-USE` | 2 s | 5 min |
| Attach, detach | `IN-USE` with the server listed; `AVAILABLE` | 2 s | 5 min |
| Volume delete | 404 or `DELETED` | 2 s | 5 min |

- A 404 during a create wait keeps polling. `ERROR` returns the Output and
  an error wrapping `ErrFailed`. The bound returns the Output and an error
  wrapping `ErrNotSettled`, whose message says a create must not be
  repeated. After an uncertain resize, read `GetServer` or `GetVolume`
  before deciding on another write; reading first does not prevent races.
- Unknown and moving statuses keep polling. The live runs record the
  times; a bound changes only by amending this table.

## Server rules

From VNG Cloud's SDK error patterns and the reference. None is verified
live except where marked.

- Recorded live evidence (`hcm-3`): `DeleteServer` with `deleteAllVolume` false
  deleted the server and its boot volume together. The server read 404
  after 15 s, `KeptVolumeIDs` was empty, and no volume remained.
  `deleteAllVolume` governs only attached data volumes. The same run:
  quote 347,800 VND a month; create settled at `ACTIVE` in 1m13s; stop
  41 s; start 21 s; reboot 20 s; rename; delete refunded to the minute.

- A server cannot be deleted while `CREATING`, `CREATING-BILLING`,
  `DELETING`, or `CHANGING-SECURITY-GROUP`.
- Quotas refuse a create with `exceeded vm quota`, `exceeded vcpu quota`,
  `exceeded volume quota`, and `exceeded volume_size quota`; an attach
  with `exceeded volume_per_server quota`. The probe read the test
  account's quota rows `VM`, `VCPU`, `VOLUME`, `VOLUME_SIZE`,
  `VOLUME_MAXSIZE`, and `VOLUME_PER_SERVER`; each leaves room for the
  live runs.
- A flavor that does not support an image fails with
  `flavor <id> don't support image <id>`. A sold-out flavor fails with
  `there are no more remaining flavor with id`.
- A billing refusal on create reads `payment method is not allowed for the
  user`. Whether a zero balance gives this message is a
  [probe](vserver-paid-writes-checks.md#refusal-probe).
- Attach fails for a volume already attached (`already attached to
  instance`, `this volume has been attached`). Attaching an encrypted
  volume to a server with plain disks fails with HTTP 400 `BadRequest` and
  `cannot attach encryption volume` (live); it attaches to a server with
  encrypted disks, see
  [encrypted volumes](encrypted-volumes.md#live-results). Any volume write
  fails while the volume `is in-process` or `is migrating`.
- A volume resize that changes neither size nor type fails with `volume
  size or volume type must be changed`. A new type must be in the same
  zone. Sizes outside the type's range fail with `field new_volume_size
  must from`.
- Volume names allow letters, digits, `.`, `@`, `_`, `-`, and space, 5 to
  50 characters.

## Billing model

From GreenNode's billing docs and the console.

- New accounts are prepaid; postpaid needs a request to Sales. The test
  probes used a prepaid account; its current balance is not evidence here.
- A prepaid user pays a resource's period price from the GreenNode credit
  wallet when it is created. The console's price request for a server or
  volume always sends `period: 1`, one month. The server create body has
  no period field; that the direct API charges one month is an
  [open question](#open-questions).
- vServer has no hourly rate. A create quote is a monthly price: `period` 1, 3,
  and 12 all returned the same `optimumPrice` (live). `period` 0 returns
  400 `period: From must be greater than zero`; no `period` returns 500.
- A prepaid delete refunds the unused value, counted to the minute, to the
  credit wallet. The docs say some services may not refund; which ones is
  not stated. Recorded deletes returned the balance to its starting value.
  That observation alone does not prove the precision of refund arithmetic.
- Resize quotes can be signed. The negative CLI observation supports a
  credit interpretation; neither it nor the positive live quote totals
  establishes the gateway's exact arithmetic.
  See [resize credits](#resize-credits).
- Auto-renew renews one month, 3 days before the end, from credit. When
  renewal fails the resource expires, and the user must recover it. A
  resource without auto-renew also expires at the end of its period.
- A postpaid user is billed at the end of each month for actual use from
  the recorded start time.

The console creates a single server through a billing order and a payment
page. For more than one server, and for an IAM user's public VIP, it calls
the vServer create directly, which pays from credit. The SDK uses the
direct call; an IAM user has no payment page.

## Resize credits

Evidence behind the [credit rules](vserver-paid-writes.md#resize-credits):

- Published text: the [billing resize page][resize-billing] describes a
  prepaid configuration-change payment flow and minute-level unused
  value calculations, with service-specific refund exclusions. Its price
  formula is an image hosted on `1985221522-files.gitbook.io`, outside
  the permitted source hosts. The exact arithmetic and worked examples
  are unverified here. The page does not document `v1/price` fields or
  guarantee the direct IAM API uses the displayed payment calculation.
- Observation: the manager-supplied CLI error supports a negative server
  resize `optimumPrice`. The [vLB evidence](lb-writes-api.md#price-quotes)
  separately records negative load balancer resize quotes. Neither proves
  a server refund completed.
- Inference: a prepaid adjustment can represent new value minus unused
  old value over remaining time. If both rates and resource state stay
  fixed, elapsed time can reduce a credit toward zero. This conditional
  model is not a guarantee across price, discount, renewal, or state changes.
- Missing evidence: a sanitized raw server credit response and the signs
  of `originalPrice`, `discountPrice`, each `propertiesPrice` field, and
  the meaning of `discountPercent` on credits. Preserve unknown fields
  in the fixture; do not infer their sign or recompute `optimumPrice`.

No documented bound relates the credit to one month's list price. A longer
prepaid term could exceed such a bound, and the quote contains neither
unused paid value nor an authoritative period end. Finite-only acceptance
is a representation policy, not verification of billing accuracy. Do not
round, take absolute values, add credits to budgets, or use credit size to
permit another operation. Only the sign and positive-price comparison
control the proposed guard.

The [resize API][vserver-api] documents target IDs but no quote ID, price
cap, expected old flavor, or conditional version. If A quotes a downsize
from an expensive flavor to a medium one and B moves the server to a
cheaper flavor before A sends, A's target can become an upgrade. `Once`
prevents a resend within A's call; it neither serializes callers nor makes
the quote atomic. A price or discount change can also reverse the sign.
The actual charge can exceed `MaxPrice`, including 0. Another pre-read
only narrows the gap. Decision 20 gates accepting this residual risk.

Volume growth retains its nonpositive-price refusal as SDK policy; the
published text does not prove a grow can never quote a credit. Generic
resize quotes remain reads for all resource types. Creates cannot select
resize semantics through a caller-controlled action.

[vserver-api]: https://docs.api.greennode.ai/service-docs/vserver.html
[resize-billing]: https://docs.greennode.ai/billing-management/experience-with-billing-and-payment/resource-lifecycle-management/resize-resource-configuration

## Quote requests

These are the SDK request shapes. Create forms have recorded live
evidence; console resize shapes omit the two defaults the SDK helper
adds. `core.QuoteResourceInfo` sets `period` 1 and `isPoc` false for all
four operations (`internal/core/quote.go:12`, tested in `quote_test.go`).
Preserve these defaults; their separate billing effect on a resize is
unverified. Free check 7 captures the existing server request shape.

| Quote | `resourceType`, `action` | `resourceInfo` |
|-|-|-|
| Server create | `server`, `create` | `period` 1, `isPoc` false, `zoneId`, `flavorId`, `imageId`, `rootDiskSize`, `rootDiskTypeId`, `encryptionVolume`; `dataDiskSize` and `dataDiskTypeId` when set |
| Server resize | `server`, `resize` | `period` 1, `isPoc` false, `serverId`, `flavorId` |
| Volume create | `volume`, `create` | `period` 1, `isPoc` false, `size`, `volumeTypeId`, `zoneId`; `encryptionType` when set |
| Volume resize | `volume`, `resize` | `period` 1, `isPoc` false, `volumeId`, `newSize`, `newVolumeTypeId` |

- The server quote ignores keys it does not price: `name`,
  `securityGroup`, `subnetId`, and `attachFloating` did not change it
  (live). So the SDK sends the priced keys in the table and nothing else:
  no `name`, `networkId`, `subnetId`, `securityGroup`, `sshKeyId`,
  `serverGroupId`, or `userData`.
  The priced-only bodies quote the same as the full create bodies (live):
  347,800 VND for `s2-general-1x2` with Ubuntu 24.04 and a 20 GB SSD root,
  951,600 VND for `s2-general-2x4` with a 40 GB root and an 80 GB data
  disk, and 32,000 VND for a 10 GB volume.
- Neither the quote nor the server create sends `osLicence`; the SDK
  creates no Windows server
  ([non-goals](vserver-paid-writes.md#non-goals)).
- One function per create builds the quote body. The quote operation and
  the create's price guard both call it; see
  [quote before a paid write](vserver-paid-writes.md#quote-before-a-paid-write).
- A drift test builds a quote and a create from one Input with every
  field set. It checks that every quote key except `period` and `isPoc`
  appears in the create body with an equal value, so a priced key the
  create sends cannot differ from the one the guard priced. The
  encryption keys are in
  [encrypted volumes](encrypted-volumes.md#body-keys).
- A missing `zoneId` did not change a price (live).
- A resize quote for an unknown ID returns 400: `Can not find this volume
  with id: <id>` for a volume and `Volume is not found` for a server
  (live).

## Prices

Recorded quotes in `hcm-3`, zone `HCM03-1C`, VND a month with VAT;
these are evidence samples, not current price promises.
The account had no discount. Prices are public list prices.

| Item | Monthly |
|-|-|
| Flavor `s2-general-1x2` (1 vCPU, 2 GB) | 283,800 |
| Flavor `s-general-1x2` (1 vCPU, 2 GB) | 264,880 |
| Flavor `s2-general-2x4` (2 vCPU, 4 GB) | 567,600 |
| SSD volume, 3,000 IOPS | 3,200 a GB |
| Volume encryption (`CES`), 1x2 flavor | 85,140 |
| Volume encryption on a separate volume | 0 |
| Elastic IP | 120,000 |
| Snapshot service quote | 5,040 |

| Probe | `optimumPrice` |
|-|-|
| Server `s2-general-1x2`, Ubuntu 24.04, 20 GB SSD root | 347,800 |
| The same with a 40 GB root | 411,800 |
| The same 20 GB root with a 10 GB SSD data disk | 379,800 |
| The same with `encryptionVolume` true | 432,940 |
| The 10 GB data disk server with `encryptionVolume` true | 464,940 |
| `encryptionVolume` true, 20 GB root and 20 GB data disk | 496,940 |
| Server `s2-general-2x4`, 20 GB SSD root | 631,600 |
| Volume, 1 GB SSD | 3,200 |
| Volume, 10 GB SSD | 32,000 |
| Volume, 10 GB SSD, either encryption type | 32,000 |
| Volume, 20 GB SSD | 64,000 |

The server quote splits into `propertiesPrice` lines named `INSTANCE
TYPE`, `ROOT DISK`, `DATA DISK`, and `CES`; the volume quote has one line
named `Volume`. The smallest server above costs about 11,600 VND a day, or
480 VND an hour, since a delete refunds to the minute.

## Catalog (live)

- `hcm-3` zone `HCM03-1C`, the test account's only enabled zone, has five
  flavor zones: General Purpose, General Purpose Code S, Standard Code S,
  High Mem Code S, and High CPU Code S. The smallest flavors are
  `s2-general-1x2` and `s-general-1x2`.
- The zone has one volume type zone, SSD, whose default type is 3,000 IOPS
  and allows 1 to 5,000 GB; its description says 10 GB to 5 TB, and 20 GB
  to 2 TB for a root disk.
- The OS image list returns the same 45 images with or without `zoneId`,
  each marked zone `HCM03-1A`, with a `packageLimit` of 1 vCPU, 1 GB, and a
  20 GB disk for Ubuntu.

## Open questions

- Whether the direct create charges one month at once.
- Whether `deleteAllVolume` false keeps attached data volumes.
- What happens when a period ends without renewal, and after how long an
  expired server is deleted.
- The status and message of a create refused for a zero balance.
- The create responses' shapes, and whether server and volume names must
  be unique on the server.
- Whether a stop is graceful, and whether a reboot shows `REBOOTING`.
- The smallest volume the SSD type accepts: 1 GB or 10 GB.
- Raw server credit fields and their meanings, as listed under
  [resize credits](#resize-credits).
