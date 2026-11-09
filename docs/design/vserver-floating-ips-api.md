# vServer Floating IPs: API

Status: Base decisions accepted; pricing revision proposed; evidence gates open.
See [vServer floating IPs](vserver-floating-ips.md).

Published means first-party schema/product docs. First-party code proves
client expectations, not SDK verification. Recorded live means repository
or manager-supplied sanitized evidence; console/ledger observations are not
published contracts. This pass reads no captures or cloud API. Reported
means unverified external experience. Missing/inferred contracts stay gated.

## Sources

- [vServer OpenAPI reference][api], including its embedded OpenAPI
  schema: `WanIpDto`, `Paging_WanIpDto_`, `InterfaceNetworkInterface`,
  `SecgroupruleDto`, `Zone`, and `ElasticIpDetail`.
- Product pages: [Floating IP][fip], [Instance IP Address][addresses],
  [wizard][wizard], [security groups][groups], [connect guide][connect],
  and [vServer FAQ][faq].
- [Provider server resource][provider]:
  `resourceServerAttachDetachFloating` uses the first internal interface,
  auto-allocates when enabled, and detaches then deletes when disabled.
- First-party Go SDK [server methods][sdk-server] and [URL builder][sdk-url]:
  auto-attach and detach routes, with expected status 204.
