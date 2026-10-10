# Public NAT and VPN Write Evidence

Status: Draft, pending owner approval with
[the write design](network-writes.md).

The observations below come from root-console calls verified on
2026-10-11. They do not establish IAM user authorization for writes or
payment. No account values appear here. The
[read evidence](network-services-api.md) owns list schemas, regional
origins, and resource state observations.

## NAT price and order

Paths are relative to the selected vNetwork origin. Define the billing
prefix as `/vnetwork-gateway/vnetwork/billing/v1/{zoneId}/{projectId}`.
HAN captures establish these operations:

- Price: `POST {billing-prefix}/price`.
- Order: `POST {billing-prefix}/create-order`.

Captured price body:

```json
{
  "resourceType": "nat",
  "action": "create",
  "resourceInfo": {
    "isPoc": false,
    "isEnableAutoRenew": true,
    "isBuyMorePoc": false,
    "natName": "<name>",
    "packageUuid": "<uuid>",
    "vpcUuid": "net-<uuid>",
    "regionUuid": "<id>",
    "projectUuid": "pro-<uuid>"
  }
}
```

The price envelope has `message`, `code: 0`, `success: true`, and `data`.
Data contains numeric `optimumPrice`, `discountPercent`, `originalPrice`,
and `discountPrice`, plus `propertiesPrice`. Each property has
`optimumPrice`, `monthlyPrice`, `currentPrice`, `discountPercent`, `name`,
and `description`. Nullability beyond the supplied observation remains
unverified. The observed Standard quote is 712,400 VND/month.

Captured order body:

```json
{
  "resourceType": "nat",
  "action": "create",
  "resourceInfo": {
    "isPoc": false,
    "isEnableAutoRenew": true,
    "natName": "<name>",
    "packageUuid": "<uuid>",
    "vpcUuid": "net-<uuid>",
    "regionUuid": "<id>",
    "projectUuid": "pro-<uuid>",
    "period": 1
  },
  "tagDetails": []
}
```

The order omits `isBuyMorePoc` and sends no `paymentType`. Its response is:

```json
{
  "message": "Success",
  "code": 0,
  "success": true,
  "data": {
    "id": null,
    "redirectUrl": "https://payment.console.vngcloud.vn/orders/<uuid>"
  }
}
```

There is no usable resource or order ID in the observed `id` field.
The draft's `paymentType: "auto"` at the top level and explicit
`isEnableAutoRenew: false` in both serializers require probes. Do not
copy the captured true renewal default into the proposed SDK.
The supplied evidence does not record price/order HTTP success statuses;
record those before fixing the transport's accepted-status sets.

## Payment

The browser checkout runs on `https://payment.console.greennode.ai`.
It sends `POST /payment-api/v1/payments` with this NAT body:

```json
{
  "paymentMethod": "pay-now-credit",
  "items": [{
    "product": "vserver",
    "resId": "nat-<uuid>",
    "resName": "nat",
    "action": "create",
    "resType": "nat",
    "billingElements": [{
      "sku": "nat.s-standard",
      "quantity": 1,
      "metaKey": "APP-LICENSE"
    }],
    "billingTime": {"type": "block", "duration": 43200},
    "metadata": {
      "paymentId": "<id>",
      "callbackUrl": "<callback-url>"
    }
  }],
  "region": "hn-1"
}
```

The observed callback URL is
`https://vnetwork-han01.vngcloud.vn/vnetwork-core/vnetwork/v1/payment/callback`.
It is evidence, not an SDK credential destination. Checkout's `hn-1` must
not be substituted for the SDK's `han-1` without a verified mapping.
The source of `paymentId`, resource ID, and other payment fields is not
captured. Do not construct a payment from guessed values or the order URL.
The duration value is observed; its unit needs confirmation.

Payment returns HTTP 204. The browser then sends
`DELETE /payment-api/v1/orders/<uuid>`, also returning 204. That sequence
does not prove that this DELETE cancels an unpaid order or refunds a
payment. The SDK must not use it as speculative cleanup.

Unticking auto-renew sends no request at that moment. Billing records the
NAT as `MANUAL` despite the order's true renewal field. The mechanism
carrying the checkout choice into payment remains unknown. A payment 204
alone therefore cannot confirm the draft's renewal-off contract.

