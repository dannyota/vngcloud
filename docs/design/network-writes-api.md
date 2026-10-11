# Public NAT and VPN Write Evidence

Status: Verified evidence (2026-10-11) for the accepted
[write design](network-writes.md) and [NAT contract](network-writes-nat.md).

Evidence comes from root-console captures, public HAN console code fetched
without credentials, and two IAM-user NAT live runs on 2026-10-11. Each IAM
run used SDK login, a bearer token without cookies, one order, and its own
new disposable VPC. Console code proves request construction, not live
service behavior. No account values appear here. The
[read evidence](network-services-api.md) owns inventory schemas and origins;
the newer IAM observations below close only the named write evidence gaps.

## Paths and payment flow

Let `{vnet}` mean the selected trusted vNetwork origin plus
`/vnetwork-gateway`. `{regionId}` is the region-level path identifier called
`ZoneID` in the read SDK; it is not an availability-zone UUID. Let
`{nat}` mean `{vnet}/vnetwork/v1/{regionId}/{projectId}/nats` and
`{billing}` mean the configured central Billing origin.

The public console price component selects:

- Root: AUTO only for POSTPAID accounts; otherwise MANUAL.
- Every non-root IAM user: AUTO.
- AUTO: POST the order directly to the resource collection. NAT uses
  `{nat}`; VPN uses `{vnet}/vnetwork/v1/{projectId}/vpns`. Send no `period`
  or `paymentType` and retain `isBuyMorePoc`.
- MANUAL: send a billing `create-order`, add `resourceInfo.period: 1`, and
  drop `isBuyMorePoc`, then use payment-console checkout.

AUTO is a console flow name, not a `paymentType: "auto"` field. The live
IAM NAT calls below verify the direct path. Root POSTPAID direct purchase,
service accounts, static bearer providers, custom providers, and VPN direct
purchase are not live-verified. vStorage's `paymentType` precedent is not
the vNetwork wire contract.

## NAT discovery

The console uses these paths beneath `{nat}`:

```text
GET /v3-whitelist
GET /zones?params={"search":[],"sort":{},"page":1,"size":1000}
GET /nat-package?params=<encoded-params>&zoneUuid=<availabilityZoneId>
GET /vpcs?params=<encoded-params>
```

The params values are URL-encoded JSON. The whitelist response has
`data: {enabledForAll: boolean, whitelistedPortalUserIds: [...]}`. The form
omits and does not require subnetUuid for an eligible account; outside the
V3 whitelist the form sends subnetUuid. Live IAM reads showed enabledForAll
true. No portal-user values or membership logic are needed for the accepted
V3-only SDK contract.

The zones reply is an unpaged array envelope, as is the package reply:

```text
message: "Successfully", code: 200, success: true, data: object[]
row: uuid, name, zoneType, description: string
     isEnabled, isDefault, enable: boolean
     volumeCount, serverCount: integer
     id, dnsStatus: null
```

UUIDs look like `HAN01-1B`. The form selects zoneType AVAILABILITY with
isDefault. `enable` was false for both zones, including the enabled one, so
`isEnabled` is the availability flag.

The NAT VPC picker is paged: `{success, data, page, size, totalPage,
total}`. Its rows match the read evidence's VPC object, except that
`zones` holds `[{uuid: "<availabilityZoneId>", ...}]`. Multi-page picker
behavior is not observed.

Live `nat-package` without zoneUuid returned a package for HAN01-1A, whose
zone was disabled with "Contact to enable". With zoneUuid HAN01-1B, enabled
and default, it returned the package used by the successful root purchase.
The package UUID implies the order's availability zone; the order has no
separate availability-zone field. The form selects the default package.

The package response is an unpaged array envelope:

```text
message: "Successfully", code: 200, success: true, data: object[]
row: uuid, name, packageId, resourceServiceId, billingSku, serviceName,
     currencyUnit, createdAt: string
     description: null
     monthlyPrice: number
     isDefault: boolean
     price, image: object
price: optimumPrice, originalPrice, discountPrice: integer
       discountPercent: number
```

