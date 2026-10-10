# vNetwork NAT and VPN API Evidence

Status: Discovery incomplete (2026-10-10). These observations support the
[read design](network-services.md); they do not approve implementation.

The authenticated GreenNode console returned HTTP 200 for regions, NAT
list, and VPN list. Both resource inventories were empty. The observations
below contain route templates and field types, with no account values or
capture bodies that contain resource data.

## Verified requests

The observed console origin is
`https://hcm-3-vnetwork.console.greennode.ai`. The gateway prefix is
`/vnetwork-gateway/`. Paths below include that prefix.

| Operation | Method | Path |
|---|---|---|
| Regions | GET | `/vnetwork-gateway/vnetwork/v1/regions` |
| NAT list | GET | See NAT path below |
| VPN list | GET | See VPN path below |

NAT list path:

```text
/vnetwork-gateway/vnetwork/v1/{zoneId}/{projectId}/nats
```

VPN list path:

```text
/vnetwork-gateway/vnetwork/v1/{projectId}/vpns
```

The VPN path has no zone segment. The browser included an Authorization
header on the resource requests. No portal, project, or region headers
were observed on those requests. In a fresh isolated browser context with
no cookies, the same list URLs returned HTTP 200 using only the captured
IAM bearer Authorization header, with no extra headers. Both returned the
same empty envelope below. This proves cookie-free IAM bearer reads for
these HCM list calls. A live check through the SDK login provider, other
auth modes, and other regions remain unverified.

Both list calls use a `params` query parameter containing JSON. NAT sends:

```json
{"search":[],"sort":{},"page":1,"size":10}
```

VPN sends:

```json
{"search":[{"field":"any","value":""}],"sort":{},"page":1,"size":10}
```

The empty `any` filter is verified; nonempty search terms and other filters
are not. A successful request with size 10 establishes one accepted value,
not the server's default or maximum. No nonempty pages were available to
verify page advancement.

## Verified response shapes

Regions returns `success` as a boolean and `data` as an array. Each observed
region entry contains these string fields:

```text
uuid
name
gatewayUrl
vnetworkDashboard
code
vserverEndpoint
```

The presence of routing URLs does not establish which value determines the
NAT zone, project namespace, or trusted destination. Verify that mapping
before reusing the existing loose region matcher or endpoint selector.

Both list responses contain exactly this successful empty envelope:

```json
{"success":true,"page":1,"size":10,"totalPage":0,"total":0}
```

`data` is omitted. Accept this verified zero-total shape as an empty list;
do not reject it solely because the list array is absent. Decode presence
as well as value so `{}`, missing totals, or a missing success flag cannot
become a successful empty list. The main design defines the malformed
response rules and SDK field mappings.

The empty envelopes establish no NAT or VPN record fields. They do not
prove a populated list's array location, nullable fields, detail routes,
child routes, secret fields, or service error shapes. No NAT or VPN
resource was created or activated to collect these observations.

## Remaining gates

Before a list release, verify:

1. A read through the SDK login provider using the established IAM bearer
   request contract, plus safe IAM-denial behavior.
2. Region-to-zone mapping for NAT, the project namespace for each list,
   and trusted gateway selection for supported regions. VPN still has no
   zone segment even when its gateway is regional.
3. Any nonempty search term or additional filter the SDK will expose.
4. A populated raw record or published typed response schema tied to the
   exact operation, including list array placement and field nullability.
5. Accepted pagination inputs and any size cap that the SDK will claim.
   Keep defaults at the observed page 1 and size 10 until stronger evidence
   exists; do not claim multi-page live verification on empty inventories.
6. VPN secret field exclusions and safe treatment of error bodies and
   capture hooks, using the main design's adversarial test requirements.

Detail and child methods need their own route and schema evidence. A
list-only release may proceed once every list gate is met; missing detail
schemas do not justify inventing list record types. Preserve the
[live-data rules](../../instructions/live-data.md) while closing these
gaps, including secret sanitization before any VPN response is persisted.