vStorage's verified `paymentType: "auto"` precedent is described in
[its evidence](storage-projects-api.md#paid-check-record). Neither that
success nor this root-browser payment proves vNetwork auto payment or
IAM user access to `payment-api`.

## VPN and deletes

The VPN create form requires name, zone, package, VPC, subnet, a default
site (name, remote gateway IP, pre-shared key), and a default tunnel (name,
remote CIDR), with IKE and IPsec algorithm choices. Its price route,
create-order route, resource type, body, payment fields, and response are
not captured. Do not derive a create body from the list model.

Observed monthly VPN prices are 545,700 VND for Standard and 1,691,400 VND
for Medium. These are reference prices, not the missing VPN quote contract.

Resource deletes use these exact paths on the selected vNetwork origin:

```text
DELETE /vnetwork-gateway/vnetwork/v1/{zoneId}/{projectId}/nats/{natId}
DELETE /vnetwork-gateway/vnetwork/v1/{projectId}/vpns/{vpnId}
```

NAT returns HTTP 200 with `{message, code, success}`. VPN returns HTTP 200
with `{success}`. Record the NAT success code value and whether either
request needs a body; the supplied delete evidence does not establish
those details. Never confuse resource DELETE with checkout-order DELETE.

NAT reaches `ACTIVE` about 5 to 6 minutes after payment. Creation adds
0.0.0.0/0 to the VPC route table, changing every VM's egress. An `ERROR`
NAT was automatically refunded in full. Deleting an active NAT refunded
in full at once; VPN deletion refunded in full within minutes. Refunds
are observations, not guaranteed amounts or timing. NAT route cleanup
and dependency delay are covered by the read evidence and still need a
before/after route comparison in a disposable VPC.

## Probes before implementation

These are evidence requirements, not authorization to call the API.
Read-only discovery can share the preparation for a paid run. Each order
attempt belongs to one separately approved refundable paid run below.

| Missing evidence | Required result |
|---|---|
| IAM user scope and auth | Cookie-free price, order, delete and confirm reads |
| Regional mapping | Zone, region, project and VPC namespaces proven |
| Package lookup | Verified selectable package IDs and one-month support |
| NAT price | False renewal accepted; exact status and price field types |
| NAT auto order | Top-level auto accepted; one debit; response ID fields |
| Payment confirmation | Verified read binds order, debit and resource |
| Renewal confirmation | Verified billing read and `MANUAL` meaning |
| Delete details | Body requirements, NAT code, refusals, absence semantics |
| Guard scans | Page advancement, total consistency and scope membership |
| NAT route effects | Added route, removal, prior-route behavior, detach delay |
| VPN quote and order | Exact routes, bodies, IDs, term and price semantics |
| VPN phase inputs | Algorithms, lifetime units, required phase fields |
| VPN key placement | Key only in create; pricing works without the key |
| VPN settlement | Safe identity fields, failure states and renewal mapping |
| Unpaid orders | Verified read, expiry/cancel path, holds and leftovers |

The auto-order probe must establish whether auto is accepted, ignored,
or refused. An ignored field can still create an unpaid order. Record
response schemas with placeholders, payment and renewal evidence, and
cleanup results even when create is deferred. Read success does not prove
write authorization. HCM root failure does not prove supported HCM writes.

Only if auto cannot support create and the owner elects a payment
extension, probe cookie-free IAM user `payment-api` access. Establish
trusted host configuration, same-order binding, amount/term/renewal fields,
204 semantics, failure ambiguity, and unpaid-order DELETE semantics.
Do not reuse browser cookies or add a root-token fallback. Update the
design before implementing this alternative.

## Refundable paid live runs

Each run needs approval under [live-data](../../instructions/live-data.md),
naming the private account, region, disposable VPC, package, cap, and one
order attempt. Refundable describes the observed cleanup path, not a
promise of zero net cost. Never test NAT in a production VPC. Each run
creates its own parents, and only one VPC-creating run executes at a time.

1. **NAT in HAN:** one Standard NAT, one month, explicit renewal false,
   one proposed auto order. Quote afresh and stop above the approved cap.
   Record auto acceptance or refusal, payment evidence, renewal state,
   provisioning time, route changes, delete behavior, and refund.
2. **VPN:** after body discovery, one Standard VPN with one site and
   tunnel in a disposable VPC/subnet, an invented key, and an approved
   test peer. Use one auto order. Check safe returned configuration,
   payment, renewal, provisioning, delete, and refund. No connectivity
   claim follows from `ACTIVE`; never persist the live key-bearing body.
3. **Additional region:** one resource of the service being enabled,
   repeating its full lifecycle before enabling writes in that region.
   A successful HAN run cannot establish HCM payment or provisioning.
4. **Conditional payment extension:** only under an approved amendment,
   one order and one payment for that same order. Prove IAM access and
   renewal-off behavior, then delete the resource and reconcile. Do not
   use a failed earlier run as permission for this run or a second order.

For every run, record baseline resource IDs, route state, balance, and
transactions privately. Pause unrelated spending for reconciliation.
Use a unique name, confirm no baseline match, and record only the safe
schemas needed above. Discovery and later SDK verification are distinct
paid attempts if both are needed; approval for one never covers the other.

After sending, only read to settle uncertainty. Register cleanup once a
new resource is uniquely identified. Never resend to repair an error.
A false success, debit above quote, unexpected renewal, refusal, or timeout
fails the run even if cleanup succeeds. Inspect pending orders and holds
when no resource appears. Cancel an unpaid order only through a proven
path authorized for that run; otherwise report it as a leftover.

Delete only the run's resource. Confirm inventory absence and, for NAT,
compare routes and wait for the VPC dependency to clear. Delete children
before parents, stopping if a child still holds a parent. Never repair a
shared route or delete a guessed resource. Reconcile debit and refund
from transactions, polling for at most 10 minutes after deletion. Do not
assume a full refund or resend DELETE because credit has not returned.

The private leftover record includes known IDs, attempted name and scope,
last state, renewal, debit/hold/refund, pending order, and cleanup outcome.
Uncertain expiry, missing credit, or remaining resources block release.
Public records contain only schema, placeholders, durations, counts,
statuses, and approved price evidence. SDK live verification repeats the
applicable successful lifecycle with the same one-attempt and cleanup
rules before release; CLI request mapping uses deterministic tests.