Observed values include name Standard, billingSku `nat.s-standard`,
monthlyPrice 712400.0, currencyUnit VND, and isDefault true. The image is an
object; its discovery fields are not established here. Do not reuse the
inventory package schema blindly: inventory monthlyPrice is zero and its
nullable fields differ.

## NAT price and direct order

Price is `POST {vnet}/vnetwork/billing/v1/{regionId}/{projectId}/price`.
The IAM request used the captured fields with renewal false:

```json
{
  "resourceType": "nat",
  "action": "create",
  "resourceInfo": {
    "isPoc": false,
    "isEnableAutoRenew": false,
    "isBuyMorePoc": false,
    "natName": "<name>",
    "packageUuid": "<packageId>",
    "vpcUuid": "<vpcId>",
    "regionUuid": "<regionId>",
    "projectUuid": "<projectId>"
  }
}
```

HTTP 200 returned the same envelope and numbers as the root price capture:
`{message, code: 0, success: true, data}`. Data contains numeric
`optimumPrice`, `discountPercent`, `originalPrice`, and `discountPrice`, plus
`propertiesPrice`. Each property has `optimumPrice`, `monthlyPrice`,
`currentPrice`, `discountPercent`, `name`, and `description`. Nullability
beyond the observation remains unverified. Standard quoted 712,400
VND/month. Price omission probes remain optional; preserve the priced fields.

The live order is `POST {nat}` with exactly that resourceType, action, and
resourceInfo, plus top-level `tagDetails: []`. There is no subnetUuid for
the observed V3 account, no period, and no paymentType. Console code uses
the same object but defaults isEnableAutoRenew to true.

Both IAM creates returned HTTP 201:

```json
{"message":"Success","code":0,"success":true}
```

No data, order ID, or resource ID was returned. Cash available fell by
exactly the quote at once, with no checkout or hold. The NAT was initially
absent from inventory, became PROVISIONING within about a minute, and
became ACTIVE about 5 minutes 45 seconds after ordering. The immediate debit
and later matching billing row establish the observed paid purchase; a
successful response or temporary list absence alone does not.

## NAT billing and renewal

The server ignored `isEnableAutoRenew: false` in both orders. The existing
`billing list-resources` read uses
`GET {billing}/gateway/api/v1/resources`. A matching row appeared only after
ACTIVE, with none while PROVISIONING:

```json
{
  "product": "vserver",
  "artifactType": "nat",
  "artifactId": "nat-<uuid>",
  "renewType": "AUTO-RENEW",
  "billingType": "PREPAID",
  "renewPeriod": 1,
  "channel": 1,
  "status": "active",
  "cost": 100,
  "billingElements": [{"sku": "nat.s-standard", "quantity": 1}]
}
```