- [Paid-write prices](vserver-paid-writes-api.md#prices) and
  [quote requests](vserver-paid-writes-api.md#quote-requests): recorded
  server quote exclusion of `attachFloating`. The exact IP quote bodies
  and manager-supplied observations are recorded below.
- `testdata/server/instance.json` has empty `externalInterfaces` and
  empty internal `floatingIp`/`floatingIpId` fields. It does not prove
  populated floating IP behavior. `testdata/network/list_wan_ips.json`
  has no recorded raw-response provenance and differs from `WanIpDto`.

Provider and SDK links pin the inspected source revisions. The manager
supplied sanitized quote, console, and ledger evidence; no account identity,
private capture path, or ledger row belongs in this document.

## Terms and records

A floating IP is a NAT address on a primary internal interface, invisible
inside the guest OS ([Floating IP][fip]). Provider code passes the
internal row's `floatingIpId` as the WAN IP identifier. This supports
the mapping to `WANIP.UUID`; a populated raw pair must confirm it.

`ElasticIpDetail` and `WanIpDto` are different schemas. They share fields
such as `uuid`, `createdAt`, and `status`, but the former also has
allocation, package, target, and public-subnet fields. Whether
`GET elastic-ips` lists the same addresses is unknown. Do not model that
endpoint as a synonym or infer a separate allocation capability from it.

External Interfaces attach a public interface visible inside the OS.
They cannot coexist with a floating IP under the product rules. An empty
external list in one fixture does not prove all servers have none.

## Calls

Use the existing regional IAM vServer route, `v2/{projectId}`, for the
proposed operations. The reference publishes the legacy vServer gateway
and requires `portal-user-id`; existing IAM SDK reads omit that header.
The proposed IAM writes must verify this compatibility. Do not label
the IAM gateway or header omission as a published floating-write contract.

| Operation | Method and suffix | Success | Evidence |
|-|-|-|-|
| List | `GET wanIps` | 200 | Published |
| Delete | `DELETE wanIps/{wanIpId}` | 204 | Published |
| Attach | `PUT servers/{serverId}/wan-ips/{wanIpId}/attach` | 204 | Published |
| Allocate/attach | `PUT servers/{serverId}/wan-ips/auto/attach` | 204 | First-party code |
| Detach | `PUT servers/{serverId}/wan-ips/{wanIpId}/detach` | 204 | Published |
| Server create | `POST servers` with `attachFloating` | 202 | Published |
| Quote | `POST v1/price` on the regional portal route | 200 | Recorded live |

The list schema publishes `name`, `page`, `size`, `ids`, `serverIds`,
`publicIps`, `status`, `zoneId`, and other filters. Keep the existing SDK
query surface; the ID filter is not live-verified. No WAN detail GET is
published. Second-interface floating operations remain out of scope.

## Bodies

Attach and detach send a JSON object with only `networkInterfaceId`.
For example, `{"networkInterfaceId":"nic-1"}`. Auto-attach uses the
same body in first-party code. The schema also permits `tags` and
`zoneId`; their omission on the proposed IAM route needs verification.
For detach use the interface actually holding the captured floating ID.
Provider index zero is not proof of primary order for multiple interfaces.

Delete has no body. Server create's schema has a boolean `attachFloating`;
R2 sends it explicitly. Current code omits it entirely. The remaining
create contract stays in [paid-write bodies](vserver-paid-writes-api.md#bodies).

## Responses

204 is a bodyless acknowledgement, not proof that a subsequent read has
settled. Validate status before decoding; bound any body the transport
reads even when it will be ignored. Unexpected 200/202 on these writes
is uncertain, not a documented alternative success.

`Paging_WanIpDto_` publishes `listData`, `page`, `pageSize`, `totalPage`,
and `totalItem`. `WanIpDto` has:

| Wire key | Published type | SDK mapping |
|-|-|-|
| `uuid` | string | existing `UUID` |
| `ip` | string | new `IP string` |
| `fixedIp` | string | new `FixedIP string` |
| `networkInterfaceId` | string | existing `NetworkInterfaceID` |
| `status` | string | existing `Status` |
| `createdAt` | date-time string | existing `CreatedAt` |
| `zone` | `Zone` object | new `Zone network.Zone` |

The SDK also has `ID`, `Name`, `IPAddress`, `FloatingIP`, `ServerID`, and
`Type`. Keep these public fields unchanged. Published schema absence
alone does not justify removal or aliasing. Zone uses the existing shared
zone model; the schema includes `uuid`, `name`, `description`, enabled
and default flags, counts, and `zoneType`.

The schema marks no WAN row property required. Missing attachment fields
must not decode as proof of an unattached IP. Before writes ship, a raw
attached/unattached pair must establish presence, null/empty semantics,
status, zone, and interface identity. Preserve presence in private wire
types where needed; public zero values cannot carry that proof.

`InterfaceNetworkInterface` supplies `uuid`, `floatingIp`, `floatingIpId`,
`fixedIp`, `interfaceType`, `type`, `status`, `mac`, `networkUuid`,
`subnetUuid`, `portUuid`, `product`, `serverUuid`, `createdAt`, and
`updatedAt`. Both interface lists use it. Populated internal fields,
not the mere existence of this schema, gate the write settlement rules.

## Mutation outcomes

These SDK rules cover existing-IP attach, detach, and delete, including
both stages of `DetachFloatingIP{Delete:true}`. They do not establish
new API evidence. Use the [wait bounds](vserver-floating-ips.md#waits)
and identity predicates from the design; never resend a mutation.

A 204 is accepted. A definite 4xx rejection or proven unsent request
returns its original error without confirmation, regardless of `NoWait`.
A 5xx, redirect, unexpected 2xx, malformed acknowledgement, or transport
failure without proof of non-send is uncertain. After either acceptance
or uncertainty, apply these rules:

| Send | Confirmation | Return |
|-|-|-|
| Accepted | `NoWait` | Safe Output, nil error; settlement unobserved |
| Uncertain | `NoWait` | Safe Output and `network.ErrNotSettled` wrapping send error |
| Accepted | Target confirmed | Settled Output, nil error |
| Uncertain | Target confirmed | Settled Output and `network.ErrNotSettled` wrapping send error |
| Either | Target not reached by bound | Last safe Output and `network.ErrNotSettled` |
| Either | Read, schema, completeness, or context failure | Last safe Output and `network.ErrNotSettled` |

With waiting enabled, poll until the exact target is confirmed, the
bound expires, or confirmation fails. Incomplete/malformed reads stop
confirmation; they cannot prove success. Preserve both send and confirm
causes with `errors.Is`/`errors.As` (send cause first when both are API
errors). Keep cancellation/deadline identity. Do not overwrite an
uncertain send error with nil after a successful read.

For fields declared by each Output, `Changed` is true after acceptance
or observed target completion, false otherwise. It does not attribute a
concurrent actor's change. `Detached` requires the detach predicate;
`Deleted` requires complete-list absence.
After a send, `NoWait` leaves settlement flags false and billing nil.
A fresh retained, unattached row sets `BillingContinues` true; complete
absence sets it false; unconfirmed state leaves it nil. Known IDs and
last safe server observations remain in Output, even with an error.

Nested DELETE starts only when detach returns nil and `Detached` true.
An uncertain detach returns its error even if confirmed and sends no
DELETE. When detach confirmation proves full-list absence, set `Deleted`
true and billing false regardless of the send outcome; no DELETE is
needed. Otherwise nested delete uses the same rules, retaining `Detached`
true and the captured ID. Complete absence
at its pre-read also completes cleanup without a send; standalone delete
still returns `ErrNotFound` for pre-read absence. A rejected nested delete
keeps `Deleted` false and prior billing state. An unconfirmed one keeps
`Deleted` false and billing nil unless a fresh row proves retention.
An uncertain DELETE followed by complete absence sets `Deleted` true and
billing false but still returns `ErrNotSettled`. Thus Output can report
cleanup complete while the command reports a failed acknowledgement.
`Delete && NoWait` remains invalid before requests; never start another
mutation merely to recover from an error.

This preserves ADR 0003's send-once and uncertain-error identity rules.
Unlike [server actions](../../compute/server_actions.go), which return
immediately on an uncertain send, these operations read when waiting
is enabled to report state and retained charge. Unlike that code's
broad network-error path, a proven unsent request preserves the original
error, per ADR 0003.
R2 keeps existing `CreateServer` send-error behavior: it does not adopt
by name after an uncertain create. The deferred auto-allocation branch
has no verified identity-recovery contract and remains gated.

## Statuses and messages

The reference documents 401 and 500 for list, attach, detach, delete,
server create, and group-rule reads. It supplies no error body schema
for those responses. Test these statuses explicitly. Preserve the shared
`*vngcloud.APIError` status/code and `errors.Is` identities without
inventing a service-specific error body or status-to-message mapping.

The first-party clients indicate quota, one-IP-per-server, missing server
or interface, and already-available WAN refusals. Their exact statuses
on this IAM route are unverified. Synthetic 400, 403, 404, 409, 429,
redirect, and 5xx tests define defensive SDK behavior, not observed API
behavior. Do not match English substrings to invent new sentinels.

The [Floating IP page][fip] requires a server in a VPC, in the same zone,
running or stopped, and without an External Interface. The SDK maps the
known server statuses to `ACTIVE` and `STOPPED`. The service's own
attachment conflict checks are the final authority after a pre-read;
the reference provides no conditional version or idempotency token.

## Billing

The standalone Elastic IP quote is a monthly VND reference, not proof of
an extra `attachFloating` charge. The server quote ignores that flag.
The [price model](vserver-floating-ips.md#cost) keeps both hypotheses open.
Server/volume refunds do not establish an IP refund.

Observed, manager-supplied evidence from the console create form: Floating
IP is checked by default and shown as enabled. The price panel contains
only `s2-general-1x2` at 283,800 VND and 20 GB NVME at 74,800 VND; original
and total are 358,600 VND, with no IP line. The form was not submitted.
This differs from the 347,800 server reference; neither fixes a live price.

Observed, manager-supplied ledger evidence: a one-month credit-history
window contains no Elastic/Floating IP charge. A server reported created
with `attach_floating = true` has only the instance order (instance, root
disk, encryption) on create and renewal; cost explorer lists no IP resource.
This supports inclusion but cannot establish a universal billing rule or
exclude delayed, bundled, discounted, or reservation-dependent charges.

The [wizard][wizard] documents assignment at create and paid reservation;
the [address comparison][addresses] ties billing to reservation requests.
The [Floating IP page][fip] says billing continues after disassociation.
None explicitly prices `attachFloating` at create; inclusion is inference.
Preserve all three statements: detach is not a billing stop; exact rates
and reservation effects need evidence. Never change reservation implicitly
or promise zero retained cost.

The [FAQ][faq] says deletion loses the address permanently. Whether
server deletion also removes its floating IP is unknown. Prefer explicit
IP cleanup; do not infer cleanup from the provider's server delete or
use an undocumented cascade to make R2 shippable.

Existing-IP attachment is intended to create no allocation charge, but
that is inference. Its no-new-charge evidence gates R3. Auto-allocation
can incur a new charge; a concurrent refusal may leave a billed orphan.
That risk gates the optional allocation extension, not existing-IP reuse.

## Elastic IP quote

The manager's free IAM `hcm-3` probe used public `pricing.GetQuote` on the
regional portal `POST v1/price` route. Both bodies returned HTTP 200 with
identical raw bodies, satisfying the body probe, not the create-charge gate.

```json
{"resourceType":"elastic-ip","action":"create","resourceInfo":{
  "period":1,"isPoc":false}}
{"resourceType":"elastic-ip","action":"create","resourceInfo":{
  "period":1,"isPoc":false,"zoneId":"zone-1"}}
```

`zone-1` replaces the enabled zone code from `ListZones.UUID`. It did not
change the price; this does not prove global zone independence. Select the
zone-bearing body with the validated create zone for the conservative
reference quote. No reservation selector is verified; never invent one.

The supplied raw-body excerpt is identical for both candidates:

```json
{"optimumPrice":120000,"discountPercent":0.0,"originalPrice":120000,
 "discountPrice":0,"propertiesPrice":[{"optimumPrice":120000,
 "monthlyPrice":120000,"currentPrice":null,"discountPercent":0.0,
 "name":"Elastic IP","description":null}],"artifactPrices":null}
```

The unenveloped object has numeric totals, one property, null current
price/description/`artifactPrices`, equal original/optimum, and zero
discount. `period:1` and `monthlyPrice` support monthly units; currency is
absent, so VND follows the console. Nonzero discounts remain unobserved;
checked arithmetic is an SDK rule, not new API evidence.

Provenance: manager-supplied sanitized excerpts of saved raw responses,
not SDK output. Before implementation acceptance, obtain both raw fixtures,
content type and sanitation metadata, compare raw versus SDK privately,
and test `pricing.GetQuote` decoding, nulls, and retained unknown raw fields.
This fixture gate stays open. Keep 1 MiB bounds, 30 s contexts, credential
suppression, and finite retries; no new capture/order is authorized.

## Credit-history decision procedure

The console uses `gateway/api/v1/transactions` with the IAM user token
(manager observation, not published contract). Use approved console reads;
never extract tokens or add CLI/harness HTTP bypasses. Transactions are
outside [billing scope](billing.md#deferred); SDK automation needs its own
brief. Method, filters, schema, pagination, and posting delay are missing;
observe them privately, never guess wire fields.

Before purchase, verify read access and complete paging for the run's
account and time range. Capture a baseline, then snapshots after create,
after each detach, after reattach if performed, after IP deletion, and
after server deletion. Use the same window starting before create; extend
its end at each read. Record privately at every checkpoint:

- Request method, route, filters, page/cursor coverage, HTTP status,
  content type, retrieval time, and the operation's start/settlement time.
- Every new/changed transaction's stable ID, posted/effective times,
  status, debit/credit direction, currency, gross amount, discounts/tax,
  net amount, product/description, resource/order linkage, billing period,
  reservation indicator, and linked refund/reversal, where present.
- Map those facts to actual raw keys and mark absent fields unknown.
  Retain raw rows privately; deduplicate IDs across overlapping snapshots.
  Track pending/failed/reversed rows separately from settled charges.
- Reconcile server/root/encryption and IP components with both quotes,
  order details, wallet deltas, and unrelated activity. Gross debit and
  refund remain separate; net zero is not proof that no IP was charged.
  An amount or description alone cannot attribute a row to the owned IP.

Missing pages, denied reads, ambiguous linkage, unexplained deltas, or
unsettled entries leave the result inconclusive. Limit each console read
session to 5 min and 50 pages; never poll without a bound. Clean up without
waiting for billing. A separately authorized read-only reconciliation
within 24 hours after cleanup may finish posting; expiry without complete
attribution keeps the release gate open. No finite empty window alone
proves there can never be a delayed or renewal charge.

- Included hypothesis: the settled create order itemizes only server and
  disk costs, reconciles to the server quote, and complete credit history
  through cleanup/reconciliation has no attributable IP debit. Record this
  as observed inclusion for the tested configuration, not universal free IP.
- Separate hypothesis: a settled IP debit or explicit IP component in the
  create order explains the additional quote amount after period, discount,
  and tax reconciliation. Keep the reserve only if that model matches.
- A charge beginning only on detach/reservation is a conditional outcome:
  it does not prove an extra create charge or free retention. Stop further
  experiments, complete approved cleanup, and return the model to design.
- Delayed IP debits/refunds must be attributed to their effective operation;
  deletion does not erase an earlier charge. Different amounts, missing
  itemization, or incomplete evidence decide neither hypothesis.

The delegated manager records the outcome. Inclusion requires the design's
reviewed pre-tag correction; separate charging needs matching evidence and
guidance. Other results block the tag. Keep detach warnings/refund gates.

## Published behavior

The [wizard][wizard], [group page][groups], and [FAQ][faq] document SSH on
234 and RDP on 3490. The default-group contract is in
[free writes](vserver-writes.md#server-rules-from-the-product-docs).
Include 3490 in the guard; these sources do not inspect a current
account's default group.

The [connect guide][connect] describes generated passwords and SSH keys.
The FAQ's statement about a disabled administrator does not prove Linux
password login is disabled for key-only API creates. The Ubuntu image's
actual sshd policy is unverified. Never describe the SDK as guaranteeing
key-only guest access merely because it sends no password fields.

The [provider resource][provider] explicitly detaches then deletes when
`attach_floating` is disabled. That proves provider behavior; it does not
prove that the detach API itself deletes an IP. The SDK's explicit
`Delete` option preserves that distinction.

## Reported behavior

An external project reports that its Ubuntu 24.04 image permits password
login on port 234, its default group is world-open, and provider detach
loses the IP. These reports justify caution, not fixture provenance or
an API contract. Do not expose that project's account data in this design.

## Missing evidence

- R2: raw quote fixtures decoded through SDK; pricing decision approval;
  created-IP billing attribution, reservation scope, and nonzero discounts;
  IAM public create compatibility; populated server fields; settlement
  timing; a usable cleanup route and its permissions. The quote-body
  probe is complete, not the create-charge or fixture gate.
- R3: populated WAN list envelope and attached/unattached rows, stable
  identity and zone fields, empty-list encoding, IAM attach/detach/delete
  acknowledgements, existing-IP attachment charge, and final conflict
  refusal against deleting an attached IP. Missing refusal evidence
  needs a separately authorized safe check, never a speculative delete.
- Optional auto-allocation: matching quote, charge, settlement, and
  concurrent failure/orphan behavior. No evidence permits a second IP
  purchase in L4.
- IP refunds, reservation effects, server-delete cascade, and guest
  password policy remain unknown. Do not make release claims about them
  without evidence; cleanup must not depend on a cascade or refund.

[api]: https://docs.api.greennode.ai/service-docs/vserver.html
[fip]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/floating-ip
[addresses]: https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/instance-ip-address
[wizard]: https://docs.greennode.ai/vserver/compute-hcm03-1a/instance/create-an-instance-by-using-the-wizard
[groups]: https://docs.greennode.ai/vserver/compute-hcm03-1a/security/security-groups.md
[connect]: https://docs.greennode.ai/vserver/compute-hcm03-1a/instance/connect-to-virtual-server.md
[faq]: https://docs.greennode.ai/faq/vserver.md
[provider]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/resource/vserver/resource_server.go
[sdk-server]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/services/compute/v2/server.go
[sdk-url]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/services/compute/v2/url.go