This is a field excerpt, not the full billing envelope. Cost's meaning is
unknown; it is not a price, charge, balance, or eligibility guard. Use the
[billing read contract](auto-renew.md#billing-resource-read) for decoding.

Run 2 sent one renewal write through the existing
`internal/billingresources.Put` contract:

```text
PUT {billing}/gateway/api/v1/resources/autoRenew
portal-user-id: <portalUserId>
```

```json
[{
  "product": "vserver",
  "artifactType": "nat",
  "artifactId": "nat-<uuid>",
  "channel": 1,
  "autoRenewInfo": {"isEnable": false, "period": 43200}
}]
```

The helper succeeded. The next billing read, less than one second later,
showed renewType MANUAL and renewPeriod null. Channel 1 is observed, not a
caller default. Identity lookup, PUT success decoding, privacy, and the
43200-minute disable field follow [auto-renew](auto-renew.md), the same
transport used by `storage put-project-auto-renew`. This is NAT-specific
live evidence for reuse; it does not authorize generic resource settings.

## NAT delete and route effects

The console and both live runs sent `DELETE {nat}/{natId}` with JSON `{}`.
Live success was HTTP 200:

```json
{"message":"Success","code":0,"success":true}
```

The row showed DELETING for about 50 seconds, then disappeared from the
list. The matching billing row disappeared too. Before create, the new VPC
had no route tables. After ACTIVE, one `rt-<uuid>` belonged to that VPC with
`0.0.0.0/0 -> <NAT private IP inside the VPC CIDR>`, routingType ip, status
ACTIVE. After delete, the VPC again listed no route tables. Run 1 deleted
the VPC on the first attempt after NAT inventory absence.

Run 1 received the full refund immediately, with zero net cost. Run 2's
net cost was 16 VND. Billing began at ACTIVE and deletion followed about a
minute later; 16 VND is about a minute of the observed monthly price.
Proration by use is an inference, not a guaranteed formula. Refund amount,
timing, prior-route restoration, and immediate parent deletion are not
promises. Earlier root evidence includes an ERROR NAT refunded in full
and a short VPC dependency delay after delete. These observations can
coexist with the successful empty-VPC IAM cleanup.

## Root checkout evidence

Root MANUAL NAT used
`POST {vnet}/vnetwork/billing/v1/{regionId}/{projectId}/create-order`.
Its body has renewal true, resourceInfo.period 1, no isBuyMorePoc, no
paymentType, and tagDetails []. The response was:

```json
{
  "message": "Success", "code": 0, "success": true,
  "data": {
    "id": null,
    "redirectUrl": "https://payment.console.vngcloud.vn/orders/<uuid>"
  }
}
```

The browser checkout on `https://payment.console.greennode.ai` sent
`POST /payment-api/v1/payments` with paymentMethod `pay-now-credit`:

```json
{
  "paymentMethod": "pay-now-credit",
  "items": [{
    "product": "vserver", "resId": "nat-<uuid>",
    "resName": "nat", "action": "create", "resType": "nat",
    "billingElements": [{
      "sku": "nat.s-standard", "quantity": 1, "metaKey": "APP-LICENSE"
    }],
    "billingTime": {"type": "block", "duration": 43200},
    "metadata": {"paymentId": "<id>", "callbackUrl": "<callback-url>"}
  }],
  "region": "hn-1"
}
```

The callback was
`https://vnetwork-han01.vngcloud.vn/vnetwork-core/vnetwork/v1/payment/callback`.
It is evidence, not a credential destination. Do not substitute checkout's
hn-1 for SDK han-1. Payment returned HTTP 204, followed by
`DELETE /payment-api/v1/orders/<uuid>`, also 204. That DELETE does not prove
unpaid cancellation or refund semantics. The source of paymentId and
resource ID, duration units, and checkout renewal propagation remain open.
Unticking renewal made no immediate request; later billing showed MANUAL.

Root price/order HTTP statuses were not recorded. IAM statuses above do
not fill that gap. Root checkout and unpaid-order probes are irrelevant to
shipping the verified direct NAT path and remain outside SDK scope. No
root cookie or payment-console fallback is allowed.

## VPN console-code evidence

These fields come from public code only, not an IAM VPN live purchase.
AUTO posts to `{vnet}/vnetwork/v1/{projectId}/vpns`. The order uses
resourceType vpn, action create, tagDetails [], and resourceInfo with
isPoc, isEnableAutoRenew, isBuyMorePoc plus:

```text
vpnName, packageUuid, vpcUuid, subnetUuid, regionUuid, projectUuid
tunnels: [{
  siteName, tunnelName, remoteGatewayIp, remoteNetworkCidr,
  customPsk: true, preShareKey,
  phase1Configs: [{
    phase1Algorithm, phase1Hash, phase1DhGroup, phase1IkeLifeTime
  }],
  phase1IkeLifeTime,
  phase2Configs: [{
    phase2Algorithm, phase2Hash, phase2DhGroup, phase2IkeLifeTime
  }],
  phase2IkeLifeTime, phase2DhGroup
}]
```

Algorithm lists use GET at the VPN collection's `/tunnels/phase1` and
`/tunnels/phase2`. The code also has POST `/tunnels/generate-psk`; do not
call it, since it returns a key. VPN delete sends no body to
`{vnet}/vnetwork/v1/{projectId}/vpns/{vpnId}`. Earlier root delete returned
HTTP 200 with `{success}` and refunded within minutes.

The code locates the key in the create body. It does not prove key-free
pricing, accepted phase values, lifetime units, required duplicate lifetime
fields, response IDs, direct debit, or renewal behavior. VPN remains gated
on its own live probe and approved body contract. Keep the existing
Sensitive request, omitted-key, and synthetic-fixture boundaries.

## Probes before implementation

These records are evidence requirements, not authorization for API calls.
"Proven" means only the scope stated. Read-only discovery can close schema
gaps without another purchase. Multi-page guards remain an implementation
gate; do not buy resources solely to produce a multi-page inventory.

| Evidence | State and remaining work |
|---|---|
| IAM NAT auth | Proven: HAN SDK IAM login, bearer only, no cookies |
| Other auth | Unverified: root direct, service/static/custom providers |
| HAN mapping | Direct lifecycle proven; preserve region/AZ distinction |
| NAT discovery | Zone-filtered package and V3 true proven |
| Catalog decoders | Record zones/VPC envelopes, scalar types, nullability |
| NAT price | Proven: false renewal, HTTP 200, root price parity |
| NAT direct order | Proven: HTTP 201, one debit, no ID, no checkout |
| NAT payment | Proven: immediate debit and later matching PREPAID row |
| NAT renewal | Proven: order flag ignored; one PUT then MANUAL/null |
| NAT delete | Proven: body {}, code 0, DELETING then absence |
| Billing cleanup | Proven: NAT billing row disappears after delete |
| Guard scans | Open: multi-page advancement, totals, membership scans |
| Empty-VPC routes | Proven: route table added then removed |
| Existing routes | Open: prior-route behavior and detach timing bounds |
| HCM writes | Open: successful lifecycle; root failure proves no support |
| Unpaid orders | Not a direct-path gate; root checkout stays out of scope |
| VPN body | Code only; live acceptance and required fields remain open |
| VPN algorithms | Code routes found; values and lifetime units open |
| VPN quote | Open: route, schema, key exclusion, amount and term |
| VPN settlement | Open: IAM debit, IDs, states, renewal-off, cleanup |
| SDK write release | Open: implementation lifecycle and adversarial review |

No refused or ambiguous order authorizes a second attempt. Unexpected
checkout on the direct path remains NotSettled unless reads establish a
clean checkout requirement. Empty inventory alone cannot do so. A future
payment extension needs its own approved contract and probes for IAM auth,
same-order binding, amount, term, renewal, and unpaid-order cleanup.

## Paid implementation checks

The two IAM probe runs are complete evidence, not SDK implementation tests.
The manager scopes each later run under the accepted owner decisions and
[live-data](../../instructions/live-data.md), naming the private account,
region, disposable VPC, package, cap, one order, renewal step, and cleanup.
The NAT renewal-step amendment still needs owner approval. Do not run NAT
in production or execute concurrent VPC-creating runs.

For HAN NAT, verify a fresh quote, one direct POST, uniquely identified
ACTIVE NAT, one matching billing row, and the approved renewal-off step.
Record route changes, one DELETE, inventory/billing absence, parent cleanup,
and debit/refund reconciliation. A renewal PUT failure never permits resend
or automatic rollback inside create. Test harness cleanup is separately
scoped to the uniquely identified resource and never repairs shared routes.

VPN needs its own body and key-free quote evidence before a paid lifecycle.
Use an invented key and approved test peer; never persist a live key-bearing
body. Each additional region needs its own successful service lifecycle.
Root checkout remains excluded unless a separate amendment is approved.

Record baseline IDs, routes, balance, and transactions privately. Pause
unrelated spending for reconciliation. Use one unique name and one order.
SDK verification is a distinct paid attempt from earlier discovery. A
false success, debit above quote, unconfirmed renewal, or timeout fails the
check even if cleanup succeeds. Never resend to repair uncertainty.

Delete only a uniquely identified run resource. Confirm absence, compare
routes, and wait for parent dependencies before deleting parents. Poll
refund reconciliation for at most 10 minutes; never resend DELETE because
credit has not returned. A prorated refund is allowed when the net cost is
explained; full reimbursement is not the expected constant.

Keep unresolved renewal, payment, or resources in a private leftover record
with known IDs, last state, and cleanup outcome. Such leftovers block
release. Public records contain only schemas, placeholders, durations,
counts, statuses, and approved cost evidence. CLI mapping uses deterministic
tests; the implementation also needs the required independent write review.
